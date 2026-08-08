package main

import (
	"testing"
	"time"

	"github.com/nesh/sestelemetry/internal/config"
)

func TestMonitoredDevicesLegacySingleDevice(t *testing.T) {
	cfg := &config.Root{Organizations: []config.Organization{{
		ID:     "ke",
		Name:   "Кролевецький елеватор",
		Modbus: config.Modbus{Host: "10.24.40.238"},
	}}}
	got := monitoredDevices(cfg)
	if len(got) != 1 {
		t.Fatalf("devices = %d, want 1", len(got))
	}
	if got[0].Host != "10.24.40.238" || got[0].OrganizationName != "Кролевецький елеватор" {
		t.Fatalf("device = %+v", got[0].Device)
	}
	if got[0].ProbeMetricKey != probePreferredKey {
		t.Fatalf("probe = %q, want %q", got[0].ProbeMetricKey, probePreferredKey)
	}
}

// TestMonitoredDevicesPrefersExclusiveProbe guards the case that makes
// per-device detection work at all: both SmartLoggers of the `ze` site
// poll the shared clock register, so probing it would keep the org
// looking healthy while one of the two boxes is dark.
func TestMonitoredDevicesPrefersExclusiveProbe(t *testing.T) {
	cfg := &config.Root{Organizations: []config.Organization{{
		ID:   "ze",
		Name: "ZE",
		ModbusDevices: []config.ModbusDevice{
			{
				Modbus:     config.Modbus{Host: "10.28.40.102"},
				MetricKeys: []string{"active_ess_power_kw", "local_time_epoch_s"},
			},
			{
				Modbus:     config.Modbus{Host: "10.28.40.101"},
				MetricKeys: []string{"pv_energy_yield_day_kwh", "active_pv_power_kw", "local_time_epoch_s"},
			},
		},
	}}}
	got := monitoredDevices(cfg)
	if len(got) != 2 {
		t.Fatalf("devices = %d, want 2", len(got))
	}
	if got[0].ProbeMetricKey != "active_ess_power_kw" {
		t.Fatalf("probe[0] = %q", got[0].ProbeMetricKey)
	}
	// Ties among exclusive keys resolve alphabetically for stability.
	if got[1].ProbeMetricKey != "active_pv_power_kw" {
		t.Fatalf("probe[1] = %q", got[1].ProbeMetricKey)
	}
}

func TestProbeMetricKeyPrefersClockWhenExclusive(t *testing.T) {
	devices := []config.ModbusDevice{
		{Modbus: config.Modbus{Host: "a"}, MetricKeys: []string{"load_power_kw", "local_time_epoch_s"}},
		{Modbus: config.Modbus{Host: "b"}, MetricKeys: []string{"active_pv_power_kw"}},
	}
	counts := metricKeyCounts(devices)
	if got := probeMetricKey(devices[0], counts); got != probePreferredKey {
		t.Fatalf("probe = %q, want %q", got, probePreferredKey)
	}
}

func TestProbeMetricKeyFallsBackWhenWhitelistsOverlap(t *testing.T) {
	// Identical whitelists leave nothing exclusive; the probe then only
	// proves the organization is alive, which still beats no check.
	devices := []config.ModbusDevice{
		{Modbus: config.Modbus{Host: "a"}, MetricKeys: []string{"load_power_kw"}},
		{Modbus: config.Modbus{Host: "b"}, MetricKeys: []string{"load_power_kw"}},
	}
	counts := metricKeyCounts(devices)
	if got := probeMetricKey(devices[0], counts); got != "load_power_kw" {
		t.Fatalf("probe = %q", got)
	}
}

func TestLookbackFloorAndScaling(t *testing.T) {
	if got := lookback(10 * time.Minute); got != minLookback {
		t.Fatalf("lookback = %v, want the %v floor", got, minLookback)
	}
	if got, want := lookback(12*time.Hour), 48*time.Hour; got != want {
		t.Fatalf("lookback = %v, want %v", got, want)
	}
}
