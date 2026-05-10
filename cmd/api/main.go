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
	"github.com/nesh/sestelemetry/internal/energyflow"
	"github.com/nesh/sestelemetry/internal/storage"
)

func main() {
	listenAddr := flag.String("listen", ":8080", "HTTP listen address")
	defaultDB := flag.String("database-url", "", "PostgreSQL connection string (fallback if DATABASE_URL is unset)")
	allowOrigin := flag.String("allow-origin", "*", "Allowed CORS origin")
	configPath := flag.String("config", "", "YAML config path (optional; enables /api/v1/organizations)")
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
	if cfgPath != "" {
		cfg, err := config.Load(cfgPath)
		if err != nil {
			log.Warn("api_config_load", "path", cfgPath, "err", err)
		} else {
			svc.SetOrganizations(toOrganizationInfos(cfg.Organizations))
			svc.SetEnergyFlowOrgs(toEnergyFlowOrgs(cfg.Organizations))
			log.Info("api_config_loaded", "path", cfgPath, "organizations", len(cfg.Organizations))
		}
	}
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
