package api

import (
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/nesh/sestelemetry/internal/dispatch"
)

// Expected effect of the applied desk plan per civil day — the waterfall
// of the old planner's step 3, now in the economics day report:
//
//	ефект(D) = потоки(D) + тіньова_цінність_SOC(D)
//	потоки   = Σ( розряд_у_load·imp + експорт·exp − заряд_з_мережі·imp − заряд_від_СЕС·exp − знос )
//	тіньова  = ΔSOC(D)·capacity·min(imp за D)
//
// Only the LP's known hours count (from the current hour while load and
// RDN are known), so today is the rest of the day and tomorrow may be
// partial.

type dispatchDayEffect struct {
	Date  string    `json:"date"`
	From  time.Time `json:"from"`
	Until time.Time `json:"until"`
	Hours int       `json:"hours"`

	EssToLoadUah      float64 `json:"ess_to_load_uah"`
	EssToGridUah      float64 `json:"ess_to_grid_uah"`
	PvChargeCostUah   float64 `json:"pv_charge_cost_uah"`
	GridChargeCostUah float64 `json:"grid_charge_cost_uah"`
	DegradationUah    float64 `json:"degradation_uah"`
	FlowsUah          float64 `json:"flows_uah"`

	SocOpenPct  float64 `json:"soc_open_pct"`
	SocClosePct float64 `json:"soc_close_pct"`
	SocCarryUah float64 `json:"soc_carry_uah"`
	// ShadowPriceUah is the slice's cheapest all-in import price — the
	// rate the SOC carry-over is valued at.
	ShadowPriceUah float64 `json:"shadow_price_uah"`

	NetEffectUah    float64 `json:"net_effect_uah"`
	BaselineCostUah float64 `json:"baseline_cost_uah"`
	PlanCostUah     float64 `json:"plan_cost_uah"`

	EssToLoadKwh  float64 `json:"ess_to_load_kwh"`
	EssToGridKwh  float64 `json:"ess_to_grid_kwh"`
	ChargePvKwh   float64 `json:"charge_pv_kwh"`
	ChargeGridKwh float64 `json:"charge_grid_kwh"`
}

// dispatchDayEffects slices the LP result into local civil days. Pure.
func dispatchDayEffects(env *dispatchEnv, m dispatch.Model, res dispatch.Result) []dispatchDayEffect {
	// PV stored while export is off would otherwise be curtailed, so
	// charging from it forgoes no export revenue.
	exportOK := !m.Cfg.BlockExport && m.Cfg.EffectiveExportCap() > 0
	rate := env.site.Tariffs.DegradationUahPerKwh
	var out []dispatchDayEffect
	var shadow []float64
	for i := 0; i < res.KnownHours && i < len(res.Hours); i++ {
		hr := res.Hours[i]
		if hr.Unknown || hr.P == nil || hr.Soc == nil || hr.Before == nil || m.Loads[i] == nil {
			break
		}
		ts := env.start.Add(time.Duration(i) * time.Hour)
		day := ts.In(env.loc).Format("2006-01-02")
		if len(out) == 0 || out[len(out)-1].Date != day {
			out = append(out, dispatchDayEffect{Date: day, From: ts, SocOpenPct: *hr.Before})
			shadow = append(shadow, math.Inf(1))
		}
		k := len(out) - 1
		d := &out[k]
		d.Until = ts.Add(time.Hour)
		d.Hours++
		d.SocClosePct = *hr.Soc
		if env.inputs.Rdn[i] == nil {
			continue
		}
		imp, exp := env.site.Prices(*env.inputs.Rdn[i])
		shadow[k] = math.Min(shadow[k], imp)
		load, pv := *m.Loads[i], env.inputs.PV[i]
		var toLoad, toGrid, chgPv, chgGrid float64
		for _, part := range dispatch.SplitFlow(*hr.P, pv, load) {
			switch part.Key {
			case "load":
				toLoad = part.Power
			case "export":
				toGrid = part.Power
			case "solar":
				chgPv = -part.Power
			case "grid":
				chgGrid = -part.Power
			}
		}
		d.EssToLoadUah += toLoad * imp
		d.EssToGridUah += toGrid * exp
		d.GridChargeCostUah += chgGrid * imp
		if exportOK {
			d.PvChargeCostUah += chgPv * exp
		}
		d.DegradationUah += (toLoad + toGrid) * rate
		d.EssToLoadKwh += toLoad
		d.EssToGridKwh += toGrid
		d.ChargePvKwh += chgPv
		d.ChargeGridKwh += chgGrid
		// Baseline «без УЗЕ»: the whole local deficit is imported.
		d.BaselineCostUah += math.Max(0, load-pv) * imp
	}
	for k := range out {
		d := &out[k]
		d.FlowsUah = d.EssToLoadUah + d.EssToGridUah - d.GridChargeCostUah - d.PvChargeCostUah - d.DegradationUah
		if !math.IsInf(shadow[k], 1) {
			d.ShadowPriceUah = round3(shadow[k])
			d.SocCarryUah = (d.SocClosePct - d.SocOpenPct) / 100 * env.site.CapacityKwh * shadow[k]
		}
		d.NetEffectUah = d.FlowsUah + d.SocCarryUah
		d.PlanCostUah = d.BaselineCostUah - d.FlowsUah

		for _, v := range []*float64{
			&d.EssToLoadUah, &d.EssToGridUah, &d.PvChargeCostUah, &d.GridChargeCostUah, &d.DegradationUah,
			&d.FlowsUah, &d.SocCarryUah, &d.NetEffectUah, &d.BaselineCostUah, &d.PlanCostUah,
			&d.EssToLoadKwh, &d.EssToGridKwh, &d.ChargePvKwh, &d.ChargeGridKwh, &d.SocOpenPct, &d.SocClosePct,
		} {
			*v = round1(*v)
		}
	}
	return out
}

type dispatchEffectResponse struct {
	SiteID    string             `json:"site_id"`
	Date      string             `json:"date"`
	Version   int                `json:"version"`
	Available bool               `json:"available"`
	Reason    string             `json:"reason,omitempty"`
	Effect    *dispatchDayEffect `json:"effect,omitempty"`
}

// dispatchEffect handles GET /api/v1/dispatch/effect?organization_id=&date=:
// the expected effect of the applied desk plan on one civil day (today
// or tomorrow) for the economics day report.
func (h *Handlers) dispatchEffect(w http.ResponseWriter, r *http.Request) {
	if h.edge == nil {
		http.Error(w, "edge ingest not configured", http.StatusServiceUnavailable)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	siteID := strings.TrimSpace(r.URL.Query().Get("organization_id"))
	if siteID == "" {
		http.Error(w, "organization_id is required", http.StatusBadRequest)
		return
	}
	if _, ok := h.edge.Tokens[siteID]; !ok {
		http.Error(w, "unknown edge site", http.StatusNotFound)
		return
	}
	ctx := r.Context()
	env, err := h.loadDispatchEnv(ctx, siteID)
	if err != nil {
		h.edge.Log.Error("dispatch_effect", "site_id", siteID, "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	date := r.URL.Query().Get("date")
	if _, err := time.ParseInLocation("2006-01-02", date, env.loc); err != nil {
		http.Error(w, "date must be YYYY-MM-DD", http.StatusBadRequest)
		return
	}
	m, ver, err := h.appliedModel(ctx, env)
	if err != nil {
		h.edge.Log.Error("dispatch_effect_applied", "site_id", siteID, "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	res, err := dispatch.Simulate(env.inputs, m)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	resp := dispatchEffectResponse{SiteID: siteID, Date: date, Version: ver.Version}
	for _, d := range dispatchDayEffects(env, m, res) {
		if d.Date == date {
			eff := d
			resp.Effect = &eff
			resp.Available = true
		}
	}
	if !resp.Available {
		if res.KnownHours == 0 {
			resp.Reason = "У пульті не задано споживання на найближчі години — плану немає, edge працює на самоспоживання."
		} else {
			resp.Reason = "План пульта не покриває цю дату."
		}
	}
	writeJSON(w, http.StatusOK, resp)
}
