package economics

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"
)

// Group is one economic object over several sites whose economics are
// computed and stored apart. A member belongs to the group from its From
// day (YYYY-MM-DD; empty = always).
type Group struct {
	ID      string
	Name    string
	Members []GroupMember
}

// GroupMember is one site of a Group.
type GroupMember struct {
	ID   string
	From string
}

// Groups are the economic objects reported over several sites.
// Дубовʼязівський's Т1 got its own logger on 2026-09-24; until then `de`
// metered the whole elevator, so the elevator is `de` alone before that
// day and `de` + `de1` from it.
var Groups = []Group{{
	ID:   "de-all",
	Name: "Дубов'язівський елеватор",
	Members: []GroupMember{
		{ID: "de"},
		{ID: "de1", From: "2026-09-24"},
	},
}}

// GroupByID finds one of Groups.
func GroupByID(id string) (Group, bool) { return findGroup(Groups, id) }

func findGroup(groups []Group, id string) (Group, bool) {
	for _, g := range groups {
		if g.ID == id {
			return g, true
		}
	}
	return Group{}, false
}

// MemberIDs lists the group's members in order.
func (g Group) MemberIDs() []string {
	out := make([]string, len(g.Members))
	for i, m := range g.Members {
		out[i] = m.ID
	}
	return out
}

// ActiveOn lists the members that belong to the group on the civil date
// day (YYYY-MM-DD), in order.
func (g Group) ActiveOn(day string) []string {
	var out []string
	for _, m := range g.Members {
		if m.From == "" || day >= m.From {
			out = append(out, m.ID)
		}
	}
	return out
}

// VersionDates lists the civil dates the group's tariff schedule changes
// on, given each member's version dates: the versions of a member from
// the day it joins, and the joining days themselves.
func (g Group) VersionDates(memberDates map[string][]string) []string {
	set := map[string]bool{}
	for _, m := range g.Members {
		if m.From != "" {
			set[m.From] = true
		}
		for _, d := range memberDates[m.ID] {
			if m.From == "" || d >= m.From {
				set[d] = true
			}
		}
	}
	out := make([]string, 0, len(set))
	for d := range set {
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}

// errGroupView refuses what a group cannot do: its economics are only
// ever its members' stored ones added up.
var errGroupView = errors.New("economics: a group only reads its members' stored days")

// InGroups returns backend reading each of groups as one organization: its
// days and hours are its members' stored ones added up, its tariff
// schedule theirs combined. A group is never computed from telemetry nor
// saved; every other organization passes through untouched.
func InGroups(backend Backend, groups []Group) Backend {
	return &groupBackend{inner: backend, groups: groups}
}

type groupBackend struct {
	inner  Backend
	groups []Group
}

func (b *groupBackend) group(id string) (Group, bool) { return findGroup(b.groups, id) }

func (b *groupBackend) HourlyFlows(ctx context.Context, orgID string, dayStart time.Time) ([]FlowRow, error) {
	if _, ok := b.group(orgID); ok {
		return nil, errGroupView
	}
	return b.inner.HourlyFlows(ctx, orgID, dayStart)
}

func (b *groupBackend) Timeseries(ctx context.Context, orgID string, metricKeys []string, from, to time.Time, bucket, tz, aggregation string) ([]Point, error) {
	if _, ok := b.group(orgID); ok {
		return nil, errGroupView
	}
	return b.inner.Timeseries(ctx, orgID, metricKeys, from, to, bucket, tz, aggregation)
}

func (b *groupBackend) DAMPrices(ctx context.Context, zone int, from, to time.Time) ([]DAMHour, error) {
	return b.inner.DAMPrices(ctx, zone, from, to)
}

func (b *groupBackend) CanonicalDaily(ctx context.Context, orgID string, day time.Time) (CanonicalDaily, bool, error) {
	if _, ok := b.group(orgID); ok {
		return CanonicalDaily{}, false, errGroupView
	}
	return b.inner.CanonicalDaily(ctx, orgID, day)
}

func (b *groupBackend) SaveDay(ctx context.Context, day StoredDay) error {
	if _, ok := b.group(day.OrganizationID); ok {
		return errGroupView
	}
	return b.inner.SaveDay(ctx, day)
}

// CounterEpisodes keeps the inner backend's counter episodes reachable for
// the organizations that are computed.
func (b *groupBackend) CounterEpisodes(ctx context.Context, orgID string, from, to time.Time) ([]CounterEpisode, error) {
	src, ok := b.inner.(CounterEpisodeSource)
	if !ok {
		return nil, nil
	}
	return src.CounterEpisodes(ctx, orgID, from, to)
}

// TariffSchedule of a group takes every price from the first member in
// force and adds the members' УЗЕ capacity and power up.
func (b *groupBackend) TariffSchedule(ctx context.Context, orgID string) (Schedule, error) {
	g, ok := b.group(orgID)
	if !ok {
		return b.inner.TariffSchedule(ctx, orgID)
	}
	schedules := make(map[string]Schedule, len(g.Members))
	dates := make(map[string][]string, len(g.Members))
	for _, m := range g.Members {
		s, err := b.inner.TariffSchedule(ctx, m.ID)
		if err != nil {
			return nil, err
		}
		schedules[m.ID] = s
		for _, e := range s {
			dates[m.ID] = append(dates[m.ID], e.EffectiveFrom.Format("2006-01-02"))
		}
	}
	var out Schedule
	for _, d := range g.VersionDates(dates) {
		day, err := time.Parse("2006-01-02", d)
		if err != nil {
			return nil, fmt.Errorf("group %s: bad version date %q", g.ID, d)
		}
		var merged Tariffs
		var capacity, power float64
		found := false
		for _, id := range g.ActiveOn(d) {
			t, ok := schedules[id].ResolveForDay(day)
			if !ok {
				continue
			}
			if !found {
				merged, found = t, true
			}
			r := t.EssRatings().withPowerFallback()
			capacity += r.CapacityKwh
			power += r.PowerLimitKw
		}
		if !found {
			continue
		}
		merged.EssCapacityKwh = capacity
		merged.EssPowerLimitKw = power
		out = append(out, ScheduleEntry{EffectiveFrom: day, Tariffs: merged})
	}
	return out, nil
}

func (b *groupBackend) LoadDay(ctx context.Context, orgID string, day time.Time, tz string) (StoredDay, bool, error) {
	g, ok := b.group(orgID)
	if !ok {
		return b.inner.LoadDay(ctx, orgID, day, tz)
	}
	var days []StoredDay
	for _, id := range g.ActiveOn(day.Format("2006-01-02")) {
		d, ok, err := b.inner.LoadDay(ctx, id, day, tz)
		if err != nil {
			return StoredDay{}, false, err
		}
		if ok {
			days = append(days, d)
		}
	}
	if len(days) == 0 {
		return StoredDay{}, false, nil
	}
	return mergeDays(g.ID, days), true, nil
}

func (b *groupBackend) LoadDailyRange(ctx context.Context, orgID string, from, to time.Time) ([]DailyRecord, error) {
	g, ok := b.group(orgID)
	if !ok {
		return b.inner.LoadDailyRange(ctx, orgID, from, to)
	}
	byDay := map[string][]DailyRecord{}
	for _, m := range g.Members {
		recs, err := b.inner.LoadDailyRange(ctx, m.ID, from, to)
		if err != nil {
			return nil, err
		}
		for _, r := range recs {
			key := r.Day.Format("2006-01-02")
			if m.From != "" && key < m.From {
				continue
			}
			byDay[key] = append(byDay[key], r)
		}
	}
	keys := make([]string, 0, len(byDay))
	for k := range byDay {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]DailyRecord, 0, len(keys))
	for _, k := range keys {
		out = append(out, mergeDailyRecords(byDay[k]))
	}
	return out, nil
}

func (b *groupBackend) LoadHourlyRange(ctx context.Context, orgID string, from, to time.Time) ([]HourlyRecord, error) {
	g, ok := b.group(orgID)
	if !ok {
		return b.inner.LoadHourlyRange(ctx, orgID, from, to)
	}
	loc := from.Location()
	byHour := map[int64]HourlyRecord{}
	for _, m := range g.Members {
		recs, err := b.inner.LoadHourlyRange(ctx, m.ID, from, to)
		if err != nil {
			return nil, err
		}
		for _, r := range recs {
			if m.From != "" && r.HourStart.In(loc).Format("2006-01-02") < m.From {
				continue
			}
			k := r.HourStart.Unix()
			if acc, ok := byHour[k]; ok {
				byHour[k] = mergeHourly(acc, r)
			} else {
				byHour[k] = r
			}
		}
	}
	out := make([]HourlyRecord, 0, len(byHour))
	for _, r := range byHour {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].HourStart.Before(out[j].HourStart) })
	return out, nil
}

// SumEbitdaBefore of a group counts each member from the day it joins.
func (b *groupBackend) SumEbitdaBefore(ctx context.Context, orgID string, before time.Time) (float64, int, error) {
	g, ok := b.group(orgID)
	if !ok {
		return b.inner.SumEbitdaBefore(ctx, orgID, before)
	}
	var sum float64
	var months int
	for _, m := range g.Members {
		s, n, err := b.inner.SumEbitdaBefore(ctx, m.ID, before)
		if err != nil {
			return 0, 0, err
		}
		if m.From != "" {
			joined, err := time.ParseInLocation("2006-01-02", m.From, before.Location())
			if err != nil {
				return 0, 0, fmt.Errorf("group %s: bad member start %q", g.ID, m.From)
			}
			if !joined.Before(before) {
				continue
			}
			s0, n0, err := b.inner.SumEbitdaBefore(ctx, m.ID, joined)
			if err != nil {
				return 0, 0, err
			}
			s, n = s-s0, n-n0
		}
		sum += s
		months = max(months, n)
	}
	return sum, months, nil
}

// groupOf reports whether orgID is a group the service's backend reads as
// one organization.
func (s *Service) groupOf(orgID string) (Group, bool) {
	if gb, ok := s.backend.(*groupBackend); ok {
		return gb.group(orgID)
	}
	return Group{}, false
}

// groupDay is a group's day: each member's own GetDay, so a final day
// whose РДН prices came late still heals, added up hour by hour.
func (s *Service) groupDay(ctx context.Context, g Group, date, tz string) (StoredDay, error) {
	loc, err := loadLocation(tz)
	if err != nil {
		return StoredDay{}, err
	}
	parsed, err := time.ParseInLocation("2006-01-02", date, loc)
	if err != nil {
		return StoredDay{}, fmt.Errorf("date must be YYYY-MM-DD")
	}
	dayStart := time.Date(parsed.Year(), parsed.Month(), parsed.Day(), 0, 0, 0, 0, loc)
	key := dayStart.Format("2006-01-02")
	var days []StoredDay
	for _, id := range g.ActiveOn(key) {
		d, err := s.GetDay(ctx, id, key, tz)
		if err != nil {
			return StoredDay{}, fmt.Errorf("%s: %w", id, err)
		}
		days = append(days, d)
	}
	if len(days) == 0 {
		return StoredDay{OrganizationID: g.ID, Day: dayStart, Tz: loc.String(), Rows: make([]*HourRow, 24)}, nil
	}
	return mergeDays(g.ID, days), nil
}

// recomputeGroup recomputes each member over the part of [from, to] it
// belongs to the group; the progress counts the members' days together.
func (s *Service) recomputeGroup(ctx context.Context, g Group, from, to, tz string, onProgress func(done, total int, label string)) (RangeResult, error) {
	fromDay, err := time.Parse("2006-01-02", from)
	if err != nil {
		return RangeResult{}, fmt.Errorf("from must be YYYY-MM-DD")
	}
	toDay, err := time.Parse("2006-01-02", to)
	if err != nil {
		return RangeResult{}, fmt.Errorf("to must be YYYY-MM-DD")
	}
	if toDay.Before(fromDay) {
		return RangeResult{}, fmt.Errorf("to must be on or after from")
	}
	type span struct{ id, from string }
	var spans []span
	total := 0
	for _, m := range g.Members {
		start := from
		if m.From > start {
			start = m.From
		}
		startDay, err := time.Parse("2006-01-02", start)
		if err != nil {
			return RangeResult{}, fmt.Errorf("group %s: bad member start %q", g.ID, m.From)
		}
		if startDay.After(toDay) {
			continue
		}
		spans = append(spans, span{id: m.ID, from: start})
		total += int(toDay.Sub(startDay)/(24*time.Hour)) + 1
	}

	result := RangeResult{From: from, To: to}
	done := 0
	for _, sp := range spans {
		r, err := s.RecomputeRange(ctx, sp.id, sp.from, to, tz, func(d, _ int, label string) {
			if onProgress != nil {
				onProgress(done+d, total, sp.id+" "+label)
			}
		})
		done += r.Days
		result.Days += r.Days
		result.DaysOK += r.DaysOK
		result.DaysFailed += r.DaysFailed
		for _, e := range r.Errors {
			if len(result.Errors) < maxRangeErrors {
				result.Errors = append(result.Errors, DayError{Date: sp.id + " " + e.Date, Error: e.Error})
			}
		}
		if err != nil {
			return result, err
		}
	}
	return result, nil
}

// mergeDays adds several members' stored days of one date up: flows and
// money hour by hour, the daily totals as mergeDailyTotals does. The day
// is final once every member's is, and current as of the least fresh.
func mergeDays(orgID string, days []StoredDay) StoredDay {
	out := StoredDay{OrganizationID: orgID, Day: days[0].Day, Tz: days[0].Tz, IsFinal: true, Rows: make([]*HourRow, 24)}
	totals := make([]DailyTotals, len(days))
	for i, d := range days {
		totals[i] = d.Totals
		out.IsFinal = out.IsFinal && d.IsFinal
		out.ComputedAt = earliestComputed(out.ComputedAt, d.ComputedAt)
		for h, r := range d.Rows {
			if r == nil || h >= len(out.Rows) {
				continue
			}
			if out.Rows[h] == nil {
				row := *r
				out.Rows[h] = &row
				continue
			}
			out.Rows[h] = mergeHourRow(out.Rows[h], r)
		}
	}
	out.Totals = mergeDailyTotals(totals)
	return out
}

func mergeDailyRecords(recs []DailyRecord) DailyRecord {
	out := DailyRecord{Day: recs[0].Day, IsFinal: true, MoneyScale: recs[0].MoneyScale}
	totals := make([]DailyTotals, len(recs))
	for i, r := range recs {
		totals[i] = r.Totals
		out.IsFinal = out.IsFinal && r.IsFinal
		out.ComputedAt = earliestComputed(out.ComputedAt, r.ComputedAt)
	}
	out.Totals = mergeDailyTotals(totals)
	return out
}

// mergeDailyTotals adds members' daily totals up. Prices are weighted by
// the kWh they priced, the УЗЕ cost basis by the energy it carries; hour
// counts are the day's (the members share one clock and one РДН), quality
// flags pool. A group is never reconciled as a whole, so it carries no
// per-field reconciliation.
func mergeDailyTotals(ts []DailyTotals) DailyTotals {
	var out DailyTotals
	var importUah, exportUah float64
	seen := map[string]bool{}
	for _, t := range ts {
		out.BaselineCost += t.BaselineCost
		out.ActualCost += t.ActualCost
		out.Effect += t.Effect
		out.EssNet += t.EssNet
		out.Load += t.Load
		out.PV += t.PV
		out.GridImport += t.GridImport
		out.GridExport += t.GridExport
		out.EssCharged += t.EssCharged
		out.EssDischarged += t.EssDischarged
		out.PVToLoad += t.PVToLoad
		out.PVToEss += t.PVToEss
		out.PVToGrid += t.PVToGrid
		out.GridToLoad += t.GridToLoad
		out.GridToEss += t.GridToEss
		out.EssToLoad += t.EssToLoad
		out.EssToGrid += t.EssToGrid
		out.HoursWithData = max(out.HoursWithData, t.HoursWithData)
		out.HoursMissingPrice = max(out.HoursMissingPrice, t.HoursMissingPrice)
		importUah += t.AvgImportPrice * t.GridImport
		exportUah += t.AvgExportPrice * t.GridExport
		out.RevenuePvExport += t.RevenuePvExport
		out.RevenuePvSelf += t.RevenuePvSelf
		out.RevenueEssExport += t.RevenueEssExport
		out.RevenueEssSelf += t.RevenueEssSelf
		out.RevenueTotal += t.RevenueTotal
		out.ExpenseGridCharge += t.ExpenseGridCharge
		out.ExpenseTotal += t.ExpenseTotal
		out.Ebitda += t.Ebitda
		out.EssWithdrawnCost += t.EssWithdrawnCost
		out.EssRealizedProfit += t.EssRealizedProfit
		out.EssDegradationCost += t.EssDegradationCost
		out.EssResidualKwhEod += t.EssResidualKwhEod
		out.EssCostBasisUahEod += t.EssCostBasisUahEod
		out.Reconciled = out.Reconciled || t.Reconciled
		for _, f := range t.QualityFlags {
			if !seen[f] {
				seen[f] = true
				out.QualityFlags = append(out.QualityFlags, f)
			}
		}
	}
	if out.GridImport > 0 {
		out.AvgImportPrice = importUah / out.GridImport
	}
	if out.GridExport > 0 {
		out.AvgExportPrice = exportUah / out.GridExport
	}
	if out.EssResidualKwhEod > 0 {
		out.EssAvgCostBasisEod = out.EssCostBasisUahEod / out.EssResidualKwhEod
	}
	return out
}

func mergeHourRow(a, b *HourRow) *HourRow {
	out := *a
	if out.Rdn == nil {
		out.Rdn = b.Rdn
	}
	out.Flow = HourFlows{
		PV:            a.Flow.PV + b.Flow.PV,
		GridImport:    a.Flow.GridImport + b.Flow.GridImport,
		GridExport:    a.Flow.GridExport + b.Flow.GridExport,
		EssCharged:    a.Flow.EssCharged + b.Flow.EssCharged,
		EssDischarged: a.Flow.EssDischarged + b.Flow.EssDischarged,
		PVToEss:       a.Flow.PVToEss + b.Flow.PVToEss,
		GridToEss:     a.Flow.GridToEss + b.Flow.GridToEss,
		EssToLoad:     a.Flow.EssToLoad + b.Flow.EssToLoad,
		EssToGrid:     a.Flow.EssToGrid + b.Flow.EssToGrid,
	}
	out.Econ = HourEconomics{
		Load:         a.Econ.Load + b.Econ.Load,
		PVToLoad:     a.Econ.PVToLoad + b.Econ.PVToLoad,
		PVToGrid:     a.Econ.PVToGrid + b.Econ.PVToGrid,
		GridToLoad:   a.Econ.GridToLoad + b.Econ.GridToLoad,
		ImportPrice:  weightedPrice(a.Econ.ImportPrice, a.Flow.GridImport, b.Econ.ImportPrice, b.Flow.GridImport),
		ExportPrice:  weightedPrice(a.Econ.ExportPrice, a.Flow.GridExport, b.Econ.ExportPrice, b.Flow.GridExport),
		BaselineCost: a.Econ.BaselineCost + b.Econ.BaselineCost,
		ActualCost:   a.Econ.ActualCost + b.Econ.ActualCost,
		Effect:       a.Econ.Effect + b.Econ.Effect,
		EssNet:       a.Econ.EssNet + b.Econ.EssNet,
	}
	out.EssRemainingKwhStart = addPtr(a.EssRemainingKwhStart, b.EssRemainingKwhStart)
	out.EssCostBasisUahStart = addPtr(a.EssCostBasisUahStart, b.EssCostBasisUahStart)
	out.EssWithdrawnCostUah = addPtr(a.EssWithdrawnCostUah, b.EssWithdrawnCostUah)
	out.EssRealizedProfitUah = addPtr(a.EssRealizedProfitUah, b.EssRealizedProfitUah)
	out.EssCostBasisUahEnd = addPtr(a.EssCostBasisUahEnd, b.EssCostBasisUahEnd)
	out.EssResidualKwhEnd = addPtr(a.EssResidualKwhEnd, b.EssResidualKwhEnd)
	out.EssAvgCostUahPerKwhStart = ratioPtr(out.EssCostBasisUahStart, out.EssRemainingKwhStart)
	out.EssAvgCostUahPerKwhEnd = ratioPtr(out.EssCostBasisUahEnd, out.EssResidualKwhEnd)
	out.EssPeakIntervalKw = a.EssPeakIntervalKw + b.EssPeakIntervalKw
	return &out
}

func mergeHourly(a, b HourlyRecord) HourlyRecord {
	out := a
	if out.Rdn == nil {
		out.Rdn = b.Rdn
	}
	out.ImportPrice = weightedPrice(a.ImportPrice, a.GridImport, b.ImportPrice, b.GridImport)
	out.ExportPrice = weightedPrice(a.ExportPrice, a.PVToGrid+a.EssToGrid, b.ExportPrice, b.PVToGrid+b.EssToGrid)
	out.GridImport += b.GridImport
	out.PV += b.PV
	out.PVToLoad += b.PVToLoad
	out.PVToGrid += b.PVToGrid
	out.PVToEss += b.PVToEss
	out.GridToEss += b.GridToEss
	out.GridToLoad += b.GridToLoad
	out.EssToLoad += b.EssToLoad
	out.EssToGrid += b.EssToGrid
	out.EssCharged += b.EssCharged
	out.EssDischarged += b.EssDischarged
	out.EssNet += b.EssNet
	out.EssWithdrawnCostUah = addPtr(a.EssWithdrawnCostUah, b.EssWithdrawnCostUah)
	out.EssRealizedProfitUah = addPtr(a.EssRealizedProfitUah, b.EssRealizedProfitUah)
	out.EssRemainingKwhStart = addPtr(a.EssRemainingKwhStart, b.EssRemainingKwhStart)
	out.EssPeakIntervalKw += b.EssPeakIntervalKw
	return out
}

// weightedPrice is the price of the members' kWh together; with no kWh
// priced it is the first member's price that is set.
func weightedPrice(pa, wa, pb, wb float64) float64 {
	if wa+wb > 0 {
		return (pa*wa + pb*wb) / (wa + wb)
	}
	if pa != 0 {
		return pa
	}
	return pb
}

func addPtr(a, b *float64) *float64 {
	switch {
	case a == nil && b == nil:
		return nil
	case a == nil:
		v := *b
		return &v
	case b == nil:
		v := *a
		return &v
	}
	v := *a + *b
	return &v
}

func ratioPtr(num, den *float64) *float64 {
	if num == nil || den == nil || *den <= 0 {
		return nil
	}
	v := *num / *den
	return &v
}

func earliestComputed(a, b time.Time) time.Time {
	if a.IsZero() || (!b.IsZero() && b.Before(a)) {
		return b
	}
	return a
}
