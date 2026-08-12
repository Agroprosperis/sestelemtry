package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nesh/sestelemetry/internal/storage"
)

// EdgeIngest is the cloud side of the EMS edge uplink (spec: ems-spec
// ems_mvp_edge_shadow_spec.md §7): batched telemetry / control records /
// events, heartbeats, and manifest distribution. Enabled only when
// per-site Bearer tokens are configured (EDGE_SITE_TOKENS) — these are
// the first authenticated endpoints in the API, everything else stays
// behind the VPN as before.
type EdgeIngest struct {
	Pool *pgxpool.Pool
	// Tokens maps site_id → Bearer token.
	Tokens map[string]string
	// OrgSuffix is appended to the site_id to form the organization_id
	// under which edge telemetry lands in telemetry_samples. During the
	// shadow phase the VM collector still polls the same SmartLoggers,
	// so edge rows go to "<site>-edge" to keep dashboards single-source.
	// Set EDGE_ORG_SUFFIX="" at cutover.
	OrgSuffix string
	Log       *slog.Logger

	// Forward-planner settings (manifest publisher). Zone is the DAM
	// price zone (default 2 = unified UA grid), Timezone the market
	// civil time (default Europe/Kyiv).
	PlannerZone     int
	PlannerTimezone string
}

// SetEdgeIngest wires the edge endpoints; nil disables them (503).
func (h *Handlers) SetEdgeIngest(e *EdgeIngest) { h.edge = e }

// edgeBatchRequest mirrors internal/edge.BatchRequest.
type edgeBatchRequest struct {
	BatchID        string            `json:"batch_id"`
	SiteID         string            `json:"site_id"`
	EdgeID         string            `json:"edge_id"`
	SentAt         time.Time         `json:"sent_at"`
	Records        []json.RawMessage `json:"records"`
	ControlRecords []json.RawMessage `json:"control_records"`
	Events         []json.RawMessage `json:"events"`
}

type edgeBatchResponse struct {
	Duplicate bool `json:"duplicate"`
	Accepted  struct {
		Records        int `json:"records"`
		ControlRecords int `json:"control_records"`
		Events         int `json:"events"`
	} `json:"accepted"`
}

// authorizeEdge validates the Bearer token for siteID. Constant-time
// comparison; unknown sites and bad tokens are indistinguishable (401).
func (e *EdgeIngest) authorizeEdge(r *http.Request, siteID string) bool {
	auth := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if !strings.HasPrefix(auth, prefix) {
		return false
	}
	presented := strings.TrimSpace(auth[len(prefix):])
	expected, ok := e.Tokens[siteID]
	if !ok || expected == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(presented), []byte(expected)) == 1
}

// edgeBatch handles POST /api/v1/edge/batch: one idempotent unit of
// telemetry ticks + control records + events, all-or-nothing in a
// single transaction keyed by batch_id.
func (h *Handlers) edgeBatch(w http.ResponseWriter, r *http.Request) {
	e := h.edge
	if e == nil {
		http.Error(w, "edge ingest not configured", http.StatusServiceUnavailable)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req edgeBatchRequest
	if err := decodeEdgeJSON(w, r, &req, 16<<20); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.SiteID) == "" || strings.TrimSpace(req.BatchID) == "" {
		http.Error(w, "site_id and batch_id are required", http.StatusBadRequest)
		return
	}
	if !e.authorizeEdge(r, req.SiteID) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	ctx := r.Context()
	tx, err := e.Pool.Begin(ctx)
	if err != nil {
		e.Log.Error("edge_batch_begin", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback(context.WithoutCancel(ctx))

	inserted, err := storage.InsertEdgeBatch(ctx, tx, storage.EdgeBatchMeta{
		BatchID: req.BatchID, SiteID: req.SiteID, EdgeID: req.EdgeID, SentAt: req.SentAt,
	})
	if err != nil {
		e.Log.Error("edge_batch_ledger", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	var resp edgeBatchResponse
	if !inserted {
		// Batch already accepted earlier (ACK was lost) — success, so
		// the edge marks its rows uploaded and stops resending.
		resp.Duplicate = true
		writeJSON(w, http.StatusOK, resp)
		return
	}

	orgID := req.SiteID + e.OrgSuffix
	nRec, err := storage.InsertEdgeTicksAsSamples(ctx, tx, orgID, req.SiteID, req.EdgeID, req.Records)
	if err != nil {
		http.Error(w, fmt.Sprintf("records: %v", err), http.StatusBadRequest)
		return
	}
	nDec, err := storage.InsertEdgeControlDecisions(ctx, tx, req.SiteID, req.BatchID, req.ControlRecords)
	if err != nil {
		http.Error(w, fmt.Sprintf("control_records: %v", err), http.StatusBadRequest)
		return
	}
	nEv, err := storage.InsertEdgeEvents(ctx, tx, req.SiteID, req.BatchID, req.Events)
	if err != nil {
		http.Error(w, fmt.Sprintf("events: %v", err), http.StatusBadRequest)
		return
	}
	if err := storage.FinishEdgeBatch(ctx, tx, req.BatchID, nRec, nDec, nEv); err != nil {
		e.Log.Error("edge_batch_finish", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		e.Log.Error("edge_batch_commit", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	resp.Accepted.Records = nRec
	resp.Accepted.ControlRecords = nDec
	resp.Accepted.Events = nEv
	e.Log.Info("edge_batch_ok", "site_id", req.SiteID, "batch_id", req.BatchID,
		"records", nRec, "control_records", nDec, "events", nEv)
	writeJSON(w, http.StatusOK, resp)
}

// edgeHeartbeatRequest mirrors internal/edge.Heartbeat.
type edgeHeartbeatRequest struct {
	SiteID          string     `json:"site_id"`
	EdgeID          string     `json:"edge_id"`
	Status          string     `json:"status"`
	BufferPending   int64      `json:"buffer_pending"`
	LastSLPollOK    *time.Time `json:"last_sl_poll_ok"`
	FirmwareVersion string     `json:"firmware_version"`
}

// edgeHeartbeat handles POST /api/v1/edge/heartbeat.
func (h *Handlers) edgeHeartbeat(w http.ResponseWriter, r *http.Request) {
	e := h.edge
	if e == nil {
		http.Error(w, "edge ingest not configured", http.StatusServiceUnavailable)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req edgeHeartbeatRequest
	if err := decodeEdgeJSON(w, r, &req, 64<<10); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.SiteID) == "" {
		http.Error(w, "site_id is required", http.StatusBadRequest)
		return
	}
	if !e.authorizeEdge(r, req.SiteID) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	err := storage.UpsertEdgeHeartbeat(r.Context(), e.Pool, storage.EdgeHeartbeat{
		SiteID:          req.SiteID,
		EdgeID:          req.EdgeID,
		Status:          req.Status,
		BufferPending:   req.BufferPending,
		LastSLPollOK:    req.LastSLPollOK,
		FirmwareVersion: req.FirmwareVersion,
	})
	if err != nil {
		e.Log.Error("edge_heartbeat", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// edgeManifest handles GET /api/v1/edge/manifest?site_id=: serves the
// newest published manifest with the manifest_id as ETag so the edge's
// 60 s poll costs one 304 round-trip when nothing changed.
func (h *Handlers) edgeManifest(w http.ResponseWriter, r *http.Request) {
	e := h.edge
	if e == nil {
		http.Error(w, "edge ingest not configured", http.StatusServiceUnavailable)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	siteID := strings.TrimSpace(r.URL.Query().Get("site_id"))
	if siteID == "" {
		http.Error(w, "site_id is required", http.StatusBadRequest)
		return
	}
	if !e.authorizeEdge(r, siteID) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	payload, manifestID, ok, err := storage.LatestEdgeManifest(r.Context(), e.Pool, siteID)
	if err != nil {
		e.Log.Error("edge_manifest", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, "no manifest published for site", http.StatusNotFound)
		return
	}
	etag := `"` + manifestID + `"`
	if match := r.Header.Get("If-None-Match"); match != "" && strings.Contains(match, etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("ETag", etag)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(payload)
}

// decodeEdgeJSON parses a bounded JSON body.
func decodeEdgeJSON(w http.ResponseWriter, r *http.Request, dst any, maxBytes int64) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("invalid JSON body: %w", err)
	}
	// Drain any trailing whitespace so keep-alive connections reuse cleanly.
	_, _ = io.Copy(io.Discard, r.Body)
	return nil
}

// ParseEdgeTokens parses the EDGE_SITE_TOKENS env format:
// "site=token[,site=token...]" (also accepts ':' as separator).
func ParseEdgeTokens(raw string) map[string]string {
	out := map[string]string{}
	for _, pair := range strings.Split(raw, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		idx := strings.IndexAny(pair, "=:")
		if idx <= 0 || idx == len(pair)-1 {
			continue
		}
		site := strings.TrimSpace(pair[:idx])
		token := strings.TrimSpace(pair[idx+1:])
		if site != "" && token != "" {
			out[site] = token
		}
	}
	return out
}
