package economics

import (
	"math"
	"testing"
	"time"
)

// TestOptimizeDayArbitrage checks the classic charge-cheap / discharge-
// expensive case: with a 1-unit hour followed by a 10-unit hour, the
// optimizer should buy a full power-step in the cheap hour and sell it
// into load in the expensive one.
func TestOptimizeDayArbitrage(t *testing.T) {
	p := optimumParams{
		capacityKwh:          100,
		degradationUahPerKwh: 0,
		maxChargeKwh:         50,
		maxDischargeKwh:      50,
		socMinKwh:            0,
		socMaxKwh:            100,
		rte:                  1.0,
	}
	hours := make([]optimumHour, 24)
	hours[0] = optimumHour{tradable: true, importPrice: 1, exportPrice: 1, displaceableKwh: 100}
	hours[1] = optimumHour{tradable: true, importPrice: 10, exportPrice: 10, displaceableKwh: 100}

	got := optimizeDay(hours, 0, p, modeFull)
	// Charge 50 @1 (cost 50), discharge 50 to load @10 (revenue 500) → 450.
	if math.Abs(got-450) > 5 {
		t.Fatalf("optimizeDay = %v, want ~450", got)
	}
}

// TestOptimizeDayNoSpread: a flat price profile offers no arbitrage, so
// the optimum effect is zero (degradation would only make trading worse).
func TestOptimizeDayNoSpread(t *testing.T) {
	p := optimumParams{
		capacityKwh: 100, degradationUahPerKwh: 0.6,
		maxChargeKwh: 50, maxDischargeKwh: 50,
		socMinKwh: 0, socMaxKwh: 100, rte: 0.9,
	}
	hours := make([]optimumHour, 24)
	for i := range hours {
		hours[i] = optimumHour{tradable: true, importPrice: 5, exportPrice: 5, displaceableKwh: 100}
	}
	if got := optimizeDay(hours, 0, p, modeFull); got > 1e-6 {
		t.Fatalf("optimizeDay flat = %v, want ~0", got)
	}
}

// TestAggregateMonthOptimum exercises the full month path: with a cheap
// night hour and an expensive evening hour, a poorly-timed factual day
// should yield optimum ≫ fact, a positive reserve, and captured ≤ 1.
func TestAggregateMonthOptimum(t *testing.T) {
	loc := time.UTC
	day := time.Date(2026, 6, 1, 0, 0, 0, 0, loc)

	days := []DailyRecord{{
		Day:     day,
		IsFinal: true,
		Totals:  DailyTotals{EssNet: 10, EssDischarged: 40, HoursWithData: 24},
	}}

	hourly := make([]HourlyRecord, 0, 24)
	for h := 0; h < 24; h++ {
		hourly = append(hourly, HourlyRecord{HourStart: day.Add(time.Duration(h) * time.Hour)})
	}
	// Night: cheap, battery demonstrates a 40 kWh grid charge from empty.
	hourly[3] = HourlyRecord{
		HourStart: day.Add(3 * time.Hour), Rdn: floatPtr(1), ImportPrice: 1, ExportPrice: 1,
		GridToEss: 40, EssCharged: 40, EssRemainingKwhStart: floatPtr(0),
	}
	// Evening peak: expensive, demonstrates a 40 kWh discharge into load.
	hourly[19] = HourlyRecord{
		HourStart: day.Add(19 * time.Hour), Rdn: floatPtr(20), ImportPrice: 20, ExportPrice: 18,
		GridToLoad: 100, EssDischarged: 40, EssNet: 10, EssRemainingKwhStart: floatPtr(40),
	}

	got := AggregateMonth("2026-06", loc, days, hourly, 100, 0, 0, 0)

	// Optimum ≈ charge 40 @1 (−40) then discharge 40 to load @20 (+800) = 760.
	if got.Totals.EssOptimum < 700 {
		t.Fatalf("EssOptimum = %v, want ~760", got.Totals.EssOptimum)
	}
	if got.Totals.EssReserve <= 0 {
		t.Fatalf("EssReserve = %v, want > 0", got.Totals.EssReserve)
	}
	if math.Abs(got.Totals.EssReserve-(got.Totals.EssOptimum-got.Totals.EssFact)) > 1e-6 {
		t.Fatalf("EssReserve %v != optimum %v − fact %v", got.Totals.EssReserve, got.Totals.EssOptimum, got.Totals.EssFact)
	}
	// The three reasons must add up to the reserve.
	reasonsSum := got.Totals.EssReserveTiming + got.Totals.EssReserveSoc + got.Totals.EssReservePv
	if math.Abs(reasonsSum-got.Totals.EssReserve) > 1e-6 {
		t.Fatalf("reasons %v (t=%v s=%v p=%v) != reserve %v", reasonsSum,
			got.Totals.EssReserveTiming, got.Totals.EssReserveSoc, got.Totals.EssReservePv, got.Totals.EssReserve)
	}
	if got.Totals.EssCapturedShare <= 0 || got.Totals.EssCapturedShare > 1 {
		t.Fatalf("EssCapturedShare = %v, want (0,1]", got.Totals.EssCapturedShare)
	}
	if len(got.Days) != 1 || got.Days[0].EssOptimum < got.Days[0].Totals.EssNet {
		t.Fatalf("per-day optimum %+v must be ≥ fact", got.Days)
	}
}

// TestDetectEssAnomalies flags an hour whose hourly charge exceeds the
// power limit × tolerance and reports it in the DataQuality summary
// without marking sibling hours of the same day.
func TestDetectEssAnomalies(t *testing.T) {
	loc := time.UTC
	base := time.Date(2026, 6, 1, 0, 0, 0, 0, loc)
	badHour := base.Add(24*time.Hour + 5*time.Hour)
	hourly := []HourlyRecord{
		{HourStart: base.Add(3 * time.Hour), EssCharged: 40, EssDischarged: 0},
		{HourStart: badHour, EssCharged: 400, EssDischarged: 0}, // day 2 hour 5: impossible
		{HourStart: base.Add(24*time.Hour + 6*time.Hour), EssCharged: 40, EssDischarged: 0},
	}
	bad, dq := detectEssAnomalies(hourly, loc, 100, essAnomalyTolerance) // limit 150
	if len(bad) != 1 || !bad[badHour.Unix()] {
		t.Fatalf("bad = %v, want {%d}", bad, badHour.Unix())
	}
	if dq.DataOK || dq.AnomalousHours != 1 || dq.AnomalousDays != 1 {
		t.Fatalf("dq = %+v, want 1 anomalous hour / 1 day, not ok", dq)
	}
	if dq.MaxChargeKwhPerInterval != 400 {
		t.Fatalf("MaxChargeKwhPerInterval = %v, want 400", dq.MaxChargeKwhPerInterval)
	}
	if len(dq.Anomalies) != 1 || len(dq.Anomalies[0].Reasons) == 0 {
		t.Fatalf("Anomalies = %+v, want 1 hour with reasons", dq.Anomalies)
	}
	if dq.ReasonCounts[AnomalyReasonHourlyOverLimit] != 1 {
		t.Fatalf("ReasonCounts = %v, want hourly_over_limit=1", dq.ReasonCounts)
	}
	// Disabled filter (limit ≤ 0) excludes nothing.
	if b2, dq2 := detectEssAnomalies(hourly, loc, 0, essAnomalyTolerance); len(b2) != 0 || !dq2.DataOK {
		t.Fatalf("disabled filter excluded hours: %v / %+v", b2, dq2)
	}
}

// TestDetectEssAnomaliesPeakInterval verifies the sub-hourly path: an hour
// whose hourly charge/discharge sums stay UNDER the limit but whose
// per-interval peak power (EssPeakIntervalKw) exceeds it is still flagged
// (the 5-minute spike the hourly sum averages away). Sibling hours of the
// same day stay clean.
func TestDetectEssAnomaliesPeakInterval(t *testing.T) {
	loc := time.UTC
	base := time.Date(2026, 6, 1, 0, 0, 0, 0, loc)
	spikeHour := base.Add(10 * time.Hour)
	hourly := []HourlyRecord{
		// Hour 10: modest hourly sum (40 < 150) but a 200 kW 5-min spike.
		{HourStart: spikeHour, EssCharged: 40, EssPeakIntervalKw: 200},
		// Same day, later hour: modest sum and a peak within the limit.
		{HourStart: base.Add(11 * time.Hour), EssCharged: 40, EssPeakIntervalKw: 100},
		// Next day: modest hourly sum and a peak within the limit → clean.
		{HourStart: base.Add(24*time.Hour + 10*time.Hour), EssDischarged: 40, EssPeakIntervalKw: 100},
	}
	bad, dq := detectEssAnomalies(hourly, loc, 100, essAnomalyTolerance) // limit 150
	if len(bad) != 1 || !bad[spikeHour.Unix()] {
		t.Fatalf("bad = %v, want {%d}", bad, spikeHour.Unix())
	}
	if dq.DataOK || dq.AnomalousHours != 1 || dq.AnomalousDays != 1 {
		t.Fatalf("dq = %+v, want 1 anomalous hour / 1 day, not ok", dq)
	}
	if dq.MaxIntervalPowerKw != 200 {
		t.Fatalf("MaxIntervalPowerKw = %v, want 200", dq.MaxIntervalPowerKw)
	}
	if dq.ReasonCounts[AnomalyReasonPeakSpike] != 1 {
		t.Fatalf("ReasonCounts = %v, want peak_spike=1", dq.ReasonCounts)
	}
}

// TestDetectEssAnomaliesAfterGap tags a peak spike later the same day as a
// multi-hour hole (connection-loss pattern), not only the first hour back.
func TestDetectEssAnomaliesAfterGap(t *testing.T) {
	loc := time.UTC
	base := time.Date(2026, 7, 12, 0, 0, 0, 0, loc)
	spike := base.Add(14 * time.Hour)
	hourly := []HourlyRecord{
		{HourStart: base.Add(8 * time.Hour), EssCharged: 10, EssPeakIntervalKw: 50},
		// Gap 08→12, then continuous 12→13→14 with spike at 14.
		{HourStart: base.Add(12 * time.Hour), EssCharged: 20, EssPeakIntervalKw: 80},
		{HourStart: base.Add(13 * time.Hour), EssCharged: 30, EssPeakIntervalKw: 90},
		{HourStart: spike, EssCharged: 40, EssPeakIntervalKw: 200},
	}
	bad, dq := detectEssAnomalies(hourly, loc, 100, essAnomalyTolerance) // limit 150
	if len(bad) != 1 || !bad[spike.Unix()] {
		t.Fatalf("bad = %v, want spike hour", bad)
	}
	if dq.ReasonCounts[AnomalyReasonPeakSpike] != 1 || dq.ReasonCounts[AnomalyReasonAfterGap] != 1 {
		t.Fatalf("ReasonCounts = %v, want peak_spike+after_gap", dq.ReasonCounts)
	}
	if len(dq.Anomalies) != 1 {
		t.Fatalf("Anomalies len = %d", len(dq.Anomalies))
	}
	got := dq.Anomalies[0].Reasons
	hasGap, hasPeak := false, false
	for _, r := range got {
		if r == AnomalyReasonAfterGap {
			hasGap = true
		}
		if r == AnomalyReasonPeakSpike {
			hasPeak = true
		}
	}
	if !hasGap || !hasPeak {
		t.Fatalf("reasons = %v, want peak_spike and after_gap", got)
	}
}

// TestAggregateMonthExcludesAnomalousHours verifies that only the corrupt
// hour is dropped from the fact/optimum path: the rest of that day still
// contributes, and DataQuality reports the hour (and its civil day).
func TestAggregateMonthExcludesAnomalousHours(t *testing.T) {
	loc := time.UTC
	day1 := time.Date(2026, 6, 1, 0, 0, 0, 0, loc)
	day2 := time.Date(2026, 6, 2, 0, 0, 0, 0, loc)
	days := []DailyRecord{
		{Day: day1, IsFinal: true, Totals: DailyTotals{EssNet: 10, EssDischarged: 40, HoursWithData: 24}},
		{Day: day2, IsFinal: true, Totals: DailyTotals{EssNet: 15, EssDischarged: 40, HoursWithData: 24}},
	}
	mk := func(base time.Time, charge float64, eveningNet float64) []HourlyRecord {
		hs := make([]HourlyRecord, 24)
		for h := 0; h < 24; h++ {
			hs[h] = HourlyRecord{HourStart: base.Add(time.Duration(h) * time.Hour)}
		}
		hs[3] = HourlyRecord{
			HourStart: base.Add(3 * time.Hour), Rdn: floatPtr(1), ImportPrice: 1, ExportPrice: 1,
			GridToEss: charge, EssCharged: charge, EssRemainingKwhStart: floatPtr(0),
		}
		hs[19] = HourlyRecord{
			HourStart: base.Add(19 * time.Hour), Rdn: floatPtr(20), ImportPrice: 20, ExportPrice: 18,
			GridToLoad: 100, EssDischarged: 40, EssNet: eveningNet, EssRemainingKwhStart: floatPtr(40),
		}
		return hs
	}
	// Day2 hour 3 charge 1000 ≫ 150 limit; evening EssNet 10 should still count.
	hourly := append(mk(day1, 40, 10), mk(day2, 1000, 10)...)

	got := AggregateMonth("2026-06", loc, days, hourly, 100, 0, 100, 0)

	if got.Totals.EssDataQuality.AnomalousHours != 1 || got.Totals.EssDataQuality.AnomalousDays != 1 || got.Totals.EssDataQuality.DataOK {
		t.Fatalf("data quality = %+v, want 1 anomalous hour / 1 day, not ok", got.Totals.EssDataQuality)
	}
	// Fact = day1 evening (10) + day2 evening (10); corrupt charge hour contributes 0 EssNet.
	if math.Abs(got.Totals.EssFact-20) > 1e-9 {
		t.Fatalf("EssFact = %v, want 20 (only anomalous hour excluded)", got.Totals.EssFact)
	}
	for _, d := range got.Days {
		if d.Date == "2026-06-02" && d.EssFact != 10 {
			t.Fatalf("day2 EssFact = %v, want 10 (evening hour kept)", d.EssFact)
		}
	}
}

// TestOptimizeMonthEndGeStart checks the continuous monthly DP refuses to
// bank energy it never returns: with only a single cheap hour to charge and
// no later expensive hour, the SOC_end ≥ SOC_start restriction yields no
// profit (unlike optimizeDay, which may end with a charged battery).
func TestOptimizeMonthEndGeStart(t *testing.T) {
	p := optimumParams{
		capacityKwh: 100, degradationUahPerKwh: 0,
		maxChargeKwh: 50, maxDischargeKwh: 50,
		socMinKwh: 0, socMaxKwh: 100, rte: 1.0,
	}
	// One cheap hour where buying is only "profitable" if the energy is
	// kept (never sold). End ≥ start forbids ending above the start SOC
	// without having sold, so the monthly optimum is 0.
	hours := make([]optimumHour, 6)
	hours[0] = optimumHour{tradable: true, importPrice: 1, exportPrice: 1, displaceableKwh: 100}
	if got := optimizeMonth(hours, 0, p, modeFull); got > 1e-6 {
		t.Fatalf("optimizeMonth banking-only = %v, want ~0", got)
	}
	// With a later expensive hour, the round trip is allowed and profitable.
	hours[3] = optimumHour{tradable: true, importPrice: 20, exportPrice: 20, displaceableKwh: 100}
	if got := optimizeMonth(hours, 0, p, modeFull); got < 100 {
		t.Fatalf("optimizeMonth round-trip = %v, want a healthy profit", got)
	}
}

// TestOptimizeDayScheduleMatchesOptimizeDay verifies that the backtracked
// schedule's total effect equals optimizeDay, that the recovered actions
// reconstruct the arbitrage (charge cheap, discharge into load expensive),
// and that the per-hour revenue/cost figures are self-consistent.
func TestOptimizeDayScheduleMatchesOptimizeDay(t *testing.T) {
	p := optimumParams{
		capacityKwh: 100, degradationUahPerKwh: 0,
		maxChargeKwh: 50, maxDischargeKwh: 50,
		socMinKwh: 0, socMaxKwh: 100, rte: 1.0,
	}
	hours := make([]optimumHour, 24)
	hours[0] = optimumHour{tradable: true, importPrice: 1, exportPrice: 1, displaceableKwh: 100}
	hours[1] = optimumHour{tradable: true, importPrice: 10, exportPrice: 10, displaceableKwh: 100}

	want := optimizeDay(hours, 0, p, modeFull)
	steps, start, effect, ok := optimizeDaySchedule(hours, 0, p, modeFull)
	if !ok {
		t.Fatal("optimizeDaySchedule not ok")
	}
	if math.Abs(effect-want) > 1e-6 {
		t.Fatalf("schedule effect = %v, optimizeDay = %v", effect, want)
	}
	if start != 0 {
		t.Fatalf("start residual = %v, want 0", start)
	}
	if len(steps) != 24 {
		t.Fatalf("len(steps) = %d, want 24", len(steps))
	}
	if steps[0].chgGridKwh < 49 {
		t.Fatalf("hour0 grid charge = %v, want ~50", steps[0].chgGridKwh)
	}
	if steps[1].toLoadKwh < 49 {
		t.Fatalf("hour1 to-load discharge = %v, want ~50", steps[1].toLoadKwh)
	}
	// Reconstructed effect from the per-hour legs must equal the DP value
	// (degradation is 0 here; PV charge price snaps from exportPrice).
	var recon float64
	for i, s := range steps {
		recon += s.toLoadKwh*hours[i].importPrice + s.toGridKwh*hours[i].exportPrice
		recon -= s.chgPvKwh*pvChargePriceFor(hours[i]) + s.chgGridKwh*hours[i].importPrice
	}
	if math.Abs(recon-effect) > 1e-6 {
		t.Fatalf("reconstructed legs %v != DP effect %v", recon, effect)
	}
}

// TestAggregateMonthCycles checks the significant-cycle list: a day with a
// big timing reserve appears as a cycle whose chart summary reconciles
// (optimal effect ≈ schedule, reserve = max(0, opt − fact)).
func TestAggregateMonthCycles(t *testing.T) {
	loc := time.UTC
	day := time.Date(2026, 6, 1, 0, 0, 0, 0, loc)
	days := []DailyRecord{{
		Day: day, IsFinal: true,
		Totals: DailyTotals{EssNet: 10, EssDischarged: 40, HoursWithData: 24},
	}}
	hourly := make([]HourlyRecord, 24)
	for h := 0; h < 24; h++ {
		hourly[h] = HourlyRecord{HourStart: day.Add(time.Duration(h) * time.Hour)}
	}
	hourly[3] = HourlyRecord{
		HourStart: day.Add(3 * time.Hour), Rdn: floatPtr(1), ImportPrice: 1, ExportPrice: 1,
		GridToEss: 40, EssCharged: 40, EssRemainingKwhStart: floatPtr(0),
	}
	hourly[19] = HourlyRecord{
		HourStart: day.Add(19 * time.Hour), Rdn: floatPtr(40), ImportPrice: 40, ExportPrice: 38,
		GridToLoad: 100, EssDischarged: 40, EssNet: 10, EssRemainingKwhStart: floatPtr(40),
	}

	got := AggregateMonth("2026-06", loc, days, hourly, 100, 0, 0, 0)
	if len(got.Cycles) != 1 {
		t.Fatalf("len(Cycles) = %d, want 1", len(got.Cycles))
	}
	c := got.Cycles[0]
	if c.ReserveUah < cycleReserveThresholdUah {
		t.Fatalf("cycle reserve %v below threshold", c.ReserveUah)
	}
	if len(c.Chart.Labels) != 24 || len(c.Chart.Optimal.ToLoadKwh) != 24 {
		t.Fatalf("chart arrays not length 24: %+v", c.Chart.Labels)
	}
	if math.Abs(c.ReserveUah-math.Max(0, c.OptEffectUah-c.ActualEffectUah)) > 1e-6 {
		t.Fatalf("reserve %v != max(0, opt %v − fact %v)", c.ReserveUah, c.OptEffectUah, c.ActualEffectUah)
	}
	if math.Abs(c.Chart.Summary.Optimal.EffectUah-c.OptEffectUah) > 1e-6 {
		t.Fatalf("summary effect %v != opt %v", c.Chart.Summary.Optimal.EffectUah, c.OptEffectUah)
	}
}

// TestDeriveOptimumParams checks the empirical envelope: peak hourly
// charge/discharge as power, observed residual range as the SOC window,
// and discharged/charged as round-trip efficiency.
func TestDeriveOptimumParams(t *testing.T) {
	base := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	hourly := []HourlyRecord{
		{HourStart: base, EssCharged: 30, EssDischarged: 0, EssRemainingKwhStart: floatPtr(20)},
		{HourStart: base.Add(time.Hour), EssCharged: 45, EssDischarged: 0, EssRemainingKwhStart: floatPtr(60)},
		{HourStart: base.Add(2 * time.Hour), EssCharged: 0, EssDischarged: 40, EssRemainingKwhStart: floatPtr(95)},
	}
	p := deriveOptimumParams(hourly, 200, 0.6, 0, 0)
	if p.maxChargeKwh != 45 {
		t.Fatalf("maxChargeKwh = %v, want 45", p.maxChargeKwh)
	}
	if p.maxDischargeKwh != 40 {
		t.Fatalf("maxDischargeKwh = %v, want 40", p.maxDischargeKwh)
	}
	if p.socMinKwh != 20 || p.socMaxKwh != 95 {
		t.Fatalf("SOC window = %v..%v, want 20..95", p.socMinKwh, p.socMaxKwh)
	}
	// rte = discharged/charged = 40/75 ≈ 0.533, clamped above 0.5.
	if math.Abs(p.rte-(40.0/75.0)) > 1e-9 {
		t.Fatalf("rte = %v, want %v", p.rte, 40.0/75.0)
	}
}
