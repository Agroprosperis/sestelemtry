package storage

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nesh/sestelemetry/internal/outages"
)

// InitOutagesSchema creates the e-svitlo outage tables (idempotent). The
// API process owns them: it runs the hourly read, serves the passport's
// account and the day chart's bands. Mirrored by migrations/027_outages.sql.
func InitOutagesSchema(ctx context.Context, pool *pgxpool.Pool) error {
	if pool == nil {
		return fmt.Errorf("storage: nil pool")
	}
	stmts := []string{
		// A station's cabinet, set on its passport. The password has its
		// own column and is never returned by the API, only whether one is
		// stored.
		`CREATE TABLE IF NOT EXISTS outage_station_accounts (
			organization_id text PRIMARY KEY,
			host            text NOT NULL,
			login           text NOT NULL,
			password        text NOT NULL DEFAULT '',
			eic             text NOT NULL DEFAULT '',
			updated_at      timestamptz NOT NULL DEFAULT now()
		)`,
		// The station's planned outages. A read replaces those from the
		// start of its day on; earlier days stay, so the schedule can be
		// held against what the telemetry saw.
		`CREATE TABLE IF NOT EXISTS outage_intervals (
			organization_id text NOT NULL,
			start_at        timestamptz NOT NULL,
			end_at          timestamptz NOT NULL,
			kind            text NOT NULL,
			PRIMARY KEY (organization_id, start_at, kind)
		)`,
		`CREATE INDEX IF NOT EXISTS outage_intervals_org_range_idx
			ON outage_intervals (organization_id, start_at, end_at)`,
		// The last read of each station, success or failure.
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

// OutagesStore keeps the stations' cabinet accounts, their planned
// outages and the outcome of the last read. It implements outages.Store.
type OutagesStore struct {
	pool *pgxpool.Pool
}

// NewOutagesStore wraps pool.
func NewOutagesStore(pool *pgxpool.Pool) *OutagesStore {
	return &OutagesStore{pool: pool}
}

// Accounts returns every station's account. It never selects the
// password: that is Password, so every read of the secret is a call a
// reviewer can grep.
func (s *OutagesStore) Accounts(ctx context.Context) ([]outages.AccountRow, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT organization_id, host, login, eic, password <> ''
		FROM outage_station_accounts
		ORDER BY organization_id`)
	if err != nil {
		return nil, fmt.Errorf("storage: outage accounts: %w", err)
	}
	defer rows.Close()
	var out []outages.AccountRow
	for rows.Next() {
		var a outages.AccountRow
		if err := rows.Scan(&a.OrganizationID, &a.Host, &a.Login, &a.EIC, &a.PasswordConfigured); err != nil {
			return nil, fmt.Errorf("storage: scan outage account: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// Account returns one station's account and its last read; ok is false
// when the station has none.
func (s *OutagesStore) Account(ctx context.Context, organizationID string) (outages.AccountRow, outages.Read, bool, error) {
	var a outages.AccountRow
	err := s.pool.QueryRow(ctx, `
		SELECT organization_id, host, login, eic, password <> ''
		FROM outage_station_accounts
		WHERE organization_id = $1`, organizationID).Scan(&a.OrganizationID, &a.Host, &a.Login, &a.EIC, &a.PasswordConfigured)
	if errors.Is(err, pgx.ErrNoRows) {
		read, rerr := s.read(ctx, organizationID)
		return outages.AccountRow{Account: outages.Account{OrganizationID: organizationID}}, read, false, rerr
	}
	if err != nil {
		return outages.AccountRow{}, outages.Read{}, false, fmt.Errorf("storage: outage account: %w", err)
	}
	read, err := s.read(ctx, organizationID)
	return a, read, true, err
}

func (s *OutagesStore) read(ctx context.Context, organizationID string) (outages.Read, error) {
	var (
		read                 outages.Read
		attempted, succeeded *time.Time
	)
	err := s.pool.QueryRow(ctx, `
		SELECT attempted_at, succeeded_at, error FROM outage_reads WHERE organization_id = $1`,
		organizationID).Scan(&attempted, &succeeded, &read.Error)
	if errors.Is(err, pgx.ErrNoRows) {
		return outages.Read{}, nil
	}
	if err != nil {
		return outages.Read{}, fmt.Errorf("storage: outage read: %w", err)
	}
	if attempted != nil {
		read.AttemptedAt = *attempted
	}
	if succeeded != nil {
		read.SucceededAt = *succeeded
	}
	return read, nil
}

// Password returns the station's stored password, "" when there is none.
func (s *OutagesStore) Password(ctx context.Context, organizationID string) (string, error) {
	var password string
	err := s.pool.QueryRow(ctx, `SELECT password FROM outage_station_accounts WHERE organization_id = $1`, organizationID).Scan(&password)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("storage: outage password: %w", err)
	}
	return password, nil
}

// SaveAccount stores a station's account. A nil password leaves the
// stored one untouched; an empty string clears it.
func (s *OutagesStore) SaveAccount(ctx context.Context, a outages.Account, password *string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO outage_station_accounts (organization_id, host, login, password, eic, updated_at)
		VALUES ($1, $2, $3, COALESCE($4, ''), $5, now())
		ON CONFLICT (organization_id) DO UPDATE SET
			host       = EXCLUDED.host,
			login      = EXCLUDED.login,
			password   = COALESCE($4, outage_station_accounts.password),
			eic        = EXCLUDED.eic,
			updated_at = now()`,
		a.OrganizationID, a.Host, a.Login, password, a.EIC)
	if err != nil {
		return fmt.Errorf("storage: save outage account: %w", err)
	}
	return nil
}

// DeleteAccount takes a station off the hourly read. Its outages already
// read stay.
func (s *OutagesStore) DeleteAccount(ctx context.Context, organizationID string) error {
	if _, err := s.pool.Exec(ctx, `DELETE FROM outage_station_accounts WHERE organization_id = $1`, organizationID); err != nil {
		return fmt.Errorf("storage: delete outage account: %w", err)
	}
	return nil
}

// ReplaceIntervals stores a successful read: the station's intervals
// that start at or after from are replaced by list, earlier ones stay as
// history, and the read is recorded.
func (s *OutagesStore) ReplaceIntervals(ctx context.Context, organizationID string, from time.Time, list []outages.Interval, read outages.Read) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("storage: begin outage intervals: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `DELETE FROM outage_intervals WHERE organization_id = $1 AND start_at >= $2`, organizationID, from); err != nil {
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
		// One that began before from is still stored; the read may have
		// moved its end.
		if _, err := tx.Exec(ctx, `
			INSERT INTO outage_intervals (organization_id, start_at, end_at, kind)
			SELECT o, s, e, k
			FROM unnest($1::text[], $2::timestamptz[], $3::timestamptz[], $4::text[]) AS t(o, s, e, k)
			ON CONFLICT (organization_id, start_at, kind) DO UPDATE SET end_at = EXCLUDED.end_at`,
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

// MarkRead records a failed attempt; the intervals and the time of the
// last success stay.
func (s *OutagesStore) MarkRead(ctx context.Context, organizationID string, read outages.Read) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO outage_reads (organization_id, attempted_at, error)
		VALUES ($1, $2, $3)
		ON CONFLICT (organization_id) DO UPDATE SET
			attempted_at = EXCLUDED.attempted_at,
			error        = EXCLUDED.error`,
		organizationID, read.AttemptedAt, read.Error)
	if err != nil {
		return fmt.Errorf("storage: mark outage read: %w", err)
	}
	return nil
}

// Intervals returns the station's planned outages overlapping [from, to).
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
