package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nesh/sestelemetry/internal/inventory"
)

// InitPlantInventorySchema creates the plant_inventory_snapshots table
// (idempotent). Mirrored by migrations/009_plant_inventory.sql.
func InitPlantInventorySchema(ctx context.Context, pool *pgxpool.Pool) error {
	if pool == nil {
		return fmt.Errorf("storage: nil pool")
	}
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS plant_inventory_snapshots (
			time                       timestamptz NOT NULL,
			organization_id            text NOT NULL,
			device_host                text NOT NULL DEFAULT '',
			poll_reason                text NOT NULL,
			pv_rated_kw                double precision,
			ess_rated_kw               double precision,
			ess_rated_kwh              double precision,
			ess_count                  double precision,
			pcs_count                  double precision,
			ess_soh_pct                double precision,
			active_power_control_mode  double precision,
			quality_flags              text[] NOT NULL DEFAULT '{}',
			raw                        jsonb NOT NULL DEFAULT '{}'::jsonb
		)`,
		`CREATE INDEX IF NOT EXISTS plant_inventory_snapshots_org_time_idx
			ON plant_inventory_snapshots (organization_id, time DESC)`,
	}
	for _, s := range stmts {
		if _, err := pool.Exec(ctx, s); err != nil {
			return fmt.Errorf("storage: plant inventory schema: %w", err)
		}
	}
	return nil
}

// InsertPlantInventorySnapshot persists one site-level inventory row.
func InsertPlantInventorySnapshot(ctx context.Context, pool *pgxpool.Pool, snap inventory.Snapshot) error {
	if pool == nil {
		return fmt.Errorf("storage: nil pool")
	}
	raw := snap.Raw
	if raw == nil {
		raw = map[string]any{}
	}
	rawJSON, err := json.Marshal(raw)
	if err != nil {
		return fmt.Errorf("storage: marshal inventory raw: %w", err)
	}
	flags := snap.QualityFlags
	if flags == nil {
		flags = []string{}
	}
	_, err = pool.Exec(ctx, `
		INSERT INTO plant_inventory_snapshots (
			time, organization_id, device_host, poll_reason,
			pv_rated_kw, ess_rated_kw, ess_rated_kwh, ess_count, pcs_count,
			ess_soh_pct, active_power_control_mode, quality_flags, raw
		) VALUES (
			$1, $2, $3, $4,
			$5, $6, $7, $8, $9,
			$10, $11, $12, $13
		)`,
		snap.Time.UTC(),
		snap.OrganizationID,
		snap.DeviceHost,
		snap.PollReason,
		snap.PVRatedKw,
		snap.ESSRatedKw,
		snap.ESSRatedKwh,
		snap.ESSCount,
		snap.PCSCount,
		snap.ESSSOHPct,
		snap.ActivePowerControlMode,
		flags,
		rawJSON,
	)
	if err != nil {
		return fmt.Errorf("storage: insert plant inventory: %w", err)
	}
	return nil
}

const (
	defaultPlantInventoryListLimit = 200
	maxPlantInventoryListLimit     = 500
)

// ListPlantInventorySnapshots returns up to `limit` newest snapshots for
// an organization (newest first). limit <= 0 uses the default; values
// above the max are capped.
func ListPlantInventorySnapshots(ctx context.Context, pool *pgxpool.Pool, organizationID string, limit int) ([]inventory.Snapshot, error) {
	if pool == nil {
		return nil, fmt.Errorf("storage: nil pool")
	}
	if organizationID == "" {
		return nil, fmt.Errorf("storage: organization_id is required")
	}
	if limit <= 0 {
		limit = defaultPlantInventoryListLimit
	}
	if limit > maxPlantInventoryListLimit {
		limit = maxPlantInventoryListLimit
	}
	rows, err := pool.Query(ctx, `
		SELECT time, organization_id, device_host, poll_reason,
			pv_rated_kw, ess_rated_kw, ess_rated_kwh, ess_count, pcs_count,
			ess_soh_pct, active_power_control_mode, quality_flags, raw
		FROM plant_inventory_snapshots
		WHERE organization_id = $1
		ORDER BY time DESC
		LIMIT $2
	`, organizationID, limit)
	if err != nil {
		return nil, fmt.Errorf("storage: list plant inventory: %w", err)
	}
	defer rows.Close()

	out := make([]inventory.Snapshot, 0, limit)
	for rows.Next() {
		var (
			t          time.Time
			orgID      string
			host       string
			reason     string
			pv, essP   *float64
			essC, essN *float64
			pcs, soh   *float64
			mode       *float64
			flags      []string
			rawBytes   []byte
		)
		if err := rows.Scan(
			&t, &orgID, &host, &reason,
			&pv, &essP, &essC, &essN, &pcs,
			&soh, &mode, &flags, &rawBytes,
		); err != nil {
			return nil, fmt.Errorf("storage: scan plant inventory: %w", err)
		}
		raw := map[string]any{}
		if len(rawBytes) > 0 {
			_ = json.Unmarshal(rawBytes, &raw)
		}
		if flags == nil {
			flags = []string{}
		}
		out = append(out, inventory.Snapshot{
			Time:                   t.UTC(),
			OrganizationID:         orgID,
			DeviceHost:             host,
			PollReason:             reason,
			PVRatedKw:              pv,
			ESSRatedKw:             essP,
			ESSRatedKwh:            essC,
			ESSCount:               essN,
			PCSCount:               pcs,
			ESSSOHPct:              soh,
			ActivePowerControlMode: mode,
			QualityFlags:           flags,
			Raw:                    raw,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: iterate plant inventory: %w", err)
	}
	return out, nil
}
