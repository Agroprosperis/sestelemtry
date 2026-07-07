package economics

import (
	"math"
	"testing"
	"time"
)

const degradation = 0.6

// flatTariffs zeroes every per-kWh add-on so RDN == import/export price,
// keeping the cost-basis arithmetic in tests easy to follow. Mirrors the
// FLAT_TARIFFS preset in the TS suite.
var flatTariffs = Tariffs{
	DistributionUahPerKwh:   0,
	TransmissionUahPerKwh:   0,
	SupplierMarginUahPerKwh: 0,
	OtherFeesUahPerKwh:      0,
	DegradationUahPerKwh:    0,
	ExportDiscount:          0,
	IncludeVat:              false,
	VatRate:                 0.2,
	EssCapacityKwh:          200,
}

func ptr(v float64) *float64 { return &v }

func near(t *testing.T, name string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-6 {
		t.Errorf("%s: got %v, want %v", name, got, want)
	}
}

func chargeFlow(kwh float64) HourFlows {
	return HourFlows{GridToEss: kwh, EssCharged: kwh}
}

func TestRollHourWeightedAverage(t *testing.T) {
	after1 := RollHour(EssState{}, HourFlows{GridToEss: 100, EssCharged: 100}, 1, 0, degradation)
	near(t, "after1.kwh", after1.Next.Kwh, 100)
	near(t, "after1.uah", after1.Next.Uah, 100)
	near(t, "after1.avgEnd", after1.AvgCostEnd, 1)

	after2 := RollHour(after1.Next, HourFlows{GridToEss: 100, EssCharged: 100}, 3, 0, degradation)
	near(t, "after2.kwh", after2.Next.Kwh, 200)
	near(t, "after2.uah", after2.Next.Uah, 400)
	near(t, "after2.avgEnd", after2.AvgCostEnd, 2)

	after3 := RollHour(after2.Next, HourFlows{EssDischarged: 50, EssToLoad: 50}, 5, 0, degradation)
	near(t, "after3.withdrawn", after3.WithdrawnCostUah, 100)
	near(t, "after3.kwh", after3.Next.Kwh, 150)
	near(t, "after3.uah", after3.Next.Uah, 300)
	near(t, "after3.avgEnd", after3.AvgCostEnd, 2)
}

func TestRollHourPvIsFree(t *testing.T) {
	after1 := RollHour(EssState{}, HourFlows{GridToEss: 100, EssCharged: 100}, 4, 0, degradation)
	near(t, "avgEnd", after1.AvgCostEnd, 4)
	after2 := RollHour(after1.Next, HourFlows{PVToEss: 100, EssCharged: 100}, 4, 0, degradation)
	near(t, "kwh", after2.Next.Kwh, 200)
	near(t, "uah", after2.Next.Uah, 400)
	near(t, "avgEnd", after2.AvgCostEnd, 2)
}

func TestRollHourRealizedProfitPvOnly(t *testing.T) {
	charged := RollHour(EssState{}, HourFlows{PVToEss: 100, EssCharged: 100}, 4, 0, degradation)
	near(t, "kwh", charged.Next.Kwh, 100)
	near(t, "uah", charged.Next.Uah, 0)
	discharged := RollHour(charged.Next, HourFlows{EssDischarged: 50, EssToLoad: 50}, 8, 0, degradation)
	near(t, "withdrawn", discharged.WithdrawnCostUah, 0)
	near(t, "realized", discharged.RealizedProfitUah, 370)
}

func TestRollHourRealizedProfitIdentity(t *testing.T) {
	s := EssState{}
	s = RollHour(s, HourFlows{GridToEss: 50, EssCharged: 50}, 2, 0, degradation).Next
	s = RollHour(s, HourFlows{PVToEss: 50, EssCharged: 50}, 2, 0, degradation).Next
	near(t, "avg", s.Uah/s.Kwh, 1)
	out := RollHour(s, HourFlows{EssDischarged: 50, EssToLoad: 30, EssToGrid: 20}, 8, 6, degradation)
	expectedRevenue := 30.0*8 + 20.0*6
	expectedWithdrawn := 50.0 * 1
	expectedDegradation := 50.0 * degradation
	near(t, "withdrawn", out.WithdrawnCostUah, expectedWithdrawn)
	near(t, "realized", out.RealizedProfitUah, expectedRevenue-expectedWithdrawn-expectedDegradation)
}

func TestRollHourCarriesAcrossDay(t *testing.T) {
	out := RollHour(EssState{Kwh: 50, Uah: 75}, HourFlows{EssDischarged: 50, EssToGrid: 50}, 0, 8, degradation)
	near(t, "realized", out.RealizedProfitUah, 295)
	near(t, "kwh", out.Next.Kwh, 0)
	near(t, "uah", out.Next.Uah, 0)
}

func TestRollHourClampsDrained(t *testing.T) {
	out := RollHour(EssState{Kwh: 10.0001, Uah: 20}, HourFlows{EssDischarged: 12, EssToLoad: 12}, 5, 0, 0)
	if out.Next.Kwh != 0 || out.Next.Uah != 0 {
		t.Errorf("expected zero state, got %+v", out.Next)
	}
}

func TestRollHourNoop(t *testing.T) {
	s := EssState{Kwh: 30, Uah: 60}
	out := RollHour(s, HourFlows{}, 8, 6, degradation)
	if out.Next != s {
		t.Errorf("expected unchanged state, got %+v", out.Next)
	}
	near(t, "withdrawn", out.WithdrawnCostUah, 0)
	near(t, "realized", out.RealizedProfitUah, 0)
}

func TestRollHourStartAvgFromPrev(t *testing.T) {
	out := RollHour(EssState{Kwh: 100, Uah: 200}, HourFlows{GridToEss: 100, EssCharged: 100}, 0, 0, degradation)
	near(t, "avgStart", out.AvgCostStart, 2)
	near(t, "avgEnd", out.AvgCostEnd, 1)
}

// --- findAnchorAndPreRoll parity ---

func makeHistory(overrides map[int]hourHistoryRecord) []hourHistoryRecord {
	out := make([]hourHistoryRecord, historyHours)
	for i := range out {
		if rec, ok := overrides[i]; ok {
			out[i] = rec
		}
	}
	return out
}

func TestFindAnchorMostRecentDrop(t *testing.T) {
	history := makeHistory(map[int]hourHistoryRecord{
		30: {socPercentStart: ptr(8)},
		31: {flow: chargeFlow(100), rdnUahPerKwh: ptr(2)},
	})
	state := findAnchorAndPreRoll(history, nil, flatTariffs)
	// usable(8) = clamp((8-10)/80,0,1)*200 = 0, then +100 grid charge.
	near(t, "kwh", state.Kwh, 100)
	near(t, "uah", state.Uah, 200)
}

func TestFindAnchorFallbackEarliestSoc(t *testing.T) {
	history := makeHistory(map[int]hourHistoryRecord{
		0:  {socPercentStart: ptr(50)},
		24: {socPercentStart: ptr(35)},
		47: {socPercentStart: ptr(60)},
	})
	state := findAnchorAndPreRoll(history, nil, flatTariffs)
	near(t, "kwh", state.Kwh, 100)
	near(t, "uah", state.Uah, 0)
}

func TestFindAnchorRollsAfterPseudoAnchor(t *testing.T) {
	history := makeHistory(map[int]hourHistoryRecord{
		0:  {socPercentStart: ptr(50)},
		10: {flow: chargeFlow(100), rdnUahPerKwh: ptr(2)},
	})
	state := findAnchorAndPreRoll(history, nil, flatTariffs)
	near(t, "kwh", state.Kwh, 200)
	near(t, "uah", state.Uah, 200)
}

func TestFindAnchorNoSocReturnsZero(t *testing.T) {
	state := findAnchorAndPreRoll(makeHistory(nil), nil, flatTariffs)
	if state.Kwh != 0 || state.Uah != 0 {
		t.Errorf("expected zero state, got %+v", state)
	}
}

func TestFindAnchorMidWindowSeed(t *testing.T) {
	history := makeHistory(map[int]hourHistoryRecord{
		20: {socPercentStart: ptr(60)},
		25: {flow: chargeFlow(50), rdnUahPerKwh: ptr(4)},
	})
	state := findAnchorAndPreRoll(history, nil, flatTariffs)
	// usable(60) = (60-10)/80*200 = 125, then +50 grid charge.
	near(t, "kwh", state.Kwh, 175)
	near(t, "uah", state.Uah, 200)
}

func TestFindAnchorTodayHour0LowSoc(t *testing.T) {
	history := makeHistory(map[int]hourHistoryRecord{
		30: {socPercentStart: ptr(5)},
		31: {flow: chargeFlow(100), rdnUahPerKwh: ptr(2)},
	})
	state := findAnchorAndPreRoll(history, ptr(9), flatTariffs)
	// today hour-0 SOC 9% ≤ reset threshold; usable(9) clamps to 0.
	near(t, "kwh", state.Kwh, 0)
	near(t, "uah", state.Uah, 0)
}

func TestFindAnchorRollsAll47(t *testing.T) {
	history := makeHistory(map[int]hourHistoryRecord{
		0:  {socPercentStart: ptr(7)},
		10: {flow: chargeFlow(50), rdnUahPerKwh: ptr(4)},
	})
	state := findAnchorAndPreRoll(history, nil, flatTariffs)
	// usable(7) clamps to 0, then +50 grid charge.
	near(t, "kwh", state.Kwh, 50)
	near(t, "uah", state.Uah, 200)
}

func TestFindAnchorLastHour(t *testing.T) {
	history := makeHistory(map[int]hourHistoryRecord{47: {socPercentStart: ptr(6)}})
	state := findAnchorAndPreRoll(history, nil, flatTariffs)
	// usable(6) clamps to 0; no roll after the last hour.
	near(t, "kwh", state.Kwh, 0)
	near(t, "uah", state.Uah, 0)
}

func TestFindAnchorSkipsNullRdn(t *testing.T) {
	history := makeHistory(map[int]hourHistoryRecord{
		30: {socPercentStart: ptr(5)},
		31: {flow: chargeFlow(100), rdnUahPerKwh: nil},
		32: {flow: chargeFlow(100), rdnUahPerKwh: ptr(3)},
	})
	state := findAnchorAndPreRoll(history, nil, flatTariffs)
	// usable(5) clamps to 0; null-RDN hour skipped, then +100 grid charge.
	near(t, "kwh", state.Kwh, 100)
	near(t, "uah", state.Uah, 300)
}

func TestFindAnchorPicksLatestDrop(t *testing.T) {
	history := makeHistory(map[int]hourHistoryRecord{
		5:  {socPercentStart: ptr(8)},
		6:  {flow: chargeFlow(100), rdnUahPerKwh: ptr(2)},
		30: {socPercentStart: ptr(4)},
	})
	state := findAnchorAndPreRoll(history, nil, flatTariffs)
	// latest drop (idx30, soc 4%) anchors; usable(4) clamps to 0.
	near(t, "kwh", state.Kwh, 0)
	near(t, "uah", state.Uah, 0)
}

// --- hourEconomics / derived flows ---

func TestHourEconomicsBasic(t *testing.T) {
	flow := HourFlows{PV: 10, GridImport: 5, GridExport: 2, EssToLoad: 0}
	econ := HourEconomicsFor(2, flow, flatTariffs)
	// flat tariffs: importPrice = exportPrice = rdn = 2.
	near(t, "importPrice", econ.ImportPrice, 2)
	near(t, "exportPrice", econ.ExportPrice, 2)
	// load = pv + import - export = 13; pvToGrid = 2; pvToLoad = 8; gridToLoad = 5.
	near(t, "load", econ.Load, 13)
	near(t, "pvToGrid", econ.PVToGrid, 2)
	near(t, "pvToLoad", econ.PVToLoad, 8)
	near(t, "gridToLoad", econ.GridToLoad, 5)
	near(t, "baseline", econ.BaselineCost, 26)
	near(t, "actual", econ.ActualCost, 5*2-2*2)
}

func TestHourEconomicsVat(t *testing.T) {
	tar := flatTariffs
	tar.IncludeVat = true
	tar.VatRate = 0.2
	tar.DistributionUahPerKwh = 1
	econ := HourEconomicsFor(2, HourFlows{}, tar)
	near(t, "importPrice", econ.ImportPrice, (2+1)*1.2)
	near(t, "exportPrice", econ.ExportPrice, 2*1.2)
}

// --- tariff schedule resolution ---

func TestResolveForDayCivilDate(t *testing.T) {
	kyiv, err := time.LoadLocation("Europe/Kyiv")
	if err != nil {
		t.Skip("tzdata unavailable")
	}
	mk := func(s string) time.Time {
		d, _ := time.Parse("2006-01-02", s)
		return d
	}
	sched := Schedule{
		{EffectiveFrom: mk("1970-01-01"), Tariffs: Tariffs{DistributionUahPerKwh: 1}},
		{EffectiveFrom: mk("2026-04-01"), Tariffs: Tariffs{DistributionUahPerKwh: 2}},
		{EffectiveFrom: mk("2026-05-01"), Tariffs: Tariffs{DistributionUahPerKwh: 3}},
	}
	// Local midnight on the same civil date as the effective_from must
	// resolve to that version (the tz-pitfall case).
	day := time.Date(2026, 4, 1, 0, 0, 0, 0, kyiv)
	got, ok := sched.ResolveForDay(day)
	if !ok || got.DistributionUahPerKwh != 2 {
		t.Errorf("2026-04-01: got %v ok=%v, want 2", got.DistributionUahPerKwh, ok)
	}
	day = time.Date(2026, 3, 31, 0, 0, 0, 0, kyiv)
	got, _ = sched.ResolveForDay(day)
	if got.DistributionUahPerKwh != 1 {
		t.Errorf("2026-03-31: got %v, want 1", got.DistributionUahPerKwh)
	}
	day = time.Date(2026, 6, 1, 0, 0, 0, 0, kyiv)
	got, _ = sched.ResolveForDay(day)
	if got.DistributionUahPerKwh != 3 {
		t.Errorf("2026-06-01: got %v, want 3", got.DistributionUahPerKwh)
	}
}

func TestEssFlowsFromCountersUsesMeteredMagnitudes(t *testing.T) {
	flow := essFlowsFromCounters(
		FlowRow{EssCharged: 200, EssDischarged: 100, PVToEss: 50, GridToEss: 150, EssToLoad: 80, EssToGrid: 20},
		true, 148,
		true, 95,
		10, 5, 0,
	)
	near(t, "essCharged", flow.EssCharged, 148)
	near(t, "essDischarged", flow.EssDischarged, 95)
	near(t, "pvToEss", flow.PVToEss, 50*(148.0/200.0))
	near(t, "gridToEss", flow.GridToEss, 150*(148.0/200.0))
}

func TestRebalanceDailyLoadRemovesPhantomLoad(t *testing.T) {
	rdn := 10.0
	rows := []*HourRow{
		{
			Rdn: &rdn,
			Flow: HourFlows{EssCharged: 132},
			Econ: HourEconomics{Load: 0, BaselineCost: 0, ActualCost: 0, Effect: 0},
		},
		{
			Rdn: &rdn,
			Flow: HourFlows{PV: 100, GridImport: 50, EssDischarged: 50},
			Econ: HourEconomics{Load: 200, PVToLoad: 120, GridToLoad: 80, BaselineCost: 2000, ActualCost: 500, Effect: 1500},
		},
	}
	if !rebalanceDailyLoad(rows) {
		t.Fatal("expected load rebalance to apply")
	}
	// balanced load = 100+50+50 - 132 = 68
	var totalLoad float64
	for _, row := range rows {
		totalLoad += row.Econ.Load
	}
	near(t, "dailyLoad", totalLoad, 68)
	near(t, "hour1Load", rows[1].Econ.Load, 68)
	near(t, "hour1Effect", rows[1].Econ.Effect, 680-500)
}

func TestRebalanceDailyLoadNoOpWhenBalanced(t *testing.T) {
	rdn := 5.0
	rows := []*HourRow{{
		Rdn:  &rdn,
		Flow: HourFlows{PV: 10, GridImport: 5},
		Econ: HourEconomics{Load: 15, BaselineCost: 75, ActualCost: 25, Effect: 50},
	}}
	if rebalanceDailyLoad(rows) {
		t.Fatal("balanced day should not rebalance")
	}
}
