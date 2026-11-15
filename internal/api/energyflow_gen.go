package api

import (
	"context"
	"time"

	"github.com/nesh/sestelemetry/internal/energyflow"
	"github.com/nesh/sestelemetry/internal/storage"
)

// FirstSampleTime returns when the organization first reported
// metricKey; false when it never has.
func (s *Store) FirstSampleTime(ctx context.Context, organizationID, metricKey string) (time.Time, bool, error) {
	return storage.FirstSampleTime(ctx, s.pool, organizationID, metricKey)
}

// firstSampleSource is the optional store extension genSince reads.
type firstSampleSource interface {
	FirstSampleTime(ctx context.Context, organizationID, metricKey string) (time.Time, bool, error)
}

// genSince is the organization's first generator sample ever: supply
// windows before it have no mode. Found once and cached; zero when the
// store cannot tell or the organization has none yet, and the timeline
// then starts at the first generator sample it is given.
func (h *Handlers) genSince(ctx context.Context, orgID string) time.Time {
	if v, ok := h.genSinceByOrg.Load(orgID); ok {
		return v.(time.Time)
	}
	src, ok := h.store.(firstSampleSource)
	if !ok {
		return time.Time{}
	}
	t, found, err := src.FirstSampleTime(ctx, orgID, energyflow.SrcGridBreakersOnBits)
	if err != nil {
		h.log.Warn("api_energy_flow_gen_since", "organization_id", orgID, "err", err)
		return time.Time{}
	}
	if found {
		h.genSinceByOrg.Store(orgID, t)
	}
	return t
}

// genHours fills the Gen block of a day's hourly rows: the timeline's
// meter energy and minutes per hour, plus the sums of the
// generator-aware intervals.
type genHours struct {
	tl    *energyflow.GenTimeline
	hours []EnergyFlowHourlyRow
}

func newGenHours(tl *energyflow.GenTimeline, hours []EnergyFlowHourlyRow) *genHours {
	g := &genHours{tl: tl, hours: hours}
	if tl == nil {
		return g
	}
	for i := range hours {
		th, ok := tl.Hour(hours[i].From)
		if !ok {
			continue
		}
		block := g.block(i)
		block.GenKwh = th.GenKwh
		block.Estimated = th.Estimated
		for mode, m := range th.Minutes {
			block.Minutes[string(mode)] += m
		}
	}
	return g
}

func (g *genHours) block(i int) *EnergyFlowGenHour {
	if g.hours[i].Gen == nil {
		g.hours[i].Gen = &EnergyFlowGenHour{Minutes: map[string]int{}}
	}
	return g.hours[i].Gen
}

func (g *genHours) add(hour int, r energyflow.GenResult) {
	if hour < 0 || hour >= len(g.hours) {
		return
	}
	b := g.block(hour)
	b.GenToLoadKwh += r.GenToLoadKwh
	b.GenToESSKwh += r.GenToESSKwh
	b.PVToESSKwh += r.PVToESSKwh
	b.GridToESSKwh += r.GridToESSKwh
	b.ESSToLoadKwh += r.ESSToLoadKwh
	b.ESSToGridKwh += r.ESSToGridKwh
	b.EssChargedKwh += r.EssChargedKwh
	b.EssDischargedKwh += r.EssDischargedKwh
	b.LoadOffgridKwh += r.LoadOffgridKwh
	b.PVToLoadOffgridKwh += r.PVToLoadOffgridKwh
	b.ESSToLoadOffgridKwh += r.ESSToLoadOffgridKwh
	b.PVToESSOffgridKwh += r.PVToESSOffgridKwh
	b.PVToLoadIslandKwh += r.PVToLoadIslandKwh
	b.ESSToLoadIslandKwh += r.ESSToLoadIslandKwh
	b.OffgridImportKwh += r.OffgridImportKwh
	b.OffgridExportKwh += r.OffgridExportKwh
}

// log writes the day's generator totals: what to check against the
// generator meter and the SmartLogger before the economics count them.
// offgrid_import_kwh is what the grid meter claimed in hours wholly
// without the grid, which handoff §4.3 expects to be ~0.
func (g *genHours) log(h *Handlers, orgID string, dayStart time.Time) {
	if g.tl == nil {
		return
	}
	var genKwh, genToEss, genToLoad, offgridImport float64
	minutes := map[string]int{}
	estimated := 0
	for _, row := range g.hours {
		b := row.Gen
		if b == nil {
			continue
		}
		genKwh += b.GenKwh
		genToEss += b.GenToESSKwh
		genToLoad += b.GenToLoadKwh
		if b.Estimated {
			estimated++
		}
		off := 0
		for mode, m := range b.Minutes {
			minutes[mode] += m
			if energyflow.SupplyMode(mode).Offgrid() {
				off += m
			}
		}
		if off >= 60 {
			offgridImport += b.OffgridImportKwh
		}
	}
	h.log.Info("energy_flow_gen",
		"organization_id", orgID,
		"date", dayStart.Format("2006-01-02"),
		"gen_kwh", round3(genKwh),
		"gen_to_ess_kwh", round3(genToEss),
		"gen_to_load_kwh", round3(genToLoad),
		"estimated_hours", estimated,
		"minutes", minutes,
		"offgrid_import_kwh", round3(offgridImport),
	)
}
