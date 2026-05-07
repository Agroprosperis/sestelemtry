package energyflow

import (
	"math"
	"strings"
	"testing"
)

// floatPtr / int64Ptr help compose pointer-typed sample fields without
// cluttering each test case with a temporary variable. Standard Go
// idiom for table-driven tests on optional fields.
func floatPtr(v float64) *float64 { return &v }
func int64Ptr(v int64) *int64     { return &v }

// baseSample is a stable starting point with all accumulators present
// and at the spec's "previous snapshot" values. Tests copy and mutate
// this to derive both prev and curr without re-typing every field.
func baseSample(ts int64) EnergySample {
	return EnergySample{
		Timestamp:               ts,
		PvDeviceEpochSeconds:    int64Ptr(ts),
		EssDeviceEpochSeconds:   int64Ptr(ts),
		PvPowerKw:               0,
		EssPowerKw:              0,
		LoadPowerKw:             0,
		AccumulatedPvYieldKwh:   floatPtr(23193.50),
		AccumulatedLoadKwh:      floatPtr(40138.30),
		TotalGridSupplyToEssKwh: floatPtr(14469.50),
		TotalEssChargedKwh:      floatPtr(14469.50),
		TotalEssDischargedKwh:   floatPtr(12547.50),
	}
}

func almostEqual(a, b float64) bool {
	return math.Abs(a-b) < 1e-6
}

// 1. Розряд УЗЕ тільки на елеватор.
func TestDischargeOnlyToLoad(t *testing.T) {
	prev := baseSample(0)
	curr := baseSample(1)
	*curr.AccumulatedLoadKwh += 0.0023
	*curr.TotalEssDischargedKwh += 0.0023

	d := Calculate(&prev, &curr, nil, EnergyFlowOptions{})
	if d.Skipped {
		t.Fatalf("unexpected skip: %+v", d.Warnings)
	}
	if !almostEqual(d.EssToLoadKwh, 0.0023) {
		t.Fatalf("ess_to_load %v", d.EssToLoadKwh)
	}
	if !almostEqual(d.EssToGridKwh, 0) {
		t.Fatalf("ess_to_grid %v", d.EssToGridKwh)
	}
}

// 2. Розряд УЗЕ частково на елеватор, частково в мережу.
func TestDischargeSplitLoadAndGrid(t *testing.T) {
	prev := baseSample(0)
	curr := baseSample(1)
	*curr.AccumulatedLoadKwh += 0.10
	*curr.TotalEssDischargedKwh += 0.30

	d := Calculate(&prev, &curr, nil, EnergyFlowOptions{})
	if d.Skipped {
		t.Fatalf("unexpected skip: %+v", d.Warnings)
	}
	if !almostEqual(d.EssToLoadKwh, 0.10) {
		t.Fatalf("ess_to_load %v want 0.10", d.EssToLoadKwh)
	}
	if !almostEqual(d.EssToGridKwh, 0.20) {
		t.Fatalf("ess_to_grid %v want 0.20", d.EssToGridKwh)
	}
}

// 3. Заряд УЗЕ тільки від сонця.
func TestChargeOnlyFromPV(t *testing.T) {
	prev := baseSample(0)
	curr := baseSample(1)
	*curr.AccumulatedPvYieldKwh += 0.50
	*curr.TotalEssChargedKwh += 0.30

	d := Calculate(&prev, &curr, nil, EnergyFlowOptions{})
	if d.Skipped {
		t.Fatalf("unexpected skip: %+v", d.Warnings)
	}
	if !almostEqual(d.PvToEssKwh, 0.30) {
		t.Fatalf("pv_to_ess %v want 0.30", d.PvToEssKwh)
	}
	if !almostEqual(d.GridToEssKwh, 0) {
		t.Fatalf("grid_to_ess %v", d.GridToEssKwh)
	}
}

// 4. Заряд УЗЕ частково від сонця, частково від мережі.
func TestChargeSplitPVAndGrid(t *testing.T) {
	prev := baseSample(0)
	curr := baseSample(1)
	*curr.AccumulatedPvYieldKwh += 0.20
	*curr.TotalGridSupplyToEssKwh += 0.30
	*curr.TotalEssChargedKwh += 0.50

	d := Calculate(&prev, &curr, nil, EnergyFlowOptions{})
	if d.Skipped {
		t.Fatalf("unexpected skip: %+v", d.Warnings)
	}
	if !almostEqual(d.GridToEssKwh, 0.30) {
		t.Fatalf("grid_to_ess %v want 0.30", d.GridToEssKwh)
	}
	if !almostEqual(d.PvToEssKwh, 0.20) {
		t.Fatalf("pv_to_ess %v want 0.20", d.PvToEssKwh)
	}
}

// 5. Заряд УЗЕ тільки від мережі.
func TestChargeOnlyFromGrid(t *testing.T) {
	prev := baseSample(0)
	curr := baseSample(1)
	*curr.TotalGridSupplyToEssKwh += 0.20
	*curr.TotalEssChargedKwh += 0.20

	d := Calculate(&prev, &curr, nil, EnergyFlowOptions{})
	if d.Skipped {
		t.Fatalf("unexpected skip: %+v", d.Warnings)
	}
	if !almostEqual(d.GridToEssKwh, 0.20) {
		t.Fatalf("grid_to_ess %v want 0.20", d.GridToEssKwh)
	}
	if !almostEqual(d.PvToEssKwh, 0) {
		t.Fatalf("pv_to_ess %v", d.PvToEssKwh)
	}
}

// 6. Нульові потужності.
func TestZeroPowers(t *testing.T) {
	prev := baseSample(0)
	curr := baseSample(1)

	d := Calculate(&prev, &curr, nil, EnergyFlowOptions{})
	if d.Skipped {
		t.Fatalf("unexpected skip: %+v", d.Warnings)
	}
	if d.EssToGridKwh != 0 || d.EssToLoadKwh != 0 || d.PvToEssKwh != 0 || d.GridToEssKwh != 0 {
		t.Fatalf("expected all flows zero, got %+v", d)
	}
}

// 7. Невалідний timestamp (нульовий dt).
func TestInvalidTimestamp(t *testing.T) {
	prev := baseSample(10)
	curr := baseSample(10)
	d := Calculate(&prev, &curr, nil, EnergyFlowOptions{})
	if !d.Skipped {
		t.Fatal("expected skip")
	}
	if !containsWarning(d.Warnings, "dt <= 0") {
		t.Fatalf("warnings: %+v", d.Warnings)
	}
}

// 8. Від'ємний або нульовий dt.
func TestNegativeDt(t *testing.T) {
	prev := baseSample(10)
	curr := baseSample(5)
	d := Calculate(&prev, &curr, nil, EnergyFlowOptions{})
	if !d.Skipped {
		t.Fatal("expected skip")
	}
}

// 9. Надто великий розрив між samples.
func TestGapTooLarge(t *testing.T) {
	prev := baseSample(0)
	curr := baseSample(60) // 60s > maxGap=5s
	d := Calculate(&prev, &curr, nil, EnergyFlowOptions{})
	if !d.Skipped {
		t.Fatalf("expected skip, got %+v", d)
	}
	if !containsWarning(d.Warnings, "max_gap") {
		t.Fatalf("warnings: %+v", d.Warnings)
	}
}

// 10. Невалідні значення типу 42949672.95.
func TestInvalidUint32SentinelDetected(t *testing.T) {
	if !IsInvalidUint32Scaled(42949672.95, 0.01) {
		t.Fatal("expected sentinel detection for 42949672.95")
	}
	if IsInvalidUint32Scaled(100.0, 0.01) {
		t.Fatal("did not expect sentinel for 100.0")
	}
}

func TestSampleWithSentinelIsSkipped(t *testing.T) {
	prev := baseSample(0)
	curr := baseSample(1)
	*curr.AccumulatedLoadKwh = 42949672.95
	d := Calculate(&prev, &curr, nil, EnergyFlowOptions{})
	if !d.Skipped {
		t.Fatal("expected skip on sentinel")
	}
	if !containsWarning(d.Warnings, "UINT32 sentinel") {
		t.Fatalf("warnings: %+v", d.Warnings)
	}
}

// 11. Перевірка балансу з Total energy charged/discharged.
func TestBalanceWarningOnImbalance(t *testing.T) {
	// Discharge accumulator says 0.5 kWh out, but Δload is 0.05 and
	// Δpv is 0 — so spec splits 0.05 to load and 0.45 to grid; the
	// sum equals 0.5. To force imbalance we tamper: set discharged
	// to 0.5 but pretend ess_to_load+ess_to_grid would be off.
	// Easiest reproducible imbalance: charge side with grid > 1+tol.
	prev := baseSample(0)
	curr := baseSample(1)
	*curr.TotalGridSupplyToEssKwh += 1.0
	*curr.TotalEssChargedKwh += 0.5

	d := Calculate(&prev, &curr, nil, EnergyFlowOptions{})
	if d.Skipped {
		t.Fatalf("unexpected skip: %+v", d.Warnings)
	}
	if !containsWarning(d.Warnings, "delta_grid_to_ess > delta_ess_charged") {
		t.Fatalf("expected clamp warning, got %+v", d.Warnings)
	}
	if !almostEqual(d.GridToEssKwh, 0.5) || !almostEqual(d.PvToEssKwh, 0) {
		t.Fatalf("clamp produced wrong split: grid=%v pv=%v", d.GridToEssKwh, d.PvToEssKwh)
	}
}

// 12. Інверсний знак ESS через essDischargeSign = -1.
func TestEssSignNormalization(t *testing.T) {
	if got := NormalizedEssPowerKw(-3.0, -1); got != 3.0 {
		t.Fatalf("got %v want 3.0", got)
	}
	if got := NormalizedEssPowerKw(-3.0, 1); got != -3.0 {
		t.Fatalf("got %v want -3.0", got)
	}
}

// 13. Конфігураційна адреса activePvPowerAddress = 440388.
func TestActivePvPowerAddressDefault(t *testing.T) {
	o := WithDefaults(EnergyFlowOptions{})
	if o.ActivePvPowerAddress != 440388 {
		t.Fatalf("default activePvPowerAddress: %d", o.ActivePvPowerAddress)
	}
	o2 := WithDefaults(EnergyFlowOptions{ActivePvPowerAddress: 440400})
	if o2.ActivePvPowerAddress != 440400 {
		t.Fatalf("explicit override lost: %d", o2.ActivePvPowerAddress)
	}
}

// 14. Розрахунок по секундних delta накопичувальних лічильників.
// Точне відтворення прикладу зі spec ("Приклад розрахунку").
func TestSpecExample(t *testing.T) {
	prev := EnergySample{
		Timestamp:               1778112000,
		PvDeviceEpochSeconds:    int64Ptr(1778112000),
		EssDeviceEpochSeconds:   int64Ptr(1778112000),
		AccumulatedPvYieldKwh:   floatPtr(23193.50),
		AccumulatedLoadKwh:      floatPtr(40138.30),
		TotalGridSupplyToEssKwh: floatPtr(14469.50),
		TotalEssChargedKwh:      floatPtr(14469.50),
		TotalEssDischargedKwh:   floatPtr(12547.50),
	}
	curr := EnergySample{
		Timestamp:               1778112001,
		PvDeviceEpochSeconds:    int64Ptr(1778112001),
		EssDeviceEpochSeconds:   int64Ptr(1778112001),
		AccumulatedPvYieldKwh:   floatPtr(23193.50),
		AccumulatedLoadKwh:      floatPtr(40138.3023),
		TotalGridSupplyToEssKwh: floatPtr(14469.50),
		TotalEssChargedKwh:      floatPtr(14469.50),
		TotalEssDischargedKwh:   floatPtr(12547.5023),
	}
	d := Calculate(&prev, &curr, nil, EnergyFlowOptions{})
	if d.Skipped {
		t.Fatalf("unexpected skip: %+v", d.Warnings)
	}
	if !almostEqual(d.EssToLoadKwh, 0.0023) {
		t.Fatalf("ess_to_load %v", d.EssToLoadKwh)
	}
	if !almostEqual(d.EssToGridKwh, 0) {
		t.Fatalf("ess_to_grid %v", d.EssToGridKwh)
	}
	if !almostEqual(d.GridToEssKwh, 0) || !almostEqual(d.PvToEssKwh, 0) {
		t.Fatalf("charge side non-zero: %+v", d)
	}
}

// 15. Невалідний або відсутній device_epoch_seconds — інтервал
// рахуємо, але додаємо warning.
func TestMissingDeviceEpochProducesWarning(t *testing.T) {
	prev := baseSample(0)
	curr := baseSample(1)
	curr.PvDeviceEpochSeconds = nil
	d := Calculate(&prev, &curr, nil, EnergyFlowOptions{})
	if d.Skipped {
		t.Fatalf("should not skip just for missing device_epoch_seconds: %+v", d.Warnings)
	}
	if !containsWarning(d.Warnings, "device_epoch_seconds missing") {
		t.Fatalf("expected device_epoch_seconds warning, got %+v", d.Warnings)
	}
}

// 16. Різниця часу між СЕС і УЗЕ більше maxDeviceTimeSkewSeconds.
func TestDeviceTimeSkewExceedsMax(t *testing.T) {
	prev := baseSample(0)
	curr := baseSample(1)
	*curr.PvDeviceEpochSeconds = 1
	*curr.EssDeviceEpochSeconds = 10 // 9s skew >> 2s default
	d := Calculate(&prev, &curr, nil, EnergyFlowOptions{})
	if !d.Skipped {
		t.Fatalf("expected skip on time skew, got %+v", d)
	}
}

// Bonus: running totals accumulate.
func TestRunningTotalsAccumulate(t *testing.T) {
	totals := &RunningTotals{}
	prev := baseSample(0)
	curr := baseSample(1)
	*curr.AccumulatedLoadKwh += 0.10
	*curr.TotalEssDischargedKwh += 0.30
	d1 := Calculate(&prev, &curr, totals, EnergyFlowOptions{})
	if d1.Skipped {
		t.Fatalf("d1 skipped: %+v", d1.Warnings)
	}
	prev2 := curr
	curr2 := baseSample(2)
	*curr2.AccumulatedPvYieldKwh = *prev2.AccumulatedPvYieldKwh
	*curr2.AccumulatedLoadKwh = *prev2.AccumulatedLoadKwh + 0.05
	*curr2.TotalGridSupplyToEssKwh = *prev2.TotalGridSupplyToEssKwh
	*curr2.TotalEssChargedKwh = *prev2.TotalEssChargedKwh
	*curr2.TotalEssDischargedKwh = *prev2.TotalEssDischargedKwh + 0.07
	d2 := Calculate(&prev2, &curr2, totals, EnergyFlowOptions{})
	if d2.Skipped {
		t.Fatalf("d2 skipped: %+v", d2.Warnings)
	}
	if !almostEqual(totals.EssToLoadKwh, d1.EssToLoadKwh+d2.EssToLoadKwh) {
		t.Fatalf("running total mismatch: %+v", totals)
	}
	if !almostEqual(totals.EssToGridKwh, d1.EssToGridKwh+d2.EssToGridKwh) {
		t.Fatalf("running total grid mismatch: %+v", totals)
	}
}

func containsWarning(ws []string, sub string) bool {
	for _, w := range ws {
		if strings.Contains(w, sub) {
			return true
		}
	}
	return false
}
