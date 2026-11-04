package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nesh/sestelemetry/internal/fxrates"
)

// InitFxRatesSchema creates the exchange-rate tables (idempotent). The
// API process owns them: it runs the sync and serves the settings page.
// Mirrored by migrations/024_fx_rates.sql.
func InitFxRatesSchema(ctx context.Context, pool *pgxpool.Pool) error {
	if pool == nil {
		return fmt.Errorf("storage: nil pool")
	}
	stmts := []string{
		// Where the ERP rates service is, edited on the «Курси валют ERP»
		// page: one row. The password has its own column and is never
		// returned by the API, only whether one is stored.
		`CREATE TABLE IF NOT EXISTS fx_settings (
			id         smallint PRIMARY KEY DEFAULT 1 CHECK (id = 1),
			settings   jsonb NOT NULL,
			password   text NOT NULL DEFAULT '',
			updated_at timestamptz NOT NULL DEFAULT now()
		)`,
		// One rate per civil day (Europe/Kyiv) and currency, UAH per unit.
		// fetched_at tells a day's final rate (fetched at or after its
		// noon) from today's running one.
		`CREATE TABLE IF NOT EXISTS fx_rates (
			day          date NOT NULL,
			currency     text NOT NULL,
			uah_per_unit double precision NOT NULL,
			fetched_at   timestamptz NOT NULL DEFAULT now(),
			PRIMARY KEY (day, currency)
		)`,
	}
	for _, stmt := range stmts {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("storage: init fx rates schema: %w", err)
		}
	}
	return nil
}

// FxRatesStore keeps the rates and the settings of their source. It
// implements fxrates.Store.
type FxRatesStore struct {
	pool *pgxpool.Pool
}

// NewFxRatesStore wraps pool.
func NewFxRatesStore(pool *pgxpool.Pool) *FxRatesStore {
	return &FxRatesStore{pool: pool}
}

// Settings returns the service settings, whether a password is stored,
// and whether anything was saved. It never selects the password: that is
// Password, so every read of the secret is a call a reviewer can grep.
func (s *FxRatesStore) Settings(ctx context.Context) (fxrates.Settings, bool, bool, error) {
	var (
		raw         []byte
		passwordSet bool
	)
	err := s.pool.QueryRow(ctx, `SELECT settings, password <> '' FROM fx_settings WHERE id = 1`).Scan(&raw, &passwordSet)
	if errors.Is(err, pgx.ErrNoRows) {
		return fxrates.Settings{}, false, false, nil
	}
	if err != nil {
		return fxrates.Settings{}, false, false, fmt.Errorf("storage: fx settings: %w", err)
	}
	var out fxrates.Settings
	if err := json.Unmarshal(raw, &out); err != nil {
		return fxrates.Settings{}, false, false, fmt.Errorf("storage: decode fx settings: %w", err)
	}
	return out, passwordSet, true, nil
}

// Password returns the stored password, "" when there is none.
func (s *FxRatesStore) Password(ctx context.Context) (string, error) {
	var password string
	err := s.pool.QueryRow(ctx, `SELECT password FROM fx_settings WHERE id = 1`).Scan(&password)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("storage: fx password: %w", err)
	}
	return password, nil
}

// SaveSettings stores the service settings. A nil password leaves the
// stored one untouched; an empty string clears it.
func (s *FxRatesStore) SaveSettings(ctx context.Context, settings fxrates.Settings, password *string) error {
	payload, err := json.Marshal(settings)
	if err != nil {
		return fmt.Errorf("storage: encode fx settings: %w", err)
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO fx_settings (id, settings, password, updated_at)
		VALUES (1, $1::jsonb, COALESCE($2, ''), now())
		ON CONFLICT (id) DO UPDATE SET
			settings   = EXCLUDED.settings,
			password   = COALESCE($2, fx_settings.password),
			updated_at = now()`, payload, password)
	if err != nil {
		return fmt.Errorf("storage: save fx settings: %w", err)
	}
	return nil
}

// FetchTimes returns, for each civil date in [from, to] that has a rate
// for every one of fxrates.Currencies, the earliest of their fetch times.
func (s *FxRatesStore) FetchTimes(ctx context.Context, from, to time.Time) (map[string]time.Time, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT day, min(fetched_at)
		FROM fx_rates
		WHERE day >= $1::date AND day <= $2::date AND currency = ANY($3)
		GROUP BY day
		HAVING count(DISTINCT currency) = $4`,
		from.Format("2006-01-02"), to.Format("2006-01-02"), fxrates.Currencies, len(fxrates.Currencies))
	if err != nil {
		return nil, fmt.Errorf("storage: query fx fetch times: %w", err)
	}
	defer rows.Close()
	out := map[string]time.Time{}
	for rows.Next() {
		var (
			day time.Time
			at  time.Time
		)
		if err := rows.Scan(&day, &at); err != nil {
			return nil, fmt.Errorf("storage: scan fx fetch time: %w", err)
		}
		out[day.Format("2006-01-02")] = at
	}
	return out, rows.Err()
}

// SaveRates upserts rates in one round trip.
func (s *FxRatesStore) SaveRates(ctx context.Context, rates []fxrates.Rate) error {
	if len(rates) == 0 {
		return nil
	}
	days := make([]string, 0, len(rates))
	currencies := make([]string, 0, len(rates))
	values := make([]float64, 0, len(rates))
	for _, r := range rates {
		days = append(days, r.Day.Format("2006-01-02"))
		currencies = append(currencies, r.Currency)
		values = append(values, r.UahPerUnit)
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO fx_rates (day, currency, uah_per_unit, fetched_at)
		SELECT d::date, c, v, now()
		FROM unnest($1::text[], $2::text[], $3::double precision[]) AS t(d, c, v)
		ON CONFLICT (day, currency) DO UPDATE
		SET uah_per_unit = EXCLUDED.uah_per_unit,
		    fetched_at   = EXCLUDED.fetched_at`, days, currencies, values)
	if err != nil {
		return fmt.Errorf("storage: save fx rates: %w", err)
	}
	return nil
}

// Rates returns every stored rate of one currency keyed by civil date.
func (s *FxRatesStore) Rates(ctx context.Context, currency string) (map[string]float64, error) {
	rows, err := s.pool.Query(ctx, `SELECT day, uah_per_unit FROM fx_rates WHERE currency = $1`, currency)
	if err != nil {
		return nil, fmt.Errorf("storage: query fx rates: %w", err)
	}
	defer rows.Close()
	out := map[string]float64{}
	for rows.Next() {
		var (
			day  time.Time
			rate float64
		)
		if err := rows.Scan(&day, &rate); err != nil {
			return nil, fmt.Errorf("storage: scan fx rate: %w", err)
		}
		out[day.Format("2006-01-02")] = rate
	}
	return out, rows.Err()
}

// FxCoverage is how far one currency's rates reach.
type FxCoverage struct {
	Currency    string
	FirstDay    time.Time
	LastDay     time.Time
	Days        int
	LastFetched time.Time
}

// Coverage reports, per currency with any rate, the first and last day,
// how many days have a rate, and the latest fetch.
func (s *FxRatesStore) Coverage(ctx context.Context) ([]FxCoverage, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT currency, min(day), max(day), count(*), max(fetched_at)
		FROM fx_rates GROUP BY currency ORDER BY currency`)
	if err != nil {
		return nil, fmt.Errorf("storage: query fx coverage: %w", err)
	}
	defer rows.Close()
	var out []FxCoverage
	for rows.Next() {
		var c FxCoverage
		if err := rows.Scan(&c.Currency, &c.FirstDay, &c.LastDay, &c.Days, &c.LastFetched); err != nil {
			return nil, fmt.Errorf("storage: scan fx coverage: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
