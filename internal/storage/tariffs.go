package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// InitTariffsSchema creates the per-org tariff table (idempotent). The
// API process owns the table — no other service writes to it — so we
// run the init from cmd/api/main.go at startup. Mirrors the
// weather/dam pattern of programmatic schema bootstrap so a fresh
// environment without the migration files still boots.
func InitTariffsSchema(ctx context.Context, pool *pgxpool.Pool) error {
	if pool == nil {
		return fmt.Errorf("storage: nil pool")
	}
	const stmt = `
		CREATE TABLE IF NOT EXISTS organization_tariffs (
			organization_id text NOT NULL,
			tariffs         jsonb NOT NULL,
			updated_at      timestamptz NOT NULL DEFAULT now(),
			PRIMARY KEY (organization_id)
		)
	`
	if _, err := pool.Exec(ctx, stmt); err != nil {
		return fmt.Errorf("storage: exec tariffs schema: %w", err)
	}
	return nil
}

// GetOrgTariffs returns the JSON payload stored for the given org. The
// boolean is false when no row exists (the API turns that into a 404
// so the frontend can fall back to bundled defaults). Callers receive
// the raw JSON so the API layer keeps control over the field shape;
// pushing struct decoding down here would couple storage to a
// versioned DTO.
func GetOrgTariffs(
	ctx context.Context,
	pool *pgxpool.Pool,
	organizationID string,
) (json.RawMessage, bool, error) {
	if pool == nil {
		return nil, false, fmt.Errorf("storage: nil pool")
	}
	if organizationID == "" {
		return nil, false, fmt.Errorf("storage: empty organization_id")
	}
	var payload []byte
	err := pool.QueryRow(ctx, `
		SELECT tariffs
		FROM organization_tariffs
		WHERE organization_id = $1
	`, organizationID).Scan(&payload)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("storage: query org tariffs: %w", err)
	}
	return json.RawMessage(payload), true, nil
}

// UpsertOrgTariffs stores the JSON payload for the given org, replacing
// any prior value (last-writer-wins). The handler validates field
// shapes before calling — we accept opaque bytes here so storage
// stays decoupled from the API DTO.
func UpsertOrgTariffs(
	ctx context.Context,
	pool *pgxpool.Pool,
	organizationID string,
	payload json.RawMessage,
) error {
	if pool == nil {
		return fmt.Errorf("storage: nil pool")
	}
	if organizationID == "" {
		return fmt.Errorf("storage: empty organization_id")
	}
	if len(payload) == 0 {
		return fmt.Errorf("storage: empty tariffs payload")
	}
	const stmt = `
		INSERT INTO organization_tariffs (organization_id, tariffs, updated_at)
		VALUES ($1, $2::jsonb, now())
		ON CONFLICT (organization_id) DO UPDATE SET
			tariffs    = EXCLUDED.tariffs,
			updated_at = now()
	`
	if _, err := pool.Exec(ctx, stmt, organizationID, []byte(payload)); err != nil {
		return fmt.Errorf("storage: upsert org tariffs: %w", err)
	}
	return nil
}
