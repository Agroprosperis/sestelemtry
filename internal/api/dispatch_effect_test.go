package api

import (
	"math"
	"testing"

	"github.com/nesh/sestelemetry/internal/dispatch"
)

func effectModel(env *dispatchEnv, load float64) dispatch.Model {
	m := dispatch.Model{Loads: make([]*float64, dispatchFutureHours), Commands: make([]*dispatch.Command, dispatchFutureHours), Cfg: env.defaults}
	for i := range m.Loads {
		m.Loads[i] = kwp(load)
	}
	return m
}

func near(a, b float64) bool { return math.Abs(a-b) <= 0.2 }

func TestDispatchDayEffectsAccounting(t *testing.T) {
	env := dispatchTestEnv(t)
	m := effectModel(env, 300)
	res, err := dispatch.Simulate(env.inputs, m)
	if err != nil {
		t.Fatal(err)
	}
	days := dispatchDayEffects(env, m, res)
	// 18:00 UTC = 21:00 Kyiv: three hours left today, 21 tomorrow.
	if len(days) != 2 || days[0].Date != "2026-10-07" || days[0].Hours != 3 || days[1].Hours != 21 {
		t.Fatalf("days = %+v", days)
	}
	for _, d := range days {
		if !near(d.NetEffectUah, d.FlowsUah+d.SocCarryUah) {
			t.Errorf("%s: net %.1f != flows %.1f + carry %.1f", d.Date, d.NetEffectUah, d.FlowsUah, d.SocCarryUah)
		}
		dec := d.EssToLoadUah + d.EssToGridUah - d.GridChargeCostUah - d.PvChargeCostUah - d.DegradationUah
		if !near(dec, d.FlowsUah) {
			t.Errorf("%s: flows %.1f != decomposition %.1f", d.Date, d.FlowsUah, dec)
		}
		if !near(d.BaselineCostUah-d.PlanCostUah, d.FlowsUah) {
			t.Errorf("%s: baseline %.1f − plan %.1f != flows %.1f", d.Date, d.BaselineCostUah, d.PlanCostUah, d.FlowsUah)
		}
		if !near(d.DegradationUah, (d.EssToLoadKwh+d.EssToGridKwh)*env.site.Tariffs.DegradationUahPerKwh) {
			t.Errorf("%s: degradation %.1f for %.1f kWh discharged", d.Date, d.DegradationUah, d.EssToLoadKwh+d.EssToGridKwh)
		}
	}
	if days[0].SocClosePct != days[1].SocOpenPct {
		t.Errorf("SOC not continuous across midnight: %v → %v", days[0].SocClosePct, days[1].SocOpenPct)
	}
	if days[1].EssToLoadKwh <= 0 || days[1].FlowsUah <= 0 {
		t.Errorf("tomorrow should discharge into load profitably: %+v", days[1])
	}
}

func TestDispatchDayEffectsStopAtUnknownLoad(t *testing.T) {
	env := dispatchTestEnv(t)
	m := effectModel(env, 300)
	for i := 2; i < len(m.Loads); i++ {
		m.Loads[i] = nil
	}
	res, err := dispatch.Simulate(env.inputs, m)
	if err != nil {
		t.Fatal(err)
	}
	days := dispatchDayEffects(env, m, res)
	if len(days) != 1 || days[0].Hours != 2 {
		t.Fatalf("days = %+v, want today with the two known hours only", days)
	}
}

func TestDispatchDayEffectsPvChargeFreeWithoutExport(t *testing.T) {
	env := dispatchTestEnv(t)
	for i := range env.inputs.PV {
		env.inputs.PV[i] = 500
	}
	m := effectModel(env, 100)
	m.Cfg.BlockExport = true
	res, err := dispatch.Simulate(env.inputs, m)
	if err != nil {
		t.Fatal(err)
	}
	days := dispatchDayEffects(env, m, res)
	charged := 0.0
	for _, d := range days {
		charged += d.ChargePvKwh
		if d.PvChargeCostUah != 0 {
			t.Errorf("%s: PV charge priced at %.1f with export blocked", d.Date, d.PvChargeCostUah)
		}
	}
	if charged <= 0 {
		t.Fatal("fixture should charge from PV surplus")
	}
}
