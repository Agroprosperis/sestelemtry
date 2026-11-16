package api

import (
	"context"
	"fmt"
	"time"

	"github.com/nesh/sestelemetry/internal/economics"
	"github.com/nesh/sestelemetry/internal/energyflow"
)

// groupGenFlows is the generator-aware allocation of a group's sites
// sharing one generator, walked together over the day starting at
// dayStart (energyflow.IterateGroupWindows), one row per hour. The
// generator's energy comes from the first site's meter; nil rows are
// hours without generator data.
func (h *Handlers) groupGenFlows(ctx context.Context, siteIDs []string, dayStart time.Time) ([]*economics.GenFlowRow, error) {
	loc := dayStart.Location()
	dayEnd := dayStart.AddDate(0, 0, 1)
	// Windows still ahead today have no supply mode yet, not an unknown one.
	genTo := dayEnd
	if now := time.Now(); now.Before(genTo) {
		genTo = now.Truncate(time.Minute)
	}
	sites := make([]energyflow.GroupSite, 0, len(siteIDs))
	for _, id := range siteIDs {
		rows, err := h.store.EnergyFlowSources(ctx, id, dayStart, dayEnd, energyFlowSourceLookback)
		if err != nil {
			return nil, fmt.Errorf("%s: energy_flow sources: %w", id, err)
		}
		cfg := h.energyFlowOrgs[id]
		raw := buildRawSamples(rows, cfg)
		sites = append(sites, energyflow.GroupSite{
			Rows:     raw,
			Timeline: energyflow.BuildGenTimeline(raw, dayStart, genTo, time.Minute, h.genSince(ctx, id)),
			Opts: energyflow.Options{
				EssDischargeSign:        cfg.EssDischargeSign,
				AllocationWindowSeconds: 60,
				MaxGapSeconds:           0,
				MaxEssPowerKw:           essPowerCeiling(cfg),
			},
		})
	}
	out := make([]*economics.GenFlowRow, hoursPerDay)
	row := func(hour int) *economics.GenFlowRow {
		if out[hour] == nil {
			out[hour] = &economics.GenFlowRow{}
		}
		return out[hour]
	}
	if len(sites) == 0 {
		return out, nil
	}
	gen := sites[0].Timeline
	for hour := 0; hour < hoursPerDay; hour++ {
		if th, ok := gen.Hour(dayStart.Add(time.Duration(hour) * time.Hour)); ok {
			r := row(hour)
			r.GenKwh, r.Estimated = th.GenKwh, th.Estimated
		}
	}
	energyflow.IterateGroupWindows(sites, gen, func(w energyflow.GroupWindow) {
		hour := w.End.Add(-time.Nanosecond).In(loc).Hour()
		if hour < 0 || hour >= hoursPerDay {
			return
		}
		r := row(hour)
		addSupplyMinute(&r.Minutes, w.Mode)
		r.Partial = r.Partial || w.Partial
		res := w.Result
		r.PVToEss += res.PVToESSKwh
		r.GenToEss += res.GenToESSKwh
		r.GridToEss += res.GridToESSKwh
		r.EssToLoad += res.ESSToLoadKwh
		r.EssToGrid += res.ESSToGridKwh
		r.EssCharged += res.EssChargedKwh
		r.EssDischarged += res.EssDischargedKwh
		r.GenToLoad += res.GenToLoadKwh
		r.LoadOffgrid += res.LoadOffgridKwh
		r.PVToLoadOffgrid += res.PVToLoadOffgridKwh
		r.EssToLoadOffgrid += res.ESSToLoadOffgridKwh
		r.PVToEssOffgrid += res.PVToESSOffgridKwh
		r.PVToLoadIsland += res.PVToLoadIslandKwh
		r.EssToLoadIsland += res.ESSToLoadIslandKwh
	})
	return out, nil
}

// addSupplyMinute counts one window of mode; grid minutes are not counted.
func addSupplyMinute(m *economics.SupplyMinutes, mode energyflow.SupplyMode) {
	switch mode {
	case energyflow.SupplyGenerator:
		m.Generator++
	case energyflow.SupplyIsland:
		m.Island++
	case energyflow.SupplyBlackout:
		m.Blackout++
	case energyflow.SupplyTransition:
		m.Transition++
	case energyflow.SupplyUnknown:
		m.Unknown++
	}
}
