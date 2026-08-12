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

		// Liveness: one row per site, last writer wins.
		`CREATE TABLE IF NOT EXISTS edge_heartbeats (
			site_id text PRIMARY KEY,
			edge_id text,
			status text,
			buffer_pending bigint,
			last_sl_poll_ok timestamptz,
			firmware_version text,
			updated_at timestamptz NOT NULL DEFAULT now()
		)`,

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
	SiteID       string `json:"site_id"`
	TS           time.Time `json:"ts"`
	Mode         string `json:"mode"`
	Preset       string `json:"preset"`
	StateMachine string `json:"state_machine"`
	PlanSource   string `json:"plan_source"`
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

// EdgeHeartbeat is the liveness report of one edge device.
type EdgeHeartbeat struct {
	SiteID          string
	EdgeID          string
	Status          string
	BufferPending   int64
	LastSLPollOK    *time.Time
	FirmwareVersion string
}

// UpsertEdgeHeartbeat records the latest heartbeat (one row per site).
func UpsertEdgeHeartbeat(ctx context.Context, db DBTX, hb EdgeHeartbeat) error {
	_, err := db.Exec(ctx, `
		INSERT INTO edge_heartbeats (site_id, edge_id, status, buffer_pending, last_sl_poll_ok, firmware_version, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, now())
		ON CONFLICT (site_id) DO UPDATE SET
			edge_id = EXCLUDED.edge_id,
			status = EXCLUDED.status,
			buffer_pending = EXCLUDED.buffer_pending,
			last_sl_poll_ok = EXCLUDED.last_sl_poll_ok,
			firmware_version = EXCLUDED.firmware_version,
			updated_at = now()`,
		hb.SiteID, hb.EdgeID, hb.Status, hb.BufferPending, hb.LastSLPollOK, hb.FirmwareVersion)
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

func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UTC()
}
