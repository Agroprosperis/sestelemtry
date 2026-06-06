package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/nesh/sestelemetry/internal/api"
	"github.com/nesh/sestelemetry/internal/config"
	"github.com/nesh/sestelemetry/internal/dam"
	"github.com/nesh/sestelemetry/internal/energyflow"
	"github.com/nesh/sestelemetry/internal/fusionsolar"
	"github.com/nesh/sestelemetry/internal/oree"
	"github.com/nesh/sestelemetry/internal/storage"
)

func firstNonEmptyStr(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

func main() {
	listenAddr := flag.String("listen", ":8080", "HTTP listen address")
	defaultDB := flag.String("database-url", "", "PostgreSQL connection string (fallback if DATABASE_URL is unset)")
	allowOrigin := flag.String("allow-origin", "*", "Allowed CORS origin")
	configPath := flag.String("config", "", "YAML config path (optional; enables /api/v1/organizations)")
	fusionConfigPath := flag.String("fusionsolar-config", "", "Separate YAML with FusionSolar import defaults (optional; falls back to FUSIONSOLAR_CONFIG)")
	flag.Parse()

	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	dbURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if dbURL == "" {
		dbURL = strings.TrimSpace(*defaultDB)
	}
	if dbURL == "" {
		log.Error("database_url missing", "hint", "set DATABASE_URL or -database-url")
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := storage.Open(ctx, dbURL)
	if err != nil {
		log.Error("db_open", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	// The API process is the sole writer of organization_tariffs (no
	// collector touches it), so we own its bootstrap. Idempotent —
	// running this on every start lets a fresh environment boot
	// without an external migration step.
	if err := storage.InitTariffsSchema(ctx, pool); err != nil {
		log.Error("db_init_tariffs", "err", err)
		os.Exit(1)
	}

	store := api.NewStore(pool)
	// Boot-time feature detection: if the collector has run migration 004
	// (or InitContinuousAggregates), the daily CAGG is queryable and
	// month/year presets switch to the fast path. On a freshly upgraded
	// host where CAGG creation hasn't completed yet, we fall back to
	// scanning the raw hypertable so the dashboard never breaks.
	hasCAGG, err := storage.DailyCAGGAvailable(ctx, pool)
	if err != nil {
		log.Warn("db_caggs_probe", "err", err)
	}
	store.EnableDailyCAGG(hasCAGG)
	log.Info("api_features", "daily_cagg", hasCAGG)

	svc := api.NewHandlers(store, *allowOrigin)
	// Optional: load org metadata from YAML so /api/v1/organizations
	// can return display names + coordinates. The API server runs
	// fine without a config (telemetry data lives in the DB), so a
	// missing or malformed config logs a warning rather than aborting
	// startup — the org list endpoint just returns an empty array.
	cfgPath := strings.TrimSpace(*configPath)
	if cfgPath == "" {
		cfgPath = strings.TrimSpace(os.Getenv("CONFIG_PATH"))
	}
	var loadedCfg *config.Root
	if cfgPath != "" {
		cfg, err := config.Load(cfgPath)
		if err != nil {
			log.Warn("api_config_load", "path", cfgPath, "err", err)
		} else {
			loadedCfg = cfg
			svc.SetOrganizations(toOrganizationInfos(cfg.Organizations))
			svc.SetEnergyFlowOrgs(toEnergyFlowOrgs(cfg.Organizations))
			if cfg.OREE.Enabled {
				// On-demand DAM refresh: a single attempt with no
				// backoff so an operator clicking the dashboard
				// button gets a result (or an explicit error) within
				// one HTTP round-trip instead of the multi-minute
				// retry window the daily collector uses. The
				// collector daemon already owns the scheduled
				// catch-up budget; this is the escape hatch.
				oreeClient := oree.NewClient(cfg.OREE.BaseURL, cfg.OREE.HTTPTimeout, cfg.OREE.UserAgent)
				fetcher := func(ctx context.Context, date time.Time, zone int) (int, error) {
					return dam.FetchAndStore(ctx, log, oreeClient, pool, date, zone, 1, 0)
				}
				svc.SetDAMFetcher(fetcher, cfg.OREE.Zone)
				log.Info("api_dam_refresh_enabled", "zone", cfg.OREE.Zone)
			}
			log.Info("api_config_loaded", "path", cfgPath, "organizations", len(cfg.Organizations))
		}
	}

	// FusionSolar archive importer behind POST
	// /api/v1/fusionsolar/import. Always enabled — no secrets in env or
	// YAML; the operator enters the Northbound API access token (and
	// optional API base) on the import page and it travels in the
	// request body. The per-org device_host label mirrors what the
	// live collector stamps, so backfilled rows classify identically in
	// the energy-flow allocator.
	hostByOrg := map[string]string{}
	if loadedCfg != nil {
		for _, o := range loadedCfg.Organizations {
			devices := o.Devices()
			if len(devices) > 0 {
				hostByOrg[o.ID] = strings.TrimSpace(devices[0].Host)
			}
		}
	}
	importer := fusionsolar.NewImporter(pool, log, hostByOrg)
	svc.SetFusionSolarImporter(func(ctx context.Context, orgID, accessToken, apiBase string, from, to time.Time) (any, error) {
		client := fusionsolar.NewClient(apiBase, accessToken, 60*time.Second)
		return importer.Import(ctx, client, orgID, from, to)
	})
	// Server-side OAuth client so the import page only needs the
	// long-lived refresh token; the fixed app secret never leaves the
	// server. client_id defaults to fusionsolar.DefaultClientID.
	// FusionSolar connection defaults: a separate YAML file
	// (-fusionsolar-config / FUSIONSOLAR_CONFIG), with env vars as a
	// fallback for each field. The import page may leave matching fields
	// blank; any value posted in the request body still overrides these.
	fusionDefaults := api.FusionSolarDefaults{
		RefreshToken: strings.TrimSpace(os.Getenv("FUSIONSOLAR_REFRESH_TOKEN")),
		ClientID:     strings.TrimSpace(os.Getenv("FUSIONSOLAR_CLIENT_ID")),
		ClientSecret: strings.TrimSpace(os.Getenv("FUSIONSOLAR_CLIENT_SECRET")),
		OAuthBase:    strings.TrimSpace(os.Getenv("FUSIONSOLAR_OAUTH_BASE")),
		OAuthResolve: strings.TrimSpace(os.Getenv("FUSIONSOLAR_OAUTH_RESOLVE")),
		APIBase:      strings.TrimSpace(os.Getenv("FUSIONSOLAR_API_BASE")),
	}
	fusionCfgPath := strings.TrimSpace(*fusionConfigPath)
	if fusionCfgPath == "" {
		fusionCfgPath = strings.TrimSpace(os.Getenv("FUSIONSOLAR_CONFIG"))
	}
	if fusionCfgPath != "" {
		if s, err := fusionsolar.LoadSettings(fusionCfgPath); err != nil {
			if os.IsNotExist(err) {
				log.Warn("api_fusionsolar_config_missing", "path", fusionCfgPath)
			} else {
				log.Error("api_fusionsolar_config_load", "path", fusionCfgPath, "err", err)
				os.Exit(1)
			}
		} else {
			// File values take precedence over env for any non-empty field.
			fusionDefaults.RefreshToken = firstNonEmptyStr(s.RefreshToken, fusionDefaults.RefreshToken)
			fusionDefaults.ClientID = firstNonEmptyStr(s.ClientID, fusionDefaults.ClientID)
			fusionDefaults.ClientSecret = firstNonEmptyStr(s.ClientSecret, fusionDefaults.ClientSecret)
			fusionDefaults.OAuthBase = firstNonEmptyStr(s.OAuthBase, fusionDefaults.OAuthBase)
			fusionDefaults.OAuthResolve = firstNonEmptyStr(s.OAuthResolve, fusionDefaults.OAuthResolve)
			fusionDefaults.APIBase = firstNonEmptyStr(s.APIBase, fusionDefaults.APIBase)
			log.Info("api_fusionsolar_config_loaded", "path", fusionCfgPath)
		}
	}
	svc.SetFusionSolarDefaults(fusionDefaults)
	log.Info("api_fusionsolar_import_enabled",
		"refresh_token_configured", fusionDefaults.RefreshToken != "",
		"client_secret_configured", fusionDefaults.ClientSecret != "",
		"oauth_resolve_pinned", fusionDefaults.OAuthResolve != "")

	server := &http.Server{
		Addr:              *listenAddr,
		Handler:           svc.Router(),
		MaxHeaderBytes:    1 << 20, // 1 MiB
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("api_start", "listen", *listenAddr)
		errCh <- server.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			log.Error("listen", "err", err)
			os.Exit(1)
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Error("shutdown", "err", err)
	}
}

// toEnergyFlowOrgs projects the YAML config's Organization entries
// into the energy-flow recompute shape (ID, ess_discharge_sign, per
// device host → role mapping). The mapping mirrors the collector's
// startup classification so a backfill replays the exact role
// assignment a live poll cycle would have produced. Orgs that don't
// cover both PV and ESS accumulators are still included — the
// handler tolerates them and just produces no flow output rather
// than failing — because they may have switched topology over time
// and historical samples for the older topology are still valid
// inputs.
func toEnergyFlowOrgs(orgs []config.Organization) []api.EnergyFlowOrg {
	out := make([]api.EnergyFlowOrg, 0, len(orgs))
	for _, o := range orgs {
		devices := o.Devices()
		mapped := make([]api.EnergyFlowDevice, 0, len(devices))
		for _, d := range devices {
			role := energyflow.DetectRole(d.MetricKeys)
			mapped = append(mapped, api.EnergyFlowDevice{
				Host: strings.TrimSpace(d.Host),
				Role: string(role),
			})
		}
		out = append(out, api.EnergyFlowOrg{
			ID:               o.ID,
			EssDischargeSign: o.EssDischargeSign,
			Devices:          mapped,
		})
	}
	return out
}

// toOrganizationInfos projects the YAML config's Organization entries
// into the public, dashboard-safe shape served by /api/v1/organizations.
// Modbus connection details are intentionally dropped — the dashboard
// has no business knowing IPs and unit IDs.
func toOrganizationInfos(orgs []config.Organization) []api.OrganizationInfo {
	out := make([]api.OrganizationInfo, 0, len(orgs))
	for _, o := range orgs {
		info := api.OrganizationInfo{ID: o.ID, Name: o.Name}
		if o.Location != nil {
			info.Location = &api.LocationInfo{
				Latitude:  o.Location.Latitude,
				Longitude: o.Location.Longitude,
				City:      o.Location.City,
			}
		}
		out = append(out, info)
	}
	return out
}
