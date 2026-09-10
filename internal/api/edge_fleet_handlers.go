package api

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/nesh/sestelemetry/internal/storage"
)

// Console status endpoints (operator-facing, same trust level as the
// planner endpoints):
//
//	GET /api/v1/edge/status?site_id= — one site: heartbeat, newest
//	    manifest with payload (for the plan overlay), newest shadow
//	    decision, recent events. Powers the «Керування» top-bar chips
//	    and the «Стан» tab.
//	GET /api/v1/edge/fleet           — the same snapshot for every
//	    edge site, without manifest payload and events.

// edgeOnlineThreshold marks a site offline when the newest heartbeat is
// older than this. Heartbeats arrive every ~30 s; 6 intervals of slack
// tolerate a brief tunnel or uplink hiccup without flapping the chip.
const edgeOnlineThreshold = 180 * time.Second

type edgeStatusHeartbeat struct {
	Online        bool       `json:"online"`
	UpdatedAt     *time.Time `json:"updated_at,omitempty"`
	AgeSeconds    *int64     `json:"age_seconds,omitempty"`
	EdgeID        string     `json:"edge_id,omitempty"`
	Status        string     `json:"status,omitempty"`
	BufferPending int64      `json:"buffer_pending"`
	LastSLPollOK  *time.Time `json:"last_sl_poll_ok,omitempty"`
	Firmware      string     `json:"firmware,omitempty"`
}

type edgeStatusManifest struct {
	// State: none | pending | applied | expired.
	State      string          `json:"state"`
	ManifestID string          `json:"manifest_id,omitempty"`
	IssuedAt   *time.Time      `json:"issued_at,omitempty"`
	ValidUntil *time.Time      `json:"valid_until,omitempty"`
	AppliedAt  *time.Time      `json:"applied_at,omitempty"`
	Payload    json.RawMessage `json:"payload,omitempty"`
}

type edgeStatusDecision struct {
	At         time.Time       `json:"at"`
	AgeSeconds int64           `json:"age_seconds"`
	Record     json.RawMessage `json:"record"`
}

type edgeStatusEvent struct {
	Time     time.Time       `json:"time"`
	Severity string          `json:"severity"`
	Code     string          `json:"code"`
	Message  string          `json:"message"`
	Context  json.RawMessage `json:"context,omitempty"`
}

type edgeSiteStatusResp struct {
	SiteID    string              `json:"site_id"`
	Heartbeat edgeStatusHeartbeat `json:"heartbeat"`
	Manifest  edgeStatusManifest  `json:"manifest"`
	Decision  *edgeStatusDecision `json:"decision,omitempty"`
	Events    []edgeStatusEvent   `json:"events,omitempty"`
	// Health is the raw §8.3 diagnostics snapshot from the newest
	// heartbeat. Absent for edge builds that do not send it.
	Health json.RawMessage `json:"health,omitempty"`
}

func buildEdgeSiteStatus(st storage.EdgeSiteStatus, now time.Time) edgeSiteStatusResp {
	resp := edgeSiteStatusResp{SiteID: st.SiteID}

	if !st.HeartbeatAt.IsZero() {
		t := st.HeartbeatAt
		age := int64(now.Sub(t).Seconds())
		resp.Heartbeat.UpdatedAt = &t
		resp.Heartbeat.AgeSeconds = &age
		resp.Heartbeat.Online = now.Sub(t) <= edgeOnlineThreshold
	}
	resp.Heartbeat.EdgeID = st.EdgeID
	resp.Heartbeat.Status = st.Status
	resp.Heartbeat.BufferPending = st.BufferPending
	if !st.LastSLPollOK.IsZero() {
		t := st.LastSLPollOK
		resp.Heartbeat.LastSLPollOK = &t
	}
	resp.Heartbeat.Firmware = st.Firmware

	switch {
	case st.ManifestID == "":
		resp.Manifest.State = "none"
	case !st.ManifestValidUntil.IsZero() && st.ManifestValidUntil.Before(now):
		resp.Manifest.State = "expired"
	case !st.ManifestAppliedAt.IsZero():
		resp.Manifest.State = "applied"
	default:
		resp.Manifest.State = "pending"
	}
	resp.Manifest.ManifestID = st.ManifestID
	if !st.ManifestIssuedAt.IsZero() {
		t := st.ManifestIssuedAt
		resp.Manifest.IssuedAt = &t
	}
	if !st.ManifestValidUntil.IsZero() {
		t := st.ManifestValidUntil
		resp.Manifest.ValidUntil = &t
	}
	if !st.ManifestAppliedAt.IsZero() {
		t := st.ManifestAppliedAt
		resp.Manifest.AppliedAt = &t
	}
	if len(st.ManifestPayload) > 0 {
		resp.Manifest.Payload = json.RawMessage(st.ManifestPayload)
	}

	if !st.DecisionAt.IsZero() && len(st.DecisionRecord) > 0 {
		resp.Decision = &edgeStatusDecision{
			At:         st.DecisionAt,
			AgeSeconds: int64(now.Sub(st.DecisionAt).Seconds()),
			Record:     json.RawMessage(st.DecisionRecord),
		}
	}
	if len(st.Health) > 0 {
		resp.Health = json.RawMessage(st.Health)
	}
	return resp
}

// edgeStatus handles GET /api/v1/edge/status?site_id=[&events=N].
func (h *Handlers) edgeStatus(w http.ResponseWriter, r *http.Request) {
	siteID, ok := h.requireEdge(w, r, http.MethodGet)
	if !ok {
		return
	}
	now := time.Now().UTC()
	st, err := storage.GetEdgeSiteStatus(r.Context(), h.edge.Pool, siteID, true)
	if err != nil {
		h.edge.Log.Error("edge_status", "site_id", siteID, "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	resp := buildEdgeSiteStatus(st, now)

	limit := 30
	if v := r.URL.Query().Get("events"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 && n <= 500 {
			limit = n
		}
	}
	if limit > 0 {
		events, err := storage.ListEdgeEvents(r.Context(), h.edge.Pool, siteID, limit)
		if err != nil {
			h.edge.Log.Error("edge_status_events", "site_id", siteID, "err", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		resp.Events = make([]edgeStatusEvent, 0, len(events))
		for _, e := range events {
			resp.Events = append(resp.Events, edgeStatusEvent{
				Time: e.Time, Severity: e.Severity, Code: e.Code,
				Message: e.Message, Context: json.RawMessage(e.Context),
			})
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// edgeFleet handles GET /api/v1/edge/fleet.
func (h *Handlers) edgeFleet(w http.ResponseWriter, r *http.Request) {
	if h.edge == nil {
		http.Error(w, "edge ingest not configured", http.StatusServiceUnavailable)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	sites := make([]string, 0, len(h.edge.Tokens))
	for s := range h.edge.Tokens {
		sites = append(sites, s)
	}
	sort.Strings(sites)

	now := time.Now().UTC()
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	out := make([]edgeSiteStatusResp, 0, len(sites))
	for _, siteID := range sites {
		st, err := storage.GetEdgeSiteStatus(ctx, h.edge.Pool, siteID, false)
		if err != nil {
			h.edge.Log.Error("edge_fleet", "site_id", siteID, "err", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		out = append(out, buildEdgeSiteStatus(st, now))
	}
	writeJSON(w, http.StatusOK, map[string]any{"now": now, "sites": out})
}
