package api

import (
	"context"
	"net/http"
	"time"

	"github.com/nesh/sestelemetry/internal/dispatch"
	"github.com/nesh/sestelemetry/internal/offgrid"
	"github.com/nesh/sestelemetry/internal/storage"
)

// Work without the grid in the cloud (ems-spec ems_offgrid_advice.md):
// arithmetic and hints only, nothing is written to the loggers.

// offgridHistoryDays is the window of the measured-load average (§4).
const offgridHistoryDays = 7

// offgridData is what the desk needs for the outages of its horizon.
type offgridData struct {
	settings offgridSettings
	params   offgrid.Params
	outages  []offgrid.Outage
	// avg is the mean measured load of each local hour, kW.
	avg map[int]float64
}

func (h *Handlers) loadOffgrid(ctx context.Context, env *dispatchEnv, settings *EdgeSiteSettings) (*offgridData, error) {
	o := settings.offgrid()
	d := &offgridData{
		settings: o,
		params:   offgrid.Params{CapacityKwh: env.site.CapacityKwh, FloorPct: o.FloorPct, MaxPct: o.MaxPct, ErrorMarginPct: o.ErrorMarginPct},
	}
	if h.outages != nil {
		ivs, err := h.outages.Intervals(ctx, env.siteID, env.now, env.start.Add(dispatchFutureHours*time.Hour))
		if err != nil {
			return nil, err
		}
		for _, iv := range ivs {
			d.outages = append(d.outages, offgrid.Outage{Start: iv.Start, End: iv.End, Kind: string(iv.Kind)})
		}
	}
	if h.edge.Pool != nil {
		rows, err := storage.GetEconomicsHourly(ctx, h.edge.Pool, env.siteID, env.start.AddDate(0, 0, -offgridHistoryDays), env.start)
		if err != nil {
			return nil, err
		}
		samples := make(map[time.Time]float64, len(rows))
		for _, r := range rows {
			samples[r.HourStart] = r.LoadTotal
		}
		d.avg = offgrid.HourlyAverage(samples, env.loc)
	}
	return d, nil
}

// loads is §4: the energy manager's plan for the hour, else the 7-day
// average of the same hour, else nothing.
func (d *offgridData) loads(env *dispatchEnv, m dispatch.Model) offgrid.Loads {
	return func(hour time.Time) (float64, string, bool) {
		if i := int(hour.Sub(env.start) / time.Hour); hour.Sub(env.start) >= 0 && i < len(m.Loads) && m.Loads[i] != nil {
			return *m.Loads[i], offgrid.SourcePlan, true
		}
		if v, ok := d.avg[hour.In(env.loc).Hour()]; ok {
			return v, offgrid.SourceAvg7d, true
		}
		return 0, "", false
	}
}

func (d *offgridData) reserves(env *dispatchEnv, m dispatch.Model) []offgrid.Reserve {
	return offgrid.Reserves(d.outages, d.loads(env, m), d.params)
}

// floors maps each reserve onto the LP: the charge at the end of the
// hour before the one the outage starts in. An outage already under
// way or starting this hour has nothing left to charge for.
func (d *offgridData) floors(env *dispatchEnv, m dispatch.Model) map[int]float64 {
	out := map[int]float64{}
	for _, r := range d.reserves(env, m) {
		i := int(r.Start.Sub(env.start)/time.Hour) - 1
		if r.Start.Before(env.start) || i < 0 || i >= dispatchFutureHours {
			continue
		}
		if r.TargetPct > out[i] {
			out[i] = r.TargetPct
		}
	}
	return out
}

// simulateEnv is a desk run; with the site's switch on, it also holds
// the charge for the scheduled outages.
func simulateEnv(env *dispatchEnv, m dispatch.Model) (dispatch.Result, error) {
	in := env.inputs
	if env.offgrid != nil && env.offgrid.settings.Apply {
		in.OutageFloorPct = env.offgrid.floors(env, m)
	}
	return dispatch.Simulate(in, m)
}

type offgridReserveResp struct {
	SiteID   string            `json:"site_id"`
	Now      time.Time         `json:"now"`
	Timezone string            `json:"timezone"`
	Settings offgridSettings   `json:"settings"`
	Reserves []offgrid.Reserve `json:"reserves"`
}

// offgridReserve handles GET /api/v1/offgrid/reserve?site_id=: the
// charge for each outage of the desk's horizon (§5), with where its
// load came from and whether the plan holds it.
func (h *Handlers) offgridReserve(w http.ResponseWriter, r *http.Request) {
	siteID, ok := h.requireEdge(w, r, http.MethodGet)
	if !ok {
		return
	}
	ctx := r.Context()
	env, err := h.loadDispatchEnv(ctx, siteID)
	if err != nil {
		h.edge.Log.Error("offgrid_reserve_env", "site_id", siteID, "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	m, _, err := h.appliedModel(ctx, env)
	if err != nil {
		h.edge.Log.Error("offgrid_reserve_model", "site_id", siteID, "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	d := env.offgrid
	if d == nil {
		settings, _ := h.loadEdgeSiteSettings(ctx, siteID)
		if d, err = h.loadOffgrid(ctx, env, settings); err != nil {
			h.edge.Log.Error("offgrid_reserve_data", "site_id", siteID, "err", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
	}
	writeJSON(w, http.StatusOK, offgridReserveResp{
		SiteID: siteID, Now: env.now, Timezone: env.tz, Settings: d.settings, Reserves: d.reserves(env, m),
	})
}
