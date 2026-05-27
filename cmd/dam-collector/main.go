// Command dam-collector ingests Day-Ahead Market (RDN) hourly prices and
// volumes from oree.com.ua once per day and stores them in Postgres.
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nesh/sestelemetry/internal/config"
	"github.com/nesh/sestelemetry/internal/dam"
	"github.com/nesh/sestelemetry/internal/oree"
	"github.com/nesh/sestelemetry/internal/storage"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to YAML config")
	dateFlag := flag.String("date", "", "fetch a single delivery_date (YYYY-MM-DD) and exit; bypasses the daily scheduler")
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

	if !cfg.OREE.Enabled {
		log.Info("dam_collector_disabled",
			"hint", "set oree.enabled: true in config.yaml to start fetching")
		// Idle until the process is stopped so Docker's restart policy doesn't
		// restart-loop us when the operator runs the container without an
		// `oree:` section in config.yaml.
		<-ctx.Done()
		return
	}

	tz, err := time.LoadLocation(cfg.OREE.Timezone)
	if err != nil {
		log.Error("timezone", "err", err)
		os.Exit(1)
	}
	hour, minute, err := config.ParseRunAt(cfg.OREE.RunAt)
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

	if err := storage.InitDAMSchema(ctx, pool); err != nil {
		log.Error("db_schema", "err", err)
		os.Exit(1)
	}

	client := oree.NewClient(cfg.OREE.BaseURL, cfg.OREE.HTTPTimeout, cfg.OREE.UserAgent)

	if d := strings.TrimSpace(*dateFlag); d != "" {
		single, err := time.ParseInLocation("2006-01-02", d, tz)
		if err != nil {
			log.Error("date_flag", "err", err, "expected", "YYYY-MM-DD")
			os.Exit(1)
		}
		log.Info("dam_one_shot",
			"delivery_date", single.Format("2006-01-02"),
			"zone", cfg.OREE.Zone,
		)
		if err := fetchAndStore(ctx, log, client, pool, cfg.OREE, single); err != nil {
			log.Error("dam_one_shot_failed", "err", err)
			os.Exit(1)
		}
		return
	}

	log.Info("dam_collector_start",
		"zone", cfg.OREE.Zone,
		"run_at", cfg.OREE.RunAt,
		"timezone", cfg.OREE.Timezone,
		"delivery_offset_days", cfg.OREE.DeliveryOffsetDays,
	)

	// Catch-up on startup: if the target date isn't fully populated, fetch now.
	target := targetDate(time.Now(), tz, cfg.OREE.DeliveryOffsetDays)
	if needsFetch(ctx, log, pool, target, cfg.OREE.Zone) {
		if err := fetchAndStore(ctx, log, client, pool, cfg.OREE, target); err != nil {
			log.Error("dam_catchup", "err", err)
		}
	}

	for {
		next := nextRunAt(time.Now(), tz, hour, minute)
		log.Info("dam_sleep", "next_run", next.Format(time.RFC3339))
		select {
		case <-ctx.Done():
			log.Info("dam_collector_stop")
			return
		case <-time.After(time.Until(next)):
		}
		target := targetDate(time.Now(), tz, cfg.OREE.DeliveryOffsetDays)
		if err := fetchAndStore(ctx, log, client, pool, cfg.OREE, target); err != nil {
			log.Error("dam_fetch", "err", err)
		}
	}
}

// targetDate returns the OREE delivery_date in the given timezone, plus the
// configured offset (1 = tomorrow).
func targetDate(now time.Time, tz *time.Location, offsetDays int) time.Time {
	t := now.In(tz).AddDate(0, 0, offsetDays)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, tz)
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

func needsFetch(ctx context.Context, log *slog.Logger, pool *pgxpool.Pool, deliveryDate time.Time, zone int) bool {
	n, err := storage.CountDAMRowsForDate(ctx, pool, deliveryDate, zone)
	if err != nil {
		log.Warn("dam_count", "err", err)
		return true
	}
	if n >= 24 {
		log.Info("dam_skip_catchup", "delivery_date", deliveryDate.Format("2006-01-02"), "zone", zone, "rows", n)
		return false
	}
	return true
}

func fetchAndStore(
	ctx context.Context,
	log *slog.Logger,
	client *oree.Client,
	pool *pgxpool.Pool,
	cfg config.OREE,
	deliveryDate time.Time,
) error {
	_, err := dam.FetchAndStore(ctx, log, client, pool, deliveryDate, cfg.Zone, cfg.Retry.Attempts, cfg.Retry.Backoff)
	return err
}
