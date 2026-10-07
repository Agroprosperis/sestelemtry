package api

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nesh/sestelemetry/internal/auth"
	"github.com/nesh/sestelemetry/internal/storage"
)

// Control-mode endpoints (operator-facing, same trust level as the other
// dashboard APIs — no edge Bearer token):
//
//	GET     /api/v1/edge/sites      — edge-enabled sites for the picker
//	GET|PUT /api/v1/edge/settings   — «Обмеження» (SOC policy, limits, export regime)
//	GET     /api/v1/edge/manifests  — manifest versions + delivery status

// requireEdge resolves the shared preconditions of every control-mode
// endpoint; it writes the error response itself and returns ok=false.
func (h *Handlers) requireEdge(w http.ResponseWriter, r *http.Request, methods ...string) (siteID string, ok bool) {
	if h.edge == nil {
		http.Error(w, "edge ingest not configured", http.StatusServiceUnavailable)
		return "", false
	}
	allowed := false
	for _, m := range methods {
		if r.Method == m {
			allowed = true
			break
		}
	}
	if !allowed {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return "", false
	}
	siteID = strings.TrimSpace(r.URL.Query().Get("site_id"))
	if siteID == "" {
		http.Error(w, "site_id is required", http.StatusBadRequest)
		return "", false
	}
	if _, known := h.edge.Tokens[siteID]; !known {
		http.Error(w, "unknown edge site", http.StatusNotFound)
		return "", false
	}
	return siteID, true
}

// edgeSites handles GET /api/v1/edge/sites.
func (h *Handlers) edgeSites(w http.ResponseWriter, r *http.Request) {
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
		if h.visibleTo(r, s, auth.PermControl) {
			sites = append(sites, s)
		}
	}
	sort.Strings(sites)
	writeJSON(w, http.StatusOK, map[string]any{"sites": sites})
}

// EdgeSiteSettings mirrors the console's settings panel (mockup
// panel-settings): SOC policy, per-mode power limits and grid limits.
// Zero values mean "not set" — the planner falls back to inventory /
// tariffs / built-in defaults.
type EdgeSiteSettings struct {
	SocTargetPct         float64 `json:"soc_target_pct,omitempty"`
	SocReservePct        float64 `json:"soc_reserve_pct,omitempty"`
	AutoChargeMaxKw      float64 `json:"auto_charge_max_kw,omitempty"`
	AutoDischargeMaxKw   float64 `json:"auto_discharge_max_kw,omitempty"`
	IslandChargeMaxKw    float64 `json:"island_charge_max_kw,omitempty"`
	IslandDischargeMaxKw float64 `json:"island_discharge_max_kw,omitempty"`
	GridImportKw         float64 `json:"grid_import_kw,omitempty"`
	GridTargetKw         float64 `json:"grid_target_kw,omitempty"`
	PvRatedKw            float64 `json:"pv_rated_kw,omitempty"`
	// ExportRegime is the passport paragraph of the export cap
	// (active_consumer_export_power_cap.md): self_production = 50 % of
	// the contracted power (GridImportKw), storage = 100 %. Empty = not
	// chosen yet: no export limit can be agreed, export stays off.
	ExportRegime string `json:"export_regime,omitempty"`
	// ExportPccKw is the site's technical PCC export limit; 0 = none.
	ExportPccKw float64 `json:"export_pcc_kw,omitempty"`
}

const (
	exportRegimeSelfProduction = "self_production"
	exportRegimeStorage        = "storage"
)

// exportCeilingKw is min(passport ceiling, technical PCC limit); nil
// until the regime is chosen and the contracted power is known.
func (s *EdgeSiteSettings) exportCeilingKw() *float64 {
	if s == nil || s.GridImportKw <= 0 {
		return nil
	}
	var c float64
	switch s.ExportRegime {
	case exportRegimeSelfProduction:
		c = math.Floor(0.5 * s.GridImportKw)
	case exportRegimeStorage:
		c = s.GridImportKw
	default:
		return nil
	}
	if s.ExportPccKw > 0 && s.ExportPccKw < c {
		c = s.ExportPccKw
	}
	return &c
}

func (s *EdgeSiteSettings) validate() error {
	switch s.ExportRegime {
	case "", exportRegimeSelfProduction, exportRegimeStorage:
	default:
		return fmt.Errorf("невідомий режим відпуску %q", s.ExportRegime)
	}
	if s.ExportPccKw < 0 || math.IsNaN(s.ExportPccKw) || math.IsInf(s.ExportPccKw, 0) {
		return fmt.Errorf("технічний ліміт експорту має бути невід'ємним числом")
	}
	if s.SocTargetPct < 0 || s.SocTargetPct > 100 || s.SocReservePct < 0 || s.SocReservePct > 100 {
		return fmt.Errorf("SOC відсотки мають бути в межах 0..100")
	}
	if s.SocTargetPct > 0 && s.SocReservePct > 0 && s.SocReservePct >= s.SocTargetPct {
		return fmt.Errorf("резерв SOC має бути нижчим за цільовий SOC")
	}
	for _, v := range []float64{
		s.AutoChargeMaxKw, s.AutoDischargeMaxKw, s.IslandChargeMaxKw,
		s.IslandDischargeMaxKw, s.GridImportKw, s.GridTargetKw, s.PvRatedKw,
	} {
		if v < 0 || math.IsNaN(v) || math.IsInf(v, 0) {
			return fmt.Errorf("потужності й ліміти мають бути невід'ємними числами")
		}
	}
	return nil
}

// edgeSettings handles GET/PUT /api/v1/edge/settings?site_id=.
func (h *Handlers) edgeSettings(w http.ResponseWriter, r *http.Request) {
	siteID, ok := h.requireEdge(w, r, http.MethodGet, http.MethodPut)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		s, saved := h.loadEdgeSiteSettings(r.Context(), siteID)
		if s == nil {
			s = &EdgeSiteSettings{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"site_id": siteID, "saved": saved, "settings": s})
	case http.MethodPut:
		var s EdgeSiteSettings
		if err := json.NewDecoder(r.Body).Decode(&s); err != nil {
			http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
			return
		}
		if err := s.validate(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		payload, err := json.Marshal(s)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if err := storage.UpsertEdgeSiteSettings(r.Context(), h.edge.Pool, siteID, payload); err != nil {
			h.edge.Log.Error("edge_settings_put", "site_id", siteID, "err", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"site_id": siteID, "saved": true, "settings": s})
	}
}

// loadEdgeSiteSettings reads the saved settings; nil when none.
func (h *Handlers) loadEdgeSiteSettings(ctx context.Context, siteID string) (*EdgeSiteSettings, bool) {
	payload, ok, err := storage.GetEdgeSiteSettings(ctx, h.edge.Pool, siteID)
	if err != nil {
		h.edge.Log.Warn("edge_settings_get", "site_id", siteID, "err", err)
		return nil, false
	}
	if !ok {
		return nil, false
	}
	var s EdgeSiteSettings
	if err := json.Unmarshal(payload, &s); err != nil {
		h.edge.Log.Warn("edge_settings_decode", "site_id", siteID, "err", err)
		return nil, false
	}
	return &s, true
}

// --- manifest journal ---

type edgeManifestJournalRow struct {
	ManifestID string     `json:"manifest_id"`
	IssuedAt   time.Time  `json:"issued_at"`
	ValidFrom  *time.Time `json:"valid_from,omitempty"`
	ValidUntil *time.Time `json:"valid_until,omitempty"`
	Preset     string     `json:"preset"`
	LoadSource string     `json:"load_source,omitempty"`
	Intervals  int        `json:"intervals"`
	Status     string     `json:"status"` // applied | rejected | pending
	AppliedAt  *time.Time `json:"applied_at,omitempty"`
	RejectedAt *time.Time `json:"rejected_at,omitempty"`
}

// edgeManifestJournal handles GET /api/v1/edge/manifests?site_id=&limit=.
func (h *Handlers) edgeManifestJournal(w http.ResponseWriter, r *http.Request) {
	siteID, ok := h.requireEdge(w, r, http.MethodGet)
	if !ok {
		return
	}
	limit := 20
	if v := strings.TrimSpace(r.URL.Query().Get("limit")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}
	infos, err := storage.ListEdgeManifests(r.Context(), h.edge.Pool, siteID, limit)
	if err != nil {
		h.edge.Log.Error("edge_manifest_journal", "site_id", siteID, "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	rows := make([]edgeManifestJournalRow, 0, len(infos))
	for _, mi := range infos {
		row := edgeManifestJournalRow{
			ManifestID: mi.ManifestID,
			IssuedAt:   mi.IssuedAt,
			Preset:     mi.Preset,
			LoadSource: mi.LoadSource,
			Intervals:  mi.Intervals,
			Status:     "pending",
		}
		if !mi.ValidFrom.IsZero() {
			t := mi.ValidFrom
			row.ValidFrom = &t
		}
		if !mi.ValidUntil.IsZero() {
			t := mi.ValidUntil
			row.ValidUntil = &t
		}
		if !mi.AppliedAt.IsZero() {
			t := mi.AppliedAt
			row.AppliedAt = &t
			row.Status = "applied"
		}
		// A rejection recorded after (or instead of) an apply wins.
		if !mi.RejectedAt.IsZero() && (mi.AppliedAt.IsZero() || mi.RejectedAt.After(mi.AppliedAt)) {
			t := mi.RejectedAt
			row.RejectedAt = &t
			row.Status = "rejected"
		}
		rows = append(rows, row)
	}

	// Heartbeat freshness gives "pending" its meaning: a dead uplink
	// means the manifest cannot have been fetched yet.
	var hbAt *time.Time
	var hbStatus string
	err = h.edge.Pool.QueryRow(r.Context(), `
		SELECT updated_at, COALESCE(status, '') FROM edge_heartbeats WHERE site_id = $1`,
		siteID).Scan(&hbAt, &hbStatus)
	if err != nil {
		hbAt = nil // no heartbeat yet — the device never connected
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"site_id":      siteID,
		"manifests":    rows,
		"heartbeat_at": hbAt,
		"heartbeat":    hbStatus,
	})
}
