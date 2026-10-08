package api

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/nesh/sestelemetry/internal/dispatch"
)

const (
	dispatchHistoryHours = 8
	dispatchFutureHours  = 24
	// dispatchUncappedImportKw stands in for a site without a saved
	// import limit: the LP needs a number, the UI shows it as not set.
	dispatchUncappedImportKw = 10000
	// The passport capacity is the usable 10–90 % SOC window
	// (resolveEdgeRatings), so it bounds the desk reserve; the
	// «Обмеження» reserve is only the default for a new draft.
	passportSocMinPct = 10
	passportSocMaxPct = 90
)

// dispatchEnv is everything a run needs besides the operator's model.
type dispatchEnv struct {
	siteID        string
	loc           *time.Location
	tz            string
	now           time.Time
	start         time.Time // current hour, UTC
	params        edgeSiteParams
	site          dispatch.Site
	importSet     bool
	exportRegime  string
	exportCeiling *float64
	defaults      dispatch.Constraints
	startSoc      *float64 // measured; nil = unknown
	inputs        dispatch.Inputs
	rdn           map[time.Time]float64 // UTC hour → RDN, history + future
	pv            map[time.Time]float64 // forecast
	weather       map[time.Time]edgeHourWeather
}

func (h *Handlers) loadDispatchEnv(ctx context.Context, siteID string) (*dispatchEnv, error) {
	e := h.edge
	tz := e.PlannerTimezone
	if tz == "" {
		tz = "Europe/Kyiv"
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return nil, err
	}
	zone := e.PlannerZone
	if zone == 0 {
		zone = 2
	}
	now := time.Now().In(loc)
	env := &dispatchEnv{siteID: siteID, loc: loc, tz: tz, now: now.UTC(), start: now.Truncate(time.Hour).UTC()}
	env.params = edgeSiteParams{Now: now}
	if err := h.applyEdgeSiteParams(ctx, siteID, &env.params); err != nil {
		return nil, err
	}
	settings, _ := h.loadEdgeSiteSettings(ctx, siteID)
	importKw := env.params.GridImportKw
	env.importSet = importKw > 0
	if !env.importSet {
		importKw = dispatchUncappedImportKw
	}
	env.site = dispatch.Site{
		CapacityKwh: env.params.CapacityKwh,
		ChargeKw:    env.params.ChargeMaxKw,
		DischargeKw: env.params.DischargeMaxKw,
		ImportKw:    importKw,
		SocMinPct:   passportSocMinPct,
		SocMaxPct:   math.Max(passportSocMinPct, math.Min(passportSocMaxPct, env.params.SocMax)),
		Tariffs:     env.params.Tariffs,
	}
	if settings != nil {
		env.exportRegime = settings.ExportRegime
		env.exportCeiling = settings.exportCeilingKw()
	}
	env.defaults = dispatch.Constraints{
		ReservePct:  math.Min(math.Max(env.params.SocMin, env.site.SocMinPct), env.site.SocMaxPct),
		GridCharge:  true,
		EssSale:     env.exportCeiling != nil,
		ImportCapKw: importKw,
		ExportCapKw: copyKw(env.exportCeiling),
	}

	to := env.start.Add(dispatchFutureHours * time.Hour)
	env.rdn, err = h.damPricesBetween(ctx, zone, env.start.Add(-dispatchHistoryHours*time.Hour), to, loc)
	if err != nil {
		return nil, fmt.Errorf("dam prices: %w", err)
	}
	env.pv, env.weather, err = h.edgePvForecast(ctx, siteID, env.start, to, env.params.PvRatedKw)
	if err != nil {
		return nil, fmt.Errorf("pv forecast: %w", err)
	}
	if plan, ok := h.edgePvPlanForecast(ctx, siteID, loc, env.start, to); ok {
		for k, v := range plan {
			env.pv[k] = v
		}
	}
	if soc := h.edgeLatestSoc(ctx, siteID); soc > 0 {
		env.startSoc = &soc
	}
	startPct := (env.site.SocMinPct + env.site.SocMaxPct) / 2
	if env.startSoc != nil {
		startPct = *env.startSoc
	}
	env.inputs = dispatch.Inputs{
		Site:     env.site,
		StartKwh: startPct / 100 * env.site.CapacityKwh,
		PV:       make([]float64, dispatchFutureHours),
		Rdn:      make([]*float64, dispatchFutureHours),
	}
	for i := 0; i < dispatchFutureHours; i++ {
		ts := env.start.Add(time.Duration(i) * time.Hour)
		env.inputs.PV[i] = env.pv[ts]
		if p, ok := env.rdn[ts]; ok {
			v := p
			env.inputs.Rdn[i] = &v
		}
	}
	return env, nil
}

func copyKw(v *float64) *float64 {
	if v == nil {
		return nil
	}
	c := *v
	return &c
}

// damPricesBetween loads hourly RDN (UAH/kWh) for the local days that
// cover [from, to), keyed by UTC hour start.
func (h *Handlers) damPricesBetween(ctx context.Context, zone int, from, to time.Time, loc *time.Location) (map[time.Time]float64, error) {
	var dates []string
	f := from.In(loc)
	for d := time.Date(f.Year(), f.Month(), f.Day(), 0, 0, 0, 0, loc); d.Before(to); d = d.AddDate(0, 0, 1) {
		dates = append(dates, d.Format("2006-01-02"))
	}
	return h.damPricesForDates(ctx, zone, dates, loc)
}
