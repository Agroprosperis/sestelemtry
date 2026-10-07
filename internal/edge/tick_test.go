package edge

import (
	"testing"
	"time"
)

func dualCfg() *Config {
	cfg := testCfg()
	cfg.SmartLogger.Topology = TopologyDual
	cfg.SmartLogger.Devices = []Device{
		{Role: RolePV, Host: "mock"},
		{Role: RoleESS, Host: "mock"},
	}
	return cfg
}

func TestNormalizerMergesDualRoles(t *testing.T) {
	n := NewNormalizer(dualCfg())
	now := testTS
	n.Observe(reading{role: RolePV, at: now, values: map[string]float64{
		"active_pv_power_kw": 120, "load_power_kw": 80, "grid_connected_active_power_kw": -35,
	}})
	n.Observe(reading{role: RoleESS, at: now, values: map[string]float64{
		"active_ess_power_kw": -5, "soc_percent": 55,
	}})
	tick := n.BuildTick(now)
	if tick.DataQuality != QualityOK {
		t.Fatalf("quality = %s, want ok", tick.DataQuality)
	}
	if tick.PVPowerKw == nil || *tick.PVPowerKw != 120 {
		t.Fatalf("pv = %v", tick.PVPowerKw)
	}
	if tick.SocPercent == nil || *tick.SocPercent != 55 {
		t.Fatalf("soc = %v", tick.SocPercent)
	}
}

func TestNormalizerFaultWhenRoleNeverReported(t *testing.T) {
	n := NewNormalizer(dualCfg())
	now := testTS
	n.Observe(reading{role: RolePV, at: now, values: map[string]float64{"active_pv_power_kw": 100}})
	tick := n.BuildTick(now)
	if tick.DataQuality != QualityFault {
		t.Fatalf("quality = %s, want fault (ESS never reported)", tick.DataQuality)
	}
}

func TestNormalizerStaleAfterSilence(t *testing.T) {
	cfg := testCfg()
	n := NewNormalizer(cfg)
	start := testTS
	n.Observe(reading{role: RoleAll, at: start, values: map[string]float64{"active_pv_power_kw": 100}})
	tick := n.BuildTick(start.Add(10 * time.Second)) // > staleAfter (5s), < faultAfter (30s)
	if tick.DataQuality != QualityStale {
		t.Fatalf("quality = %s, want stale", tick.DataQuality)
	}
	tick = n.BuildTick(start.Add(40 * time.Second))
	if tick.DataQuality != QualityFault {
		t.Fatalf("quality = %s, want fault", tick.DataQuality)
	}
}

func TestLoadBalanceFallbackMarksEstimated(t *testing.T) {
	values := map[string]float64{
		"active_pv_power_kw":             100,
		"active_ess_power_kw":            50, // discharging
		"grid_connected_active_power_kw": 30, // importing
	}
	tick := buildTickFromValues("ab", TopologySingle, 1, testTS, values, QualityOK)
	if tick.LoadPowerKw == nil || *tick.LoadPowerKw != 180 {
		t.Fatalf("load = %v, want 180", tick.LoadPowerKw)
	}
	if tick.DataQuality != QualityEstimated {
		t.Fatalf("quality = %s, want estimated", tick.DataQuality)
	}
}

func TestEssSignFlip(t *testing.T) {
	values := map[string]float64{"active_ess_power_kw": 75}
	tick := buildTickFromValues("ze", TopologyDual, -1, testTS, values, QualityOK)
	if tick.ESSPowerKw == nil || *tick.ESSPowerKw != -75 {
		t.Fatalf("ess = %v, want -75 after sign flip", tick.ESSPowerKw)
	}
	// Raw value in Values stays unflipped for cloud ingest parity.
	if tick.Values["active_ess_power_kw"] != 75 {
		t.Fatalf("raw value must stay 75, got %v", tick.Values["active_ess_power_kw"])
	}
}

func TestSLAlarmWordsSixOrEight(t *testing.T) {
	six := testTick(map[string]float64{
		"sl_alarm_1": 0, "sl_alarm_2": 16, "sl_alarm_3": 0,
		"sl_alarm_4": 0, "sl_alarm_5": 0, "sl_alarm_6": 0,
	}, QualityOK)
	words, ok := six.SLAlarmWords()
	if !ok || len(words) != 6 || words[1] != 16 {
		t.Fatalf("six = %v ok=%v", words, ok)
	}
	if !six.SLAlarmActive() {
		t.Fatal("word 2 = 16 must be active")
	}

	eight := testTick(map[string]float64{
		"sl_alarm_1": 0, "sl_alarm_2": 0, "sl_alarm_3": 0,
		"sl_alarm_4": 0, "sl_alarm_5": 0, "sl_alarm_6": 0,
		"sl_alarm_7": 0, "sl_alarm_8": 1,
	}, QualityOK)
	words, ok = eight.SLAlarmWords()
	if !ok || len(words) != 8 || words[7] != 1 {
		t.Fatalf("eight = %v ok=%v", words, ok)
	}
	if !eight.SLAlarmActive() {
		t.Fatal("word 8 must be active")
	}
	hex := slAlarmHex(words)
	if len(hex) != 8 || hex[7] != "0x0001" {
		t.Fatalf("hex = %v", hex)
	}
}
