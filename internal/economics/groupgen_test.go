package economics

import (
	"context"
	"math"
	"slices"
	"sort"
	"testing"
	"time"
)

// orgDaysBackend stores days per organization, stamping each save one
// second later than the last, and serves the group's generator rows.
type orgDaysBackend struct {
	*fakeBackend
	days    map[string]StoredDay
	genRows []*GenFlowRow
	deleted []string
	clock   time.Time
}

func newOrgDaysBackend(t *testing.T, tariffs Tariffs) (*orgDaysBackend, *time.Location) {
	t.Helper()
	fb, loc := newKyivBackend(t)
	fb.schedule = Schedule{{EffectiveFrom: mustDate("1970-01-01"), Tariffs: tariffs}}
	return &orgDaysBackend{fakeBackend: fb, days: map[string]StoredDay{}, clock: time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)}, loc
}

func dayKey(org string, day time.Time) string { return org + "|" + day.Format("2006-01-02") }

func (b *orgDaysBackend) SaveDay(_ context.Context, d StoredDay) error {
	b.clock = b.clock.Add(time.Second)
	d.ComputedAt = b.clock
	b.days[dayKey(d.OrganizationID, d.Day)] = d
	return nil
}

func (b *orgDaysBackend) LoadDay(_ context.Context, org string, day time.Time, _ string) (StoredDay, bool, error) {
	d, ok := b.days[dayKey(org, day)]
	return d, ok, nil
}

func (b *orgDaysBackend) LoadDailyRange(_ context.Context, org string, from, to time.Time) ([]DailyRecord, error) {
	var out []DailyRecord
	for _, d := range b.days {
		key := d.Day.Format("2006-01-02")
		if d.OrganizationID != org || key < from.Format("2006-01-02") || key > to.Format("2006-01-02") {
			continue
		}
		out = append(out, DailyRecord{Day: d.Day, Totals: d.Totals, IsFinal: d.IsFinal, ComputedAt: d.ComputedAt})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Day.Before(out[j].Day) })
	return out, nil
}

func (b *orgDaysBackend) LoadHourlyRange(context.Context, string, time.Time, time.Time) ([]HourlyRecord, error) {
	return nil, nil
}

func (b *orgDaysBackend) DeleteDay(_ context.Context, org string, day time.Time) error {
	delete(b.days, dayKey(org, day))
	b.deleted = append(b.deleted, dayKey(org, day))
	return nil
}

func (b *orgDaysBackend) GroupGenFlows(context.Context, []string, time.Time) ([]*GenFlowRow, error) {
	return b.genRows, nil
}

// siteDay is a site's day computed without its shared generator: hour 2
// spent on the generator with the battery charging chg kWh (its grid
// meter claiming imp), the rest of the day nothing.
func siteDay(org string, dayStart time.Time, chg, imp float64, m SupplyMinutes) StoredDay {
	rows := make([]*HourRow, 24)
	for h := range rows {
		rows[h] = &HourRow{Hour: h, HourStart: dayStart.Add(time.Duration(h) * time.Hour), Rdn: ptr(2)}
	}
	flow := HourFlows{EssCharged: chg, GridImport: imp, GridToEss: math.Min(chg, imp)}
	rows[2].Flow = flow
	rows[2].Econ = HourEconomicsFor(2, flow, flatTariffs)
	rows[2].GenKwh = 150
	rows[2].SupplyMinutes = m
	d := StoredDay{OrganizationID: org, Day: dayStart, Tz: "Europe/Kyiv", Rows: rows, IsFinal: true}
	d.Totals = ComputeDailyTotals(rows)
	d.Totals.QualityFlags = genFlags(rows, false, true)
	return d
}

// The group's walk of hour 2: both sites without the grid on the one
// generator, 150 kWh, 50 of them into the two batteries.
func bothOnGenerator() []*GenFlowRow {
	rows := make([]*GenFlowRow, 24)
	rows[2] = &GenFlowRow{
		GenKwh: 150, Minutes: SupplyMinutes{Generator: 60},
		EssCharged: 50, GenToEss: 50, GenToLoad: 100, LoadOffgrid: 100,
	}
	return rows
}

func groupDayAt(t *testing.T) time.Time {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Kyiv")
	if err != nil {
		t.Skip("tzdata unavailable")
	}
	return time.Date(2026, 11, 16, 0, 0, 0, 0, loc)
}

// Acceptance §8, group de-all: the generator's energy is one meter's,
// the hour on the generator at both sites costs h once, and both
// batteries' charge is the generator's.
func TestGroupGenDayCountsGeneratorOnce(t *testing.T) {
	dayStart := groupDayAt(t)
	on := SupplyMinutes{Generator: 60}
	days := []StoredDay{siteDay("de", dayStart, 30, 3, on), siteDay("de1", dayStart, 20, 0, on)}

	perHour := groupGenDay("de-all", days, bothOnGenerator(), genTariffs(GenFuelPerHour))
	if perHour.Totals.GenKwh != 150 {
		t.Fatalf("generator energy %g, want the one meter's 150 kWh", perHour.Totals.GenKwh)
	}
	if perHour.Totals.GenFuelCost != 1000 {
		t.Fatalf("per_hour fuel %g, want h = 1000 once", perHour.Totals.GenFuelCost)
	}
	r := perHour.Rows[2]
	if !r.Flow.Gen.Accounted || r.Flow.Gen.GenToEss != 50 || r.Flow.GridToEss != 0 || r.Flow.GridImport != 0 {
		t.Fatalf("hour 2 flows %+v", r.Flow)
	}
	if slices.Contains(perHour.Totals.QualityFlags, "gen_shared") || !slices.Contains(perHour.Totals.QualityFlags, "offgrid_import:3") {
		t.Fatalf("flags %v", perHour.Totals.QualityFlags)
	}

	perKwh := groupGenDay("de-all", days, bothOnGenerator(), genTariffs(GenFuelPerKwh))
	if perKwh.Totals.GenFuelCost != 150*15 {
		t.Fatalf("per_kwh fuel %g, want G·c = 2250", perKwh.Totals.GenFuelCost)
	}
	if math.Abs(perKwh.Totals.Ebitda-perKwh.Totals.Effect) > 1e-6 {
		t.Fatalf("EBITDA %g vs effect %g (no wear in these tariffs)", perKwh.Totals.Ebitda, perKwh.Totals.Effect)
	}
}

// A site still on the grid keeps its import: the hour is not wholly off
// the grid for the group.
func TestGroupGenDayPartialHour(t *testing.T) {
	dayStart := groupDayAt(t)
	days := []StoredDay{siteDay("de", dayStart, 30, 0, SupplyMinutes{Generator: 60}), siteDay("de1", dayStart, 20, 25, SupplyMinutes{Transition: 60})}
	// The walk: 150 kWh of the generator, 30 into de's battery and 120
	// to de's load; de1 charges 20 from its grid.
	rows := make([]*GenFlowRow, 24)
	rows[2] = &GenFlowRow{
		GenKwh: 150, Minutes: SupplyMinutes{Generator: 60}, Partial: true,
		EssCharged: 50, GenToEss: 30, GridToEss: 20, GenToLoad: 120, LoadOffgrid: 120,
	}
	day := groupGenDay("de-all", days, rows, genTariffs(GenFuelPerKwh))
	f := day.Rows[2].Flow
	if f.GridImport != 25 || !f.Gen.Partial || f.Gen.GenToEss != 30 || f.GridToEss != 20 {
		t.Fatalf("hour 2 flows %+v", f)
	}
	if lOff := day.Rows[2].Econ.LoadOffgrid; math.Abs(lOff-120) > 1e-9 {
		t.Fatalf("off-grid load %g, want de's 120 only", lOff)
	}
}

// A site's recompute stores the group's day once both days are in, and
// drops it when the generator data is gone.
func TestComputeGroupGenDay(t *testing.T) {
	b, _ := newOrgDaysBackend(t, genTariffs(GenFuelPerKwh))
	dayStart := groupDayAt(t)
	g, _ := GroupByID("de-all")
	ctx := context.Background()
	b.genRows = bothOnGenerator()

	_ = b.SaveDay(ctx, siteDay("de", dayStart, 30, 0, SupplyMinutes{Generator: 60}))
	if err := computeGroupGenDay(ctx, b, g, dayStart, "Europe/Kyiv"); err != nil {
		t.Fatal(err)
	}
	if _, ok := b.days[dayKey("de-all", dayStart)]; ok {
		t.Fatal("a group day with one site's day missing")
	}
	_ = b.SaveDay(ctx, siteDay("de1", dayStart, 20, 0, SupplyMinutes{Generator: 60}))
	if err := computeGroupGenDay(ctx, b, g, dayStart, "Europe/Kyiv"); err != nil {
		t.Fatal(err)
	}
	gd, ok := b.days[dayKey("de-all", dayStart)]
	if !ok || gd.Totals.GenKwh != 150 {
		t.Fatalf("group day stored %v, totals %+v", ok, gd.Totals.GenTotals)
	}

	quiet := siteDay("de1", dayStart, 0, 0, SupplyMinutes{})
	quiet.Rows[2].GenKwh = 0
	_ = b.SaveDay(ctx, quiet)
	de := siteDay("de", dayStart, 0, 0, SupplyMinutes{})
	de.Rows[2].GenKwh = 0
	_ = b.SaveDay(ctx, de)
	if err := computeGroupGenDay(ctx, b, g, dayStart, "Europe/Kyiv"); err != nil {
		t.Fatal(err)
	}
	if _, ok := b.days[dayKey("de-all", dayStart)]; ok || len(b.deleted) != 1 {
		t.Fatalf("the group day must go with the generator data (deleted %v)", b.deleted)
	}
}

// The group reads its stored day while it is newer than its sites', and
// their plain sum once a site has been recomputed since.
func TestGroupReadsFreshGroupDay(t *testing.T) {
	b, _ := newOrgDaysBackend(t, genTariffs(GenFuelPerKwh))
	dayStart := groupDayAt(t)
	g, _ := GroupByID("de-all")
	ctx := context.Background()
	b.genRows = bothOnGenerator()
	_ = b.SaveDay(ctx, siteDay("de", dayStart, 30, 0, SupplyMinutes{Generator: 60}))
	_ = b.SaveDay(ctx, siteDay("de1", dayStart, 20, 0, SupplyMinutes{Generator: 60}))
	if err := computeGroupGenDay(ctx, b, g, dayStart, "Europe/Kyiv"); err != nil {
		t.Fatal(err)
	}
	group := InGroups(b, Groups)

	day, ok, err := group.LoadDay(ctx, "de-all", dayStart, "Europe/Kyiv")
	if err != nil || !ok || day.Totals.GenKwh != 150 || day.Totals.GenFuelCost == 0 {
		t.Fatalf("fresh group day: ok=%v err=%v totals %+v", ok, err, day.Totals.GenTotals)
	}
	recs, err := group.LoadDailyRange(ctx, "de-all", dayStart, dayStart)
	if err != nil || len(recs) != 1 || recs[0].Totals.GenKwh != 150 {
		t.Fatalf("fresh group record: %v %+v", err, recs)
	}

	_ = b.SaveDay(ctx, siteDay("de1", dayStart, 20, 0, SupplyMinutes{Generator: 60}))
	day, _, _ = group.LoadDay(ctx, "de-all", dayStart, "Europe/Kyiv")
	if day.Totals.GenKwh != 0 || day.Totals.GenFuelCost != 0 {
		t.Fatalf("stale group day served: %+v", day.Totals.GenTotals)
	}
	recs, _ = group.LoadDailyRange(ctx, "de-all", dayStart, dayStart)
	if len(recs) != 1 || recs[0].Totals.GenFuelCost != 0 {
		t.Fatalf("stale group record served: %+v", recs)
	}
}
