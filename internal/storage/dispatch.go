package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Manual-control desk (ems_manual_control_mvp.md): every confirmed plan
// is a new version; every publication stores the LP result so «план /
// факт» can show what the edge was asked to follow at each hour.
// Mirrored by migrations/018_dispatch.sql.
var dispatchSchema = []string{
	`CREATE TABLE IF NOT EXISTS dispatch_versions (
		site_id text NOT NULL,
		version integer NOT NULL,
		confirmed_at timestamptz NOT NULL DEFAULT now(),
		confirmed_by text NOT NULL DEFAULT '',
		model jsonb NOT NULL,
		PRIMARY KEY (site_id, version)
	)`,
	`CREATE TABLE IF NOT EXISTS dispatch_runs (
		site_id text NOT NULL,
		start_hour timestamptz NOT NULL,
		manifest_id text NOT NULL,
		run_at timestamptz NOT NULL DEFAULT now(),
		version integer NOT NULL,
		hours jsonb NOT NULL,
		PRIMARY KEY (site_id, start_hour, manifest_id)
	)`,
	`CREATE INDEX IF NOT EXISTS dispatch_runs_site_run
		ON dispatch_runs (site_id, run_at DESC)`,
}

// ErrDispatchVersionConflict means someone confirmed another version
// since the caller loaded the plan (optimistic concurrency).
var ErrDispatchVersionConflict = errors.New("storage: dispatch plan changed since it was loaded")

// DispatchVersion is one confirmed plan.
type DispatchVersion struct {
	SiteID      string
	Version     int
	ConfirmedAt time.Time
	ConfirmedBy string
	Model       json.RawMessage
}

// LatestDispatchVersion returns the newest confirmed plan; ok=false when
// the site has none yet.
func LatestDispatchVersion(ctx context.Context, db DBTX, siteID string) (DispatchVersion, bool, error) {
	v := DispatchVersion{SiteID: siteID}
	err := db.QueryRow(ctx, `
		SELECT version, confirmed_at, confirmed_by, model FROM dispatch_versions
		WHERE site_id = $1 ORDER BY version DESC LIMIT 1`, siteID).
		Scan(&v.Version, &v.ConfirmedAt, &v.ConfirmedBy, &v.Model)
	if err == pgx.ErrNoRows {
		return DispatchVersion{SiteID: siteID}, false, nil
	}
	if err != nil {
		return DispatchVersion{}, false, err
	}
	return v, true, nil
}

// InsertDispatchVersion stores baseVersion+1. It fails with
// ErrDispatchVersionConflict unless baseVersion is still the latest.
func InsertDispatchVersion(ctx context.Context, pool *pgxpool.Pool, siteID string, baseVersion int, confirmedBy string, model []byte) (DispatchVersion, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return DispatchVersion{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var current int
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(max(version), 0) FROM dispatch_versions WHERE site_id = $1`, siteID).Scan(&current); err != nil {
		return DispatchVersion{}, err
	}
	if current != baseVersion {
		return DispatchVersion{}, ErrDispatchVersionConflict
	}
	v := DispatchVersion{SiteID: siteID, Version: current + 1, ConfirmedBy: confirmedBy, Model: model}
	err = tx.QueryRow(ctx, `
		INSERT INTO dispatch_versions (site_id, version, confirmed_by, model)
		VALUES ($1, $2, $3, $4) RETURNING confirmed_at`,
		siteID, v.Version, confirmedBy, model).Scan(&v.ConfirmedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return DispatchVersion{}, ErrDispatchVersionConflict
		}
		return DispatchVersion{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return DispatchVersion{}, err
	}
	return v, nil
}

// DispatchRun is the LP result behind one published manifest, from
// StartHour on. Hours is the per-hour JSON the desk renders.
type DispatchRun struct {
	SiteID     string
	StartHour  time.Time
	ManifestID string
	RunAt      time.Time
	Version    int
	Hours      json.RawMessage
}

// InsertDispatchRun records a run once per (start hour, manifest): the
// 15-minute loop re-running an unchanged plan adds nothing.
func InsertDispatchRun(ctx context.Context, db DBTX, run DispatchRun) error {
	_, err := db.Exec(ctx, `
		INSERT INTO dispatch_runs (site_id, start_hour, manifest_id, version, hours)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (site_id, start_hour, manifest_id) DO NOTHING`,
		run.SiteID, run.StartHour.UTC(), run.ManifestID, run.Version, run.Hours)
	return err
}

// DispatchRunsBetween returns runs issued in [from-24h, to), oldest
// first — enough to find the plan in force at any hour of [from, to).
func DispatchRunsBetween(ctx context.Context, pool *pgxpool.Pool, siteID string, from, to time.Time) ([]DispatchRun, error) {
	rows, err := pool.Query(ctx, `
		SELECT start_hour, manifest_id, run_at, version, hours FROM dispatch_runs
		WHERE site_id = $1 AND run_at >= $2 AND run_at < $3
		ORDER BY run_at ASC`, siteID, from.Add(-24*time.Hour).UTC(), to.UTC())
	if err != nil {
		return nil, fmt.Errorf("storage: dispatch runs: %w", err)
	}
	defer rows.Close()
	var out []DispatchRun
	for rows.Next() {
		r := DispatchRun{SiteID: siteID}
		if err := rows.Scan(&r.StartHour, &r.ManifestID, &r.RunAt, &r.Version, &r.Hours); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
