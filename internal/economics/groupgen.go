package economics

import (
	"context"
	"fmt"
	"math"
	"slices"
	"time"
)

// A group's shared generator (ems-spec generator handoff §6). Its sites
// keep their own days, computed without the generator (gen_shared): one
// generator behind several grid inputs cannot be split between them. The
// group's day is their sum except in the hours the generator ran or a
// site was off the grid. Those take the group's generator-aware
// allocation — the sites without the grid together, the generator's
// energy once, from the first site's meter — and the first site's tariff,
// fuel included. A day with such hours is stored under the group's id
// and recomputed after each recompute of a site's day; while a site's day
// is newer, the group reads the plain sum.

// GroupGenFlowSource is the optional backend extension a shared
// generator's group day is computed from: the generator-aware allocation
// of the sites walked together (energyflow.IterateGroupWindows), one row
// per hour of the day starting at dayStart, nil for an hour without
// generator data. The generator's energy comes from the first site.
type GroupGenFlowSource interface {
	GroupGenFlows(ctx context.Context, siteIDs []string, dayStart time.Time) ([]*GenFlowRow, error)
}

// DayDeleter is the optional backend extension that drops a stored day.
type DayDeleter interface {
	DeleteDay(ctx context.Context, orgID string, day time.Time) error
}

// sharedGenGroupOf finds the group whose sites share orgID's generator.
func sharedGenGroupOf(orgID string) (Group, bool) {
	for _, g := range Groups {
		if !g.SharedGenerator {
			continue
		}
		for _, m := range g.Members {
			if m.ID == orgID {
				return g, true
			}
		}
	}
	return Group{}, false
}

// SetGroupGenErrorLog makes the service report failures to recompute a
// shared generator's group day. They do not fail the site's day: the
// group then reads the plain sum of its sites until the next recompute.
func (s *Service) SetGroupGenErrorLog(log func(groupID, date string, err error)) {
	s.groupGenErr = log
}

// storeBackend is the backend that computes and stores organizations'
// days, below the group view; nil for a currency view, which never writes.
func (s *Service) storeBackend() Backend {
	switch b := s.backend.(type) {
	case *currencyBackend:
		return nil
	case *groupBackend:
		return b.inner
	default:
		return b
	}
}

// refreshGroupGen recomputes the stored day of orgID's shared-generator
// group, after orgID's day starting at dayStart was saved.
func (s *Service) refreshGroupGen(ctx context.Context, orgID string, dayStart time.Time, tz string) {
	g, ok := sharedGenGroupOf(orgID)
	if !ok {
		return
	}
	inner := s.storeBackend()
	if inner == nil {
		return
	}
	if err := computeGroupGenDay(ctx, inner, g, dayStart, tz); err != nil && s.groupGenErr != nil {
		s.groupGenErr(g.ID, dayStart.Format("2006-01-02"), err)
	}
}

// computeGroupGenDay stores the group's day starting at dayStart when its
// sites' days hold generator data, and drops it when they no longer do.
// A site whose day is not computed yet leaves it to that site's compute.
func computeGroupGenDay(ctx context.Context, b Backend, g Group, dayStart time.Time, tz string) error {
	sites := g.ActiveOn(dayStart.Format("2006-01-02"))
	if len(sites) < 2 {
		return nil
	}
	days := make([]StoredDay, 0, len(sites))
	for _, id := range sites {
		d, ok, err := b.LoadDay(ctx, id, dayStart, tz)
		if err != nil {
			return fmt.Errorf("%s: load day: %w", id, err)
		}
		if !ok {
			return nil
		}
		days = append(days, d)
	}
	if !daysGenActive(days) {
		if del, ok := b.(DayDeleter); ok {
			return del.DeleteDay(ctx, g.ID, dayStart)
		}
		return nil
	}
	src, ok := b.(GroupGenFlowSource)
	if !ok {
		return nil
	}
	genRows, err := src.GroupGenFlows(ctx, sites, dayStart)
	if err != nil {
		return fmt.Errorf("generator flows: %w", err)
	}
	schedule, err := b.TariffSchedule(ctx, sites[0])
	if err != nil {
		return fmt.Errorf("tariff schedule: %w", err)
	}
	tariffs, ok := schedule.ResolveForDay(dayStart)
	if !ok {
		tariffs = DefaultTariffs
	}
	return b.SaveDay(ctx, groupGenDay(g.ID, days, genRows, tariffs))
}

// daysGenActive reports sites' days with generator data: the generator
// ran or a site was off the grid.
func daysGenActive(days []StoredDay) bool {
	for _, d := range days {
		for _, r := range d.Rows {
			if r != nil && (r.GenKwh > 0 || r.SupplyMinutes.Offgrid() > 0) {
				return true
			}
		}
	}
	return false
}

// groupGenDay is the group's day over its sites' days: their sum, with
// every hour of genRows that holds generator data put on the group's
// generator, and in tariff t's accounting (fuel priced) recomputed on the
// sites' counters with the group's allocation. The УЗЕ cost basis stays
// the sites' sum.
func groupGenDay(groupID string, days []StoredDay, genRows []*GenFlowRow, t Tariffs) StoredDay {
	out := mergeDays(groupID, days)
	accounting := t.FuelSet()
	var offgridImport float64
	for h, row := range out.Rows {
		if row == nil || h >= len(genRows) || genRows[h] == nil {
			continue
		}
		g := genRows[h]
		row.GenKwh, row.GenEstimated, row.SupplyMinutes = g.GenKwh, g.Estimated, g.Minutes
		if !accounting || !g.active() {
			continue
		}
		var pv, imp, exp, chg, dis float64
		for _, d := range days {
			if h >= len(d.Rows) || d.Rows[h] == nil {
				continue
			}
			site := d.Rows[h]
			pv += site.Flow.PV
			chg += site.Flow.EssCharged
			dis += site.Flow.EssDischarged
			// A site wholly without the grid has no import or export,
			// whatever its meter claims (handoff §4.3).
			if whollyOffgrid(site.SupplyMinutes, false) {
				offgridImport += math.Max(site.Flow.GridImport, 0)
				continue
			}
			imp += site.Flow.GridImport
			exp += site.Flow.GridExport
		}
		flow, claimed := genFlowsFromCounters(FlowRow{Gen: g}, true, chg, true, dis, pv, imp, exp)
		offgridImport += claimed
		rdn := 0.0
		if row.Rdn != nil {
			rdn = *row.Rdn
		}
		row.Flow = flow
		row.Econ = HourEconomicsFor(rdn, flow, t)
	}

	totals := ComputeDailyTotals(out.Rows)
	for _, d := range days {
		totals.Reconciled = totals.Reconciled || d.Totals.Reconciled
		for _, f := range d.Totals.QualityFlags {
			if f != "gen_shared" && !slices.Contains(totals.QualityFlags, f) {
				totals.QualityFlags = append(totals.QualityFlags, f)
			}
		}
	}
	totals.QualityFlags = append(totals.QualityFlags, genFlags(out.Rows, accounting, false)...)
	if offgridImport >= offgridImportFlagKwh {
		totals.QualityFlags = append(totals.QualityFlags, fmt.Sprintf("offgrid_import:%.0f", offgridImport))
	}
	out.Totals = totals
	return out
}
