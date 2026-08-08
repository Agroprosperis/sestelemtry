package main

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/nesh/sestelemetry/internal/alerts"
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

func testWatchdog(devices ...monitoredDevice) *watchdog {
	return &watchdog{
		log:                slog.New(slog.NewTextHandler(io.Discard, nil)),
		devices:            devices,
		warnedNoRecipients: map[string]bool{},
	}
}

func device(orgID, host string) monitoredDevice {
	return monitoredDevice{
		Device:         alerts.Device{OrganizationID: orgID, Host: host},
		ProbeMetricKey: probePreferredKey,
	}
}

func enabledSettings(recipients ...string) alerts.Settings {
	s := alerts.DefaultSettings()
	s.Enabled = true
	s.SMTP.Host = "smtp.example.com"
	s.SMTP.From = "alerts@example.com"
	s.Recipients = recipients
	return s
}

func TestTargetsResolvesRecipientsPerOrganization(t *testing.T) {
	w := testWatchdog(device("ke", "10.24.40.238"), device("pde", "10.32.40.102"))
	eff := effective{
		settings: enabledSettings("ops@example.com"),
		overrides: map[string]alerts.OrgSettings{
			"ke": {Enabled: true, Recipients: []string{"ke@example.com"}},
		},
	}
	targets, recipients := w.targets(eff)
	if len(targets) != 2 {
		t.Fatalf("targets = %d, want 2", len(targets))
	}
	if got := recipients["ke"]; len(got) != 1 || got[0] != "ke@example.com" {
		t.Fatalf("ke recipients = %#v", got)
	}
	if got := recipients["pde"]; len(got) != 1 || got[0] != "ops@example.com" {
		t.Fatalf("pde recipients = %#v", got)
	}
}

// TestTargetsSkipsDisabledOrganization is the per-site switch from the
// notifications page: a muted organization must not be probed or
// reported at all.
func TestTargetsSkipsDisabledOrganization(t *testing.T) {
	w := testWatchdog(device("ke", "10.24.40.238"), device("pde", "10.32.40.102"))
	eff := effective{
		settings:  enabledSettings("ops@example.com"),
		overrides: map[string]alerts.OrgSettings{"ke": {Enabled: false}},
	}
	targets, _ := w.targets(eff)
	if len(targets) != 1 || targets[0].OrganizationID != "pde" {
		t.Fatalf("targets = %+v", targets)
	}
	// Muting is deliberate, so it must not be reported as a
	// misconfiguration.
	if w.warnedNoRecipients["ke"] {
		t.Fatal("a deliberately muted organization must not warn")
	}
}

func TestTargetsWarnsOnceWhenNobodyWouldBeEmailed(t *testing.T) {
	w := testWatchdog(device("ke", "10.24.40.238"))
	eff := effective{settings: enabledSettings()}
	if targets, _ := w.targets(eff); len(targets) != 0 {
		t.Fatalf("targets = %+v, want none", targets)
	}
	if !w.warnedNoRecipients["ke"] {
		t.Fatal("an organization nobody would hear about must be flagged")
	}
	// Second pass must stay quiet: this runs every check interval.
	before := len(w.warnedNoRecipients)
	w.targets(eff)
	if len(w.warnedNoRecipients) != before {
		t.Fatal("the warning must not repeat every tick")
	}
}

// TestRunOnceWithoutDatabaseUsesConfigFallback covers the deployment
// that has never opened the notifications page.
func TestRunOnceWithoutDatabaseUsesConfigFallback(t *testing.T) {
	w := testWatchdog(device("ke", "10.24.40.238"))
	w.fallback = enabledSettings("ops@example.com")
	w.fallback.CheckInterval = alerts.Duration(2 * time.Minute)

	// No pool: loadSettings returns the fallback and check() is skipped,
	// so this exercises the interval the ticker will be retuned to.
	interval, err := w.runOnce(context.Background())
	if err != nil {
		t.Fatalf("runOnce: %v", err)
	}
	if interval != 2*time.Minute {
		t.Fatalf("interval = %v, want 2m", interval)
	}
}

func TestRunOnceDisabledStillReportsInterval(t *testing.T) {
	w := testWatchdog(device("ke", "10.24.40.238"))
	w.fallback = alerts.DefaultSettings()
	interval, err := w.runOnce(context.Background())
	if err != nil {
		t.Fatalf("runOnce: %v", err)
	}
	// A disabled watchdog keeps ticking so the UI switch can turn it
	// back on without a restart.
	if interval != time.Minute {
		t.Fatalf("interval = %v, want 1m", interval)
	}
}
