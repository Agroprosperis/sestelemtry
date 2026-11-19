package storage

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nesh/sestelemetry/internal/outages"
)

// InitOutagesSchema creates the e-svitlo outage tables (idempotent). The
// API process owns them: it runs the sync and serves settings + day chart. Mirrored by migrations/027_outages.sql.
func InitOutagesSchema(ctx context.Context, pool *pgxpool.Pool) error {
	if pool == nil {
		return fmt.Errorf("storage: nil pool")
	}
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS outage_accounts (
			host       text PRIMARY KEY,
			login      text NOT NULL DEFAULT '',
			password   text NOT NULL DEFAULT '',
			updated_at timestamptz NOT NULL DEFAULT now()
		)`,
		`CREATE TABLE IF NOT EXISTS outage_points (
			organization_id text PRIMARY KEY,
			eic             text NOT NULL DEFAULT '',
			host            text NOT NULL DEFAULT '',
			updated_at      timestamptz NOT NULL DEFAULT now()
		)`,
		`CREATE TABLE IF NOT EXISTS outage_intervals (
			organization_id text NOT NULL,
			start_at        timestamptz NOT NULL,
			end_at          timestamptz NOT NULL,
			kind            text NOT NULL,
			PRIMARY KEY (organization_id, start_at, kind)
		)`,
		`CREATE INDEX IF NOT EXISTS outage_intervals_org_range_idx
			ON outage_intervals (organization_id, start_at, end_at)`,
		`CREATE TABLE IF NOT EXISTS outage_reads (
			organization_id text PRIMARY KEY,
			attempted_at    timestamptz,
			succeeded_at    timestamptz,
			error           text NOT NULL DEFAULT ''
		)`,
	}
	for _, stmt := range stmts {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("storage: init outages schema: %w", err)
		}
	}
	return nil
}

// OutagesStore keeps cabinet accounts, station EICs and planned intervals.
// It implements outages.Store.
type OutagesStore struct {
	pool *pgxpool.Pool
}

// NewOutagesStore wraps pool.
func NewOutagesStore(pool *pgxpool.Pool) *OutagesStore {
	return &OutagesStore{pool: pool}
}

// Accounts returns every known operator row, including empty ones the settings page still shows. It never selects the password.
func (s *OutagesStore) Accounts(ctx context.Context) ([]outages.AccountRow, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT host, login, password <> ''
		FROM outage_accounts`)
	if err != nil {
		return nil, fmt.Errorf("storage: outage accounts: %w", err)
	}
	defer rows.Close()
	byHost := map[string]outages.AccountRow{}
	for rows.Next() {
		var a outages.AccountRow
		if err := rows.Scan(&a.Host, &a.Login, &a.PasswordConfigured); err != nil {
			return nil, fmt.Errorf("storage: scan outage account: %w", err)
		}
		byHost[a.Host] = a
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]outages.AccountRow, 0, len(outages.Operators))
	for _, op := range outages.Operators {
		if a, ok := byHost[op.Host]; ok {
			out = append(out, a)
			continue
		}
		out = append(out, outages.AccountRow{Account: outages.Account{Host: op.Host}})
	}
	return out, nil
}

// Password returns the stored password for host, "" when there is none.
func (s *OutagesStore) Password(ctx context.Context, host string) (string, error) {
	var password string
	err := s.pool.QueryRow(ctx, `SELECT password FROM outage_accounts WHERE host = $1`, host).Scan(&password)
	if err == pgx.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("storage: outage password: %w", err)
	}
	return password, nil
}

// SaveAccount stores one cabinet. A nil password leaves the stored one untouched; an empty string clears it.
func (s *OutagesStore) SaveAccount(ctx context.Context, a outages.Account, password *string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO outage_accounts (host, login, password, updated_at)
		VALUES ($1, $2, COALESCE($3, ''), now())
		ON CONFLICT (host) DO UPDATE SET
			login      = EXCLUDED.login,
			password   = COALESCE($3, outage_accounts.password),
			updated_at = now()`, a.Host, a.Login, password)
	if err != nil {
		return fmt.Errorf("storage: save outage account: %w", err)
	}
	return nil
}

// Points returns every station metering point that has been saved.
func (s *OutagesStore) Points(ctx context.Context) ([]outages.Point, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT organization_id, eic, host FROM outage_points
		WHERE eic <> '' AND host <> ''`)
	if err != nil {
		return nil, fmt.Errorf("storage: outage points: %w", err)
	}
	defer rows.Close()
	var out []outages.Point
	for rows.Next() {
		var p outages.Point
		if err := rows.Scan(&p.OrganizationID, &p.EIC, &p.Host); err != nil {
			return nil, fmt.Errorf("storage: scan outage point: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Point returns one station's metering point and its last read.
func (s *OutagesStore) Point(ctx context.Context, organizationID string) (outages.Point, outages.Read, bool, error) {
	var (
		p                    outages.Point
		read                 outages.Read
		errMsg               string
		attempted, succeeded *time.Time
	)
	err := s.pool.QueryRow(ctx, `
		SELECT p.organization_id, p.eic, p.host,
		       r.attempted_at, r.succeeded_at, COALESCE(r.error, '')
		FROM outage_points p
		LEFT JOIN outage_reads r ON r.organization_id = p.organization_id
		WHERE p.organization_id = $1`, organizationID).Scan(
		&p.OrganizationID, &p.EIC, &p.Host, &attempted, &succeeded, &errMsg)
	if err == pgx.ErrNoRows {
		return outages.Point{OrganizationID: organizationID}, outages.Read{}, false, nil
	}
	if err != nil {
		return outages.Point{}, outages.Read{}, false, fmt.Errorf("storage: outage point: %w", err)
	}
	if attempted != nil {
		read.AttemptedAt = *attempted
	}
	if succeeded != nil {
		read.SucceededAt = *succeeded
	}
	read.Error = errMsg
	return p, read, true, nil
}

// SavePoint stores one station's EIC and operator. Empty eic clears the point (keeps the row so the passport can show blanks).
func (s *OutagesStore) SavePoint(ctx context.Context, p outages.Point) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO outage_points (organization_id, eic, host, updated_at)
		VALUES ($1, $2, $3, now())
		ON CONFLICT (organization_id) DO UPDATE SET
			eic        = EXCLUDED.eic,
			host       = EXCLUDED.host,
			updated_at = now()`, p.OrganizationID, p.EIC, p.Host)
	if err != nil {
		return fmt.Errorf("storage: save outage point: %w", err)
	}
	return nil
}

// ReplaceIntervals writes a fresh set of intervals for the organization and records a successful read. Prior intervals for that org are dropped.
func (s *OutagesStore) ReplaceIntervals(ctx context.Context, organizationID string, list []outages.Interval, read outages.Read) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("storage: begin outage intervals: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `DELETE FROM outage_intervals WHERE organization_id = $1`, organizationID); err != nil {
		return fmt.Errorf("storage: clear outage intervals: %w", err)
	}
	if len(list) > 0 {
		orgs := make([]string, len(list))
		starts := make([]time.Time, len(list))
		ends := make([]time.Time, len(list))
		kinds := make([]string, len(list))
		for i, iv := range list {
			orgs[i] = organizationID
			starts[i] = iv.Start
			ends[i] = iv.End
			kinds[i] = string(iv.Kind)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO outage_intervals (organization_id, start_at, end_at, kind)
			SELECT o, s, e, k
			FROM unnest($1::text[], $2::timestamptz[], $3::timestamptz[], $4::text[]) AS t(o, s, e, k)`,
			orgs, starts, ends, kinds); err != nil {
			return fmt.Errorf("storage: insert outage intervals: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO outage_reads (organization_id, attempted_at, succeeded_at, error)
		VALUES ($1, $2, $3, '')
		ON CONFLICT (organization_id) DO UPDATE SET
			attempted_at = EXCLUDED.attempted_at,
			succeeded_at = EXCLUDED.succeeded_at,
			error        = ''`, organizationID, read.AttemptedAt, read.SucceededAt); err != nil {
		return fmt.Errorf("storage: save outage read: %w", err)
	}
	return tx.Commit(ctx)
}

// MarkRead records a failed (or empty) attempt without touching intervals.
func (s *OutagesStore) MarkRead(ctx context.Context, organizationID string, read outages.Read) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO outage_reads (organization_id, attempted_at, succeeded_at, error)
		VALUES ($1, $2, NULLIF($3, '0001-01-01'::timestamptz), $4)
		ON CONFLICT (organization_id) DO UPDATE SET
			attempted_at = EXCLUDED.attempted_at,
			succeeded_at = COALESCE(EXCLUDED.succeeded_at, outage_reads.succeeded_at),
			error        = EXCLUDED.error`,
		organizationID, read.AttemptedAt, read.SucceededAt, read.Error)
	if err != nil {
		return fmt.Errorf("storage: mark outage read: %w", err)
	}
	return nil
}

// Intervals returns planned outages for organization overlapping [from, to].
func (s *OutagesStore) Intervals(ctx context.Context, organizationID string, from, to time.Time) ([]outages.Interval, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT start_at, end_at, kind
		FROM outage_intervals
		WHERE organization_id = $1
		  AND start_at < $3
		  AND end_at > $2
		ORDER BY start_at, kind`, organizationID, from, to)
	if err != nil {
		return nil, fmt.Errorf("storage: outage intervals: %w", err)
	}
	defer rows.Close()
	var out []outages.Interval
	for rows.Next() {
		var (
			iv   outages.Interval
			kind string
		)
		if err := rows.Scan(&iv.Start, &iv.End, &kind); err != nil {
			return nil, fmt.Errorf("storage: scan outage interval: %w", err)
		}
		iv.Kind = outages.Kind(kind)
		out = append(out, iv)
	}
	return out, rows.Err()
}
