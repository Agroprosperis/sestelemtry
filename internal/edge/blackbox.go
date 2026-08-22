package edge

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"
	_ "modernc.org/sqlite"
)

// Blackbox is the on-device SQLite WAL buffer (spec:
// ems_blackbox_offline_buffer.md). Every telemetry tick, control
// decision and event is written here first; the uplink marks rows
// uploaded only after the cloud acknowledged them, so a dead link
// never loses data (30-day retention).
type Blackbox struct {
	db            *sql.DB
	siteID        string
	path          string
	retentionDays int
	criticalPct   int
}

const blackboxSchema = `
CREATE TABLE IF NOT EXISTS telemetry_raw (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	ts_utc TEXT NOT NULL,
	site_id TEXT NOT NULL,
	source TEXT NOT NULL,
	metric TEXT NOT NULL,
	value REAL,
	value_json TEXT,
	quality TEXT DEFAULT 'ok',
	uploaded INTEGER DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_telemetry_upload ON telemetry_raw(uploaded, ts_utc);

CREATE TABLE IF NOT EXISTS control_decisions (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	ts_utc TEXT NOT NULL,
	site_id TEXT NOT NULL,
	preset TEXT,
	mode TEXT,
	state_machine TEXT,
	p_grid_meas REAL,
	p_grid_target REAL,
	p_bess_plan REAL,
	p_bess_safety REAL,
	p_bess_cmd REAL,
	p_pv_limit REAL,
	soc_pct REAL,
	di_state_json TEXT,
	rationale TEXT,
	record_json TEXT,
	uploaded INTEGER DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_decisions_upload ON control_decisions(uploaded, ts_utc);

CREATE TABLE IF NOT EXISTS events (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	ts_utc TEXT NOT NULL,
	site_id TEXT NOT NULL,
	severity TEXT,
	code TEXT,
	message TEXT,
	context_json TEXT,
	uploaded INTEGER DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_events_upload ON events(uploaded, ts_utc);
`

// OpenBlackbox opens (creating if needed) the SQLite black box in WAL
// mode. A single writer connection avoids SQLITE_BUSY between the tick
// writer and the uplink marker.
func OpenBlackbox(cfg BlackboxConfig, siteID string) (*Blackbox, error) {
	if err := os.MkdirAll(filepath.Dir(cfg.DBPath), 0o755); err != nil {
		return nil, fmt.Errorf("blackbox: mkdir: %w", err)
	}
	dsn := "file:" + cfg.DBPath +
		"?_pragma=journal_mode(WAL)" +
		"&_pragma=synchronous(NORMAL)" +
		"&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("blackbox: open: %w", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(blackboxSchema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("blackbox: schema: %w", err)
	}
	return &Blackbox{
		db:            db,
		siteID:        siteID,
		path:          cfg.DBPath,
		retentionDays: cfg.RetentionDays,
		criticalPct:   cfg.DiskCriticalPct,
	}, nil
}

func (b *Blackbox) Close() error { return b.db.Close() }

func tsUTC(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

// WriteTick stores one normalized tick as a single row, the whole
// document in value_json (spec MVP-0..2 pattern: one row per tick, not
// one row per metric).
func (b *Blackbox) WriteTick(ctx context.Context, t Tick) error {
	doc, err := json.Marshal(t)
	if err != nil {
		return err
	}
	_, err = b.db.ExecContext(ctx, `
		INSERT INTO telemetry_raw (ts_utc, site_id, source, metric, value_json, quality)
		VALUES (?, ?, ?, 'tick', ?, ?)`,
		tsUTC(t.TS), b.siteID, "smartlogger", string(doc), t.DataQuality)
	return err
}

// WriteDecision stores a shadow decision: typed columns for local
// inspection plus the canonical §9.3 record in record_json (that exact
// document is what the uplink ships as control_records[]).
func (b *Blackbox) WriteDecision(ctx context.Context, d Decision) error {
	doc, err := json.Marshal(d.Record(b.siteID))
	if err != nil {
		return err
	}
	_, err = b.db.ExecContext(ctx, `
		INSERT INTO control_decisions
			(ts_utc, site_id, preset, mode, state_machine,
			 p_bess_plan, p_bess_cmd, p_pv_limit, soc_pct, rationale, record_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		tsUTC(d.TS), b.siteID, d.Preset, string(d.Mode), d.StateMachine,
		nullF64(d.PBessPlanKw), d.PBessVirtualKw, d.PPVLimitVirtualKw,
		nullF64(d.SocPercent), d.Rationale, string(doc))
	return err
}

func (b *Blackbox) WriteEvent(ctx context.Context, ev Event) error {
	var ctxJSON any
	if len(ev.Context) > 0 {
		raw, err := json.Marshal(ev.Context)
		if err != nil {
			return err
		}
		ctxJSON = string(raw)
	}
	_, err := b.db.ExecContext(ctx, `
		INSERT INTO events (ts_utc, site_id, severity, code, message, context_json)
		VALUES (?, ?, ?, ?, ?, ?)`,
		tsUTC(ev.TS), b.siteID, ev.Severity, ev.Code, ev.Message, ctxJSON)
	return err
}

func nullF64(v *float64) any {
	if v == nil {
		return nil
	}
	return *v
}

// Pending is a slice of not-yet-uploaded rows of one table: parallel
// ids and raw JSON documents.
type Pending struct {
	IDs  []int64
	Docs []json.RawMessage
}

// PendingTicks returns up to limit unuploaded telemetry ticks, oldest
// first (FIFO after an offline window, per spec).
func (b *Blackbox) PendingTicks(ctx context.Context, limit int) (Pending, error) {
	return b.pending(ctx, `SELECT id, value_json FROM telemetry_raw WHERE uploaded = 0 ORDER BY ts_utc LIMIT ?`, limit)
}

// PendingDecisions returns up to limit unuploaded control decisions
// as their canonical §9.3 JSON records.
func (b *Blackbox) PendingDecisions(ctx context.Context, limit int) (Pending, error) {
	return b.pending(ctx, `SELECT id, record_json FROM control_decisions WHERE uploaded = 0 ORDER BY ts_utc LIMIT ?`, limit)
}

// PendingEvents returns up to limit unuploaded events as JSON docs.
func (b *Blackbox) PendingEvents(ctx context.Context, limit int) (Pending, error) {
	return b.pending(ctx, `
		SELECT id, json_object(
			'site_id', site_id,
			'ts', ts_utc,
			'severity', severity,
			'code', code,
			'message', message,
			'context', json(coalesce(context_json, 'null'))
		)
		FROM events WHERE uploaded = 0 ORDER BY ts_utc LIMIT ?`, limit)
}

func (b *Blackbox) pending(ctx context.Context, q string, limit int) (Pending, error) {
	rows, err := b.db.QueryContext(ctx, q, limit)
	if err != nil {
		return Pending{}, err
	}
	defer rows.Close()
	var p Pending
	for rows.Next() {
		var id int64
		var doc sql.NullString
		if err := rows.Scan(&id, &doc); err != nil {
			return Pending{}, err
		}
		if !doc.Valid || strings.TrimSpace(doc.String) == "" {
			continue
		}
		p.IDs = append(p.IDs, id)
		p.Docs = append(p.Docs, json.RawMessage(doc.String))
	}
	return p, rows.Err()
}

var blackboxTables = map[string]bool{"telemetry_raw": true, "control_decisions": true, "events": true}

// MarkUploaded flags rows as delivered after a cloud ACK.
func (b *Blackbox) MarkUploaded(ctx context.Context, table string, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	if !blackboxTables[table] {
		return fmt.Errorf("blackbox: unknown table %q", table)
	}
	args := make([]any, len(ids))
	ph := make([]string, len(ids))
	for i, id := range ids {
		args[i] = id
		ph[i] = "?"
	}
	q := fmt.Sprintf(`UPDATE %s SET uploaded = 1 WHERE id IN (%s)`, table, strings.Join(ph, ","))
	_, err := b.db.ExecContext(ctx, q, args...)
	return err
}

// RecentDecisions returns the newest `limit` decision records (full
// canonical JSON documents, newest first) for the local console.
func (b *Blackbox) RecentDecisions(ctx context.Context, limit int) ([]json.RawMessage, error) {
	return b.recentJSON(ctx, `SELECT record_json FROM control_decisions ORDER BY id DESC LIMIT ?`, limit)
}

// RecentEvents returns the newest `limit` events (newest first) for
// the local console.
func (b *Blackbox) RecentEvents(ctx context.Context, limit int) ([]json.RawMessage, error) {
	return b.recentJSON(ctx, `
		SELECT json_object(
			'ts', ts_utc, 'severity', severity, 'code', code,
			'message', message, 'context', json(context_json))
		FROM events ORDER BY id DESC LIMIT ?`, limit)
}

func (b *Blackbox) recentJSON(ctx context.Context, q string, limit int) ([]json.RawMessage, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	rows, err := b.db.QueryContext(ctx, q, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []json.RawMessage
	for rows.Next() {
		var doc string
		if err := rows.Scan(&doc); err != nil {
			return nil, err
		}
		out = append(out, json.RawMessage(doc))
	}
	return out, rows.Err()
}

// Stats summarizes the black box for the local console: per-table
// pending-upload counts plus the database file size.
func (b *Blackbox) Stats(ctx context.Context) (map[string]int64, int64, error) {
	pending := map[string]int64{}
	for table := range blackboxTables {
		var n int64
		if err := b.db.QueryRowContext(ctx,
			`SELECT count(*) FROM `+table+` WHERE uploaded = 0`).Scan(&n); err != nil {
			return nil, 0, err
		}
		pending[table] = n
	}
	var size int64
	if fi, err := os.Stat(b.path); err == nil {
		size = fi.Size()
	}
	return pending, size, nil
}

// Path reports the database file location (console display).
func (b *Blackbox) Path() string { return b.path }

// PendingCount reports the total backlog across all tables (heartbeat's
// buffer_pending field).
func (b *Blackbox) PendingCount(ctx context.Context) (int64, error) {
	var total int64
	for table := range blackboxTables {
		var n int64
		if err := b.db.QueryRowContext(ctx,
			`SELECT count(*) FROM `+table+` WHERE uploaded = 0`).Scan(&n); err != nil {
			return 0, err
		}
		total += n
	}
	return total, nil
}

// Maintain enforces retention (drop rows older than retention_days)
// and the disk guard: above disk_critical_pct usage, uploaded rows are
// deleted oldest-first regardless of age.
func (b *Blackbox) Maintain(ctx context.Context, now time.Time) error {
	cutoff := tsUTC(now.AddDate(0, 0, -b.retentionDays))
	for table := range blackboxTables {
		if _, err := b.db.ExecContext(ctx,
			`DELETE FROM `+table+` WHERE ts_utc < ?`, cutoff); err != nil {
			return err
		}
	}

	usedPct, err := diskUsedPct(filepath.Dir(b.path))
	if err != nil {
		return nil // disk stats are best-effort; retention already ran
	}
	if usedPct < float64(b.criticalPct) {
		return nil
	}
	for table := range blackboxTables {
		if _, err := b.db.ExecContext(ctx, `
			DELETE FROM `+table+` WHERE id IN (
				SELECT id FROM `+table+` WHERE uploaded = 1 ORDER BY ts_utc LIMIT 50000
			)`); err != nil {
			return err
		}
	}
	return nil
}

func diskUsedPct(dir string) (float64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return 0, err
	}
	if st.Blocks == 0 {
		return 0, fmt.Errorf("statfs: zero blocks")
	}
	free := float64(st.Bavail)
	total := float64(st.Blocks)
	return 100 * (1 - free/total), nil
}
