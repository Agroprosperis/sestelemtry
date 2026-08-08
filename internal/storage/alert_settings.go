package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// InitAlertSettingsSchema creates the alert settings tables
// (idempotent). Mirrors migrations/011_alert_settings.sql so a fresh
// environment boots without an external migration step, the same way
// the tariffs and weather schemas do.
func InitAlertSettingsSchema(ctx context.Context, pool *pgxpool.Pool) error {
	if pool == nil {
		return fmt.Errorf("storage: nil pool")
	}
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS alert_settings (
			id            smallint PRIMARY KEY DEFAULT 1 CHECK (id = 1),
			settings      jsonb NOT NULL,
			smtp_password text NOT NULL DEFAULT '',
			updated_at    timestamptz NOT NULL DEFAULT now()
		)`,
		`CREATE TABLE IF NOT EXISTS organization_alert_settings (
			organization_id text PRIMARY KEY,
			settings        jsonb NOT NULL,
			updated_at      timestamptz NOT NULL DEFAULT now()
		)`,
	}
	for _, s := range stmts {
		if _, err := pool.Exec(ctx, s); err != nil {
			return fmt.Errorf("storage: exec alert settings schema: %w", err)
		}
	}
	return nil
}

// GetAlertSettings returns the stored settings blob and whether an SMTP
// password has been saved. The bool `ok` is false when nothing has been
// saved yet, which the caller turns into the config.yaml fallback.
//
// The password itself is deliberately not returned: this is the query
// the API uses, and it must be impossible for a settings response to
// carry the secret. GetSMTPPassword is the separate, explicit path for
// the two callers that actually need to authenticate to a mail server.
func GetAlertSettings(
	ctx context.Context,
	pool *pgxpool.Pool,
) (payload json.RawMessage, passwordSet bool, ok bool, err error) {
	if pool == nil {
		return nil, false, false, fmt.Errorf("storage: nil pool")
	}
	var raw []byte
	err = pool.QueryRow(ctx, `
		SELECT settings, smtp_password <> ''
		FROM alert_settings
		WHERE id = 1
	`).Scan(&raw, &passwordSet)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, false, nil
		}
		return nil, false, false, fmt.Errorf("storage: query alert settings: %w", err)
	}
	return json.RawMessage(raw), passwordSet, true, nil
}

// GetSMTPPassword returns the stored mail password, empty when none was
// saved. Kept apart from GetAlertSettings so every use of the secret is
// an explicit call a reviewer can grep for.
func GetSMTPPassword(ctx context.Context, pool *pgxpool.Pool) (string, error) {
	if pool == nil {
		return "", fmt.Errorf("storage: nil pool")
	}
	var password string
	err := pool.QueryRow(ctx, `SELECT smtp_password FROM alert_settings WHERE id = 1`).Scan(&password)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		return "", fmt.Errorf("storage: query smtp password: %w", err)
	}
	return password, nil
}

// UpsertAlertSettings stores the settings blob.
//
// A nil `password` leaves the stored password untouched — that is how
// the API handles a request that omits the field, so an operator editing
// the recipient list does not have to retype the mail password (and the
// UI never has to hold it). A non-nil empty string clears it.
func UpsertAlertSettings(
	ctx context.Context,
	pool *pgxpool.Pool,
	payload json.RawMessage,
	password *string,
) error {
	if pool == nil {
		return fmt.Errorf("storage: nil pool")
	}
	if len(payload) == 0 {
		return fmt.Errorf("storage: empty alert settings payload")
	}
	const stmt = `
		INSERT INTO alert_settings (id, settings, smtp_password, updated_at)
		VALUES (1, $1::jsonb, COALESCE($2, ''), now())
		ON CONFLICT (id) DO UPDATE SET
			settings      = EXCLUDED.settings,
			smtp_password = COALESCE($2, alert_settings.smtp_password),
			updated_at    = now()
	`
	if _, err := pool.Exec(ctx, stmt, []byte(payload), password); err != nil {
		return fmt.Errorf("storage: upsert alert settings: %w", err)
	}
	return nil
}

// OrgAlertSettingsRow is one organization's stored override.
type OrgAlertSettingsRow struct {
	OrganizationID string
	Settings       json.RawMessage
}

// LoadOrgAlertSettings returns every stored per-organization override.
// The set has at most one row per configured site, so the watchdog reads
// it whole on each check rather than querying per organization.
func LoadOrgAlertSettings(ctx context.Context, pool *pgxpool.Pool) ([]OrgAlertSettingsRow, error) {
	if pool == nil {
		return nil, fmt.Errorf("storage: nil pool")
	}
	rows, err := pool.Query(ctx, `
		SELECT organization_id, settings
		FROM organization_alert_settings
	`)
	if err != nil {
		return nil, fmt.Errorf("storage: query organization alert settings: %w", err)
	}
	defer rows.Close()

	out := make([]OrgAlertSettingsRow, 0, 8)
	for rows.Next() {
		var (
			id  string
			raw []byte
		)
		if err := rows.Scan(&id, &raw); err != nil {
			return nil, fmt.Errorf("storage: scan organization alert settings: %w", err)
		}
		out = append(out, OrgAlertSettingsRow{OrganizationID: id, Settings: json.RawMessage(raw)})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: iterate organization alert settings: %w", err)
	}
	return out, nil
}

// GetOrgAlertSettings returns one organization's override. ok is false
// when the operator has never customized this organization.
func GetOrgAlertSettings(
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
	var raw []byte
	err := pool.QueryRow(ctx, `
		SELECT settings
		FROM organization_alert_settings
		WHERE organization_id = $1
	`, organizationID).Scan(&raw)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("storage: query organization alert settings: %w", err)
	}
	return json.RawMessage(raw), true, nil
}

// UpsertOrgAlertSettings stores one organization's override
// (last-writer-wins).
func UpsertOrgAlertSettings(
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
		return fmt.Errorf("storage: empty organization alert settings payload")
	}
	const stmt = `
		INSERT INTO organization_alert_settings (organization_id, settings, updated_at)
		VALUES ($1, $2::jsonb, now())
		ON CONFLICT (organization_id) DO UPDATE SET
			settings   = EXCLUDED.settings,
			updated_at = now()
	`
	if _, err := pool.Exec(ctx, stmt, organizationID, []byte(payload)); err != nil {
		return fmt.Errorf("storage: upsert organization alert settings: %w", err)
	}
	return nil
}
