package economics

import (
	"math"
	"testing"
	"time"
)

func fwdHours(n int, start time.Time, price func(i int) *float64, pv, load func(i int) float64) []ForwardHour {
	out := make([]ForwardHour, n)
	for i := range out {
		out[i] = ForwardHour{
			TS:           start.Add(time.Duration(i) * time.Hour),
			RdnUahPerKwh: price(i),
			PvKw:         pv(i),
			LoadKw:       load(i),
		}
	}
	return out
}

func pricePtr(v float64) *float64 { return &v }

func TestForwardPlanArbitrage(t *testing.T) {
	// Flat 100 kW load, no PV. Cheap night (1 UAH/kWh, hours 0–5),
	// expensive evening (10 UAH/kWh, hours 18–21), mid otherwise. The
	// DP should buy at night and burn the stored energy in the evening.
	start := time.Date(2026, 6, 3, 0, 0, 0, 0, time.UTC)
	hours := fwdHours(24, start,
		func(i int) *float64 {
			switch {
			case i < 6:
				return pricePtr(1.0)
			case i >= 18 && i <= 21:
				return pricePtr(10.0)
			default:
				return pricePtr(4.0)
			}
		},
		func(int) float64 { return 0 },
		func(int) float64 { return 100 },
	)
	steps, err := BuildForwardPlan(hours, ForwardParams{
		CapacityKwh: 400,
		PowerKw:     200,
		SocMinPct:   20,
		SocMaxPct:   90,
		StartSocPct: 50,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 24 {
		t.Fatalf("steps = %d, want 24", len(steps))
	}

	var chargeCheap, chargeOther, dischargeExpensive, dischargeOther float64
	for i, s := range steps {
		charge := s.ChargePvKwh + s.ChargeGridKwh
		if i < 6 {
			chargeCheap += charge
		} else {
			chargeOther += charge
		}
		if i >= 18 && i <= 21 {
			dischargeExpensive += s.DischargeKwh
		} else {
			dischargeOther += s.DischargeKwh
		}
		if s.SocEndPct < 20-1e-6 || s.SocEndPct > 90+1e-6 {
			t.Errorf("hour %d: SOC %.2f%% outside 20..90", i, s.SocEndPct)
		}
	}
	if chargeCheap == 0 {
		t.Error("expected grid charging during the cheap night window")
	}
	if dischargeExpensive == 0 {
		t.Error("expected discharge during the expensive evening window")
	}
	if dischargeOther > dischargeExpensive {
		t.Errorf("discharge concentrated outside the peak: peak=%.1f other=%.1f",
			dischargeExpensive, dischargeOther)
	}
}

func TestForwardPlanNoExportCapsDischargeAtLoad(t *testing.T) {
	// 50 kW deficit, 200 kW inverter: without export, discharge in any
	// hour must not exceed the local deficit.
	start := time.Date(2026, 6, 3, 0, 0, 0, 0, time.UTC)
	hours := fwdHours(24, start,
		func(i int) *float64 {
			if i < 6 {
				return pricePtr(1.0)
			}
			return pricePtr(8.0)
		},
		func(int) float64 { return 0 },
		func(int) float64 { return 50 },
	)
	steps, err := BuildForwardPlan(hours, ForwardParams{
		CapacityKwh: 400,
		PowerKw:     200,
		SocMinPct:   20,
		SocMaxPct:   90,
		StartSocPct: 50,
	})
	if err != nil {
		t.Fatal(err)
	}
	for i, s := range steps {
		if s.DischargeKwh > 50+1e-6 {
			t.Errorf("hour %d: discharge %.1f kWh exceeds the 50 kWh local deficit", i, s.DischargeKwh)
		}
	}
}

func TestForwardPlanPvSurplusCharges(t *testing.T) {
	// Midday PV surplus (300 PV vs 100 load) with flat prices: the
	// evening deficit should be served by PV energy stored midday.
	start := time.Date(2026, 6, 3, 0, 0, 0, 0, time.UTC)
	hours := fwdHours(24, start,
		func(int) *float64 { return pricePtr(5.0) },
		func(i int) float64 {
			if i >= 10 && i <= 15 {
				return 300
			}
			return 0
		},
		func(int) float64 { return 100 },
	)
	steps, err := BuildForwardPlan(hours, ForwardParams{
		Tariffs:     Tariffs{ExportDiscount: 0.5}, // stored PV clearly beats its export value
		CapacityKwh: 600,
		PowerKw:     300,
		SocMinPct:   20,
		SocMaxPct:   90,
		StartSocPct: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	var pvCharge, gridCharge, eveningDischarge float64
	for i, s := range steps {
		pvCharge += s.ChargePvKwh
		gridCharge += s.ChargeGridKwh
		if i > 15 {
			eveningDischarge += s.DischargeKwh
		}
	}
	if pvCharge == 0 {
		t.Error("expected charging from the midday PV surplus")
	}
	if eveningDischarge == 0 {
		t.Error("expected evening discharge from stored PV")
	}
	if gridCharge > pvCharge {
		t.Errorf("flat prices should prefer PV charge: pv=%.1f grid=%.1f", pvCharge, gridCharge)
	}
}

func TestForwardPlanNoPricesMeansNoTrades(t *testing.T) {
	start := time.Date(2026, 6, 3, 0, 0, 0, 0, time.UTC)
	hours := fwdHours(12, start,
		func(int) *float64 { return nil },
		func(int) float64 { return 50 },
		func(int) float64 { return 100 },
	)
	steps, err := BuildForwardPlan(hours, ForwardParams{
		CapacityKwh: 400,
		PowerKw:     200,
		SocMinPct:   20,
		SocMaxPct:   90,
		StartSocPct: 60,
	})
	if err != nil {
		t.Fatal(err)
	}
	for i, s := range steps {
		if s.Tradable {
			t.Errorf("hour %d: tradable without a DAM price", i)
		}
		if s.EssKw != 0 || s.Action != "hold" {
			t.Errorf("hour %d: expected hold, got EssKw=%.1f action=%s", i, s.EssKw, s.Action)
		}
		if math.Abs(s.SocEndPct-60) > 1 {
			t.Errorf("hour %d: SOC drifted to %.2f%% with no tradable hours", i, s.SocEndPct)
		}
	}
}

func TestForwardPlanGridTargetImportCap(t *testing.T) {
	// 100 kW load with a 120 kW import target leaves only 20 kW of
	// grid-charge headroom per hour.
	start := time.Date(2026, 6, 3, 0, 0, 0, 0, time.UTC)
	hours := fwdHours(24, start,
		func(i int) *float64 {
			if i < 8 {
				return pricePtr(1.0)
			}
			return pricePtr(9.0)
		},
		func(int) float64 { return 0 },
		func(int) float64 { return 100 },
	)
	steps, err := BuildForwardPlan(hours, ForwardParams{
		CapacityKwh:        400,
		PowerKw:            200,
		SocMinPct:          20,
		SocMaxPct:          90,
		StartSocPct:        30,
		GridTargetImportKw: 120,
	})
	if err != nil {
		t.Fatal(err)
	}
	for i, s := range steps {
		if s.ChargeGridKwh > 20+1e-6 {
			t.Errorf("hour %d: grid charge %.1f kWh exceeds the 20 kWh import headroom", i, s.ChargeGridKwh)
		}
	}
}

func TestForwardPlanRejectsBadParams(t *testing.T) {
	start := time.Date(2026, 6, 3, 0, 0, 0, 0, time.UTC)
	hours := fwdHours(4, start,
		func(int) *float64 { return pricePtr(5.0) },
		func(int) float64 { return 0 },
		func(int) float64 { return 100 },
	)
	if _, err := BuildForwardPlan(nil, ForwardParams{CapacityKwh: 100, PowerKw: 50, SocMinPct: 20, SocMaxPct: 90}); err == nil {
		t.Error("empty horizon must fail")
	}
	if _, err := BuildForwardPlan(hours, ForwardParams{CapacityKwh: 0, PowerKw: 50, SocMinPct: 20, SocMaxPct: 90}); err == nil {
		t.Error("zero capacity must fail")
	}
	if _, err := BuildForwardPlan(hours, ForwardParams{CapacityKwh: 100, PowerKw: 50, SocMinPct: 90, SocMaxPct: 20}); err == nil {
		t.Error("inverted SOC band must fail")
	}
}
