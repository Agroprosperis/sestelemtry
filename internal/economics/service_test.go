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
	svc := NewService(b)
	// First read computes + stores a final (past) day.
	if _, err := svc.GetDay(context.Background(), "org1", "2026-04-01", "Europe/Kyiv"); err != nil {
		t.Fatalf("GetDay: %v", err)
	}
	if b.saveCount != 1 {
		t.Fatalf("first read should compute+save once, got %d", b.saveCount)
	}
	flowsAfterFirst := b.flowsCalls
	// Second read of a final day must serve cache (no recompute → no new
	// flow gathering, no new save).
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
