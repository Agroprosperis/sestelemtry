package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// CounterSegment is one stored stitching correction: over
// [StartsAt, EndsAt) the counter's raw reading is shifted by an offset
// running linearly from OffsetStart to OffsetEnd. The open segment has
// no EndsAt and holds OffsetStart.
type CounterSegment struct {
	MetricKey   string
	StartsAt    time.Time
	EndsAt      *time.Time
	OffsetStart float64
	OffsetEnd   float64
}

// CounterEpisode is one counter discontinuity (dropout, seam, spike) or
// frozen stretch, kept for day flags and alerts.
type CounterEpisode struct {
	OrganizationID  string
	MetricKey       string
	StartedAt       time.Time
	Kind            string
	EndedAt         *time.Time
	ValueBefore     float64
	ValueAfter      float64
	ValueReturned   *float64
	GapKwh          *float64
	DetectedAt      time.Time
	NotifiedOpenAt  *time.Time
	NotifiedCloseAt *time.Time
}

// CounterStitchState is where the stitcher stopped for one counter.
type CounterStitchState struct {
	MetricKey      string
	ProcessedUntil time.Time
	State          json.RawMessage
}

// CounterStep is one sample-to-sample transition returned by the
// candidate scan. Settled is when the counter first showed Prev.
type CounterStep struct {
	PrevTime time.Time
	Prev     float64
	Time     time.Time
	Value    float64
	Settled  time.Time
}

// CounterDaySpan is one civil day of a counter from the daily aggregate.
type CounterDaySpan struct {
	Day     time.Time
	First   float64
	Last    float64
	Samples int64
}

// InitCounterSchema creates the stitching tables and the
// counter_offset() function every counter delta query adds to raw
// readings (idempotent). Mirrors migrations/020_counter_stitch.sql.
func InitCounterSchema(ctx context.Context, pool *pgxpool.Pool) error {
	if pool == nil {
		return fmt.Errorf("storage: nil pool")
	}
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS counter_corrections (
			organization_id text NOT NULL,
			metric_key      text NOT NULL,
			starts_at       timestamptz NOT NULL,
			ends_at         timestamptz,
			offset_start    double precision NOT NULL,
			offset_end      double precision NOT NULL,
			PRIMARY KEY (organization_id, metric_key, starts_at)
		)`,
		`CREATE TABLE IF NOT EXISTS counter_episodes (
			organization_id   text NOT NULL,
			metric_key        text NOT NULL,
			started_at        timestamptz NOT NULL,
			kind              text NOT NULL,
			ended_at          timestamptz,
			value_before      double precision NOT NULL DEFAULT 0,
			value_after       double precision NOT NULL DEFAULT 0,
			value_returned    double precision,
			gap_kwh           double precision,
			detected_at       timestamptz NOT NULL DEFAULT now(),
			notified_open_at  timestamptz,
			notified_close_at timestamptz,
			PRIMARY KEY (organization_id, metric_key, started_at)
		)`,
		`CREATE INDEX IF NOT EXISTS counter_episodes_org_started
			ON counter_episodes (organization_id, started_at)`,
		`CREATE TABLE IF NOT EXISTS counter_stitch_state (
			organization_id text NOT NULL,
			metric_key      text NOT NULL,
			processed_until timestamptz NOT NULL,
			state           jsonb NOT NULL DEFAULT '{}'::jsonb,
			updated_at      timestamptz NOT NULL DEFAULT now(),
			PRIMARY KEY (organization_id, metric_key)
		)`,
		`CREATE TABLE IF NOT EXISTS counter_stitch_rebuilds (
			organization_id text PRIMARY KEY,
			changed_from    timestamptz NOT NULL,
			requested_at    timestamptz NOT NULL DEFAULT now()
		)`,
		// The offset at ts: the segment starting at or before ts, linear
		// across a closed one. 0 where nothing was ever corrected.
		`CREATE OR REPLACE FUNCTION counter_offset(org text, metric text, ts timestamptz)
		RETURNS double precision
		LANGUAGE sql STABLE PARALLEL SAFE AS $fn$
			SELECT COALESCE((
				SELECT CASE
					WHEN c.ends_at IS NULL OR c.ends_at <= c.starts_at OR ts <= c.starts_at THEN c.offset_start
					WHEN ts >= c.ends_at THEN c.offset_end
					ELSE c.offset_start + (c.offset_end - c.offset_start)
						* (EXTRACT(epoch FROM ts - c.starts_at) / EXTRACT(epoch FROM c.ends_at - c.starts_at))
				END
				FROM counter_corrections c
				WHERE c.organization_id = org
					AND c.metric_key = metric
					AND c.starts_at <= ts
				ORDER BY c.starts_at DESC
				LIMIT 1
			), 0)
		$fn$`,
	}
	for _, s := range stmts {
		if _, err := pool.Exec(ctx, s); err != nil {
			return fmt.Errorf("storage: exec counter schema: %w", err)
		}
	}
	return nil
}

// LoadCounterStitchStates returns the stored stitcher state of every
// counter of the organization, keyed by metric.
func LoadCounterStitchStates(ctx context.Context, pool *pgxpool.Pool, organizationID string) (map[string]CounterStitchState, error) {
	rows, err := pool.Query(ctx, `
		SELECT metric_key, processed_until, state
		FROM counter_stitch_state
		WHERE organization_id = $1
	`, organizationID)
	if err != nil {
		return nil, fmt.Errorf("storage: query counter_stitch_state: %w", err)
	}
	defer rows.Close()
	out := make(map[string]CounterStitchState)
	for rows.Next() {
		var s CounterStitchState
		if err := rows.Scan(&s.MetricKey, &s.ProcessedUntil, &s.State); err != nil {
			return nil, err
		}
		out[s.MetricKey] = s
	}
	return out, rows.Err()
}

// SaveCounterStitch writes one stitcher pass of a counter in a single
// transaction: the segments and episodes it produced and where it
// stopped. With replace set, the counter's previous segments are
// dropped first (a rebuild), so readers never see a half-built set.
// Episodes keep their notification marks across upserts.
func SaveCounterStitch(
	ctx context.Context,
	pool *pgxpool.Pool,
	organizationID string,
	state CounterStitchState,
	segments []CounterSegment,
	episodes []CounterEpisode,
	replace bool,
) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if replace {
		if _, err := tx.Exec(ctx, `
			DELETE FROM counter_corrections WHERE organization_id = $1 AND metric_key = $2
		`, organizationID, state.MetricKey); err != nil {
			return fmt.Errorf("storage: clear counter_corrections: %w", err)
		}
	}
	for _, g := range segments {
		if _, err := tx.Exec(ctx, `
			INSERT INTO counter_corrections (organization_id, metric_key, starts_at, ends_at, offset_start, offset_end)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (organization_id, metric_key, starts_at) DO UPDATE SET
				ends_at = EXCLUDED.ends_at,
				offset_start = EXCLUDED.offset_start,
				offset_end = EXCLUDED.offset_end
		`, organizationID, g.MetricKey, g.StartsAt, g.EndsAt, g.OffsetStart, g.OffsetEnd); err != nil {
			return fmt.Errorf("storage: upsert counter_corrections: %w", err)
		}
	}
	if err := upsertCounterEpisodes(ctx, tx, organizationID, episodes); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO counter_stitch_state (organization_id, metric_key, processed_until, state, updated_at)
		VALUES ($1, $2, $3, $4, now())
		ON CONFLICT (organization_id, metric_key) DO UPDATE SET
			processed_until = EXCLUDED.processed_until,
			state = EXCLUDED.state,
			updated_at = now()
	`, organizationID, state.MetricKey, state.ProcessedUntil, []byte(state.State)); err != nil {
		return fmt.Errorf("storage: upsert counter_stitch_state: %w", err)
	}
	return tx.Commit(ctx)
}

// UpsertCounterEpisodes stores episodes found outside a stitcher pass
// (frozen stretches), keeping their notification marks.
func UpsertCounterEpisodes(ctx context.Context, pool *pgxpool.Pool, organizationID string, episodes []CounterEpisode) error {
	if len(episodes) == 0 {
		return nil
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := upsertCounterEpisodes(ctx, tx, organizationID, episodes); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func upsertCounterEpisodes(ctx context.Context, tx pgx.Tx, organizationID string, episodes []CounterEpisode) error {
	for _, e := range episodes {
		if _, err := tx.Exec(ctx, `
			INSERT INTO counter_episodes (
				organization_id, metric_key, started_at, kind, ended_at,
				value_before, value_after, value_returned, gap_kwh
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
			ON CONFLICT (organization_id, metric_key, started_at) DO UPDATE SET
				kind = EXCLUDED.kind,
				ended_at = EXCLUDED.ended_at,
				value_before = EXCLUDED.value_before,
				value_after = EXCLUDED.value_after,
				value_returned = EXCLUDED.value_returned,
				gap_kwh = EXCLUDED.gap_kwh
		`, organizationID, e.MetricKey, e.StartedAt, e.Kind, e.EndedAt,
			e.ValueBefore, e.ValueAfter, e.ValueReturned, e.GapKwh); err != nil {
			return fmt.Errorf("storage: upsert counter_episodes: %w", err)
		}
	}
	return nil
}

// FirstSampleTime returns the earliest sample of one counter.
func FirstSampleTime(ctx context.Context, pool *pgxpool.Pool, organizationID, metricKey string) (time.Time, bool, error) {
	var t time.Time
	err := pool.QueryRow(ctx, `
		SELECT time FROM telemetry_samples
		WHERE organization_id = $1 AND metric_key = $2
		ORDER BY time ASC
		LIMIT 1
	`, organizationID, metricKey).Scan(&t)
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, fmt.Errorf("storage: first sample: %w", err)
	}
	return t, true, nil
}

// CounterStepCandidates returns the transitions of one counter inside
// [from, to) that the stitcher has to look at: every backward step
// beyond dropTolKwh, every forward step of at least floorKwh, and every
// forward step faster than maxPowerKw (plus slackKwh). A step's time is
// the longer of the gap since the previous sample and the time the
// previous reading had been held, capped at coarseUpdate — registers
// that publish minutes of metering at once are not discontinuities.
// The scan reads coarseUpdate back from `from` (plus the sample before
// that) so steps on the window edge are measured the same way.
// Everything else is ordinary metering and stays in the database.
func CounterStepCandidates(
	ctx context.Context,
	pool *pgxpool.Pool,
	organizationID, metricKey string,
	from, to time.Time,
	dropTolKwh, floorKwh, maxPowerKw, slackKwh float64,
	coarseUpdate time.Duration,
) ([]CounterStep, error) {
	lead := from.Add(-coarseUpdate)
	rows, err := pool.Query(ctx, `
		WITH win AS (
			(
				SELECT time, value FROM telemetry_samples
				WHERE organization_id = $1 AND metric_key = $2 AND time < $9
				ORDER BY time DESC
				LIMIT 1
			)
			UNION ALL
			SELECT time, value FROM telemetry_samples
			WHERE organization_id = $1 AND metric_key = $2 AND time >= $9 AND time < $4
		),
		lagged AS (
			SELECT
				time,
				value,
				lag(time)  OVER w AS prev_time,
				lag(value) OVER w AS prev_value
			FROM win
			WINDOW w AS (ORDER BY time, value)
		),
		settled AS (
			SELECT
				time,
				value,
				prev_time,
				prev_value,
				max(CASE WHEN prev_value IS NULL OR value <> prev_value THEN time END) OVER (
					ORDER BY time, value ROWS BETWEEN UNBOUNDED PRECEDING AND 1 PRECEDING
				) AS settled_at
			FROM lagged
		)
		SELECT prev_time, prev_value, time, value, COALESCE(settled_at, prev_time)
		FROM settled
		WHERE prev_value IS NOT NULL
			AND time >= $3
			AND (
				value - prev_value < -$5::float8
				OR value - prev_value >= $6::float8
				OR value - prev_value > $7::float8 * GREATEST(
					EXTRACT(epoch FROM (time - prev_time)),
					LEAST(EXTRACT(epoch FROM (time - COALESCE(settled_at, prev_time))), $10::float8)
				) / 3600.0 + $8::float8
			)
		ORDER BY time, value
	`, organizationID, metricKey, from.UTC(), to.UTC(), dropTolKwh, floorKwh, maxPowerKw, slackKwh,
		lead.UTC(), coarseUpdate.Seconds())
	if err != nil {
		return nil, fmt.Errorf("storage: counter step scan: %w", err)
	}
	defer rows.Close()
	var out []CounterStep
	for rows.Next() {
		var s CounterStep
		if err := rows.Scan(&s.PrevTime, &s.Prev, &s.Time, &s.Value, &s.Settled); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// CounterSegmentsInRange returns the correction segments of the given
// counters that are in force anywhere inside [from, to), ordered by
// metric and start.
func CounterSegmentsInRange(ctx context.Context, pool *pgxpool.Pool, organizationID string, metricKeys []string, from, to time.Time) ([]CounterSegment, error) {
	rows, err := pool.Query(ctx, `
		SELECT metric_key, starts_at, ends_at, offset_start, offset_end
		FROM counter_corrections
		WHERE organization_id = $1
			AND metric_key = ANY($2)
			AND starts_at < $4
			AND (ends_at IS NULL OR ends_at > $3)
		ORDER BY metric_key, starts_at
	`, organizationID, metricKeys, from.UTC(), to.UTC())
	if err != nil {
		return nil, fmt.Errorf("storage: query counter_corrections: %w", err)
	}
	defer rows.Close()
	var out []CounterSegment
	for rows.Next() {
		var g CounterSegment
		if err := rows.Scan(&g.MetricKey, &g.StartsAt, &g.EndsAt, &g.OffsetStart, &g.OffsetEnd); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

const counterEpisodeColumns = `organization_id, metric_key, started_at, kind, ended_at,
	value_before, value_after, value_returned, gap_kwh, detected_at,
	notified_open_at, notified_close_at`

func scanCounterEpisodes(rows pgx.Rows) ([]CounterEpisode, error) {
	defer rows.Close()
	var out []CounterEpisode
	for rows.Next() {
		var e CounterEpisode
		if err := rows.Scan(&e.OrganizationID, &e.MetricKey, &e.StartedAt, &e.Kind, &e.EndedAt,
			&e.ValueBefore, &e.ValueAfter, &e.ValueReturned, &e.GapKwh, &e.DetectedAt,
			&e.NotifiedOpenAt, &e.NotifiedCloseAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// CounterEpisodesInRange returns the organization's episodes that
// overlap [from, to), ordered by start.
func CounterEpisodesInRange(ctx context.Context, pool *pgxpool.Pool, organizationID string, from, to time.Time) ([]CounterEpisode, error) {
	rows, err := pool.Query(ctx, `
		SELECT `+counterEpisodeColumns+`
		FROM counter_episodes
		WHERE organization_id = $1
			AND started_at < $3
			AND (ended_at IS NULL OR ended_at > $2 OR (ended_at = started_at AND started_at >= $2))
		ORDER BY started_at
	`, organizationID, from.UTC(), to.UTC())
	if err != nil {
		return nil, fmt.Errorf("storage: query counter_episodes: %w", err)
	}
	return scanCounterEpisodes(rows)
}

// CounterEpisodesOfKind returns the organization's episodes of one kind
// for one counter that start inside [from, to).
func CounterEpisodesOfKind(ctx context.Context, pool *pgxpool.Pool, organizationID, metricKey, kind string, from, to time.Time) ([]CounterEpisode, error) {
	rows, err := pool.Query(ctx, `
		SELECT `+counterEpisodeColumns+`
		FROM counter_episodes
		WHERE organization_id = $1 AND metric_key = $2 AND kind = $3
			AND started_at >= $4 AND started_at < $5
		ORDER BY started_at
	`, organizationID, metricKey, kind, from.UTC(), to.UTC())
	if err != nil {
		return nil, fmt.Errorf("storage: query counter_episodes: %w", err)
	}
	return scanCounterEpisodes(rows)
}

// CounterEpisodesToNotify returns the episodes of the given kinds whose
// opening or closing has not been announced yet: openings that started
// at or after openedSince, closings of episodes whose opening was
// announced.
func CounterEpisodesToNotify(ctx context.Context, pool *pgxpool.Pool, kinds []string, openedSince time.Time) ([]CounterEpisode, error) {
	rows, err := pool.Query(ctx, `
		SELECT `+counterEpisodeColumns+`
		FROM counter_episodes
		WHERE kind = ANY($1)
			AND (
				(notified_open_at IS NULL AND started_at >= $2)
				OR (notified_open_at IS NOT NULL AND ended_at IS NOT NULL AND notified_close_at IS NULL)
			)
		ORDER BY organization_id, started_at
	`, kinds, openedSince.UTC())
	if err != nil {
		return nil, fmt.Errorf("storage: query counter_episodes: %w", err)
	}
	return scanCounterEpisodes(rows)
}

// LastCounterOpenNotification returns when an opening of this counter
// was last announced; zero when never.
func LastCounterOpenNotification(ctx context.Context, pool *pgxpool.Pool, organizationID, metricKey string) (time.Time, error) {
	var t *time.Time
	if err := pool.QueryRow(ctx, `
		SELECT max(notified_open_at) FROM counter_episodes
		WHERE organization_id = $1 AND metric_key = $2
	`, organizationID, metricKey).Scan(&t); err != nil {
		return time.Time{}, fmt.Errorf("storage: last counter notification: %w", err)
	}
	if t == nil {
		return time.Time{}, nil
	}
	return *t, nil
}

// MarkCounterEpisodeNotified records that an episode's opening (open)
// or closing (!open) was dealt with at `at`.
func MarkCounterEpisodeNotified(ctx context.Context, pool *pgxpool.Pool, organizationID, metricKey string, startedAt time.Time, open bool, at time.Time) error {
	column := "notified_close_at"
	if open {
		column = "notified_open_at"
	}
	if _, err := pool.Exec(ctx, `
		UPDATE counter_episodes SET `+column+` = $4
		WHERE organization_id = $1 AND metric_key = $2 AND started_at = $3
	`, organizationID, metricKey, startedAt, at); err != nil {
		return fmt.Errorf("storage: mark counter episode: %w", err)
	}
	return nil
}

// CounterDaySpans returns one row per civil day of a counter from the
// daily continuous aggregate for [from, to).
func CounterDaySpans(ctx context.Context, pool *pgxpool.Pool, organizationID, metricKey string, from, to time.Time) ([]CounterDaySpan, error) {
	rows, err := pool.Query(ctx, `
		SELECT day, first_value, last_value, sample_count
		FROM `+DailyCAGGView+`
		WHERE organization_id = $1 AND metric_key = $2 AND day >= $3 AND day < $4
		ORDER BY day
	`, organizationID, metricKey, from.UTC(), to.UTC())
	if err != nil {
		return nil, fmt.Errorf("storage: counter day spans: %w", err)
	}
	defer rows.Close()
	var out []CounterDaySpan
	for rows.Next() {
		var d CounterDaySpan
		if err := rows.Scan(&d.Day, &d.First, &d.Last, &d.Samples); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// RequestCounterRebuild asks the stitcher to rebuild the organization's
// corrections from scratch: samples were written into the past (an
// archive import), so its incremental state no longer holds. Data
// before changedFrom is unchanged, which bounds what gets recomputed.
func RequestCounterRebuild(ctx context.Context, pool *pgxpool.Pool, organizationID string, changedFrom time.Time) error {
	if _, err := pool.Exec(ctx, `
		INSERT INTO counter_stitch_rebuilds (organization_id, changed_from, requested_at)
		VALUES ($1, $2, now())
		ON CONFLICT (organization_id) DO UPDATE SET
			changed_from = LEAST(counter_stitch_rebuilds.changed_from, EXCLUDED.changed_from),
			requested_at = now()
	`, organizationID, changedFrom.UTC()); err != nil {
		return fmt.Errorf("storage: request counter rebuild: %w", err)
	}
	return nil
}

// CounterRebuild is a pending rebuild request.
type CounterRebuild struct {
	ChangedFrom time.Time
	RequestedAt time.Time
}

// PendingCounterRebuild reports the rebuild requested for the
// organization, if any.
func PendingCounterRebuild(ctx context.Context, pool *pgxpool.Pool, organizationID string) (CounterRebuild, bool, error) {
	var r CounterRebuild
	err := pool.QueryRow(ctx, `
		SELECT changed_from, requested_at FROM counter_stitch_rebuilds WHERE organization_id = $1
	`, organizationID).Scan(&r.ChangedFrom, &r.RequestedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return CounterRebuild{}, false, nil
	}
	if err != nil {
		return CounterRebuild{}, false, fmt.Errorf("storage: query counter rebuild: %w", err)
	}
	return r, true, nil
}

// ClearCounterRebuild drops a served rebuild request, unless a newer
// one arrived while it ran.
func ClearCounterRebuild(ctx context.Context, pool *pgxpool.Pool, organizationID string, served CounterRebuild) error {
	if _, err := pool.Exec(ctx, `
		DELETE FROM counter_stitch_rebuilds WHERE organization_id = $1 AND requested_at <= $2
	`, organizationID, served.RequestedAt); err != nil {
		return fmt.Errorf("storage: clear counter rebuild: %w", err)
	}
	return nil
}
