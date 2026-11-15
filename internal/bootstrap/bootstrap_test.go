package bootstrap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nesh/sestelemetry/internal/config"
	"github.com/nesh/sestelemetry/internal/energyflow"
)

const encombiCatalog = "../../registers/encombi_ecpvx.yaml"

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadDeviceCatalog(t *testing.T) {
	rt, err := Load(writeConfig(t, `register_catalog: ../../registers/huawei_smartlogger.yaml
organizations:
  - id: ke
    poll_interval: 1s
    modbus_devices:
      - host: 10.24.40.238
      - host: 10.24.40.239
        unit_id: 1
        register_catalog: `+encombiCatalog+`
        register_map: input
        poll_interval: 5s
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	devices := rt.Config.Organizations[0].Devices()
	sl, enc := devices[0], devices[1]
	if enc.RegisterMapOr(rt.Config.ModbusRegisterMap) != config.MapInput || sl.RegisterMapOr(rt.Config.ModbusRegisterMap) != config.MapHolding {
		t.Fatal("register maps wrong")
	}
	if enc.PollIntervalOr(time.Second) != 5*time.Second || sl.PollIntervalOr(time.Second) != time.Second {
		t.Fatal("poll intervals wrong")
	}
	if got := rt.CatalogFor(enc); len(got) != 19 || got[0].MetricKey != "active_gen_power_kw" {
		t.Fatalf("Encombi catalog: %d entries", len(got))
	}
	if got := rt.CatalogFor(sl); len(got) != len(rt.Resolved) {
		t.Fatal("the SmartLogger must keep the main catalog")
	}
	if rt.DeviceCatalogs[encombiCatalog].Device.Name != "Encombi ECpvX" {
		t.Fatalf("device metadata: %+v", rt.DeviceCatalogs[encombiCatalog].Device)
	}
}

func TestLoadChecksDeviceKeysAgainstItsOwnCatalog(t *testing.T) {
	_, err := Load(writeConfig(t, `register_catalog: ../../registers/huawei_smartlogger.yaml
organizations:
  - id: ke
    modbus_devices:
      - host: 10.24.40.239
        register_catalog: `+encombiCatalog+`
        metric_keys: [active_pv_power_kw]
`))
	if err == nil || !strings.Contains(err.Error(), "active_pv_power_kw") {
		t.Fatalf("a SmartLogger key on the Encombi device must be rejected, got %v", err)
	}
}

func TestLoadRejectsMissingDeviceCatalog(t *testing.T) {
	_, err := Load(writeConfig(t, `register_catalog: ../../registers/huawei_smartlogger.yaml
organizations:
  - id: ke
    modbus_devices:
      - host: 10.24.40.239
        register_catalog: /nonexistent/encombi.yaml
`))
	if err == nil || !strings.Contains(err.Error(), "register_catalog") {
		t.Fatalf("expected a register_catalog error, got %v", err)
	}
}

func TestDeviceRole(t *testing.T) {
	catalogs, err := LoadDeviceCatalogs(&config.Root{Organizations: []config.Organization{{
		ID:            "ke",
		ModbusDevices: []config.ModbusDevice{{RegisterCatalog: encombiCatalog}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	pv := []string{"accumulated_pv_energy_yield_kwh", "accumulated_electricity_purchased_kwh", "accumulated_electricity_sold_kwh"}
	ess := []string{"total_energy_charged_kwh", "total_energy_discharged_kwh"}
	cases := []struct {
		name string
		dev  config.ModbusDevice
		want energyflow.Role
	}{
		{"single SmartLogger", config.ModbusDevice{}, energyflow.RoleSingle},
		{"ze PV logger", config.ModbusDevice{MetricKeys: pv}, energyflow.RolePV},
		{"ze ESS logger", config.ModbusDevice{MetricKeys: ess}, energyflow.RoleESS},
		{"Encombi", config.ModbusDevice{RegisterCatalog: encombiCatalog}, energyflow.RoleNone},
		{"catalog not loaded", config.ModbusDevice{RegisterCatalog: "/missing.yaml"}, energyflow.RoleNone},
	}
	for _, c := range cases {
		if got := DeviceRole(c.dev, catalogs); got != c.want {
			t.Errorf("%s: role %q, want %q", c.name, got, c.want)
		}
	}
}

// TestLoadRejectsMissingRegisterCatalog locks in the contract that the modbus
// collector entrypoint refuses to start without `register_catalog` even though
// `config.Load` itself allows it (so dam-collector can use the same YAML).
func TestLoadRejectsMissingRegisterCatalog(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := `organizations:
  - id: org-a
    modbus:
      host: 127.0.0.1
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for missing register_catalog")
	}
	if !strings.Contains(err.Error(), "register_catalog") {
		t.Fatalf("expected 'register_catalog' in error, got %q", err.Error())
	}
}

func TestLoadRejectsMissingOrganizations(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := `register_catalog: registers/huawei_smartlogger.yaml
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for empty organizations")
	}
	if !strings.Contains(err.Error(), "organization") {
		t.Fatalf("expected 'organization' in error, got %q", err.Error())
	}
}
