package economics

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"
)

// orgBackends serves each organization from its own fakeBackend.
type orgBackends map[string]*fakeBackend

func (m orgBackends) of(org string) *fakeBackend {
	b, ok := m[org]
	if !ok {
		b = &fakeBackend{}
		m[org] = b
	}
	return b
}

func (m orgBackends) HourlyFlows(ctx context.Context, org string, dayStart time.Time) ([]FlowRow, error) {
	return m.of(org).HourlyFlows(ctx, org, dayStart)
}

func (m orgBackends) Timeseries(ctx context.Context, org string, keys []string, from, to time.Time, bucket, tz, agg string) ([]Point, error) {
	return m.of(org).Timeseries(ctx, org, keys, from, to, bucket, tz, agg)
}

func (m orgBackends) DAMPrices(ctx context.Context, zone int, from, to time.Time) ([]DAMHour, error) {
	return nil, nil
}

func (m orgBackends) TariffSchedule(ctx context.Context, org string) (Schedule, error) {
	return m.of(org).TariffSchedule(ctx, org)
}

func (m orgBackends) CanonicalDaily(ctx context.Context, org string, day time.Time) (CanonicalDaily, bool, error) {
	return m.of(org).CanonicalDaily(ctx, org, day)
}

func (m orgBackends) SaveDay(ctx context.Context, day StoredDay) error {
	return m.of(day.OrganizationID).SaveDay(ctx, day)
}

func (m orgBackends) LoadDay(ctx context.Context, org string, day time.Time, tz string) (StoredDay, bool, error) {
	return m.of(org).LoadDay(ctx, org, day, tz)
}

func (m orgBackends) LoadDailyRange(ctx context.Context, org string, from, to time.Time) ([]DailyRecord, error) {
	return m.of(org).LoadDailyRange(ctx, org, from, to)
}

func (m orgBackends) LoadHourlyRange(ctx context.Context, org string, from, to time.Time) ([]HourlyRecord, error) {
	return m.of(org).LoadHourlyRange(ctx, org, from, to)
}

func (m orgBackends) SumEbitdaBefore(ctx context.Context, org string, before time.Time) (float64, int, error) {
	return m.of(org).SumEbitdaBefore(ctx, org, before)
}

// testGroup is `a` throughout and `b` from 2026-09-24, the shape of
// Дубовʼязівський's Т2 and Т1.
var testGroup = Group{ID: "g", Name: "Елеватор", Members: []GroupMember{{ID: "a", Label: "A"}, {ID: "b", Label: "B", From: "2026-09-24"}}}

func groupService(b orgBackends) *Service {
	return NewService(InGroups(b, []Group{testGroup}))
}

// A group's month is its members' stored days added up, each member from
// the day it joins: b's day before it joined is not the elevator's.
func TestGroupAddsMembersUpFromTheDayTheyJoin(t *testing.T) {
	b := orgBackends{
		"a": {saved: map[string]StoredDay{"2026-09-20": savedDay("2026-09-20", 100), "2026-09-25": savedDay("2026-09-25", 200)}},
		"b": {saved: map[string]StoredDay{"2026-09-20": savedDay("2026-09-20", 999), "2026-09-25": savedDay("2026-09-25", 50)}},
	}
	svc := groupService(b)
	ctx := context.Background()

	month, err := svc.GetMonthTotals(ctx, "g", "2026-09", "UTC")
	if err != nil {
		t.Fatal(err)
	}
	if !sameMoney(month.Ebitda, 350) || month.PV != 30 || month.DaysWithData != 2 {
		t.Errorf("group month: ebitda %v pv %v days %d, want 350, 30, 2", month.Ebitda, month.PV, month.DaysWithData)
	}
	if own, _ := svc.GetMonthTotals(ctx, "a", "2026-09", "UTC"); !sameMoney(own.Ebitda, 300) {
		t.Errorf("a member read on its own: ebitda %v, want 300", own.Ebitda)
	}

	if sum, _, err := svc.backend.SumEbitdaBefore(ctx, "g", mustDate("2026-10-01")); err != nil || !sameMoney(sum, 350) {
		t.Errorf("EBITDA before October = %v (err %v), want 350", sum, err)
	}
	if sum, _, _ := svc.backend.SumEbitdaBefore(ctx, "g", mustDate("2026-09-22")); !sameMoney(sum, 100) {
		t.Errorf("EBITDA before b joined = %v, want a's 100", sum)
	}
}

// A group's day adds its members' hours up, pricing them by the kWh each
// imported; it is final only once every member's is, and it is never
// computed or saved under the group's id.
func TestGroupDayAddsMembersHoursUp(t *testing.T) {
	rdn := 5.0
	hour := func(imp, price float64) *HourRow {
		return &HourRow{
			Hour: 10, HourStart: mustDate("2026-10-10").Add(10 * time.Hour), Rdn: &rdn,
			Flow: HourFlows{GridImport: imp},
			Econ: HourEconomics{Load: imp, GridToLoad: imp, ImportPrice: price, BaselineCost: imp * price, ActualCost: imp * price},
		}
	}
	day := func(row *HourRow, final bool) StoredDay {
		rows := make([]*HourRow, 24)
		rows[10] = row
		return StoredDay{Day: mustDate("2026-10-10"), Rows: rows, IsFinal: final, Totals: ComputeDailyTotals(rows)}
	}
	b := orgBackends{
		"a": {saved: map[string]StoredDay{"2026-10-10": day(hour(5, 6), true)}},
		"b": {saved: map[string]StoredDay{"2026-10-10": day(hour(15, 8), false)}},
	}
	svc := groupService(b)
	ctx := context.Background()

	got, err := svc.GetDay(ctx, "g", "2026-10-10", "UTC")
	if err != nil {
		t.Fatal(err)
	}
	r := got.Rows[10]
	if r == nil || r.Flow.GridImport != 20 || !sameMoney(r.Econ.ImportPrice, 7.5) || !sameMoney(r.Econ.BaselineCost, 150) {
		t.Fatalf("merged hour = %+v, want 20 kWh at 7.5 costing 150", r)
	}
	if got.IsFinal || got.OrganizationID != "g" || !sameMoney(got.Totals.AvgImportPrice, 7.5) {
		t.Errorf("group day: final %v org %q avg import %v", got.IsFinal, got.OrganizationID, got.Totals.AvgImportPrice)
	}
	if _, err := svc.ComputeDay(ctx, "g", "2026-10-10", "UTC"); !errors.Is(err, errGroupView) {
		t.Errorf("ComputeDay of a group = %v, want errGroupView", err)
	}
	if b.of("g").saveCount != 0 || b.of("g").flowsCalls != 0 {
		t.Error("a group was computed or saved under its own id")
	}
	if empty, err := svc.GetDay(ctx, "g", "2026-01-05", "UTC"); err != nil || len(empty.Rows) != 24 {
		t.Errorf("a day before any data: %v rows, err %v", len(empty.Rows), err)
	}
}

// The group's УЗЕ is its members' in force added up, priced by the first:
// a alone until b joins, then both — so the split day does not shrink the
// elevator's battery.
func TestGroupTariffScheduleAddsTheUzeInForce(t *testing.T) {
	version := func(day string, capacity, power, dist float64) ScheduleEntry {
		return ScheduleEntry{EffectiveFrom: mustDate(day), Tariffs: Tariffs{EssCapacityKwh: capacity, EssPowerLimitKw: power, DistributionUahPerKwh: dist}}
	}
	b := orgBackends{
		"a": {schedule: Schedule{version("1970-01-01", 860, 540, 4.1), version("2026-09-24", 516, 324, 4.1)}},
		"b": {schedule: Schedule{version("1970-01-01", 344, 216, 9.9)}},
	}
	s, err := InGroups(b, []Group{testGroup}).TariffSchedule(context.Background(), "g")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		day             string
		capacity, power float64
	}{{"2026-09-23", 860, 540}, {"2026-09-24", 860, 540}, {"2026-11-01", 860, 540}} {
		r := s.EssRatingsFor(mustDate(c.day))
		if r.CapacityKwh != c.capacity || r.PowerLimitKw != c.power {
			t.Errorf("%s: УЗЕ %v kWh / %v kW, want %v / %v", c.day, r.CapacityKwh, r.PowerLimitKw, c.capacity, c.power)
		}
	}
	if tf, _ := s.ResolveForDay(mustDate("2026-10-01")); math.Abs(tf.DistributionUahPerKwh-4.1) > 1e-12 {
		t.Errorf("prices from %v, want the first member's 4.1", tf.DistributionUahPerKwh)
	}
}

// Recomputing a group recomputes each member over the part of the range it
// belongs to the group, under the member's own id.
func TestGroupRecomputeRunsEachMemberFromItJoins(t *testing.T) {
	b := orgBackends{}
	svc := groupService(b)
	var total int
	res, err := svc.RecomputeRange(context.Background(), "g", "2026-09-22", "2026-09-25", "UTC", func(_, n int, _ string) { total = n })
	if err != nil {
		t.Fatal(err)
	}
	if res.Days != 6 || total != 6 {
		t.Errorf("recomputed %d days (progress total %d), want 4 of a and 2 of b", res.Days, total)
	}
	if b.of("a").saveCount != 4 || b.of("b").saveCount != 2 || b.of("g").saveCount != 0 {
		t.Errorf("saved a %d, b %d, g %d; want 4, 2, 0", b.of("a").saveCount, b.of("b").saveCount, b.of("g").saveCount)
	}
	if _, ok := b.of("b").saved["2026-09-23"]; ok {
		t.Error("b was recomputed before it joined")
	}
}

// twoBatteries is testGroup over September and October 2026: a has the PV
// and a 516 kWh pack; b a 344 kWh pack and no PV, and a 100 kW limit its
// evening discharges overrun, so some of its hours are anomalous.
func twoBatteries() orgBackends {
	days, hourly, _ := synthPeriod(time.UTC, time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), 2)
	saved := make(map[string]StoredDay, len(days))
	for _, d := range days {
		saved[d.Day.Format("2006-01-02")] = StoredDay{Day: d.Day, Totals: d.Totals, IsFinal: true}
	}
	noPv := make([]HourlyRecord, len(hourly))
	for i, h := range hourly {
		h.EssCharged -= h.PVToEss
		h.PV, h.PVToLoad, h.PVToGrid, h.PVToEss = 0, 0, 0, 0
		if h.EssRemainingKwhStart != nil {
			soc := *h.EssRemainingKwhStart * 344 / 516
			h.EssRemainingKwhStart = &soc
		}
		noPv[i] = h
	}
	pack := func(capacity, power float64) Schedule {
		return Schedule{{EffectiveFrom: mustDate("1970-01-01"), Tariffs: Tariffs{
			EssCapacityKwh: capacity, EssPowerLimitKw: power, DegradationUahPerKwh: 0.6, RoundtripEfficiency: 0.9,
		}}}
	}
	return orgBackends{
		"a": {saved: saved, hourly: hourly, schedule: pack(516, 250)},
		"b": {saved: saved, hourly: noPv, schedule: pack(344, 100)},
	}
}

// checkUzeSum checks that a group's УЗЕ figures are its batteries' added up.
func checkUzeSum(t *testing.T, what string, got MonthlyTotals, batteries ...MonthlyTotals) {
	t.Helper()
	for _, f := range []struct {
		name string
		of   func(MonthlyTotals) float64
	}{
		{"fact", func(m MonthlyTotals) float64 { return m.EssFact }},
		{"optimum", func(m MonthlyTotals) float64 { return m.EssOptimum }},
		{"reserve", func(m MonthlyTotals) float64 { return m.EssReserve }},
		{"timing reserve", func(m MonthlyTotals) float64 { return m.EssReserveTiming }},
		{"SOC reserve", func(m MonthlyTotals) float64 { return m.EssReserveSoc }},
		{"PV reserve", func(m MonthlyTotals) float64 { return m.EssReservePv }},
		{"missed PV", func(m MonthlyTotals) float64 { return m.EssPvMissedKwh }},
		{"export potential with the УЗЕ", func(m MonthlyTotals) float64 { return m.PvEssExportPotential }},
	} {
		var want float64
		for _, b := range batteries {
			want += f.of(b)
		}
		if !sameMoney(f.of(got), want) {
			t.Errorf("%s: %s %v, want the batteries' %v", what, f.name, f.of(got), want)
		}
	}
	if got.EssOptimum <= 0 || !sameMoney(got.EssCapturedShare, got.EssFact/got.EssOptimum) {
		t.Errorf("%s: optimum %v, captured share %v", what, got.EssOptimum, got.EssCapturedShare)
	}
}

// Batteries behind separate grid inputs can't serve each other's site, so
// the group's УЗЕ is each one solved on its own site and added up — not
// one pack the size of both on the merged flows — while its money stays
// the merged records'.
func TestGroupSolvesEachBatteryOnItsOwnSite(t *testing.T) {
	b := twoBatteries()
	svc := groupService(b)
	ctx := context.Background()

	got, err := svc.GetMonth(ctx, "g", "2026-10", "UTC")
	if err != nil {
		t.Fatal(err)
	}
	ownA, _ := svc.GetMonth(ctx, "a", "2026-10", "UTC")
	ownB, _ := svc.GetMonth(ctx, "b", "2026-10", "UTC")
	checkUzeSum(t, "October", got.Totals, ownA.Totals, ownB.Totals)
	if !sameMoney(got.Totals.Ebitda, ownA.Totals.Ebitda+ownB.Totals.Ebitda) || !sameMoney(got.Totals.PV, ownA.Totals.PV+ownB.Totals.PV) {
		t.Errorf("money: EBITDA %v, PV %v; want the sites' together", got.Totals.Ebitda, got.Totals.PV)
	}

	daily, _ := svc.backend.LoadDailyRange(ctx, "g", mustDate("2026-10-01"), mustDate("2026-10-31"))
	hourly, _ := svc.backend.LoadHourlyRange(ctx, "g", mustDate("2026-10-01"), mustDate("2026-11-01"))
	schedule, _ := svc.backend.TariffSchedule(ctx, "g")
	if one := AggregateMonth("2026-10", time.UTC, daily, hourly, schedule.EssRatingsFor).Totals; math.Abs(one.EssOptimum-got.Totals.EssOptimum) < 1 {
		t.Fatalf("one pack of both has the same optimum %v; the fixture can't tell it apart", one.EssOptimum)
	}

	dayOf := func(m StoredMonth, date string) MonthDay {
		for _, d := range m.Days {
			if d.Date == date {
				return d
			}
		}
		return MonthDay{}
	}
	for _, d := range got.Days {
		a, b := dayOf(ownA, d.Date), dayOf(ownB, d.Date)
		if !sameMoney(d.EssOptimum, a.EssOptimum+b.EssOptimum) || !sameMoney(d.EssReserve, a.EssReserve+b.EssReserve) {
			t.Errorf("%s: optimum %v reserve %v, want %v and %v", d.Date, d.EssOptimum, d.EssReserve, a.EssOptimum+b.EssOptimum, a.EssReserve+b.EssReserve)
		}
	}

	if len(got.Cycles) == 0 || len(got.Cycles) != len(ownA.Cycles)+len(ownB.Cycles) {
		t.Fatalf("%d cycles, want a's %d and b's %d", len(got.Cycles), len(ownA.Cycles), len(ownB.Cycles))
	}
	for _, c := range got.Cycles {
		if !strings.HasPrefix(c.Label, "A · ") && !strings.HasPrefix(c.Label, "B · ") {
			t.Errorf("cycle %q is not told apart by its site", c.Label)
		}
	}
	dq, own := got.Totals.EssDataQuality, ownB.Totals.EssDataQuality
	if own.AnomalousHours == 0 || dq.AnomalousHours != ownA.Totals.EssDataQuality.AnomalousHours+own.AnomalousHours || dq.DataOK {
		t.Fatalf("anomalous hours %d, want a's %d and b's %d", dq.AnomalousHours, ownA.Totals.EssDataQuality.AnomalousHours, own.AnomalousHours)
	}
	for _, a := range dq.Anomalies {
		if a.Site != "B" {
			t.Errorf("anomalous hour %s of site %q, want b's", a.At, a.Site)
		}
	}
}

// A battery counts from the day its site joins: the month it joins is the
// first site's month and the second's days from then on, untagged while
// the first is the group alone, and the year is its months added up.
func TestGroupBatteryCountsFromTheDayItJoins(t *testing.T) {
	b := twoBatteries()
	svc := groupService(b)
	ctx := context.Background()

	sep, err := svc.GetMonth(ctx, "g", "2026-09", "UTC")
	if err != nil {
		t.Fatal(err)
	}
	ownA, _ := svc.GetMonth(ctx, "a", "2026-09", "UTC")
	bDaily, _ := b.of("b").LoadDailyRange(ctx, "b", mustDate("2026-09-24"), mustDate("2026-09-30"))
	bHourly, _ := b.of("b").LoadHourlyRange(ctx, "b", mustDate("2026-09-24"), mustDate("2026-10-01"))
	joined := AggregateMonth("2026-09", time.UTC, bDaily, bHourly, b.of("b").schedule.EssRatingsFor)
	checkUzeSum(t, "September", sep.Totals, ownA.Totals, joined.Totals)
	for _, c := range sep.Cycles {
		if alone := c.StartDate < "2026-09-24"; alone == strings.Contains(c.Label, " · ") {
			t.Errorf("cycle %s labelled %q", c.StartDate, c.Label)
		}
	}
	for _, a := range sep.Totals.EssDataQuality.Anomalies {
		if a.Date < "2026-09-24" {
			t.Errorf("anomalous hour %s of b before it joined", a.At)
		}
	}

	oct, _ := svc.GetMonthTotals(ctx, "g", "2026-10", "UTC")
	year, err := svc.GetYear(ctx, "g", "2026", "UTC")
	if err != nil {
		t.Fatal(err)
	}
	period, err := svc.GetPeriod(ctx, "g", "2026-09", "2026-10", "UTC")
	if err != nil {
		t.Fatal(err)
	}
	for _, y := range []StoredYear{year, period} {
		checkUzeSum(t, y.Period, y.Totals, sep.Totals, oct)
	}
}
