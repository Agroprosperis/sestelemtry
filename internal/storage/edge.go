package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DBTX is the subset of pgxpool.Pool / pgx.Tx the edge-ingest storage
// functions need. Batch ingest runs inside one transaction so a
// half-written batch can never be marked as received: the edge would
// otherwise skip re-sending rows the cloud silently lost.
type DBTX interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	CopyFrom(ctx context.Context, tableName pgx.Identifier, columnNames []string, rowSrc pgx.CopyFromSource) (int64, error)
}

// InitEdgeSchema creates the EMS edge ingest tables (idempotent).
// Mirrored by migrations/012_edge.sql.
func InitEdgeSchema(ctx context.Context, pool *pgxpool.Pool) error {
	if pool == nil {
		return fmt.Errorf("storage: nil pool")
	}
	stmts := []string{
		// Idempotency ledger: one row per accepted uplink batch.
		`CREATE TABLE IF NOT EXISTS edge_batches (
			batch_id text PRIMARY KEY,
			site_id text NOT NULL,
			edge_id text,
			sent_at timestamptz,
			received_at timestamptz NOT NULL DEFAULT now(),
			records int NOT NULL DEFAULT 0,
			control_records int NOT NULL DEFAULT 0,
			events int NOT NULL DEFAULT 0
		)`,
		`CREATE INDEX IF NOT EXISTS edge_batches_site_received
			ON edge_batches (site_id, received_at DESC)`,

		// Shadow-engine decisions (spec §9.3): typed columns for SQL
		// analytics + the full canonical record for audit/replay.
		`CREATE TABLE IF NOT EXISTS control_decisions (
			time timestamptz NOT NULL,
			site_id text NOT NULL,
			mode text,
			preset text,
			state_machine text,
			plan_source text,
			reason_code text,
			rationale text,
			p_bess_virtual_kw double precision,
			p_pv_limit_virtual_kw double precision,
			record jsonb NOT NULL,
			batch_id text
		)`,
		`CREATE INDEX IF NOT EXISTS control_decisions_site_time
			ON control_decisions (site_id, time DESC)`,

		// Edge events (SL_POLL_FAIL, SHADOW_ANOMALY, ...).
		`CREATE TABLE IF NOT EXISTS edge_events (
			time timestamptz NOT NULL,
			site_id text NOT NULL,
			severity text,
			code text,
			message text,
			context jsonb,
			batch_id text
		)`,
		`CREATE INDEX IF NOT EXISTS edge_events_site_time
			ON edge_events (site_id, time DESC)`,

		// Liveness: one row per site, last writer wins. `health` is the
		// §8.3 diagnostics snapshot from the same heartbeat (nullable:
		// old edge builds do not send it). Mirrored by 016_edge_health.sql.
		`CREATE TABLE IF NOT EXISTS edge_heartbeats (
			site_id text PRIMARY KEY,
			edge_id text,
			status text,
			buffer_pending bigint,
			last_sl_poll_ok timestamptz,
			firmware_version text,
			health jsonb,
			updated_at timestamptz NOT NULL DEFAULT now()
		)`,
		`ALTER TABLE edge_heartbeats ADD COLUMN IF NOT EXISTS health jsonb`,

		// Published manifests (manifest-lite). The newest issued_at row
		// per site is what GET /api/v1/edge/manifest serves.
		`CREATE TABLE IF NOT EXISTS edge_manifests (
			site_id text NOT NULL,
			manifest_id text NOT NULL,
			payload jsonb NOT NULL,
			issued_at timestamptz NOT NULL DEFAULT now(),
			valid_from timestamptz,
			valid_until timestamptz,
			published boolean NOT NULL DEFAULT true,
			PRIMARY KEY (site_id, manifest_id)
		)`,
		`CREATE INDEX IF NOT EXISTS edge_manifests_site_issued
			ON edge_manifests (site_id, issued_at DESC)`,

		// Operator-entered hourly load plan (cloud planner UI). One row
		// per planned hour; the forward planner prefers these hours over
		// the heuristic median profile (mirrored by 013_edge_load_plans.sql).
		`CREATE TABLE IF NOT EXISTS edge_load_plans (
			site_id text NOT NULL,
			hour timestamptz NOT NULL,
			load_kw double precision NOT NULL,
			updated_at timestamptz NOT NULL DEFAULT now(),
			PRIMARY KEY (site_id, hour)
		)`,

		// Per-site planner/control settings edited in the console
		// (SOC policy, power limits, grid limits). One JSONB blob per
		// site, last writer wins (mirrored by 014_edge_site_settings.sql).
		`CREATE TABLE IF NOT EXISTS edge_site_settings (
			site_id text PRIMARY KEY,
			payload jsonb NOT NULL,
			updated_at timestamptz NOT NULL DEFAULT now()
		)`,
	}
	for _, s := range stmts {
		if _, err := pool.Exec(ctx, s); err != nil {
			return fmt.Errorf("storage: edge schema: %w", err)
		}
	}
	// Decisions arrive at telemetry cadence (1/s per site) — same
	// hypertable treatment as telemetry_samples.
	_, err := pool.Exec(ctx, `SELECT create_hypertable('control_decisions', 'time', if_not_exists => TRUE, migrate_data => TRUE)`)
	if err != nil {
		msg := strings.ToLower(err.Error())
		if !strings.Contains(msg, "already a hypertable") {
			return fmt.Errorf("storage: control_decisions hypertable: %w", err)
		}
	}
	return nil
}

// EdgeBatchMeta describes one uplink batch for the idempotency ledger.
type EdgeBatchMeta struct {
	BatchID string
	SiteID  string
	EdgeID  string
	SentAt  time.Time
}

// InsertEdgeBatch registers a batch id. Returns false when the id was
// already accepted earlier (duplicate delivery after a lost ACK) — the
// caller must then skip the payload but still answer success.
func InsertEdgeBatch(ctx context.Context, db DBTX, meta EdgeBatchMeta) (bool, error) {
	tag, err := db.Exec(ctx, `
		INSERT INTO edge_batches (batch_id, site_id, edge_id, sent_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (batch_id) DO NOTHING`,
		meta.BatchID, meta.SiteID, meta.EdgeID, nullTime(meta.SentAt))
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// FinishEdgeBatch records the per-array counts after the payload landed.
func FinishEdgeBatch(ctx context.Context, db DBTX, batchID string, records, controlRecords, events int) error {
	_, err := db.Exec(ctx, `
		UPDATE edge_batches SET records = $2, control_records = $3, events = $4
		WHERE batch_id = $1`,
		batchID, records, controlRecords, events)
	return err
}

// edgeTick is the subset of the edge tick document the ingest needs.
type edgeTick struct {
	SiteID      string             `json:"site_id"`
	TS          time.Time          `json:"ts"`
	DataQuality string             `json:"data_quality"`
	Values      map[string]float64 `json:"values"`
}

// InsertEdgeTicksAsSamples fans every tick's raw values out into
// telemetry_samples rows — the same rows the VM collector would have
// written — attributed to organizationID (during the shadow phase this
// is "<site>-edge" so parallel polling never double-counts dashboards).
func InsertEdgeTicksAsSamples(ctx context.Context, db DBTX, organizationID, siteID, edgeID string, docs []json.RawMessage) (int, error) {
	if len(docs) == 0 {
		return 0, nil
	}
	labels, err := json.Marshal(map[string]string{
		"site_id": siteID,
		"source":  "edge",
		"edge_id": edgeID,
	})
	if err != nil {
		return 0, err
	}
	var rows [][]any
	for i, doc := range docs {
		var t edgeTick
		if err := json.Unmarshal(doc, &t); err != nil {
			return 0, fmt.Errorf("records[%d]: %w", i, err)
		}
		if t.TS.IsZero() {
			return 0, fmt.Errorf("records[%d]: missing ts", i)
		}
		if t.SiteID != "" && t.SiteID != siteID {
			return 0, fmt.Errorf("records[%d]: site_id %q does not match token site %q", i, t.SiteID, siteID)
		}
		for k, v := range t.Values {
			rows = append(rows, []any{t.TS.UTC(), organizationID, k, v, labels})
		}
	}
	if len(rows) == 0 {
		return 0, nil
	}
	if _, err := db.CopyFrom(ctx,
		pgx.Identifier{"telemetry_samples"},
		[]string{"time", "organization_id", "metric_key", "value", "labels"},
		pgx.CopyFromRows(rows),
	); err != nil {
		return 0, err
	}
	return len(docs), nil
}

// edgeControlRecord mirrors the §9.3 decision document.
type edgeControlRecord struct {
	SiteID       string    `json:"site_id"`
	TS           time.Time `json:"ts"`
	Mode         string    `json:"mode"`
	Preset       string    `json:"preset"`
	StateMachine string    `json:"state_machine"`
	PlanSource   string    `json:"plan_source"`
	Outputs      struct {
		PBessVirtualKw    *float64 `json:"p_bess_virtual_kw"`
		PPVLimitVirtualKw *float64 `json:"p_pv_limit_virtual_kw"`
	} `json:"outputs"`
	ReasonCode string `json:"reason_code"`
	Rationale  string `json:"rationale"`
}

// InsertEdgeControlDecisions stores shadow decisions.
func InsertEdgeControlDecisions(ctx context.Context, db DBTX, siteID, batchID string, docs []json.RawMessage) (int, error) {
	if len(docs) == 0 {
		return 0, nil
	}
	rows := make([][]any, 0, len(docs))
	for i, doc := range docs {
		var r edgeControlRecord
		if err := json.Unmarshal(doc, &r); err != nil {
			return 0, fmt.Errorf("control_records[%d]: %w", i, err)
		}
		if r.TS.IsZero() {
			return 0, fmt.Errorf("control_records[%d]: missing ts", i)
		}
		if r.SiteID != "" && r.SiteID != siteID {
			return 0, fmt.Errorf("control_records[%d]: site_id %q does not match token site %q", i, r.SiteID, siteID)
		}
		rows = append(rows, []any{
			r.TS.UTC(), siteID, r.Mode, r.Preset, r.StateMachine, r.PlanSource,
			r.ReasonCode, r.Rationale,
			r.Outputs.PBessVirtualKw, r.Outputs.PPVLimitVirtualKw,
			[]byte(doc), batchID,
		})
	}
	if _, err := db.CopyFrom(ctx,
		pgx.Identifier{"control_decisions"},
		[]string{"time", "site_id", "mode", "preset", "state_machine", "plan_source",
			"reason_code", "rationale", "p_bess_virtual_kw", "p_pv_limit_virtual_kw",
			"record", "batch_id"},
		pgx.CopyFromRows(rows),
	); err != nil {
		return 0, err
	}
	return len(rows), nil
}

// edgeEvent mirrors the black-box event document.
type edgeEvent struct {
	SiteID   string          `json:"site_id"`
	TS       time.Time       `json:"ts"`
	Severity string          `json:"severity"`
	Code     string          `json:"code"`
	Message  string          `json:"message"`
	Context  json.RawMessage `json:"context"`
}

// InsertEdgeEvents stores edge events.
func InsertEdgeEvents(ctx context.Context, db DBTX, siteID, batchID string, docs []json.RawMessage) (int, error) {
	if len(docs) == 0 {
		return 0, nil
	}
	rows := make([][]any, 0, len(docs))
	for i, doc := range docs {
		var e edgeEvent
		if err := json.Unmarshal(doc, &e); err != nil {
			return 0, fmt.Errorf("events[%d]: %w", i, err)
		}
		if e.TS.IsZero() {
			return 0, fmt.Errorf("events[%d]: missing ts", i)
		}
		if e.SiteID != "" && e.SiteID != siteID {
			return 0, fmt.Errorf("events[%d]: site_id %q does not match token site %q", i, e.SiteID, siteID)
		}
		var ctxJSON any
		if len(e.Context) > 0 && string(e.Context) != "null" {
			ctxJSON = []byte(e.Context)
		}
		rows = append(rows, []any{e.TS.UTC(), siteID, e.Severity, e.Code, e.Message, ctxJSON, batchID})
	}
	if _, err := db.CopyFrom(ctx,
		pgx.Identifier{"edge_events"},
		[]string{"time", "site_id", "severity", "code", "message", "context", "batch_id"},
		pgx.CopyFromRows(rows),
	); err != nil {
		return 0, err
	}
	return len(rows), nil
}

// EdgeHeartbeat is the liveness report of one edge device. Health is
// the raw §8.3 diagnostics snapshot (nil when the edge build predates
// the field).
type EdgeHeartbeat struct {
	SiteID          string
	EdgeID          string
	Status          string
	BufferPending   int64
	LastSLPollOK    *time.Time
	FirmwareVersion string
	Health          []byte
}

// UpsertEdgeHeartbeat records the latest heartbeat (one row per site).
// A heartbeat without health keeps the previous snapshot NULLed —
// stale diagnostics must not outlive the build that produced them.
func UpsertEdgeHeartbeat(ctx context.Context, db DBTX, hb EdgeHeartbeat) error {
	var health any
	if len(hb.Health) > 0 {
		health = hb.Health
	}
	_, err := db.Exec(ctx, `
		INSERT INTO edge_heartbeats (site_id, edge_id, status, buffer_pending, last_sl_poll_ok, firmware_version, health, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, now())
		ON CONFLICT (site_id) DO UPDATE SET
			edge_id = EXCLUDED.edge_id,
			status = EXCLUDED.status,
			buffer_pending = EXCLUDED.buffer_pending,
			last_sl_poll_ok = EXCLUDED.last_sl_poll_ok,
			firmware_version = EXCLUDED.firmware_version,
			health = EXCLUDED.health,
			updated_at = now()`,
		hb.SiteID, hb.EdgeID, hb.Status, hb.BufferPending, hb.LastSLPollOK, hb.FirmwareVersion, health)
	return err
}

// UpsertEdgeManifest stores a published manifest version.
func UpsertEdgeManifest(ctx context.Context, db DBTX, siteID, manifestID string, payload []byte, validFrom, validUntil time.Time) error {
	_, err := db.Exec(ctx, `
		INSERT INTO edge_manifests (site_id, manifest_id, payload, valid_from, valid_until)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (site_id, manifest_id) DO UPDATE SET
			payload = EXCLUDED.payload,
			valid_from = EXCLUDED.valid_from,
			valid_until = EXCLUDED.valid_until,
			issued_at = now()`,
		siteID, manifestID, payload, nullTime(validFrom), nullTime(validUntil))
	return err
}

// LatestEdgeManifest returns the newest published manifest for a site.
// ok is false when the site has none.
func LatestEdgeManifest(ctx context.Context, db DBTX, siteID string) (payload []byte, manifestID string, ok bool, err error) {
	err = db.QueryRow(ctx, `
		SELECT payload, manifest_id FROM edge_manifests
		WHERE site_id = $1 AND published
		ORDER BY issued_at DESC LIMIT 1`, siteID).Scan(&payload, &manifestID)
	if err == pgx.ErrNoRows {
		return nil, "", false, nil
	}
	if err != nil {
		return nil, "", false, err
	}
	return payload, manifestID, true, nil
}

// EdgeLoadPlanEntry is one operator-planned hour (hour start, UTC).
type EdgeLoadPlanEntry struct {
	Hour   time.Time
	LoadKw float64
}

// UpsertEdgeLoadPlan stores operator load-plan hours (last writer wins
// per hour).
func UpsertEdgeLoadPlan(ctx context.Context, pool *pgxpool.Pool, siteID string, entries []EdgeLoadPlanEntry) error {
	for _, e := range entries {
		if _, err := pool.Exec(ctx, `
			INSERT INTO edge_load_plans (site_id, hour, load_kw)
			VALUES ($1, $2, $3)
			ON CONFLICT (site_id, hour) DO UPDATE SET
				load_kw = EXCLUDED.load_kw,
				updated_at = now()`,
			siteID, e.Hour.UTC().Truncate(time.Hour), e.LoadKw); err != nil {
			return err
		}
	}
	return nil
}

// DeleteEdgeLoadPlan clears operator hours in [from, to).
func DeleteEdgeLoadPlan(ctx context.Context, pool *pgxpool.Pool, siteID string, from, to time.Time) (int64, error) {
	tag, err := pool.Exec(ctx, `
		DELETE FROM edge_load_plans
		WHERE site_id = $1 AND hour >= $2 AND hour < $3`,
		siteID, from.UTC(), to.UTC())
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// GetEdgeLoadPlan returns the operator hours in [from, to) keyed by the
// UTC hour start.
func GetEdgeLoadPlan(ctx context.Context, pool *pgxpool.Pool, siteID string, from, to time.Time) (map[time.Time]float64, error) {
	rows, err := pool.Query(ctx, `
		SELECT hour, load_kw FROM edge_load_plans
		WHERE site_id = $1 AND hour >= $2 AND hour < $3`,
		siteID, from.UTC(), to.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[time.Time]float64{}
	for rows.Next() {
		var h time.Time
		var kw float64
		if err := rows.Scan(&h, &kw); err != nil {
			return nil, err
		}
		out[h.UTC()] = kw
	}
	return out, rows.Err()
}

// EdgeManifestInfo is one journal row for the planner UI: a published
// manifest version plus its delivery outcome derived from edge events.
type EdgeManifestInfo struct {
	ManifestID string
	IssuedAt   time.Time
	ValidFrom  time.Time
	ValidUntil time.Time
	Preset     string
	LoadSource string
	Intervals  int
	AppliedAt  time.Time // zero = not (yet) confirmed by the edge
	RejectedAt time.Time // zero = not rejected
}

// ListEdgeManifests returns the newest manifest versions for a site
// with per-version delivery status (MANIFEST_APPLIED / _REJECTED events
// the edge uplinks reference the manifest_id in their context).
func ListEdgeManifests(ctx context.Context, pool *pgxpool.Pool, siteID string, limit int) ([]EdgeManifestInfo, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := pool.Query(ctx, `
		SELECT m.manifest_id, m.issued_at, m.valid_from, m.valid_until,
		       COALESCE(m.payload->>'preset', ''),
		       COALESCE(m.payload->'plan'->>'load_source', ''),
		       COALESCE(jsonb_array_length(m.payload->'plan'->'intervals'), 0),
		       a.time, r.time
		FROM edge_manifests m
		LEFT JOIN LATERAL (
			SELECT e.time FROM edge_events e
			WHERE e.site_id = m.site_id AND e.code = 'MANIFEST_APPLIED'
			  AND e.context->>'manifest_id' = m.manifest_id
			ORDER BY e.time ASC LIMIT 1
		) a ON true
		LEFT JOIN LATERAL (
			SELECT e.time FROM edge_events e
			WHERE e.site_id = m.site_id AND e.code = 'MANIFEST_REJECTED'
			  AND e.context->>'manifest_id' = m.manifest_id
			ORDER BY e.time DESC LIMIT 1
		) r ON true
		WHERE m.site_id = $1 AND m.published
		ORDER BY m.issued_at DESC
		LIMIT $2`, siteID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EdgeManifestInfo
	for rows.Next() {
		var mi EdgeManifestInfo
		var validFrom, validUntil, appliedAt, rejectedAt *time.Time
		if err := rows.Scan(&mi.ManifestID, &mi.IssuedAt, &validFrom, &validUntil,
			&mi.Preset, &mi.LoadSource, &mi.Intervals, &appliedAt, &rejectedAt); err != nil {
			return nil, err
		}
		if validFrom != nil {
			mi.ValidFrom = *validFrom
		}
		if validUntil != nil {
			mi.ValidUntil = *validUntil
		}
		if appliedAt != nil {
			mi.AppliedAt = *appliedAt
		}
		if rejectedAt != nil {
			mi.RejectedAt = *rejectedAt
		}
		out = append(out, mi)
	}
	return out, rows.Err()
}

// EdgeSiteStatus is the live-state snapshot of one edge site: last
// heartbeat, newest published manifest (with delivery status), and the
// newest shadow decision. It powers the console's status chips and the
// «Стан» tab; zero times mean "never seen".
type EdgeSiteStatus struct {
	SiteID string

	// Heartbeat (edge_heartbeats, one row per site).
	HeartbeatAt   time.Time
	EdgeID        string
	Status        string
	BufferPending int64
	LastSLPollOK  time.Time
	Firmware      string
	Health        []byte // §8.3 snapshot jsonb; nil when the edge does not send it

	// Newest published manifest.
	ManifestID         string
	ManifestIssuedAt   time.Time
	ManifestValidUntil time.Time
	ManifestAppliedAt  time.Time // zero = not confirmed by the edge yet
	ManifestPayload    []byte    // nil unless requested

	// Newest shadow decision (control_decisions).
	DecisionAt     time.Time
	DecisionRecord []byte // canonical record jsonb, nil when none
}

// GetEdgeSiteStatus assembles the per-site snapshot. withPayload
// controls whether the manifest payload jsonb is fetched (the fleet
// summary skips it; the site status view needs it for the plan overlay).
func GetEdgeSiteStatus(ctx context.Context, pool *pgxpool.Pool, siteID string, withPayload bool) (EdgeSiteStatus, error) {
	st := EdgeSiteStatus{SiteID: siteID}

	var hbAt, slPollOK *time.Time
	var edgeID, status, firmware *string
	var pending *int64
	err := pool.QueryRow(ctx, `
		SELECT updated_at, edge_id, status, buffer_pending, last_sl_poll_ok, firmware_version, health
		FROM edge_heartbeats WHERE site_id = $1`, siteID).
		Scan(&hbAt, &edgeID, &status, &pending, &slPollOK, &firmware, &st.Health)
	if err != nil && err != pgx.ErrNoRows {
		return st, err
	}
	if hbAt != nil {
		st.HeartbeatAt = *hbAt
	}
	if edgeID != nil {
		st.EdgeID = *edgeID
	}
	if status != nil {
		st.Status = *status
	}
	if pending != nil {
		st.BufferPending = *pending
	}
	if slPollOK != nil {
		st.LastSLPollOK = *slPollOK
	}
	if firmware != nil {
		st.Firmware = *firmware
	}

	payloadCol := "NULL::jsonb"
	if withPayload {
		payloadCol = "m.payload"
	}
	var issuedAt, validUntil, appliedAt *time.Time
	err = pool.QueryRow(ctx, `
		SELECT m.manifest_id, m.issued_at, m.valid_until, a.time, `+payloadCol+`
		FROM edge_manifests m
		LEFT JOIN LATERAL (
			SELECT e.time FROM edge_events e
			WHERE e.site_id = m.site_id AND e.code = 'MANIFEST_APPLIED'
			  AND e.context->>'manifest_id' = m.manifest_id
			ORDER BY e.time ASC LIMIT 1
		) a ON true
		WHERE m.site_id = $1 AND m.published
		ORDER BY m.issued_at DESC LIMIT 1`, siteID).
		Scan(&st.ManifestID, &issuedAt, &validUntil, &appliedAt, &st.ManifestPayload)
	if err != nil && err != pgx.ErrNoRows {
		return st, err
	}
	if issuedAt != nil {
		st.ManifestIssuedAt = *issuedAt
	}
	if validUntil != nil {
		st.ManifestValidUntil = *validUntil
	}
	if appliedAt != nil {
		st.ManifestAppliedAt = *appliedAt
	}

	var decAt *time.Time
	err = pool.QueryRow(ctx, `
		SELECT time, record FROM control_decisions
		WHERE site_id = $1 ORDER BY time DESC LIMIT 1`, siteID).
		Scan(&decAt, &st.DecisionRecord)
	if err != nil && err != pgx.ErrNoRows {
		return st, err
	}
	if decAt != nil {
		st.DecisionAt = *decAt
	}
	return st, nil
}

// EdgeEventRow is one uplinked edge event for console views.
type EdgeEventRow struct {
	Time     time.Time
	Severity string
	Code     string
	Message  string
	Context  []byte
}

// ListEdgeEvents returns the newest events for a site.
func ListEdgeEvents(ctx context.Context, pool *pgxpool.Pool, siteID string, limit int) ([]EdgeEventRow, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := pool.Query(ctx, `
		SELECT time, COALESCE(severity,''), COALESCE(code,''), COALESCE(message,''), context
		FROM edge_events WHERE site_id = $1
		ORDER BY time DESC LIMIT $2`, siteID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EdgeEventRow
	for rows.Next() {
		var e EdgeEventRow
		if err := rows.Scan(&e.Time, &e.Severity, &e.Code, &e.Message, &e.Context); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// GetEdgeSiteSettings returns the console-edited settings blob for a
// site. ok is false when the site has none saved yet.
func GetEdgeSiteSettings(ctx context.Context, pool *pgxpool.Pool, siteID string) (payload []byte, ok bool, err error) {
	err = pool.QueryRow(ctx,
		`SELECT payload FROM edge_site_settings WHERE site_id = $1`, siteID).Scan(&payload)
	if err == pgx.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return payload, true, nil
}

// UpsertEdgeSiteSettings stores the settings blob (last writer wins).
func UpsertEdgeSiteSettings(ctx context.Context, pool *pgxpool.Pool, siteID string, payload []byte) error {
	_, err := pool.Exec(ctx, `
		INSERT INTO edge_site_settings (site_id, payload)
		VALUES ($1, $2)
		ON CONFLICT (site_id) DO UPDATE SET
			payload = EXCLUDED.payload,
			updated_at = now()`, siteID, payload)
	return err
}

func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UTC()
}
