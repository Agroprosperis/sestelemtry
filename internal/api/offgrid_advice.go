package api

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"sort"
	"time"

	"github.com/nesh/sestelemetry/internal/economics"
	"github.com/nesh/sestelemetry/internal/offgrid"
	"github.com/nesh/sestelemetry/internal/storage"
)

// Advice, the power at hand and plan against fact (ems_offgrid_advice.md
// §6–§9). Hints only: nothing here goes to the IoT2050.

// offgridDefaultExtraLPerKwh is the generator's extra fuel per kWh of
// charge when the tariff counts fuel by the hour (§7: the passport's
// 0,26 л/кВт·год).
const offgridDefaultExtraLPerKwh = 0.26

// offgridLive is what the IoT2050 last reported, for the advice.
type offgridLive struct {
	Online         bool     `json:"online"`
	Mode           string   `json:"mode,omitempty"`
	SocPct         *float64 `json:"soc_pct,omitempty"`
	DischargeMaxKw *float64 `json:"discharge_max_kw,omitempty"`
	PVKw           *float64 `json:"pv_kw,omitempty"`
	LoadKw         *float64 `json:"load_kw,omitempty"`
	GenKw          *float64 `json:"gen_kw,omitempty"`
}

func (h *Handlers) offgridLiveState(ctx context.Context, siteID string, now time.Time) offgridLive {
	st, err := storage.GetEdgeSiteStatus(ctx, h.edge.Pool, siteID, false)
	if err != nil {
		h.edge.Log.Warn("offgrid_live", "site_id", siteID, "err", err)
		return offgridLive{}
	}
	live := offgridLive{Online: !st.HeartbeatAt.IsZero() && now.Sub(st.HeartbeatAt) <= edgeOnlineThreshold}
	var health struct {
		Bess *struct {
			Soc       *float64 `json:"soc_percent"`
			Discharge *float64 `json:"discharge_max_kw"`
		} `json:"bess"`
		Supply *struct {
			Mode   string   `json:"mode"`
			GenKw  *float64 `json:"gen_power_kw"`
			LoadKw *float64 `json:"load_kw"`
		} `json:"supply"`
	}
	if len(st.Health) > 0 && json.Unmarshal(st.Health, &health) == nil {
		if b := health.Bess; b != nil {
			live.SocPct, live.DischargeMaxKw = b.Soc, b.Discharge
		}
		if s := health.Supply; s != nil {
			live.Mode, live.GenKw, live.LoadKw = s.Mode, s.GenKw, s.LoadKw
		}
	}
	var rec struct {
		Inputs struct {
			PV   *float64 `json:"pv_power_kw"`
			Load *float64 `json:"load_power_kw"`
			Soc  *float64 `json:"soc_percent"`
		} `json:"inputs"`
	}
	if len(st.DecisionRecord) > 0 && json.Unmarshal(st.DecisionRecord, &rec) == nil {
		live.PVKw = rec.Inputs.PV
		if live.LoadKw == nil {
			live.LoadKw = rec.Inputs.Load
		}
		if live.SocPct == nil {
			live.SocPct = rec.Inputs.Soc
		}
	}
	return live
}

func offgridModeOff(mode string) bool {
	return mode == "blackout" || mode == "island" || mode == "generator"
}

func fuelOf(t economics.Tariffs) offgrid.Fuel {
	return offgrid.Fuel{
		Set: t.FuelSet(), Method: t.FuelMethod(),
		UahPerKwh: t.FuelCostPerKwh(), UahPerHour: t.FuelCostPerHour(),
		LPerKwh: t.GenFuelConsumptionLPerKwh, LPerHour: t.GenFuelConsumptionLPerH,
	}
}

func adviceParams(env *dispatchEnv, o offgridSettings) offgrid.AdviceParams {
	return offgrid.AdviceParams{
		CapacityKwh: env.site.CapacityKwh, FloorPct: o.FloorPct, SwitchSocPct: o.GeneratorSwitchSocPct,
		ChargeKw: env.site.ChargeKw, IslandMaxKw: o.IslandPowerMaxKw, GenRatedKw: o.GeneratorRatedKw,
		GenMinKw: o.GeneratorMinKw, ExtraStopThresholdUah: o.ExtraStopThresholdUah,
		Fuel: fuelOf(env.site.Tariffs),
	}
}

// recharge is §6.3 for an outage ending at end: the published prices of
// the hours after it, else the same hours of the last 7 days (an
// estimate).
func (h *Handlers) recharge(ctx context.Context, env *dispatchEnv, end time.Time, needKwh float64) (float64, bool) {
	var buys []float64
	for ts := end.Truncate(time.Hour); ts.Before(end.Add(24 * time.Hour)); ts = ts.Add(time.Hour) {
		if p, ok := env.rdn[ts]; ok {
			buy, _ := env.site.Prices(p)
			buys = append(buys, buy)
		}
	}
	t := env.site.Tariffs
	if len(buys) > 0 {
		return offgrid.RechargePrice(buys, needKwh, env.site.ChargeKw, t.RoundtripEfficiency, t.DegradationUahPerKwh), false
	}
	rows, err := storage.GetEconomicsHourly(ctx, h.edge.Pool, env.siteID, env.start.AddDate(0, 0, -offgridHistoryDays), env.start)
	if err == nil {
		for _, r := range rows {
			if r.ImportPrice > 0 {
				buys = append(buys, r.ImportPrice)
			}
		}
	}
	return offgrid.RechargePrice(buys, needKwh, env.site.ChargeKw, t.RoundtripEfficiency, t.DegradationUahPerKwh), true
}

type offgridAdviceOutage struct {
	Start     time.Time        `json:"start"`
	End       time.Time        `json:"end"`
	Kind      string           `json:"kind"`
	Final     bool             `json:"final"` // the grid is already gone: the advice is from the live charge
	SocPct    float64          `json:"soc_pct"`
	Recharge  float64          `json:"recharge_uah_per_kwh"`
	Estimated bool             `json:"recharge_estimated"`
	Advice    offgrid.Advice   `json:"advice"`
	Reserve   *offgrid.Reserve `json:"reserve,omitempty"`
}

type offgridUnscheduled struct {
	Hours  int            `json:"hours"`
	Advice offgrid.Advice `json:"advice"`
}

type offgridAdviceResp struct {
	SiteID      string                 `json:"site_id"`
	Now         time.Time              `json:"now"`
	Timezone    string                 `json:"timezone"`
	Live        offgridLive            `json:"live"`
	Available   *offgrid.Available     `json:"available,omitempty"`
	Outage      *offgridAdviceOutage   `json:"outage,omitempty"`
	Unscheduled []offgridUnscheduled   `json:"unscheduled,omitempty"`
	GenCharge   *offgrid.GenChargeHint `json:"gen_charge,omitempty"`
}

// offgridAdvice handles GET /api/v1/offgrid/advice?site_id=.
func (h *Handlers) offgridAdvice(w http.ResponseWriter, r *http.Request) {
	siteID, ok := h.requireEdge(w, r, http.MethodGet)
	if !ok {
		return
	}
	ctx := r.Context()
	env, err := h.loadDispatchEnv(ctx, siteID)
	if err != nil {
		h.edge.Log.Error("offgrid_advice_env", "site_id", siteID, "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	m, _, err := h.appliedModel(ctx, env)
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	d := env.offgrid
	if d == nil {
		settings, _ := h.loadEdgeSiteSettings(ctx, siteID)
		if d, err = h.loadOffgrid(ctx, env, settings); err != nil {
			h.edge.Log.Error("offgrid_advice_data", "site_id", siteID, "err", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
	}
	res, err := simulateEnv(env, m)
	if err != nil {
		h.edge.Log.Warn("offgrid_advice_plan", "site_id", siteID, "err", err)
	}
	resp := offgridAdviceResp{SiteID: siteID, Now: env.now, Timezone: env.tz, Live: h.offgridLiveState(ctx, siteID, env.now)}
	p := adviceParams(env, d.settings)
	k := 1 + d.params.ErrorMarginPct/100
	loads := d.loads(env, m)
	at := func(hour time.Time) (float64, float64) {
		load, _, ok := loads(hour)
		if !ok {
			load = 0
		}
		return load * k, env.pv[hour]
	}
	off := resp.Live.Online && offgridModeOff(resp.Live.Mode)
	reserves := d.reserves(env, m)
	var next *offgrid.Reserve
	for i := range reserves {
		if reserves[i].End.After(env.now) {
			next = &reserves[i]
			break
		}
	}
	liveSoc := 0.0
	if resp.Live.SocPct != nil {
		liveSoc = *resp.Live.SocPct
	}
	if next != nil {
		o := &offgridAdviceOutage{Start: next.Start, End: next.End, Kind: next.Kind, Reserve: next, SocPct: liveSoc}
		start := next.Start
		if off || !start.After(env.now) {
			o.Final, start = true, env.now
		} else if i := int(next.Start.Sub(env.start)/time.Hour) - 1; i >= 0 && i < len(res.Hours) && res.Hours[i].Soc != nil {
			o.SocPct = *res.Hours[i].Soc
		}
		hours := offgrid.HoursOf(start, next.End, at)
		need := 0.0
		for _, hr := range hours {
			need += math.Max(0, hr.LoadKw-hr.PVKw) * hr.Fraction
		}
		p.RechargeUahPerKwh, o.Estimated = h.recharge(ctx, env, next.End, need)
		o.Recharge = math.Round(p.RechargeUahPerKwh*100) / 100
		o.Advice = offgrid.Advise(hours, o.SocPct, p)
		resp.Outage = o
	}
	if off && (next == nil || next.Start.After(env.now)) {
		// Off the grid with nothing on the schedule: how long is unknown.
		for _, hrs := range []int{1, 2, 4} {
			end := env.now.Add(time.Duration(hrs) * time.Hour)
			hours := offgrid.HoursOf(env.now, end, at)
			p.RechargeUahPerKwh, _ = h.recharge(ctx, env, end, float64(hrs)*100)
			resp.Unscheduled = append(resp.Unscheduled, offgridUnscheduled{Hours: hrs, Advice: offgrid.Advise(hours, liveSoc, p)})
		}
	}
	if off || (next != nil && next.Start.Sub(env.now) < 24*time.Hour) {
		dis, pv, load := 0.0, 0.0, 0.0
		if v := resp.Live.DischargeMaxKw; v != nil {
			dis = *v
		}
		if v := resp.Live.PVKw; v != nil {
			pv = *v
		}
		if v := resp.Live.LoadKw; v != nil {
			load = *v
		}
		avail := offgrid.AvailableNow(env.now, liveSoc, dis, pv, load, offgrid.HoursOf(env.now, env.start.Add(dispatchFutureHours*time.Hour), at), p)
		resp.Available = &avail
	}
	if off && resp.Live.Mode == "generator" && resp.Live.GenKw != nil {
		t := env.site.Tariffs
		extra := offgridDefaultExtraLPerKwh
		if t.FuelMethod() == economics.GenFuelPerKwh && t.GenFuelConsumptionLPerKwh > 0 {
			extra = t.GenFuelConsumptionLPerKwh
		}
		vat := 1.0
		if t.IncludeVat {
			vat += t.VatRate
		}
		end := env.now.Add(4 * time.Hour)
		if next != nil && !next.Start.After(env.now) {
			end = next.End
		}
		p.RechargeUahPerKwh, _ = h.recharge(ctx, env, end, 500)
		hint := offgrid.GenCharge(*resp.Live.GenKw, p, d.settings.GeneratorTargetKw, t.GenFuelPriceUahPerL, vat, extra,
			gridInTime(env, reserves, end, liveSoc))
		resp.GenCharge = &hint
	}
	writeJSON(w, http.StatusOK, resp)
}

// gridInTime: after the grid comes back at end, can it fill the battery
// to the next outage's target before that outage starts (§7)?
func gridInTime(env *dispatchEnv, reserves []offgrid.Reserve, end time.Time, socPct float64) bool {
	for _, r := range reserves {
		if !r.Start.After(end) {
			continue
		}
		need := (r.TargetPct - socPct) / 100 * env.site.CapacityKwh
		if need <= 0 {
			return true
		}
		return r.Start.Sub(end).Hours()*env.site.ChargeKw*env.site.Eta() >= need
	}
	return true
}

type offgridOutagesResp struct {
	SiteID         string           `json:"site_id"`
	From           string           `json:"from"`
	To             string           `json:"to"`
	Timezone       string           `json:"timezone"`
	Records        []offgrid.Record `json:"records"`
	Summary        offgrid.Summary  `json:"summary"`
	ErrorMarginPct float64          `json:"error_margin_pct"`
}

// offgridOutages handles GET /api/v1/offgrid/outages?site_id=&from=&to=
// (local dates, to exclusive; the month so far by default): plan against
// fact for each outage (§9).
func (h *Handlers) offgridOutages(w http.ResponseWriter, r *http.Request) {
	siteID, ok := h.requireEdge(w, r, http.MethodGet)
	if !ok {
		return
	}
	ctx := r.Context()
	env, err := h.loadDispatchEnv(ctx, siteID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	now := env.now.In(env.loc)
	from := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, env.loc)
	to := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, env.loc).AddDate(0, 0, 1)
	q := r.URL.Query()
	if v := q.Get("from"); v != "" {
		if from, err = time.ParseInLocation("2006-01-02", v, env.loc); err != nil {
			http.Error(w, "from: YYYY-MM-DD", http.StatusBadRequest)
			return
		}
	}
	if v := q.Get("to"); v != "" {
		if to, err = time.ParseInLocation("2006-01-02", v, env.loc); err != nil {
			http.Error(w, "to: YYYY-MM-DD", http.StatusBadRequest)
			return
		}
	}
	if !to.After(from) || to.Sub(from) > 93*24*time.Hour {
		http.Error(w, "the period must be 1…93 days", http.StatusBadRequest)
		return
	}
	settings, _ := h.loadEdgeSiteSettings(ctx, siteID)
	o := settings.offgrid()
	var scheduled []offgrid.Outage
	if h.outages != nil {
		ivs, err := h.outages.Intervals(ctx, siteID, from.UTC(), to.UTC())
		if err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		for _, iv := range ivs {
			scheduled = append(scheduled, offgrid.Outage{Start: iv.Start, End: iv.End, Kind: string(iv.Kind)})
		}
	}
	rows, err := storage.GetEconomicsHourly(ctx, h.edge.Pool, siteID, from.UTC(), to.UTC())
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].HourStart.Before(rows[j].HourStart) })
	hourTS := make([]time.Time, len(rows))
	for i, r := range rows {
		hourTS[i] = r.HourStart.UTC()
	}
	runs, err := storage.DispatchRunsBetween(ctx, h.edge.Pool, siteID, from.UTC(), to.UTC())
	if err != nil {
		h.edge.Log.Warn("offgrid_outages_runs", "site_id", siteID, "err", err)
	}
	plans := plansInForce(runs, hourTS)
	p := adviceParams(env, o)
	buys := make([]float64, 0, len(rows))
	hours := make([]offgrid.FactHour, 0, len(rows))
	for _, r := range rows {
		g := r.Gen
		fh := offgrid.FactHour{
			TS: r.HourStart.UTC(), MinGenerator: g.MinutesGenerator, MinIsland: g.MinutesIsland, MinBlackout: g.MinutesBlackout,
			MinTransition: g.MinutesTransition, GenKwh: g.GenKwh, FuelL: g.FuelL, FuelUah: g.FuelCost,
			LoadKwh: g.LoadOffgrid, EssKwh: g.EssToLoadOffgrid, PVKwh: g.PVToLoadOffgrid + g.PVToEssOffgrid,
			BuyUahPerKwh: r.ImportPrice,
		}
		if pl, ok := plans[fh.TS]; ok {
			v := pl.LoadKw
			fh.PlanLoadKw = &v
		}
		if r.EssRemainingKwhStart != nil && env.site.CapacityKwh > 0 {
			v := *r.EssRemainingKwhStart / env.site.CapacityKwh * 100
			fh.SocStartPct = &v
		}
		if r.ImportPrice > 0 {
			buys = append(buys, r.ImportPrice)
		}
		hours = append(hours, fh)
	}
	p.RechargeUahPerKwh = offgrid.RechargePrice(buys, 500, env.site.ChargeKw, env.site.Tariffs.RoundtripEfficiency, env.site.Tariffs.DegradationUahPerKwh)
	recs, sum := offgrid.Records(scheduled, hours, p, o.ErrorMarginPct)
	if recs == nil {
		recs = []offgrid.Record{}
	}
	writeJSON(w, http.StatusOK, offgridOutagesResp{
		SiteID: siteID, From: from.Format("2006-01-02"), To: to.Format("2006-01-02"), Timezone: env.tz,
		Records: recs, Summary: sum, ErrorMarginPct: offgrid.MarginFromFact(sum, o.ErrorMarginPct),
	})
}
