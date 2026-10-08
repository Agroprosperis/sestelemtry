package api

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"time"

	"github.com/nesh/sestelemetry/internal/storage"
)

// dispatchFacts reads hourly means of the measured series in [from, to):
// PV, PCC (40505, + import) and BESS (sign per org config), SOC at the
// end of each hour. Load is the node balance — on a dual-logger site
// load_power_kw (PV logger 40503) does not see the BESS.
func (h *Handlers) dispatchFacts(ctx context.Context, env *dispatchEnv, from, to time.Time) (map[time.Time]*dispatchFact, error) {
	out := map[time.Time]*dispatchFact{}
	if h.store == nil {
		return out, nil
	}
	avg, err := h.store.Timeseries(ctx, env.siteID,
		[]string{"active_pv_power_kw", "grid_connected_active_power_kw", "active_ess_power_kw"},
		from, to.Add(-time.Second), "1 hour", env.tz, AggregationAvg)
	if err != nil {
		return out, err
	}
	last, err := h.store.Timeseries(ctx, env.siteID, []string{"soc_percent"}, from, to.Add(-time.Second), "1 hour", env.tz, AggregationLast)
	if err != nil {
		return out, err
	}
	sign := 1.0
	if cfg, ok := h.energyFlowOrgs[env.siteID]; ok && cfg.EssDischargeSign == -1 {
		sign = -1
	}
	get := func(ts time.Time) *dispatchFact {
		key := ts.UTC().Truncate(time.Hour)
		f, ok := out[key]
		if !ok {
			f = &dispatchFact{}
			out[key] = f
		}
		return f
	}
	for _, p := range avg.Points {
		v := round1(p.Value)
		f := get(p.Time)
		switch p.MetricKey {
		case "active_pv_power_kw":
			f.PvKw = &v
		case "grid_connected_active_power_kw":
			f.GridKw = &v
		case "active_ess_power_kw":
			s := round1(p.Value * sign)
			f.EssKw = &s
		}
	}
	for _, p := range last.Points {
		v := round1(p.Value)
		get(p.Time).SocPct = &v
	}
	for _, f := range out {
		if f.PvKw != nil && f.GridKw != nil && f.EssKw != nil {
			load := round1(math.Max(0, *f.GridKw+*f.PvKw+*f.EssKw))
			f.LoadKw = &load
		}
	}
	return out, nil
}

// --- history: plan in force -------------------------------------------

// plansInForce maps each hour to the planned hour of the newest run
// published before that hour started (what the edge was following).
// runs must be oldest first (DispatchRunsBetween order).
func plansInForce(runs []storage.DispatchRun, hours []time.Time) map[time.Time]dispatchRunHour {
	out := map[time.Time]dispatchRunHour{}
	decoded := make([][]dispatchRunHour, len(runs))
	for i, r := range runs {
		_ = json.Unmarshal(r.Hours, &decoded[i])
	}
	for _, ts := range hours {
		for i := len(runs) - 1; i >= 0; i-- {
			if runs[i].RunAt.After(ts) {
				continue
			}
			found := false
			for _, hr := range decoded[i] {
				if hr.TS.Equal(ts) {
					out[ts] = hr
					found = true
					break
				}
			}
			if found {
				break
			}
			// The newest run before ts does not cover it (no known load
			// that far): the edge had no plan for the hour.
			if !runs[i].StartHour.After(ts) {
				break
			}
		}
	}
	return out
}

type dispatchDayHour struct {
	TS   time.Time        `json:"ts"`
	Rdn  *float64         `json:"rdn_uah_per_kwh"`
	Buy  *float64         `json:"buy_uah_per_kwh"`
	Sell *float64         `json:"sell_uah_per_kwh"`
	Fact *dispatchFact    `json:"fact,omitempty"`
	Plan *dispatchRunHour `json:"plan,omitempty"`
}

type dispatchDayResponse struct {
	SiteID   string            `json:"site_id"`
	Timezone string            `json:"timezone"`
	Date     string            `json:"date"`
	Site     dispatchSiteInfo  `json:"site"`
	Hours    []dispatchDayHour `json:"hours"`
}

// dispatchDay handles GET /api/v1/dispatch/day?site_id=&date=YYYY-MM-DD:
// a past day for read-only review — measured fact next to the plan the
// edge was following.
func (h *Handlers) dispatchDay(w http.ResponseWriter, r *http.Request) {
	siteID, ok := h.requireEdge(w, r, http.MethodGet)
	if !ok {
		return
	}
	ctx := r.Context()
	env, err := h.loadDispatchEnv(ctx, siteID)
	if err != nil {
		h.edge.Log.Error("dispatch_day", "site_id", siteID, "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	date := r.URL.Query().Get("date")
	day, err := time.ParseInLocation("2006-01-02", date, env.loc)
	if err != nil {
		http.Error(w, "date must be YYYY-MM-DD", http.StatusBadRequest)
		return
	}
	end := day.AddDate(0, 0, 1)
	if !day.Before(env.start) {
		http.Error(w, "the day must be in the past", http.StatusBadRequest)
		return
	}
	if end.After(env.start) {
		end = env.start
	}
	zone := h.edge.PlannerZone
	if zone == 0 {
		zone = 2
	}
	prices, err := h.damPricesForDates(ctx, zone, []string{date}, env.loc)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	facts, err := h.dispatchFacts(ctx, env, day.UTC(), end.UTC())
	if err != nil {
		h.edge.Log.Warn("dispatch_day_facts", "site_id", siteID, "err", err)
	}
	var hours []time.Time
	for ts := day.UTC(); ts.Before(end.UTC()); ts = ts.Add(time.Hour) {
		hours = append(hours, ts)
	}
	runs, err := storage.DispatchRunsBetween(ctx, h.edge.Pool, siteID, day.UTC(), end.UTC())
	if err != nil {
		h.edge.Log.Warn("dispatch_day_runs", "site_id", siteID, "err", err)
	}
	plans := plansInForce(runs, hours)

	resp := dispatchDayResponse{SiteID: siteID, Timezone: env.tz, Date: date, Site: env.siteInfo()}
	for _, ts := range hours {
		hr := dispatchDayHour{TS: ts, Fact: facts[ts]}
		if p, ok := prices[ts]; ok {
			buy, sell := env.site.Prices(p)
			hr.Rdn, hr.Buy, hr.Sell = f64p(round3(p)), f64p(round3(buy)), f64p(round3(sell))
		}
		if p, ok := plans[ts]; ok {
			pl := p
			hr.Plan = &pl
		}
		resp.Hours = append(resp.Hours, hr)
	}
	writeJSON(w, http.StatusOK, resp)
}
