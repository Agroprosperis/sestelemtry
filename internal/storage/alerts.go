package storage

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Device connectivity states persisted in device_alert_state.
const (
	DeviceStateOK   = "ok"
	DeviceStateDown = "down"
)

// DeviceAlertState is one row of device_alert_state: what the watchdog
// last concluded about a Modbus device and when it last emailed about
// it. Keeping this in the database (rather than in process memory)
// means a redeploy of the watchdog container does not re-announce an
// outage operators already know about.
type DeviceAlertState struct {
	OrganizationID string
	DeviceHost     string
	State          string
	Since          time.Time
	// LastSampleAt is nil when the device had no telemetry inside the
	// watchdog's lookback window at the time of the check.
	LastSampleAt *time.Time
	// LastNotifiedAt is nil until the first successful email about the
	// current state.
	LastNotifiedAt *time.Time
}

// InitAlertSchema creates the alert-state table (idempotent). Mirrors
// migrations/010_alerts.sql so the watchdog can boot against a database
// that was never migrated by hand.
func InitAlertSchema(ctx context.Context, pool *pgxpool.Pool) error {
	if pool == nil {
		return fmt.Errorf("storage: nil pool")
	}
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS device_alert_state (
			organization_id   text NOT NULL,
			device_host       text NOT NULL DEFAULT '',
			state             text NOT NULL,
			since             timestamptz NOT NULL,
			last_sample_at    timestamptz,
			last_notified_at  timestamptz,
			PRIMARY KEY (organization_id, device_host)
		)`,
	}
	for _, s := range stmts {
		if _, err := pool.Exec(ctx, s); err != nil {
			return fmt.Errorf("storage: exec alert schema: %w", err)
		}
	}
	return nil
}

// LoadDeviceAlertStates returns every persisted device state. The set is
// small (one row per configured Modbus device), so the watchdog reads it
// whole on each check rather than querying per device.
func LoadDeviceAlertStates(ctx context.Context, pool *pgxpool.Pool) ([]DeviceAlertState, error) {
	if pool == nil {
		return nil, fmt.Errorf("storage: nil pool")
	}
	rows, err := pool.Query(ctx, `
		SELECT organization_id, device_host, state, since, last_sample_at, last_notified_at
		FROM device_alert_state
	`)
	if err != nil {
		return nil, fmt.Errorf("storage: query device_alert_state: %w", err)
	}
	defer rows.Close()

	out := make([]DeviceAlertState, 0, 16)
	for rows.Next() {
		var s DeviceAlertState
		if err := rows.Scan(
			&s.OrganizationID, &s.DeviceHost, &s.State,
			&s.Since, &s.LastSampleAt, &s.LastNotifiedAt,
		); err != nil {
			return nil, fmt.Errorf("storage: scan device_alert_state: %w", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: iterate device_alert_state: %w", err)
	}
	return out, nil
}

// UpsertDeviceAlertStates writes the post-check state of every device in
// one transaction.
func UpsertDeviceAlertStates(ctx context.Context, pool *pgxpool.Pool, rows []DeviceAlertState) error {
	if len(rows) == 0 {
		return nil
	}
	if pool == nil {
		return fmt.Errorf("storage: nil pool")
	}
	const stmt = `
		INSERT INTO device_alert_state (
			organization_id, device_host, state, since, last_sample_at, last_notified_at
		) VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (organization_id, device_host) DO UPDATE SET
			state            = EXCLUDED.state,
			since            = EXCLUDED.since,
			last_sample_at   = EXCLUDED.last_sample_at,
			last_notified_at = EXCLUDED.last_notified_at
	`
	batch := &pgx.Batch{}
	for _, r := range rows {
		batch.Queue(stmt,
			r.OrganizationID, r.DeviceHost, r.State,
			r.Since.UTC(), r.LastSampleAt, r.LastNotifiedAt,
		)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("storage: begin device_alert_state tx: %w", err)
	}
	defer tx.Rollback(ctx)

	br := tx.SendBatch(ctx, batch)
	for i := 0; i < len(rows); i++ {
		if _, err := br.Exec(); err != nil {
			br.Close()
			return fmt.Errorf("storage: upsert device_alert_state row %d: %w", i, err)
		}
	}
	if err := br.Close(); err != nil {
		return fmt.Errorf("storage: close device_alert_state batch: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("storage: commit device_alert_state tx: %w", err)
	}
	return nil
}

// LatestSampleAt returns the freshest telemetry timestamp for one
// (organization, metric_key) pair at or after `notBefore`, or nil when
// the pair produced nothing in that window.
//
// The shape is deliberate: `ORDER BY time DESC LIMIT 1` against the
// (organization_id, metric_key, time DESC) index is a single seek, while
// `max(time)` over an unbounded predicate degenerates into a scan across
// every chunk of a multi-gigabyte hypertable. `notBefore` bounds the
// range so a device that has been silent for months still costs one
// empty index range, not a full history walk.
//
// Callers pick metric_key as a per-device probe: for multi-device
// organizations each SmartLogger owns a disjoint metric_keys whitelist,
// so a single key unambiguously identifies the device that wrote it.
func LatestSampleAt(
	ctx context.Context,
	pool *pgxpool.Pool,
	organizationID, metricKey string,
	notBefore time.Time,
) (*time.Time, error) {
	if pool == nil {
		return nil, fmt.Errorf("storage: nil pool")
	}
	if organizationID == "" || metricKey == "" {
		return nil, fmt.Errorf("storage: organization_id and metric_key are required")
	}
	var ts time.Time
	err := pool.QueryRow(ctx, `
		SELECT time
		FROM telemetry_samples
		WHERE organization_id = $1
			AND metric_key = $2
			AND time >= $3
		ORDER BY time DESC
		LIMIT 1
	`, organizationID, metricKey, notBefore.UTC()).Scan(&ts)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("storage: latest sample for %s/%s: %w", organizationID, metricKey, err)
	}
	t := ts.UTC()
	return &t, nil
}
