package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nesh/sestelemetry/internal/pvplan"
)

// InitPvPlanSchema creates the PV plan tables (idempotent). The API
// process owns them, so cmd/api/main.go runs this at startup alongside
// the economics bootstrap. Mirrored by migrations/015_pv_plan_daily.sql
// and 023_pv_plan_model.sql.
func InitPvPlanSchema(ctx context.Context, pool *pgxpool.Pool) error {
	if pool == nil {
		return fmt.Errorf("storage: nil pool")
	}
	stmts := []string{
		// The n8n source's day totals. planned_kwh = 0 records that the
		// upstream flow was asked and had no forecast for that day.
		// Keeping the row (rather than leaving a hole) is what stops
		// every dashboard load from re-asking for the same missing
		// history; fetched_at lets the caller retry it on a slow cadence
		// in case the flow backfills.
		`CREATE TABLE IF NOT EXISTS pv_plan_daily (
			organization_id text NOT NULL,
			day             date NOT NULL,
			planned_kwh     double precision NOT NULL,
			fetched_at      timestamptz NOT NULL DEFAULT now(),
			PRIMARY KEY (organization_id, day)
		)`,
		// The station passport: which source plans it, and the
		// station-level inputs of the model. Coordinates left NULL fall
		// back to config.yaml.
		`CREATE TABLE IF NOT EXISTS pv_plan_config (
			organization_id       text PRIMARY KEY,
			source                text NOT NULL DEFAULT 'n8n',
			latitude              double precision,
			longitude             double precision,
			pac_limit_kw          double precision NOT NULL DEFAULT 0,
			gamma_pct_per_c       double precision NOT NULL DEFAULT 0,
			whole_elevator_from   date,
			history_backfilled_at timestamptz,
			updated_at            timestamptz NOT NULL DEFAULT now(),
			updated_by            text NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS pv_orientations (
			organization_id text NOT NULL REFERENCES pv_plan_config (organization_id) ON DELETE CASCADE,
			idx             integer NOT NULL,
			tilt_deg        double precision NOT NULL,
			azimuth_deg     double precision NOT NULL,
			modules_count   integer NOT NULL,
			module_power_w  double precision NOT NULL,
			enabled         boolean NOT NULL DEFAULT true,
			note            text NOT NULL DEFAULT '',
			PRIMARY KEY (organization_id, idx)
		)`,
		`CREATE TABLE IF NOT EXISTS pv_orientations_history (
			id              bigserial PRIMARY KEY,
			organization_id text NOT NULL,
			changed_at      timestamptz NOT NULL DEFAULT now(),
			changed_by      text NOT NULL DEFAULT '',
			changes         jsonb NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS pv_orientations_history_org_idx
			ON pv_orientations_history (organization_id, changed_at DESC)`,
		// The model's hourly plans. kind is day_ahead (frozen the evening
		// before; plan-vs-actual) or latest (freshest; control).
		`CREATE TABLE IF NOT EXISTS pv_plan_hourly (
			organization_id text NOT NULL,
			day             date NOT NULL,
			kind            text NOT NULL,
			hour            integer NOT NULL,
			planned_kwh     double precision NOT NULL,
			model_version   text NOT NULL,
			computed_at     timestamptz NOT NULL DEFAULT now(),
			PRIMARY KEY (organization_id, day, kind, hour)
		)`,
		// Where the plan's upstreams are, edited on the «Прогноз СЕС»
		// page: one row. The Open-Meteo key has its own column and is
		// never returned by the API, only whether one is stored.
		`CREATE TABLE IF NOT EXISTS pv_plan_settings (
			id             smallint PRIMARY KEY DEFAULT 1 CHECK (id = 1),
			settings       jsonb NOT NULL,
			open_meteo_key text NOT NULL DEFAULT '',
			updated_at     timestamptz NOT NULL DEFAULT now()
		)`,
	}
	for _, stmt := range stmts {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("storage: init pv plan schema: %w", err)
		}
	}
	return nil
}

// PvPlanDay is one cached site-day of planned generation. PlannedKwh
// of 0 means "asked, upstream had nothing" (see InitPvPlanSchema).
type PvPlanDay struct {
	Day        time.Time
	PlannedKwh float64
	FetchedAt  time.Time
}

// ListPvPlanDays returns the cached plan rows for the inclusive civil
// date span [fromDay, toDay], ordered by day. Days are returned as UTC
// midnight instants (Postgres `date` semantics) — the caller compares
// them by calendar date, not by instant.
func ListPvPlanDays(ctx context.Context, pool *pgxpool.Pool, organizationID string, fromDay, toDay time.Time) ([]PvPlanDay, error) {
	if pool == nil {
		return nil, fmt.Errorf("storage: nil pool")
	}
	if organizationID == "" {
		return nil, fmt.Errorf("storage: empty organization_id")
	}
	rows, err := pool.Query(ctx, `
		SELECT day, planned_kwh, fetched_at
		FROM pv_plan_daily
		WHERE organization_id = $1 AND day >= $2::date AND day <= $3::date
		ORDER BY day ASC
	`, organizationID, fromDay, toDay)
	if err != nil {
		return nil, fmt.Errorf("storage: query pv plan daily: %w", err)
	}
	defer rows.Close()
	var out []PvPlanDay
	for rows.Next() {
		var r PvPlanDay
		if err := rows.Scan(&r.Day, &r.PlannedKwh, &r.FetchedAt); err != nil {
			return nil, fmt.Errorf("storage: scan pv plan daily: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: iterate pv plan daily: %w", err)
	}
	return out, nil
}

// UpsertPvPlanDays writes the given days for one organization in a
// single round trip, overwriting whatever was cached for them.
func UpsertPvPlanDays(ctx context.Context, pool *pgxpool.Pool, organizationID string, days []PvPlanDay) error {
	if pool == nil {
		return fmt.Errorf("storage: nil pool")
	}
	if organizationID == "" {
		return fmt.Errorf("storage: empty organization_id")
	}
	if len(days) == 0 {
		return nil
	}
	dayVals := make([]time.Time, 0, len(days))
	kwhVals := make([]float64, 0, len(days))
	for _, d := range days {
		dayVals = append(dayVals, d.Day)
		kwhVals = append(kwhVals, d.PlannedKwh)
	}
	_, err := pool.Exec(ctx, `
		INSERT INTO pv_plan_daily (organization_id, day, planned_kwh, fetched_at)
		SELECT $1, d, k, now()
		FROM unnest($2::date[], $3::double precision[]) AS t(d, k)
		ON CONFLICT (organization_id, day) DO UPDATE
		SET planned_kwh = EXCLUDED.planned_kwh,
		    fetched_at  = EXCLUDED.fetched_at
	`, organizationID, dayVals, kwhVals)
	if err != nil {
		return fmt.Errorf("storage: upsert pv plan daily: %w", err)
	}
	return nil
}

// PvPlanStore keeps station passports and model plans. It implements
// pvplan.Store.
type PvPlanStore struct {
	pool *pgxpool.Pool
}

// NewPvPlanStore wraps pool.
func NewPvPlanStore(pool *pgxpool.Pool) *PvPlanStore {
	return &PvPlanStore{pool: pool}
}

// Settings returns the plan's upstream settings, whether an Open-Meteo
// key is stored, and whether anything was saved. Like GetAISettings it
// never selects the key: that is OpenMeteoKey, so every read of the
// secret is a call a reviewer can grep.
func (s *PvPlanStore) Settings(ctx context.Context) (pvplan.Settings, bool, bool, error) {
	var (
		raw    []byte
		keySet bool
	)
	err := s.pool.QueryRow(ctx, `SELECT settings, open_meteo_key <> '' FROM pv_plan_settings WHERE id = 1`).Scan(&raw, &keySet)
	if errors.Is(err, pgx.ErrNoRows) {
		return pvplan.Settings{}, false, false, nil
	}
	if err != nil {
		return pvplan.Settings{}, false, false, fmt.Errorf("storage: pv plan settings: %w", err)
	}
	var out pvplan.Settings
	if err := json.Unmarshal(raw, &out); err != nil {
		return pvplan.Settings{}, false, false, fmt.Errorf("storage: decode pv plan settings: %w", err)
	}
	return out, keySet, true, nil
}

// OpenMeteoKey returns the stored Open-Meteo key, "" when there is none.
func (s *PvPlanStore) OpenMeteoKey(ctx context.Context) (string, error) {
	var key string
	err := s.pool.QueryRow(ctx, `SELECT open_meteo_key FROM pv_plan_settings WHERE id = 1`).Scan(&key)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("storage: open-meteo key: %w", err)
	}
	return key, nil
}

// SaveSettings stores the upstream settings. A nil key leaves the
// stored one untouched; an empty string clears it.
func (s *PvPlanStore) SaveSettings(ctx context.Context, settings pvplan.Settings, key *string) error {
	payload, err := json.Marshal(settings)
	if err != nil {
		return fmt.Errorf("storage: encode pv plan settings: %w", err)
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO pv_plan_settings (id, settings, open_meteo_key, updated_at)
		VALUES (1, $1::jsonb, COALESCE($2, ''), now())
		ON CONFLICT (id) DO UPDATE SET
			settings       = EXCLUDED.settings,
			open_meteo_key = COALESCE($2, pv_plan_settings.open_meteo_key),
			updated_at     = now()`, payload, key)
	if err != nil {
		return fmt.Errorf("storage: save pv plan settings: %w", err)
	}
	return nil
}

type pvQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// Station returns org's passport.
func (s *PvPlanStore) Station(ctx context.Context, org string) (pvplan.Station, bool, error) {
	return loadPvStation(ctx, s.pool, org, false)
}

func loadPvStation(ctx context.Context, q pvQuerier, org string, forUpdate bool) (pvplan.Station, bool, error) {
	st := pvplan.Station{OrganizationID: org}
	sql := `
		SELECT source, latitude, longitude, pac_limit_kw, gamma_pct_per_c,
		       whole_elevator_from, history_backfilled_at, updated_at, updated_by
		FROM pv_plan_config WHERE organization_id = $1`
	if forUpdate {
		sql += ` FOR UPDATE`
	}
	var (
		source     string
		lat, lon   *float64
		wholeFrom  *time.Time
		backfilled *time.Time
		updatedAt  time.Time
		updatedBy  string
	)
	err := q.QueryRow(ctx, sql, org).Scan(&source, &lat, &lon, &st.PacLimitKw, &st.GammaPctPerC,
		&wholeFrom, &backfilled, &updatedAt, &updatedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return pvplan.Station{}, false, nil
	}
	if err != nil {
		return pvplan.Station{}, false, fmt.Errorf("storage: load pv station: %w", err)
	}
	st.Source = pvplan.Source(source)
	st.Latitude, st.Longitude = lat, lon
	if wholeFrom != nil {
		st.WholeElevatorFrom = wholeFrom.Format("2006-01-02")
	}
	st.HistoryBackfilledAt = backfilled
	st.UpdatedAt = &updatedAt
	st.UpdatedBy = updatedBy

	rows, err := q.Query(ctx, `
		SELECT idx, tilt_deg, azimuth_deg, modules_count, module_power_w, enabled, note
		FROM pv_orientations WHERE organization_id = $1 ORDER BY idx`, org)
	if err != nil {
		return pvplan.Station{}, false, fmt.Errorf("storage: load pv orientations: %w", err)
	}
	defer rows.Close()
	st.Orientations = []pvplan.Orientation{}
	for rows.Next() {
		var o pvplan.Orientation
		if err := rows.Scan(&o.Idx, &o.TiltDeg, &o.AzimuthDeg, &o.ModulesCount, &o.ModulePowerW, &o.Enabled, &o.Note); err != nil {
			return pvplan.Station{}, false, fmt.Errorf("storage: scan pv orientation: %w", err)
		}
		st.Orientations = append(st.Orientations, o)
	}
	if err := rows.Err(); err != nil {
		return pvplan.Station{}, false, fmt.Errorf("storage: iterate pv orientations: %w", err)
	}
	return st, true, nil
}

func wholeElevatorDate(day string) (*time.Time, error) {
	if day == "" {
		return nil, nil
	}
	t, err := time.Parse("2006-01-02", day)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func writePvStation(ctx context.Context, tx pgx.Tx, st pvplan.Station, by string) error {
	wholeFrom, err := wholeElevatorDate(st.WholeElevatorFrom)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO pv_plan_config (organization_id, source, latitude, longitude, pac_limit_kw,
			gamma_pct_per_c, whole_elevator_from, updated_at, updated_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, now(), $8)
		ON CONFLICT (organization_id) DO UPDATE SET
			source = EXCLUDED.source,
			latitude = EXCLUDED.latitude,
			longitude = EXCLUDED.longitude,
			pac_limit_kw = EXCLUDED.pac_limit_kw,
			gamma_pct_per_c = EXCLUDED.gamma_pct_per_c,
			whole_elevator_from = EXCLUDED.whole_elevator_from,
			updated_at = now(),
			updated_by = EXCLUDED.updated_by`,
		st.OrganizationID, string(st.Source), st.Latitude, st.Longitude, st.PacLimitKw,
		st.GammaPctPerC, wholeFrom, by)
	if err != nil {
		return fmt.Errorf("storage: write pv station: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM pv_orientations WHERE organization_id = $1`, st.OrganizationID); err != nil {
		return fmt.Errorf("storage: clear pv orientations: %w", err)
	}
	if len(st.Orientations) == 0 {
		return nil
	}
	var (
		idx, modules       []int32
		tilt, azimuth, pow []float64
		enabled            []bool
		notes              []string
	)
	for _, o := range st.Orientations {
		idx = append(idx, int32(o.Idx))
		tilt = append(tilt, o.TiltDeg)
		azimuth = append(azimuth, o.AzimuthDeg)
		modules = append(modules, int32(o.ModulesCount))
		pow = append(pow, o.ModulePowerW)
		enabled = append(enabled, o.Enabled)
		notes = append(notes, o.Note)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO pv_orientations (organization_id, idx, tilt_deg, azimuth_deg, modules_count,
			module_power_w, enabled, note)
		SELECT $1, i, t, a, m, p, e, n
		FROM unnest($2::int[], $3::float8[], $4::float8[], $5::int[], $6::float8[], $7::bool[], $8::text[])
			AS u(i, t, a, m, p, e, n)`,
		st.OrganizationID, idx, tilt, azimuth, modules, pow, enabled, notes)
	if err != nil {
		return fmt.Errorf("storage: write pv orientations: %w", err)
	}
	return nil
}

func writePvHistory(ctx context.Context, tx pgx.Tx, org, by string, changes []pvplan.Change) error {
	if len(changes) == 0 {
		return nil
	}
	body, err := json.Marshal(changes)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO pv_orientations_history (organization_id, changed_by, changes)
		VALUES ($1, $2, $3)`, org, by, body)
	if err != nil {
		return fmt.Errorf("storage: write pv history: %w", err)
	}
	return nil
}

// SaveStation replaces org's passport and journals the difference, in
// one transaction.
func (s *PvPlanStore) SaveStation(ctx context.Context, st pvplan.Station, by string) (pvplan.Station, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return pvplan.Station{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // superseded by Commit

	before, _, err := loadPvStation(ctx, tx, st.OrganizationID, true)
	if err != nil {
		return pvplan.Station{}, err
	}
	if err := writePvStation(ctx, tx, st, by); err != nil {
		return pvplan.Station{}, err
	}
	if err := writePvHistory(ctx, tx, st.OrganizationID, by, pvplan.Diff(before, st)); err != nil {
		return pvplan.Station{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return pvplan.Station{}, err
	}
	saved, _, err := s.Station(ctx, st.OrganizationID)
	return saved, err
}

// SeedStations writes the passports of stations that have none and
// returns which it wrote. A station that already has a passport is
// left exactly as it is, whatever the seed says.
func (s *PvPlanStore) SeedStations(ctx context.Context, stations []pvplan.Station) ([]string, error) {
	var written []string
	for _, st := range stations {
		ok, err := s.seedStation(ctx, st)
		if err != nil {
			return written, err
		}
		if ok {
			written = append(written, st.OrganizationID)
		}
	}
	return written, nil
}

func (s *PvPlanStore) seedStation(ctx context.Context, st pvplan.Station) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // superseded by Commit

	tag, err := tx.Exec(ctx, `
		INSERT INTO pv_plan_config (organization_id) VALUES ($1)
		ON CONFLICT (organization_id) DO NOTHING`, st.OrganizationID)
	if err != nil {
		return false, fmt.Errorf("storage: seed pv station: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return false, nil
	}
	if err := writePvStation(ctx, tx, st, seedAuthor); err != nil {
		return false, err
	}
	if err := writePvHistory(ctx, tx, st.OrganizationID, seedAuthor, []pvplan.Change{{Field: "seeded"}}); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

// seedAuthor marks rows the startup seed wrote rather than a person.
const seedAuthor = "seed"

// History returns org's passport edits, newest first.
func (s *PvPlanStore) History(ctx context.Context, org string, limit int) ([]pvplan.HistoryEntry, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `
		SELECT changed_at, changed_by, changes FROM pv_orientations_history
		WHERE organization_id = $1 ORDER BY changed_at DESC, id DESC LIMIT $2`, org, limit)
	if err != nil {
		return nil, fmt.Errorf("storage: query pv history: %w", err)
	}
	defer rows.Close()
	out := []pvplan.HistoryEntry{}
	for rows.Next() {
		var (
			e    pvplan.HistoryEntry
			body []byte
		)
		if err := rows.Scan(&e.At, &e.By, &body); err != nil {
			return nil, fmt.Errorf("storage: scan pv history: %w", err)
		}
		if err := json.Unmarshal(body, &e.Changes); err != nil {
			return nil, fmt.Errorf("storage: decode pv history: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// SaveDays replaces the given days' hours of one kind.
func (s *PvPlanStore) SaveDays(ctx context.Context, org string, kind pvplan.Kind, version string, days []pvplan.PlanDay) error {
	if len(days) == 0 {
		return nil
	}
	var (
		dayList []time.Time
		hDays   []time.Time
		hours   []int32
		kwh     []float64
	)
	for _, d := range days {
		day, err := time.Parse("2006-01-02", d.Day)
		if err != nil {
			return fmt.Errorf("storage: pv plan day %q: %w", d.Day, err)
		}
		dayList = append(dayList, day)
		for h, v := range d.Hours {
			hDays = append(hDays, day)
			hours = append(hours, int32(h))
			kwh = append(kwh, v)
		}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // superseded by Commit
	if _, err := tx.Exec(ctx, `
		DELETE FROM pv_plan_hourly
		WHERE organization_id = $1 AND kind = $2 AND day = ANY($3::date[])`,
		org, string(kind), dayList); err != nil {
		return fmt.Errorf("storage: clear pv plan hours: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO pv_plan_hourly (organization_id, day, kind, hour, planned_kwh, model_version, computed_at)
		SELECT $1, d, $2, h, k, $3, now()
		FROM unnest($4::date[], $5::int[], $6::float8[]) AS u(d, h, k)`,
		org, string(kind), version, hDays, hours, kwh); err != nil {
		return fmt.Errorf("storage: write pv plan hours: %w", err)
	}
	return tx.Commit(ctx)
}

// HasDay reports whether org has any hour of `kind` stored for day.
func (s *PvPlanStore) HasDay(ctx context.Context, org, day string, kind pvplan.Kind) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM pv_plan_hourly
			WHERE organization_id = $1 AND day = $2::date AND kind = $3)`,
		org, day, string(kind)).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("storage: check pv plan day: %w", err)
	}
	return ok, nil
}

// MarkHistoryBackfilled records that org's history was rebuilt.
func (s *PvPlanStore) MarkHistoryBackfilled(ctx context.Context, org string, at time.Time) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE pv_plan_config SET history_backfilled_at = $2 WHERE organization_id = $1`, org, at)
	if err != nil {
		return fmt.Errorf("storage: mark pv history: %w", err)
	}
	return nil
}

// DataStart is the first instant org has telemetry.
func (s *PvPlanStore) DataStart(ctx context.Context, org string) (time.Time, bool, error) {
	first, _, ok, err := TelemetryRange(ctx, s.pool, org)
	return first, ok, err
}

// PvPlanHours returns org's plan of one kind for a civil day, keyed by
// local hour start, and when it was computed.
func PvPlanHours(ctx context.Context, pool *pgxpool.Pool, org string, day time.Time, kind pvplan.Kind) (map[int]float64, time.Time, error) {
	rows, err := pool.Query(ctx, `
		SELECT hour, planned_kwh, computed_at FROM pv_plan_hourly
		WHERE organization_id = $1 AND day = $2::date AND kind = $3 ORDER BY hour`,
		org, day, string(kind))
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("storage: query pv plan hours: %w", err)
	}
	defer rows.Close()
	out := map[int]float64{}
	var computed time.Time
	for rows.Next() {
		var (
			hour int
			kwh  float64
			at   time.Time
		)
		if err := rows.Scan(&hour, &kwh, &at); err != nil {
			return nil, time.Time{}, fmt.Errorf("storage: scan pv plan hour: %w", err)
		}
		out[hour] = kwh
		if at.After(computed) {
			computed = at
		}
	}
	return out, computed, rows.Err()
}

// SumPvPlanHourlyDays returns org's day totals of one kind over the
// inclusive civil-date span, one row per day that has hours.
func SumPvPlanHourlyDays(ctx context.Context, pool *pgxpool.Pool, org string, fromDay, toDay time.Time, kind pvplan.Kind) ([]PvPlanDay, error) {
	rows, err := pool.Query(ctx, `
		SELECT day, SUM(planned_kwh), max(computed_at) FROM pv_plan_hourly
		WHERE organization_id = $1 AND kind = $2 AND day >= $3::date AND day <= $4::date
		GROUP BY day ORDER BY day`,
		org, string(kind), fromDay, toDay)
	if err != nil {
		return nil, fmt.Errorf("storage: sum pv plan hours: %w", err)
	}
	defer rows.Close()
	var out []PvPlanDay
	for rows.Next() {
		var r PvPlanDay
		if err := rows.Scan(&r.Day, &r.PlannedKwh, &r.FetchedAt); err != nil {
			return nil, fmt.Errorf("storage: scan pv plan day total: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
