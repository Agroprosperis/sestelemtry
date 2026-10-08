package api

// Manual-control desk API (ems-spec docs/specs/ems_manual_control_mvp.md):
//
//	GET  /api/v1/dispatch/state?site_id=    now, envelope, 8 h fact + 24 h forecast, applied plan
//	POST /api/v1/dispatch/preview?site_id=  LP on a draft, nothing stored
//	POST /api/v1/dispatch/confirm?site_id=  store a new version and publish its plan
//
// The desk works on arrays aligned to start_hour (index 0 = the current
// hour); stored versions are keyed by UTC hour so they survive time
// moving on.

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/nesh/sestelemetry/internal/dispatch"
	"github.com/nesh/sestelemetry/internal/economics"
	"github.com/nesh/sestelemetry/internal/storage"
)

type dispatchSiteInfo struct {
	CapacityKwh     float64  `json:"capacity_kwh"`
	ChargeKw        float64  `json:"charge_kw"`
	DischargeKw     float64  `json:"discharge_kw"`
	ImportKw        float64  `json:"import_kw"`
	ImportSet       bool     `json:"import_set"`
	SocMinPct       float64  `json:"soc_min_pct"`
	SocMaxPct       float64  `json:"soc_max_pct"`
	Eta             float64  `json:"eta"`
	PvRatedKw       float64  `json:"pv_rated_kw"`
	ExportRegime    string   `json:"export_regime"`
	ExportCeilingKw *float64 `json:"export_ceiling_kw"`
}

type dispatchTariffs struct {
	DistributionUahPerKwh float64 `json:"distribution_uah_per_kwh"`
	TransmissionUahPerKwh float64 `json:"transmission_uah_per_kwh"`
	SupplierMarginUah     float64 `json:"supplier_margin_uah_per_kwh"`
	SupplierMarginMode    string  `json:"supplier_margin_mode"`
	SupplierMarginPct     float64 `json:"supplier_margin_pct"`
	OtherFeesUahPerKwh    float64 `json:"other_fees_uah_per_kwh"`
	ExportDiscount        float64 `json:"export_discount"`
	DegradationUahPerKwh  float64 `json:"degradation_uah_per_kwh"`
	IncludeVat            bool    `json:"include_vat"`
	VatRate               float64 `json:"vat_rate"`
	RoundtripEfficiency   float64 `json:"roundtrip_efficiency"`
}

// dispatchFact is one measured hour (history): hourly means, SOC at the
// end of the hour, load from the node balance (grid + PV + BESS).
type dispatchFact struct {
	PvKw   *float64 `json:"pv_kw"`
	LoadKw *float64 `json:"load_kw"`
	GridKw *float64 `json:"grid_kw"` // + import / − export
	EssKw  *float64 `json:"ess_kw"`  // + discharge / − charge
	SocPct *float64 `json:"soc_pct"`
}

type dispatchHour struct {
	TS      time.Time        `json:"ts"`
	Offset  int              `json:"offset"` // hours from start_hour (history < 0)
	Rdn     *float64         `json:"rdn_uah_per_kwh"`
	Buy     *float64         `json:"buy_uah_per_kwh"`
	Sell    *float64         `json:"sell_uah_per_kwh"`
	PvKw    float64          `json:"pv_kw"` // forecast (future)
	Weather *edgeHourWeather `json:"weather,omitempty"`
	Fact    *dispatchFact    `json:"fact,omitempty"`
	// Plan is what the edge was following in a history hour.
	Plan *dispatchRunHour `json:"plan,omitempty"`
}

type dispatchModelDTO struct {
	Loads    []*float64           `json:"loads"`
	Commands []*dispatch.Command  `json:"commands"`
	Cfg      dispatch.Constraints `json:"cfg"`
}

type dispatchApplied struct {
	Version     int              `json:"version"`
	ConfirmedAt *time.Time       `json:"confirmed_at"`
	ConfirmedBy string           `json:"confirmed_by"`
	Model       dispatchModelDTO `json:"model"`
}

type dispatchStateResponse struct {
	SiteID        string               `json:"site_id"`
	Timezone      string               `json:"timezone"`
	Now           time.Time            `json:"now"`
	StartHour     time.Time            `json:"start_hour"`
	HistoryHours  int                  `json:"history_hours"`
	FutureHours   int                  `json:"future_hours"`
	Site          dispatchSiteInfo     `json:"site"`
	Tariffs       dispatchTariffs      `json:"tariffs"`
	StartSocPct   float64              `json:"start_soc_pct"`
	StartSocKnown bool                 `json:"start_soc_known"`
	Hours         []dispatchHour       `json:"hours"`
	Defaults      dispatch.Constraints `json:"defaults"`
	Applied       dispatchApplied      `json:"applied"`
	Result        dispatch.Result      `json:"result"`
}

type dispatchPreviewResponse struct {
	StartHour time.Time        `json:"start_hour"`
	Invalid   string           `json:"invalid,omitempty"`
	Result    *dispatch.Result `json:"result,omitempty"`
}

type dispatchConfirmResponse struct {
	Version    int               `json:"version"`
	Publish    EdgePublishResult `json:"publish"`
	Invalid    string            `json:"invalid,omitempty"`
	Issues     []dispatch.Issue  `json:"issues,omitempty"`
	Conflict   bool              `json:"conflict,omitempty"`
	ResultHint string            `json:"message,omitempty"`
}

// --- handlers ------------------------------------------------------------

func (h *Handlers) dispatchState(w http.ResponseWriter, r *http.Request) {
	siteID, ok := h.requireEdge(w, r, http.MethodGet)
	if !ok {
		return
	}
	env, err := h.loadDispatchEnv(r.Context(), siteID)
	if err != nil {
		h.edge.Log.Error("dispatch_state", "site_id", siteID, "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	m, ver, err := h.appliedModel(r.Context(), env)
	if err != nil {
		h.edge.Log.Error("dispatch_state_applied", "site_id", siteID, "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	res, err := dispatch.Simulate(env.inputs, m)
	if err != nil {
		h.edge.Log.Error("dispatch_state_lp", "site_id", siteID, "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	histFrom := env.start.Add(-dispatchHistoryHours * time.Hour)
	facts, err := h.dispatchFacts(r.Context(), env, histFrom, env.start)
	if err != nil {
		h.edge.Log.Warn("dispatch_state_facts", "site_id", siteID, "err", err)
	}
	var histHours []time.Time
	for ts := histFrom; ts.Before(env.start); ts = ts.Add(time.Hour) {
		histHours = append(histHours, ts)
	}
	runs, err := storage.DispatchRunsBetween(r.Context(), h.edge.Pool, siteID, histFrom, env.start)
	if err != nil {
		h.edge.Log.Warn("dispatch_state_runs", "site_id", siteID, "err", err)
	}
	plans := plansInForce(runs, histHours)

	resp := dispatchStateResponse{
		SiteID: siteID, Timezone: env.tz, Now: env.now, StartHour: env.start,
		HistoryHours: dispatchHistoryHours, FutureHours: dispatchFutureHours,
		Site:          env.siteInfo(),
		Tariffs:       tariffsDTO(env.site.Tariffs),
		StartSocPct:   env.inputs.StartKwh / env.site.CapacityKwh * 100,
		StartSocKnown: env.startSoc != nil,
		Defaults:      env.defaults,
		Applied: dispatchApplied{
			Version: ver.Version, ConfirmedBy: ver.ConfirmedBy,
			Model: dispatchModelDTO{Loads: m.Loads, Commands: m.Commands, Cfg: m.Cfg},
		},
		Result: res,
	}
	if ver.Version > 0 {
		t := ver.ConfirmedAt
		resp.Applied.ConfirmedAt = &t
	}
	for off := -dispatchHistoryHours; off < dispatchFutureHours; off++ {
		ts := env.start.Add(time.Duration(off) * time.Hour)
		hr := dispatchHour{TS: ts, Offset: off}
		if p, ok := env.rdn[ts]; ok {
			buy, sell := env.site.Prices(p)
			hr.Rdn, hr.Buy, hr.Sell = f64p(round3(p)), f64p(round3(buy)), f64p(round3(sell))
		}
		if off >= 0 {
			hr.PvKw = round1(env.pv[ts])
			if wx, ok := env.weather[ts]; ok {
				w := wx
				hr.Weather = &w
			}
		} else {
			hr.Fact = facts[ts]
			if p, ok := plans[ts]; ok {
				pl := p
				hr.Plan = &pl
			}
		}
		resp.Hours = append(resp.Hours, hr)
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handlers) dispatchPreview(w http.ResponseWriter, r *http.Request) {
	siteID, ok := h.requireEdge(w, r, http.MethodPost)
	if !ok {
		return
	}
	var req dispatchDraftRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	env, err := h.loadDispatchEnv(r.Context(), siteID)
	if err != nil {
		h.edge.Log.Error("dispatch_preview", "site_id", siteID, "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	m, err := req.model(env.start)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	resp := dispatchPreviewResponse{StartHour: env.start}
	if msg := validateModel(env, m); msg != "" {
		resp.Invalid = msg
		writeJSON(w, http.StatusOK, resp)
		return
	}
	res, err := dispatch.Simulate(env.inputs, m)
	if err != nil {
		h.edge.Log.Error("dispatch_preview_lp", "site_id", siteID, "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	resp.Result = &res
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handlers) dispatchConfirm(w http.ResponseWriter, r *http.Request) {
	siteID, ok := h.requireEdge(w, r, http.MethodPost)
	if !ok {
		return
	}
	var req dispatchDraftRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	env, err := h.loadDispatchEnv(ctx, siteID)
	if err != nil {
		h.edge.Log.Error("dispatch_confirm", "site_id", siteID, "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	m, err := req.model(env.start)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if msg := validateModel(env, m); msg != "" {
		writeJSON(w, http.StatusUnprocessableEntity, dispatchConfirmResponse{Invalid: msg})
		return
	}
	res, err := dispatch.Simulate(env.inputs, m)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Known shortfalls must be fixed before the plan is applied (guide
	// §6); hours past the known horizon are saved as intent only.
	if len(res.Issues) > 0 {
		writeJSON(w, http.StatusUnprocessableEntity, dispatchConfirmResponse{
			Issues: res.Issues, ResultHint: "Перед застосуванням виправте позначені інтервали.",
		})
		return
	}
	payload, err := json.Marshal(storedFromModel(m, env.start))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	by := ""
	if p := principalFrom(ctx); p != nil {
		by = p.Email
	}
	ver, err := storage.InsertDispatchVersion(ctx, h.edge.Pool, siteID, req.BaseVersion, by, payload)
	if errors.Is(err, storage.ErrDispatchVersionConflict) {
		writeJSON(w, http.StatusConflict, dispatchConfirmResponse{
			Conflict: true, ResultHint: "План змінив інший користувач. Оновіть сторінку й перенесіть свої зміни.",
		})
		return
	}
	if err != nil {
		h.edge.Log.Error("dispatch_confirm_store", "site_id", siteID, "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	pub, err := h.publishDispatch(ctx, siteID)
	if err != nil {
		h.edge.Log.Error("dispatch_confirm_publish", "site_id", siteID, "version", ver.Version, "err", err)
		http.Error(w, "план збережено, але публікація не вдалася: "+err.Error(), http.StatusBadGateway)
		return
	}
	h.edge.Log.Info("dispatch_confirmed", "site_id", siteID, "version", ver.Version, "by", by, "manifest_id", pub.ManifestID)
	writeJSON(w, http.StatusOK, dispatchConfirmResponse{Version: ver.Version, Publish: pub})
}

func (env *dispatchEnv) siteInfo() dispatchSiteInfo {
	return dispatchSiteInfo{
		CapacityKwh: env.site.CapacityKwh, ChargeKw: env.site.ChargeKw, DischargeKw: env.site.DischargeKw,
		ImportKw: env.site.ImportKw, ImportSet: env.importSet,
		SocMinPct: env.site.SocMinPct, SocMaxPct: env.site.SocMaxPct, Eta: env.site.Eta(),
		PvRatedKw: env.params.PvRatedKw, ExportRegime: env.exportRegime, ExportCeilingKw: env.exportCeiling,
	}
}

func tariffsDTO(t economics.Tariffs) dispatchTariffs {
	return dispatchTariffs{
		DistributionUahPerKwh: t.DistributionUahPerKwh, TransmissionUahPerKwh: t.TransmissionUahPerKwh,
		SupplierMarginUah: t.SupplierMarginUahPerKwh, SupplierMarginMode: t.SupplierMarginMode,
		SupplierMarginPct: t.SupplierMarginPct, OtherFeesUahPerKwh: t.OtherFeesUahPerKwh,
		ExportDiscount: t.ExportDiscount, DegradationUahPerKwh: t.DegradationUahPerKwh,
		IncludeVat: t.IncludeVat, VatRate: t.VatRate, RoundtripEfficiency: t.RoundtripEfficiency,
	}
}

func f64p(v float64) *float64 { return &v }
