// Command economics-recompute recomputes and persists hourly/daily
// economics on a schedule so the dashboard always reads a warm cache
// instead of triggering a slow live recompute on every request.
//
// It mirrors cmd/dam-collector: a single long-lived process with a
// daily-at-HH:MM pass and a fixed-interval pass, plus a one-shot
// `-once` mode for cron/tests. Two schedules run concurrently:
//
//   - nightly at economics.run_at: recompute the last finalize_days
//     days (ending yesterday). Those days are final, so the dashboard
//     serves them straight from economics_daily / economics_hourly.
//   - every economics.today_interval: recompute the current, still-open
//     day so the dashboard's "today" stays fresh.
//
// All inputs come from the local database (telemetry, DAM prices,
// tariffs, already-imported canonical KPIs). The daemon never calls
// FusionSolar or any other external API.
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/nesh/sestelemetry/internal/api"
	"github.com/nesh/sestelemetry/internal/config"
	"github.com/nesh/sestelemetry/internal/economics"
	"github.com/nesh/sestelemetry/internal/energyflow"
	"github.com/nesh/sestelemetry/internal/storage"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to YAML config")
	once := flag.Bool("once", false, "run one finalize + today-refresh pass over all orgs and exit")
	flag.Parse()

	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Error("config", "err", err)
		os.Exit(1)
	}
	if v := strings.TrimSpace(os.Getenv("DATABASE_URL")); v != "" {
		cfg.DatabaseURL = v
	}
	if cfg.DatabaseURL == "" {
		log.Error("database_url missing", "hint", "set in config YAML or DATABASE_URL")
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if !cfg.Economics.Enabled && !*once {
		log.Info("economics_recompute_disabled",
			"hint", "set economics.enabled: true in config.yaml to start the scheduler")
		// Idle until stopped so Docker's restart policy doesn't loop us
		// when the container runs without an `economics:` section.
		<-ctx.Done()
		return
	}

	tz, err := time.LoadLocation(cfg.Economics.Timezone)
	if err != nil {
		log.Error("timezone", "err", err)
		os.Exit(1)
	}
	hour, minute, err := config.ParseRunAt(cfg.Economics.RunAt)
	if err != nil {
		log.Error("run_at", "err", err)
		os.Exit(1)
	}

	pool, err := storage.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Error("db_open", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	// Idempotent schema bootstrap so the daemon can start standalone
	// (e.g. before the API has booted on a fresh host).
	if err := storage.InitEconomicsSchema(ctx, pool); err != nil {
		log.Error("db_init_economics", "err", err)
		os.Exit(1)
	}

	store := api.NewStore(pool)
	// Match the API's boot-time CAGG detection so the allocator's
	// month/year history reads take the fast path when the daily
	// continuous aggregate is available.
	if hasCAGG, err := storage.DailyCAGGAvailable(ctx, pool); err != nil {
		log.Warn("db_caggs_probe", "err", err)
	} else {
		store.EnableDailyCAGG(hasCAGG)
	}

	h := api.NewHandlers(store, "*")
	// The energy-flow allocator that backs HourlyFlows needs the per-org
	// device→role mapping; without it the directional flows come back
	// empty. This mirrors cmd/api/main.go.
	h.SetEnergyFlowOrgs(toEnergyFlowOrgs(cfg.Organizations))
	svc := economics.NewService(api.NewEconomicsBackend(h, store))

	orgs := orgIDs(cfg)
	tzName := tz.String()
	if len(orgs) == 0 {
		log.Warn("economics_no_orgs", "hint", "no organizations in config; scheduler will run but do nothing")
	}

	log.Info("economics_recompute_start",
		"orgs", len(orgs),
		"run_at", cfg.Economics.RunAt,
		"timezone", cfg.Economics.Timezone,
		"finalize_days", cfg.Economics.FinalizeDays,
		"today_interval", cfg.Economics.TodayInterval.String(),
		"max_concurrency", cfg.Economics.MaxConcurrency,
		"once", *once,
	)

	// Catch-up on startup: finalize recent days + refresh today once.
	finalizeRecent(ctx, log, svc, cfg.Economics, orgs, tz, tzName)
	refreshToday(ctx, log, svc, cfg.Economics, orgs, tz, tzName)
	if *once {
		return
	}

	var wg sync.WaitGroup
	wg.Add(2)

	// Nightly finalize loop.
	go func() {
		defer wg.Done()
		for {
			next := nextRunAt(time.Now(), tz, hour, minute)
			log.Info("economics_finalize_sleep", "next_run", next.Format(time.RFC3339))
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Until(next)):
			}
			finalizeRecent(ctx, log, svc, cfg.Economics, orgs, tz, tzName)
		}
	}()

	// Intraday today-refresh loop.
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(cfg.Economics.TodayInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				refreshToday(ctx, log, svc, cfg.Economics, orgs, tz, tzName)
			}
		}
	}()

	wg.Wait()
	log.Info("economics_recompute_stop")
}

// finalizeRecent recomputes the inclusive [yesterday-(finalize_days-1)
// .. yesterday] window for every org. These days have fully elapsed, so
// ComputeDay marks them final and the dashboard serves them from cache.
// The window overlaps prior runs on purpose: a re-run is idempotent
// (upsert) and lets late-arriving DAM prices heal recently-final days.
func finalizeRecent(
	ctx context.Context,
	log *slog.Logger,
	svc *economics.Service,
	cfg config.Economics,
	orgs []string,
	tz *time.Location,
	tzName string,
) {
	now := time.Now().In(tz)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, tz)
	yesterday := today.AddDate(0, 0, -1)
	from := yesterday.AddDate(0, 0, -(cfg.FinalizeDays - 1))
	fromStr := from.Format("2006-01-02")
	toStr := yesterday.Format("2006-01-02")

	start := time.Now()
	forEachOrg(ctx, orgs, cfg.MaxConcurrency, func(org string) {
		res, err := svc.RecomputeRange(ctx, org, fromStr, toStr, tzName, nil)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Error("economics_finalize_failed",
				"organization_id", org, "from", fromStr, "to", toStr, "err", err)
			return
		}
		log.Info("economics_finalize_ok",
			"organization_id", org, "from", fromStr, "to", toStr,
			"days_ok", res.DaysOK, "days_failed", res.DaysFailed)
	})
	log.Info("economics_finalize_pass_done",
		"orgs", len(orgs), "from", fromStr, "to", toStr,
		"duration_ms", time.Since(start).Milliseconds())
}

// refreshToday recomputes the current (still-open) day for every org so
// the dashboard's "today" reads a recently-refreshed cache.
func refreshToday(
	ctx context.Context,
	log *slog.Logger,
	svc *economics.Service,
	cfg config.Economics,
	orgs []string,
	tz *time.Location,
	tzName string,
) {
	todayStr := time.Now().In(tz).Format("2006-01-02")
	start := time.Now()
	forEachOrg(ctx, orgs, cfg.MaxConcurrency, func(org string) {
		if _, err := svc.ComputeDay(ctx, org, todayStr, tzName); err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Error("economics_today_failed",
				"organization_id", org, "date", todayStr, "err", err)
			return
		}
		log.Info("economics_today_ok", "organization_id", org, "date", todayStr)
	})
	log.Info("economics_today_pass_done",
		"orgs", len(orgs), "date", todayStr,
		"duration_ms", time.Since(start).Milliseconds())
}

// forEachOrg fans out fn across orgs bounded by maxConcurrency. It stops
// spawning new work once ctx is cancelled, and waits for in-flight work
// to drain.
func forEachOrg(ctx context.Context, orgs []string, maxConcurrency int, fn func(org string)) {
	if maxConcurrency < 1 {
		maxConcurrency = 1
	}
	sem := make(chan struct{}, maxConcurrency)
	var wg sync.WaitGroup
	for _, org := range orgs {
		if ctx.Err() != nil {
			break
		}
		org := org
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			fn(org)
		}()
	}
	wg.Wait()
}

// nextRunAt computes the next time hour:minute in tz strictly after now.
func nextRunAt(now time.Time, tz *time.Location, hour, minute int) time.Time {
	local := now.In(tz)
	candidate := time.Date(local.Year(), local.Month(), local.Day(), hour, minute, 0, 0, tz)
	if !candidate.After(local) {
		candidate = candidate.AddDate(0, 0, 1)
	}
	return candidate
}

func orgIDs(cfg *config.Root) []string {
	out := make([]string, 0, len(cfg.Organizations))
	for _, o := range cfg.Organizations {
		out = append(out, o.ID)
	}
	return out
}

// toEnergyFlowOrgs projects the YAML config into the energy-flow
// recompute shape (id, ess_discharge_sign, device host → role). It
// mirrors cmd/api/main.go so HourlyFlows classifies samples identically
// to the API and the live collector.
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
