package economics

import (
	"context"
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"
	"time"
)

// CounterEpisode is a lifetime-counter discontinuity the stitcher
// recorded (see internal/counters), as far as day flags need it.
type CounterEpisode struct {
	MetricKey string
	Kind      string
}

// CounterEpisodeSource is the optional Backend extension that reports
// the counter episodes overlapping [from, to).
type CounterEpisodeSource interface {
	CounterEpisodes(ctx context.Context, orgID string, from, to time.Time) ([]CounterEpisode, error)
}

// counterFlagNames shortens the stitched counters to the names the
// reconciliation flags already use.
var counterFlagNames = map[string]string{
	"accumulated_pv_energy_yield_kwh":       "pv",
	"accumulated_electricity_purchased_kwh": "grid_import",
	"accumulated_electricity_sold_kwh":      "grid_export",
	essChargedCounterKey:                    "ess_charged",
	essDischargedCounterKey:                 "ess_discharged",
	"accumulated_gen_energy_kwh":            genCounterFlagName,
}

// genCounterFlagName names the generator meter in counter flags. Its
// gaps say nothing about the SmartLogger flow split, so they do not
// mark a day approximate (dayHasCounterIssue).
const genCounterFlagName = "gen"

// counterKindOffgrid and counterKindReset are dropouts that are no counter
// fault: the site's outage (counters.KindOffgrid), or a counter that kept
// metering from a new level (counters.KindReset).
const (
	counterKindOffgrid = "offgrid"
	counterKindReset   = "reset"
)

// counterFlagKinds are the episode kinds counterFlags turns into flags.
var counterFlagKinds = []string{"dropout", "frozen", "seam", "spike"}

// counterFlags turns the day's counter episodes into flags such as
// counter_dropout:grid_import, one per kind and counter.
func counterFlags(episodes []CounterEpisode) []string {
	seen := map[string]bool{}
	var out []string
	for _, e := range episodes {
		name, ok := counterFlagNames[e.MetricKey]
		if !ok || e.Kind == counterKindOffgrid || e.Kind == counterKindReset {
			continue
		}
		flag := "counter_" + e.Kind + ":" + name
		if !seen[flag] {
			seen[flag] = true
			out = append(out, flag)
		}
	}
	sort.Strings(out)
	return out
}

// withCounterFlags is flags with its counter-episode flags replaced by
// counter.
func withCounterFlags(flags, counter []string) []string {
	out := make([]string, 0, len(flags)+len(counter))
	for _, f := range flags {
		if !isCounterEpisodeFlag(f) {
			out = append(out, f)
		}
	}
	return append(out, counter...)
}

func isCounterEpisodeFlag(f string) bool {
	for _, k := range counterFlagKinds {
		if strings.HasPrefix(f, "counter_"+k+":") {
			return true
		}
	}
	return false
}

// DayFlagsSaver is the optional backend extension that replaces a stored
// day's quality flags, leaving the rest of it as computed.
type DayFlagsSaver interface {
	SaveDayFlags(ctx context.Context, orgID string, day time.Time, flags []string) error
}

// RefreshCounterFlags rewrites the counter-episode flags of the
// organization's stored days over [from, to] from the episodes as they
// stand now. An episode's kind or end changes only the flags of the days
// it covers, never their numbers, so no recompute is needed. A shared
// generator's stored group days pool their sites' flags and follow. It
// returns how many days changed.
func (s *Service) RefreshCounterFlags(ctx context.Context, orgID string, from, to time.Time, tz string) (int, error) {
	b := s.storeBackend()
	src, okSrc := b.(CounterEpisodeSource)
	saver, okSave := b.(DayFlagsSaver)
	if b == nil || !okSrc || !okSave {
		return 0, nil
	}
	loc, err := loadLocation(tz)
	if err != nil {
		return 0, err
	}
	recs, err := b.LoadDailyRange(ctx, orgID, from, to)
	if err != nil {
		return 0, err
	}
	changed := 0
	for _, r := range recs {
		dayStart := time.Date(r.Day.Year(), r.Day.Month(), r.Day.Day(), 0, 0, 0, 0, loc)
		episodes, err := src.CounterEpisodes(ctx, orgID, dayStart, dayStart.AddDate(0, 0, 1))
		if err != nil {
			return changed, err
		}
		flags := withCounterFlags(r.Totals.QualityFlags, counterFlags(episodes))
		if slices.Equal(flags, r.Totals.QualityFlags) {
			continue
		}
		if err := saver.SaveDayFlags(ctx, orgID, r.Day, flags); err != nil {
			return changed, err
		}
		changed++
	}
	if g, ok := sharedGenGroupOf(orgID); ok {
		n, err := refreshGroupCounterFlags(ctx, b, saver, g, from, to)
		changed += n
		if err != nil {
			return changed, err
		}
	}
	return changed, nil
}

// refreshGroupCounterFlags gives a shared generator's stored group days
// over [from, to] the counter-episode flags of their sites.
func refreshGroupCounterFlags(ctx context.Context, b Backend, saver DayFlagsSaver, g Group, from, to time.Time) (int, error) {
	groupDays, err := b.LoadDailyRange(ctx, g.ID, from, to)
	if err != nil || len(groupDays) == 0 {
		return 0, err
	}
	siteFlags := map[string][]string{}
	for _, m := range g.Members {
		recs, err := b.LoadDailyRange(ctx, m.ID, from, to)
		if err != nil {
			return 0, err
		}
		for _, r := range recs {
			key := r.Day.Format("2006-01-02")
			for _, f := range r.Totals.QualityFlags {
				if isCounterEpisodeFlag(f) && !slices.Contains(siteFlags[key], f) {
					siteFlags[key] = append(siteFlags[key], f)
				}
			}
		}
	}
	changed := 0
	for _, r := range groupDays {
		counter := siteFlags[r.Day.Format("2006-01-02")]
		sort.Strings(counter)
		flags := withCounterFlags(r.Totals.QualityFlags, counter)
		if slices.Equal(flags, r.Totals.QualityFlags) {
			continue
		}
		if err := saver.SaveDayFlags(ctx, g.ID, r.Day, flags); err != nil {
			return changed, err
		}
		changed++
	}
	return changed, nil
}

// balanceFlag reports a day whose counters cannot all be right: the
// energy that left the site (export, ESS charge) exceeds what entered
// it (PV, import, ESS discharge) by more than metering slack. The flag
// carries the shortfall in kWh.
func balanceFlag(t DailyTotals) (string, bool) {
	in := t.PV + t.GridImport + t.EssDischarged + t.GenToLoad + t.GenToEss
	balance := in - t.GridExport - t.EssCharged
	if balance >= -math.Max(5, 0.02*in) {
		return "", false
	}
	return fmt.Sprintf("balance_negative:%.0f", -balance), true
}
