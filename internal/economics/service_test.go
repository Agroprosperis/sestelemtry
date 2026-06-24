package economics

import (
	"context"
	"testing"
	"time"
)

// fakeBackend is an in-memory Backend for exercising the Service's
// gather → assemble → persist → read-through flow without pgx / HTTP.
type fakeBackend struct {
	flows    map[string][]FlowRow // keyed by YYYY-MM-DD
	deltas   []Point
	soc      []Point
	dam      []DAMHour
	schedule Schedule

	canonical map[string]CanonicalDaily // keyed by YYYY-MM-DD

	saved      map[string]StoredDay // keyed by YYYY-MM-DD
	saveCount  int
	flowsCalls int
}

func (b *fakeBackend) HourlyFlows(_ context.Context, _ string, dayStart time.Time) ([]FlowRow, error) {
	b.flowsCalls++
	key := dayStart.Format("2006-01-02")
	if rows, ok := b.flows[key]; ok {
		return rows, nil
	}
	// Default: 24 empty hours anchored at dayStart.
	out := make([]FlowRow, 24)
	for h := 0; h < 24; h++ {
		out[h] = FlowRow{From: dayStart.Add(time.Duration(h) * time.Hour)}
	}
	return out, nil
}

func (b *fakeBackend) Timeseries(_ context.Context, _ string, keys []string, from, to time.Time, _, _, agg string) ([]Point, error) {
	if agg == "last" {
		return b.soc, nil
	}
	return b.deltas, nil
}

func (b *fakeBackend) DAMPrices(_ context.Context, _ int, _, _ time.Time) ([]DAMHour, error) {
	return b.dam, nil
}

func (b *fakeBackend) TariffSchedule(_ context.Context, _ string) (Schedule, error) {
	return b.schedule, nil
}

func (b *fakeBackend) CanonicalDaily(_ context.Context, _ string, day time.Time) (CanonicalDaily, bool, error) {
	if b.canonical == nil {
		return CanonicalDaily{}, false, nil
	}
	c, ok := b.canonical[day.Format("2006-01-02")]
	return c, ok, nil
}

func (b *fakeBackend) SaveDay(_ context.Context, day StoredDay) error {
	if b.saved == nil {
		b.saved = map[string]StoredDay{}
	}
	b.saved[day.Day.Format("2006-01-02")] = day
	b.saveCount++
	return nil
}

func (b *fakeBackend) LoadDay(_ context.Context, _ string, dayStart time.Time, _ string) (StoredDay, bool, error) {
	if b.saved == nil {
		return StoredDay{}, false, nil
	}
	d, ok := b.saved[dayStart.Format("2006-01-02")]
	return d, ok, nil
}

func (b *fakeBackend) LoadDailyRange(_ context.Context, _ string, from, to time.Time) ([]DailyRecord, error) {
	var out []DailyRecord
	for day := from; !day.After(to); day = day.AddDate(0, 0, 1) {
		d, ok := b.saved[day.Format("2006-01-02")]
		if !ok {
			continue
		}
		out = append(out, DailyRecord{
			Day:        d.Day,
			Totals:     d.Totals,
			IsFinal:    d.IsFinal,
			ComputedAt: d.ComputedAt,
		})
	}
	return out, nil
}

func (b *fakeBackend) LoadHourlyRange(_ context.Context, _ string, from, to time.Time) ([]HourlyRecord, error) {
	var out []HourlyRecord
	for _, d := range b.saved {
		for _, r := range d.Rows {
			if r == nil {
				continue
			}
			if r.HourStart.Before(from) || !r.HourStart.Before(to) {
				continue
			}
			out = append(out, HourlyRecord{
				HourStart:     r.HourStart,
				Rdn:           r.Rdn,
				GridImport:    r.Flow.GridImport,
				EssNet:        r.Econ.EssNet,
				EssDischarged: r.Flow.EssDischarged,
			})
		}
	}
	return out, nil
}

func newKyivBackend(t *testing.T) (*fakeBackend, *time.Location) {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Kyiv")
	if err != nil {
		t.Skip("tzdata unavailable")
	}
	return &fakeBackend{
		flows:  map[string][]FlowRow{},
		schedule: Schedule{{
			EffectiveFrom: mustDate("1970-01-01"),
			Tariffs:       flatTariffs,
		}},
	}, loc
}

func mustDate(s string) time.Time {
	d, _ := time.Parse("2006-01-02", s)
	return d
}

// fullDayDAM returns 24 priced DAM hours (zone 2) for the given delivery
// date so a computed day has no missing-price hours.
func fullDayDAM(date string) []DAMHour {
	d := mustDate(date)
	out := make([]DAMHour, 0, 24)
	for h := 1; h <= 24; h++ {
		out = append(out, DAMHour{DeliveryDate: d, Hour: h, Zone: 2, PriceUAHPerMWh: ptr(2000)})
	}
	return out
}

func TestServiceComputeDayPersists(t *testing.T) {
	b, _ := newKyivBackend(t)
	// One hour with PV + DAM price so the row is priced.
	b.dam = []DAMHour{{DeliveryDate: mustDate("2026-04-01"), Hour: 13, Zone: 2, PriceUAHPerMWh: ptr(2000)}}
	svc := NewService(b)
	day, err := svc.ComputeDay(context.Background(), "org1", "2026-04-01", "Europe/Kyiv")
	if err != nil {
		t.Fatalf("ComputeDay: %v", err)
	}
	if len(day.Rows) != 24 {
		t.Fatalf("expected 24 rows, got %d", len(day.Rows))
	}
	if !day.IsFinal {
		t.Errorf("a fully-past day should be final")
	}
	if b.saveCount != 1 {
		t.Errorf("expected one SaveDay, got %d", b.saveCount)
	}
	// Hour 12 (index) corresponds to DAM hour 13 → priced.
	if day.Rows[12] == nil || day.Rows[12].Rdn == nil {
		t.Errorf("hour 12 should be priced")
	}
}

func TestServiceComputeDayReconciles(t *testing.T) {
	b, loc := newKyivBackend(t)
	dayStart := time.Date(2026, 4, 1, 0, 0, 0, 0, loc)
	// Priced hour 13 (index 12) with PV + import deltas in that hour.
	b.dam = []DAMHour{{DeliveryDate: mustDate("2026-04-01"), Hour: 13, Zone: 2, PriceUAHPerMWh: ptr(2000)}}
	b.deltas = []Point{
		{Time: dayStart.Add(12*time.Hour + 5*time.Minute), MetricKey: "accumulated_pv_energy_yield_kwh", Value: 10},
		{Time: dayStart.Add(12*time.Hour + 5*time.Minute), MetricKey: "accumulated_electricity_purchased_kwh", Value: 5},
	}
	b.canonical = map[string]CanonicalDaily{
		"2026-04-01": {PV: 100, GridImport: 50, Load: 0},
	}
	svc := NewService(b)
	day, err := svc.ComputeDay(context.Background(), "org1", "2026-04-01", "Europe/Kyiv")
	if err != nil {
		t.Fatalf("ComputeDay: %v", err)
	}
	if !day.Totals.Reconciled {
		t.Fatal("day should be reconciled when canonical present")
	}
	near(t, "daily PV scaled to canonical", day.Totals.PV, 100)
	near(t, "daily import scaled to canonical", day.Totals.GridImport, 50)
}

func TestServiceComputeDayNoCanonical(t *testing.T) {
	b, _ := newKyivBackend(t)
	b.dam = []DAMHour{{DeliveryDate: mustDate("2026-04-01"), Hour: 13, Zone: 2, PriceUAHPerMWh: ptr(2000)}}
	svc := NewService(b)
	day, err := svc.ComputeDay(context.Background(), "org1", "2026-04-01", "Europe/Kyiv")
	if err != nil {
		t.Fatalf("ComputeDay: %v", err)
	}
	if day.Totals.Reconciled {
		t.Error("day should not be reconciled without canonical KPIs")
	}
}

func TestServiceGetDayReadThroughFinal(t *testing.T) {
	b, _ := newKyivBackend(t)
	// A fully-priced past day: all 24 hours have a DAM price so the
	// stored day has no missing-price hours and is eligible for the
	// immutable-final cache fast path.
	b.dam = fullDayDAM("2026-04-01")
	svc := NewService(b)
	// First read computes + stores a final (past) day.
	if _, err := svc.GetDay(context.Background(), "org1", "2026-04-01", "Europe/Kyiv"); err != nil {
		t.Fatalf("GetDay: %v", err)
	}
	if b.saveCount != 1 {
		t.Fatalf("first read should compute+save once, got %d", b.saveCount)
	}
	flowsAfterFirst := b.flowsCalls
	// Second read of a final, fully-priced day must serve cache (no
	// recompute → no new flow gathering, no new save).
	if _, err := svc.GetDay(context.Background(), "org1", "2026-04-01", "Europe/Kyiv"); err != nil {
		t.Fatalf("GetDay 2: %v", err)
	}
	if b.saveCount != 1 {
		t.Errorf("final day should be cache-served, save count grew to %d", b.saveCount)
	}
	if b.flowsCalls != flowsAfterFirst {
		t.Errorf("final day should not re-gather flows")
	}
}

// TestServiceGetDayFinalMissingPriceRecomputes covers the self-heal
// path: a final day stored with missing DAM prices (the scheduled fetch
// hadn't landed when it was first computed) must NOT be served from the
// immutable-final cache — a second read recomputes so prices ingested in
// the meantime (late OREE publication / collector backfill) are picked up.
func TestServiceGetDayFinalMissingPriceRecomputes(t *testing.T) {
	b, _ := newKyivBackend(t)
	svc := NewService(b)
	// First read computes a past day with no DAM prices → final but with
	// missing-price hours.
	first, err := svc.GetDay(context.Background(), "org1", "2026-04-01", "Europe/Kyiv")
	if err != nil {
		t.Fatalf("GetDay: %v", err)
	}
	if !first.IsFinal {
		t.Fatalf("a fully-past day should be final")
	}
	if first.Totals.HoursMissingPrice == 0 {
		t.Fatalf("expected missing-price hours for an unpriced day")
	}
	if b.saveCount != 1 {
		t.Fatalf("first read should compute+save once, got %d", b.saveCount)
	}
	// Second read must recompute (not serve the missing-price cache).
	if _, err := svc.GetDay(context.Background(), "org1", "2026-04-01", "Europe/Kyiv"); err != nil {
		t.Fatalf("GetDay 2: %v", err)
	}
	if b.saveCount != 2 {
		t.Errorf("final day with missing prices should recompute on read, save count %d", b.saveCount)
	}
}

// TestServiceGetDayFreshTodayWindow covers the read-through behaviour
// for a still-open (non-final) day that the economics-recompute daemon
// keeps warm: with no window it always recomputes; with a window it
// serves a recent cache but recomputes a stale one.
func TestServiceGetDayFreshTodayWindow(t *testing.T) {
	b, loc := newKyivBackend(t)
	svc := NewService(b)
	now := time.Now().In(loc)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	todayStr := today.Format("2006-01-02")

	seed := func(computedAt time.Time) {
		b.saved = map[string]StoredDay{
			todayStr: {
				OrganizationID: "org1",
				Day:            today,
				Tz:             loc.String(),
				IsFinal:        false,
				ComputedAt:     computedAt,
			},
		}
		b.saveCount = 0
	}

	// 1. Default (window 0): a non-final day always recomputes on read.
	seed(now)
	if _, err := svc.GetDay(context.Background(), "org1", todayStr, "Europe/Kyiv"); err != nil {
		t.Fatalf("GetDay: %v", err)
	}
	if b.saveCount == 0 {
		t.Error("with no fresh window, a non-final day must recompute (save)")
	}

	// 2. Fresh window + recently-written cache: serve cache, no recompute.
	svc.SetFreshTodayWindow(time.Hour)
	seed(now)
	if _, err := svc.GetDay(context.Background(), "org1", todayStr, "Europe/Kyiv"); err != nil {
		t.Fatalf("GetDay: %v", err)
	}
	if b.saveCount != 0 {
		t.Errorf("fresh non-final day should be cache-served, got %d saves", b.saveCount)
	}

	// 3. Fresh window + stale cache: fall back to recompute.
	seed(now.Add(-2 * time.Hour))
	if _, err := svc.GetDay(context.Background(), "org1", todayStr, "Europe/Kyiv"); err != nil {
		t.Fatalf("GetDay: %v", err)
	}
	if b.saveCount == 0 {
		t.Error("stale non-final cache should recompute (save)")
	}
}

func TestServiceRecomputeRange(t *testing.T) {
	b, _ := newKyivBackend(t)
	svc := NewService(b)
	var progress int
	res, err := svc.RecomputeRange(context.Background(), "org1", "2026-04-01", "2026-04-03", "Europe/Kyiv", func(done, total int, label string) {
		progress = done
		_ = total
		_ = label
	})
	if err != nil {
		t.Fatalf("RecomputeRange: %v", err)
	}
	if res.Days != 3 || res.DaysOK != 3 || res.DaysFailed != 0 {
		t.Errorf("unexpected result: %+v", res)
	}
	if progress != 3 {
		t.Errorf("expected final progress 3, got %d", progress)
	}
	if b.saveCount != 3 {
		t.Errorf("expected 3 saves, got %d", b.saveCount)
	}
}

func TestServiceRecomputeRangeCancel(t *testing.T) {
	b, _ := newKyivBackend(t)
	svc := NewService(b)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := svc.RecomputeRange(ctx, "org1", "2026-04-01", "2026-04-03", "Europe/Kyiv", nil)
	if err == nil {
		t.Errorf("expected cancellation error")
	}
}
