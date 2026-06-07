// Command weather-collector caches Open-Meteo forecasts per
// organization in TimescaleDB so the API can serve them without hitting
// Open-Meteo on every request, and so future backend models (economic,
// PV) have a stable source of truth. It mirrors the structure of
// cmd/dam-collector — a single long-lived process that refreshes on a
// fixed interval and supports a one-shot `-once` mode for cron/tests.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nesh/sestelemetry/internal/config"
	"github.com/nesh/sestelemetry/internal/storage"
	"github.com/nesh/sestelemetry/internal/weather"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to YAML config")
	once := flag.Bool("once", false, "run a single refresh pass over all orgs and exit")
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

	if !cfg.Weather.Enabled && !*once {
		log.Info("weather_collector_disabled",
			"hint", "set weather.enabled: true in config.yaml to start fetching")
		<-ctx.Done()
		return
	}

	orgs := orgsWithLocation(cfg)
	if len(orgs) == 0 {
		log.Warn("weather_no_orgs_with_location",
			"hint", "no organizations have a `location` block; nothing to fetch")
	}

	pool, err := storage.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Error("db_open", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	if err := storage.InitWeatherSchema(ctx, pool); err != nil {
		log.Error("db_schema", "err", err)
		os.Exit(1)
	}

	client, err := weather.NewClient(cfg.Weather.BaseURL, cfg.Weather.HTTPTimeout, cfg.Weather.UserAgent, cfg.Weather.PastDays)
	if err != nil {
		log.Error("weather_client", "err", err)
		os.Exit(1)
	}

	log.Info("weather_collector_start",
		"orgs", len(orgs),
		"interval", cfg.Weather.Interval,
		"base_url", cfg.Weather.BaseURL,
		"once", *once,
	)

	refresh := func() {
		refreshAll(ctx, log, client, pool, cfg.Weather, orgs)
	}
	refresh()

	if *once {
		return
	}

	ticker := time.NewTicker(cfg.Weather.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Info("weather_collector_stop")
			return
		case <-ticker.C:
			refresh()
		}
	}
}

// orgWithLocation pairs an org id with its coordinates. Held in a flat
// slice so the refresh loop can iterate without re-validating the
// `Location != nil` predicate every tick.
type orgWithLocation struct {
	ID        string
	Latitude  float64
	Longitude float64
}

func orgsWithLocation(cfg *config.Root) []orgWithLocation {
	out := make([]orgWithLocation, 0, len(cfg.Organizations))
	for _, o := range cfg.Organizations {
		if o.Location == nil {
			continue
		}
		out = append(out, orgWithLocation{
			ID:        o.ID,
			Latitude:  o.Location.Latitude,
			Longitude: o.Location.Longitude,
		})
	}
	return out
}

// refreshAll fans out one fetch+upsert per organization. We bound
// concurrency at 4 — Open-Meteo doesn't publish a hard rate limit but
// the public service asks clients not to hammer it, and even ~50 orgs
// per environment finishes well inside the hourly tick at this cap.
func refreshAll(
	ctx context.Context,
	log *slog.Logger,
	client *weather.Client,
	pool *pgxpool.Pool,
	cfg config.Weather,
	orgs []orgWithLocation,
) {
	const maxConcurrency = 4
	sem := make(chan struct{}, maxConcurrency)
	var wg sync.WaitGroup
	for _, o := range orgs {
		o := o
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			if err := refreshOne(ctx, client, pool, cfg, o); err != nil {
				log.Error("weather_refresh_failed",
					"organization_id", o.ID,
					"err", err,
				)
				return
			}
			log.Info("weather_refresh_ok",
				"organization_id", o.ID,
				"latitude", o.Latitude,
				"longitude", o.Longitude,
			)
		}()
	}
	wg.Wait()
}

func refreshOne(
	ctx context.Context,
	client *weather.Client,
	pool *pgxpool.Pool,
	cfg config.Weather,
	o orgWithLocation,
) error {
	forecast, url, err := client.Fetch(ctx, o.Latitude, o.Longitude, cfg.Retry.Attempts, cfg.Retry.Backoff)
	if err != nil {
		return fmt.Errorf("fetch: %w", err)
	}
	hourly, daily, err := weather.BuildRows(o.ID, forecast, url)
	if err != nil {
		return fmt.Errorf("build rows: %w", err)
	}
	if err := storage.UpsertWeatherHourly(ctx, pool, hourly); err != nil {
		return fmt.Errorf("upsert hourly: %w", err)
	}
	if err := storage.UpsertWeatherDaily(ctx, pool, daily); err != nil {
		return fmt.Errorf("upsert daily: %w", err)
	}
	return nil
}
