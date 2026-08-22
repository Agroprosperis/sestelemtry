package api

import (
	"encoding/json"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nesh/sestelemetry/internal/economics"
	"github.com/nesh/sestelemetry/internal/storage"
)

// Planner-UI endpoints (operator-facing, same trust level as the other
// dashboard APIs — no edge Bearer token):
//
//	GET  /api/v1/edge/sites         — edge-enabled sites for the picker
//	GET  /api/v1/edge/load-plan     — stored operator load hours
//	PUT  /api/v1/edge/load-plan     — upsert operator load hours
//	DELETE /api/v1/edge/load-plan   — clear hours (back to heuristic)
//	POST /api/v1/edge/plan/preview  — run the forward DP without publishing
//	GET  /api/v1/edge/manifests     — manifest versions + delivery status

// requireEdge resolves the shared preconditions of every planner
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
		sites = append(sites, s)
	}
	sort.Strings(sites)
	writeJSON(w, http.StatusOK, map[string]any{"sites": sites})
}

type edgeLoadPlanEntry struct {
	TS     time.Time `json:"ts"`
	LoadKw float64   `json:"load_kw"`
}

// edgeLoadPlan handles GET/PUT/DELETE /api/v1/edge/load-plan?site_id=.
func (h *Handlers) edgeLoadPlan(w http.ResponseWriter, r *http.Request) {
	siteID, ok := h.requireEdge(w, r, http.MethodGet, http.MethodPut, http.MethodDelete)
	if !ok {
		return
	}
	from, to := h.plannerHorizon()

	switch r.Method {
	case http.MethodGet:
		stored, err := storage.GetEdgeLoadPlan(r.Context(), h.edge.Pool, siteID, from, to)
		if err != nil {
			h.edge.Log.Error("edge_load_plan_get", "site_id", siteID, "err", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		entries := make([]edgeLoadPlanEntry, 0, len(stored))
		for ts, kw := range stored {
			entries = append(entries, edgeLoadPlanEntry{TS: ts, LoadKw: kw})
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].TS.Before(entries[j].TS) })
		writeJSON(w, http.StatusOK, map[string]any{"site_id": siteID, "entries": entries})

	case http.MethodPut:
		var body struct {
			Entries []edgeLoadPlanEntry `json:"entries"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
			return
		}
		rows := make([]storage.EdgeLoadPlanEntry, 0, len(body.Entries))
		for _, e := range body.Entries {
			if e.LoadKw < 0 || math.IsNaN(e.LoadKw) || math.IsInf(e.LoadKw, 0) {
				http.Error(w, "load_kw must be a non-negative number", http.StatusBadRequest)
				return
			}
			rows = append(rows, storage.EdgeLoadPlanEntry{Hour: e.TS, LoadKw: e.LoadKw})
		}
		if err := storage.UpsertEdgeLoadPlan(r.Context(), h.edge.Pool, siteID, rows); err != nil {
			h.edge.Log.Error("edge_load_plan_put", "site_id", siteID, "err", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"site_id": siteID, "saved": len(rows)})

	case http.MethodDelete:
		n, err := storage.DeleteEdgeLoadPlan(r.Context(), h.edge.Pool, siteID, from, to)
		if err != nil {
			h.edge.Log.Error("edge_load_plan_delete", "site_id", siteID, "err", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"site_id": siteID, "deleted": n})
	}
}

// plannerHorizon returns the planning window "current hour → end of
// tomorrow" in the planner timezone.
func (h *Handlers) plannerHorizon() (from, to time.Time) {
	tz := h.edge.PlannerTimezone
	if tz == "" {
		tz = "Europe/Kyiv"
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		loc = time.UTC
	}
	now := time.Now().In(loc)
	from = now.Truncate(time.Hour)
	to = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, 2)
	return from, to
}

// --- plan preview ---

type edgePlanPreviewHour struct {
	TS           time.Time `json:"ts"`
	LocalHour    int       `json:"local_hour"`
	Tomorrow     bool      `json:"tomorrow"`
	Tradable     bool      `json:"tradable"`
	RdnUahPerKwh *float64  `json:"rdn_uah_per_kwh,omitempty"`
	ImportUah    float64   `json:"import_uah_per_kwh"`
	ExportUah    float64   `json:"export_uah_per_kwh"`

	PvKw         float64 `json:"pv_kw"`
	LoadKw       float64 `json:"load_kw"`
	OperatorLoad bool    `json:"operator_load"`

	Weather *edgeHourWeather `json:"weather,omitempty"`

	EssKw         float64 `json:"ess_kw"` // + розряд / − заряд
	ChargePvKwh   float64 `json:"charge_pv_kwh"`
	ChargeGridKwh float64 `json:"charge_grid_kwh"`
	DischargeKwh  float64 `json:"discharge_kwh"`
	GridKw        float64 `json:"grid_kw"` // плановий імпорт(+) із урахуванням УЗЕ
	SocEndPct     float64 `json:"soc_end_pct"`
	Action        string  `json:"action"`
}

// edgePlanDayEffect is the §3 day-slice of the continuous optimisation:
// flows over the civil day plus the shadow value of the SOC change.
type edgePlanDayEffect struct {
	Date     string `json:"date"`
	Tomorrow bool   `json:"tomorrow"`

	EssToLoadUah      float64 `json:"ess_to_load_uah"`
	PvChargeCostUah   float64 `json:"pv_charge_cost_uah"`
	GridChargeCostUah float64 `json:"grid_charge_cost_uah"`
	DegradationUah    float64 `json:"degradation_uah"`
	FlowsUah          float64 `json:"flows_uah"`

	SocOpenPct  float64 `json:"soc_open_pct"`
	SocClosePct float64 `json:"soc_close_pct"`
	SocCarryUah float64 `json:"soc_carry_uah"`

	NetEffectUah    float64 `json:"net_effect_uah"`
	BaselineCostUah float64 `json:"baseline_cost_uah"`
	PlanCostUah     float64 `json:"plan_cost_uah"`

	EssToLoadKwh  float64 `json:"ess_to_load_kwh"`
	ChargePvKwh   float64 `json:"charge_pv_kwh"`
	ChargeGridKwh float64 `json:"charge_grid_kwh"`
}

type edgePlanPreviewResponse struct {
	SiteID        string    `json:"site_id"`
	Timezone      string    `json:"timezone"`
	Now           time.Time `json:"now"`
	HorizonStart  time.Time `json:"horizon_start"`
	HorizonEnd    time.Time `json:"horizon_end"`
	TomorrowStart time.Time `json:"tomorrow_start"`
	LoadSource    string    `json:"load_source"`

	Params struct {
		CapacityKwh          float64 `json:"capacity_kwh"`
		PowerKw              float64 `json:"power_kw"`
		PvRatedKw            float64 `json:"pv_rated_kw"`
		SocMinPct            float64 `json:"soc_min_pct"`
		SocMaxPct            float64 `json:"soc_max_pct"`
		StartSocPct          float64 `json:"start_soc_pct"`
		DegradationUahPerKwh float64 `json:"degradation_uah_per_kwh"`
	} `json:"params"`

	Hours []edgePlanPreviewHour `json:"hours"`
	Days  []edgePlanDayEffect   `json:"days"`
}

// edgePlanPreview handles POST /api/v1/edge/plan/preview?site_id=.
// Body: {"draft":[{"ts":...,"load_kw":...}]} — unsaved editor hours
// that override the stored operator plan for this run only.
func (h *Handlers) edgePlanPreview(w http.ResponseWriter, r *http.Request) {
	siteID, ok := h.requireEdge(w, r, http.MethodPost)
	if !ok {
		return
	}
	var body struct {
		Draft []edgeLoadPlanEntry `json:"draft"`
	}
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err.Error() != "EOF" {
			http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
			return
		}
	}
	var draft map[time.Time]float64
	if len(body.Draft) > 0 {
		draft = make(map[time.Time]float64, len(body.Draft))
		for _, e := range body.Draft {
			draft[e.TS.UTC().Truncate(time.Hour)] = e.LoadKw
		}
	}

	in, err := h.gatherEdgePlanInputs(r.Context(), siteID, draft)
	if err != nil {
		h.edge.Log.Error("edge_plan_preview", "site_id", siteID, "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	steps, err := economics.BuildForwardPlan(in.Hours, economics.ForwardParams{
		Tariffs:     in.Tariffs,
		CapacityKwh: in.CapacityKwh,
		PowerKw:     in.PowerKw,
		SocMinPct:   in.SocMin,
		SocMaxPct:   in.SocMax,
		StartSocPct: in.StartSoc,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	resp := buildEdgePlanPreview(siteID, in, steps)
	writeJSON(w, http.StatusOK, resp)
}

// buildEdgePlanPreview projects DP inputs+steps into the UI payload.
// Pure — unit-tested without a database.
func buildEdgePlanPreview(siteID string, in edgePlanInputs, steps []economics.ForwardStep) edgePlanPreviewResponse {
	resp := edgePlanPreviewResponse{
		SiteID:       siteID,
		Timezone:     in.Timezone,
		Now:          in.Now,
		HorizonStart: in.Start,
		HorizonEnd:   in.End,
		LoadSource:   in.LoadSource,
	}
	resp.TomorrowStart = time.Date(in.Now.Year(), in.Now.Month(), in.Now.Day(), 0, 0, 0, 0, in.Loc).AddDate(0, 0, 1)
	resp.Params.CapacityKwh = in.CapacityKwh
	resp.Params.PowerKw = in.PowerKw
	resp.Params.PvRatedKw = in.PvRatedKw
	resp.Params.SocMinPct = in.SocMin
	resp.Params.SocMaxPct = in.SocMax
	resp.Params.StartSocPct = in.StartSoc
	resp.Params.DegradationUahPerKwh = in.Tariffs.DegradationUahPerKwh

	resp.Hours = make([]edgePlanPreviewHour, 0, len(steps))
	for i, s := range steps {
		fh := in.Hours[i]
		local := fh.TS.In(in.Loc)
		hr := edgePlanPreviewHour{
			TS:           fh.TS.UTC(),
			LocalHour:    local.Hour(),
			Tomorrow:     !local.Before(resp.TomorrowStart),
			Tradable:     s.Tradable,
			PvKw:         round1(fh.PvKw),
			LoadKw:       round1(fh.LoadKw),
			OperatorLoad: in.OperatorHour[fh.TS.UTC()],

			EssKw:         round1(s.EssKw),
			ChargePvKwh:   round1(s.ChargePvKwh),
			ChargeGridKwh: round1(s.ChargeGridKwh),
			DischargeKwh:  round1(s.DischargeKwh),
			SocEndPct:     round1(s.SocEndPct),
			Action:        s.Action,
		}
		if fh.RdnUahPerKwh != nil {
			p := round3(*fh.RdnUahPerKwh)
			hr.RdnUahPerKwh = &p
			imp, exp := economics.ImportExportPrices(in.Tariffs, *fh.RdnUahPerKwh)
			hr.ImportUah = round3(imp)
			hr.ExportUah = round3(exp)
		}
		if wx, ok := in.Weather[fh.TS.UTC()]; ok {
			w := wx
			hr.Weather = &w
		}
		// Planned grid draw: local deficit minus battery help plus
		// grid charging (never negative — export is not planned).
		deficit := math.Max(0, fh.LoadKw-fh.PvKw)
		hr.GridKw = round1(math.Max(0, deficit-s.DischargeKwh+s.ChargeGridKwh))
		resp.Hours = append(resp.Hours, hr)
	}

	resp.Days = computeEdgeDayEffects(in, steps, resp.TomorrowStart)
	return resp
}

// computeEdgeDayEffects slices the continuous plan into civil days and
// prices each day per spec §3:
//
//	ефект(D) = потоки(D) + тіньова_цінність_SOC(D)
//	потоки   = Σ( розряд·imp − заряд_з_мережі·imp − заряд_від_СЕС·exp − знос )
//	тіньова  = ΔSOC(D)·capacity·min(imp за D)
func computeEdgeDayEffects(in edgePlanInputs, steps []economics.ForwardStep, tomorrowStart time.Time) []edgePlanDayEffect {
	byDay := map[string]*edgePlanDayEffect{}
	var order []string
	socOpen := map[string]float64{}
	socClose := map[string]float64{}
	shadow := map[string]float64{}

	prevSoc := in.StartSoc
	for i, s := range steps {
		fh := in.Hours[i]
		local := fh.TS.In(in.Loc)
		day := local.Format("2006-01-02")
		d, seen := byDay[day]
		if !seen {
			d = &edgePlanDayEffect{
				Date:     day,
				Tomorrow: !local.Before(tomorrowStart) && local.Before(tomorrowStart.AddDate(0, 0, 1)),
			}
			byDay[day] = d
			order = append(order, day)
			socOpen[day] = prevSoc
		}
		socClose[day] = s.SocEndPct
		prevSoc = s.SocEndPct

		if !s.Tradable || fh.RdnUahPerKwh == nil {
			continue
		}
		imp, exp := economics.ImportExportPrices(in.Tariffs, *fh.RdnUahPerKwh)
		if cur, ok := shadow[day]; !ok || imp < cur {
			shadow[day] = imp
		}

		d.EssToLoadUah += s.DischargeKwh * imp
		d.GridChargeCostUah += s.ChargeGridKwh * imp
		d.PvChargeCostUah += s.ChargePvKwh * exp
		d.DegradationUah += s.DischargeKwh * in.Tariffs.DegradationUahPerKwh

		d.EssToLoadKwh += s.DischargeKwh
		d.ChargePvKwh += s.ChargePvKwh
		d.ChargeGridKwh += s.ChargeGridKwh

		// Baseline «без УЗЕ»: the whole deficit is imported.
		d.BaselineCostUah += math.Max(0, fh.LoadKw-fh.PvKw) * imp
	}

	out := make([]edgePlanDayEffect, 0, len(order))
	for _, day := range order {
		d := byDay[day]
		d.FlowsUah = d.EssToLoadUah - d.GridChargeCostUah - d.PvChargeCostUah - d.DegradationUah
		d.SocOpenPct = round1(socOpen[day])
		d.SocClosePct = round1(socClose[day])
		d.SocCarryUah = (socClose[day] - socOpen[day]) / 100 * in.CapacityKwh * shadow[day]
		d.NetEffectUah = d.FlowsUah + d.SocCarryUah
		// «Без УЗЕ vs з планом»: consistent by construction.
		d.PlanCostUah = d.BaselineCostUah - d.FlowsUah

		d.EssToLoadUah = round1(d.EssToLoadUah)
		d.PvChargeCostUah = round1(d.PvChargeCostUah)
		d.GridChargeCostUah = round1(d.GridChargeCostUah)
		d.DegradationUah = round1(d.DegradationUah)
		d.FlowsUah = round1(d.FlowsUah)
		d.SocCarryUah = round1(d.SocCarryUah)
		d.NetEffectUah = round1(d.NetEffectUah)
		d.BaselineCostUah = round1(d.BaselineCostUah)
		d.PlanCostUah = round1(d.PlanCostUah)
		d.EssToLoadKwh = round1(d.EssToLoadKwh)
		d.ChargePvKwh = round1(d.ChargePvKwh)
		d.ChargeGridKwh = round1(d.ChargeGridKwh)
		out = append(out, *d)
	}
	return out
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
