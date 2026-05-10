package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadAppliesDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := `register_catalog: registers/huawei_smartlogger.yaml
organizations:
  - id: org-a
    modbus:
      host: 127.0.0.1
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if cfg.ModbusRegisterMap != MapHolding {
		t.Fatalf("unexpected map: %q", cfg.ModbusRegisterMap)
	}
	got := cfg.Organizations[0]
	if got.PollInterval != 15*time.Second {
		t.Fatalf("unexpected poll interval: %v", got.PollInterval)
	}
	if got.Modbus.Port != 502 || got.Modbus.UnitID != 99 {
		t.Fatalf("unexpected defaults: port=%d unit_id=%d", got.Modbus.Port, got.Modbus.UnitID)
	}
	if got.Modbus.ConnectTimeout != 5*time.Second || got.Modbus.RequestTimeout != 5*time.Second {
		t.Fatalf("unexpected timeouts: connect=%v request=%v", got.Modbus.ConnectTimeout, got.Modbus.RequestTimeout)
	}
}

func TestLoadAppliesOREEDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := `register_catalog: registers/huawei_smartlogger.yaml
organizations:
  - id: org-a
    modbus:
      host: 127.0.0.1
oree:
  enabled: true
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	o := cfg.OREE
	if o.BaseURL != "https://www.oree.com.ua" {
		t.Fatalf("base_url default: %q", o.BaseURL)
	}
	if o.Zone != 2 {
		t.Fatalf("zone default: %d", o.Zone)
	}
	if o.RunAt != "15:30" {
		t.Fatalf("run_at default: %q", o.RunAt)
	}
	if o.Timezone != "Europe/Kyiv" {
		t.Fatalf("timezone default: %q", o.Timezone)
	}
	if o.HTTPTimeout != 30*time.Second {
		t.Fatalf("http_timeout default: %v", o.HTTPTimeout)
	}
	if o.Retry.Attempts != 5 || o.Retry.Backoff != 5*time.Minute {
		t.Fatalf("retry defaults: %+v", o.Retry)
	}
}

func TestLoadRejectsBadOREE(t *testing.T) {
	dir := t.TempDir()
	cases := map[string]string{
		"bad zone": `oree:
  zone: -1
`,
		"bad run_at": `oree:
  run_at: "29:99"
`,
		"bad timezone": `oree:
  timezone: "Mars/Olympus"
`,
		"bad offset": `oree:
  delivery_offset_days: 999
`,
	}
	for name, oree := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(dir, name+".yaml")
			content := `register_catalog: registers/huawei_smartlogger.yaml
organizations:
  - id: org-a
    modbus:
      host: 127.0.0.1
` + oree
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(path); err == nil {
				t.Fatal("expected validation error, got nil")
			}
		})
	}
}

func TestParseRunAt(t *testing.T) {
	cases := []struct {
		in     string
		hour   int
		minute int
		ok     bool
	}{
		{"14:00", 14, 0, true},
		{"00:00", 0, 0, true},
		{"23:59", 23, 59, true},
		{"7:30", 7, 30, true},
		{"24:00", 0, 0, false},
		{"12:60", 0, 0, false},
		{"abc", 0, 0, false},
		{"", 0, 0, false},
	}
	for _, c := range cases {
		h, m, err := ParseRunAt(c.in)
		if c.ok {
			if err != nil {
				t.Fatalf("ParseRunAt(%q) unexpected err: %v", c.in, err)
			}
			if h != c.hour || m != c.minute {
				t.Fatalf("ParseRunAt(%q) = %d:%d, want %d:%d", c.in, h, m, c.hour, c.minute)
			}
		} else if err == nil {
			t.Fatalf("ParseRunAt(%q) expected error", c.in)
		}
	}
}

func TestLoadParsesModbusDevices(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := `register_catalog: registers/huawei_smartlogger.yaml
organizations:
  - id: ze
    poll_interval: 1s
    modbus_devices:
      - host: 10.0.0.1
        metric_keys:
          - a
          - b
      - host: 10.0.0.2
        port: 1502
        unit_id: 7
        connect_timeout: 3s
        request_timeout: 4s
        metric_keys:
          - c
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	org := cfg.Organizations[0]
	if len(org.ModbusDevices) != 2 {
		t.Fatalf("expected 2 modbus_devices, got %d", len(org.ModbusDevices))
	}
	d0 := org.ModbusDevices[0]
	if d0.Host != "10.0.0.1" || d0.Port != 502 || d0.UnitID != 99 {
		t.Fatalf("device 0 defaults wrong: %+v", d0)
	}
	if d0.ConnectTimeout != 5*time.Second || d0.RequestTimeout != 5*time.Second {
		t.Fatalf("device 0 timeout defaults wrong: %+v", d0)
	}
	if len(d0.MetricKeys) != 2 || d0.MetricKeys[0] != "a" || d0.MetricKeys[1] != "b" {
		t.Fatalf("device 0 metric_keys wrong: %+v", d0.MetricKeys)
	}
	d1 := org.ModbusDevices[1]
	if d1.Port != 1502 || d1.UnitID != 7 || d1.ConnectTimeout != 3*time.Second || d1.RequestTimeout != 4*time.Second {
		t.Fatalf("device 1 explicit fields wrong: %+v", d1)
	}

	devices := org.Devices()
	if len(devices) != 2 || devices[0].Host != "10.0.0.1" || devices[1].Host != "10.0.0.2" {
		t.Fatalf("Devices() returned wrong slice: %+v", devices)
	}
}

func TestDevicesLegacyFallback(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := `register_catalog: registers/huawei_smartlogger.yaml
organizations:
  - id: pe
    modbus:
      host: 192.168.0.10
      port: 1502
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	devices := cfg.Organizations[0].Devices()
	if len(devices) != 1 {
		t.Fatalf("expected 1 device, got %d", len(devices))
	}
	if devices[0].Host != "192.168.0.10" || devices[0].Port != 1502 || devices[0].UnitID != 99 {
		t.Fatalf("legacy device fallback wrong: %+v", devices[0])
	}
	if len(devices[0].MetricKeys) != 0 {
		t.Fatalf("legacy device should have no metric_keys whitelist")
	}
}

func TestLoadRejectsBothModbusAndDevices(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := `register_catalog: registers/huawei_smartlogger.yaml
organizations:
  - id: bad
    modbus:
      host: 10.0.0.1
    modbus_devices:
      - host: 10.0.0.2
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected error when both modbus and modbus_devices are set")
	}
}

func TestLoadRejectsMissingHostInDevice(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := `register_catalog: registers/huawei_smartlogger.yaml
organizations:
  - id: bad
    modbus_devices:
      - port: 502
        metric_keys:
          - a
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected error when modbus_devices entry has no host")
	}
}

func TestLoadRejectsNoModbusAtAll(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := `register_catalog: registers/huawei_smartlogger.yaml
organizations:
  - id: bad
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected error when org has neither modbus nor modbus_devices")
	}
}

func TestLoadParsesLocation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := `register_catalog: registers/huawei_smartlogger.yaml
organizations:
  - id: ze
    location:
      latitude: 49.0191004
      longitude: 28.1260144
      city: "Zhmerynka"
    modbus:
      host: 127.0.0.1
  - id: demo-org
    modbus:
      host: 127.0.0.2
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	loc := cfg.Organizations[0].Location
	if loc == nil {
		t.Fatal("expected location for ze, got nil")
	}
	if loc.Latitude != 49.0191004 || loc.Longitude != 28.1260144 || loc.City != "Zhmerynka" {
		t.Fatalf("unexpected location: %+v", loc)
	}
	if cfg.Organizations[1].Location != nil {
		t.Fatalf("demo-org should have no location, got %+v", cfg.Organizations[1].Location)
	}
}

func TestLoadRejectsBadLocation(t *testing.T) {
	dir := t.TempDir()
	cases := map[string]string{
		"bad latitude": `      latitude: 95
      longitude: 0
`,
		"bad longitude": `      latitude: 0
      longitude: 200
`,
	}
	for name, loc := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(dir, name+".yaml")
			content := `register_catalog: registers/huawei_smartlogger.yaml
organizations:
  - id: org-a
    location:
` + loc + `    modbus:
      host: 127.0.0.1
`
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(path); err == nil {
				t.Fatalf("expected validation error, got nil")
			}
		})
	}
}

func TestLoadParsesEssDischargeSign(t *testing.T) {
	dir := t.TempDir()
	cases := map[string]struct {
		raw       string
		want      int
		expectErr bool
	}{
		"omitted defaults to 0": {"", 0, false},
		"explicit 1":            {"    ess_discharge_sign: 1\n", 1, false},
		"explicit -1":           {"    ess_discharge_sign: -1\n", -1, false},
		"invalid 2 rejected":    {"    ess_discharge_sign: 2\n", 0, true},
		"invalid -2 rejected":   {"    ess_discharge_sign: -2\n", 0, true},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(dir, name+".yaml")
			content := `register_catalog: registers/huawei_smartlogger.yaml
organizations:
  - id: org-a
    modbus:
      host: 127.0.0.1
` + c.raw
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
			cfg, err := Load(path)
			if c.expectErr {
				if err == nil {
					t.Fatalf("expected validation error for %s", name)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load error: %v", err)
			}
			if got := cfg.Organizations[0].EssDischargeSign; got != c.want {
				t.Errorf("EssDischargeSign: got %d want %d", got, c.want)
			}
		})
	}
}

func TestLoadRejectsDuplicateOrgID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := `register_catalog: registers/huawei_smartlogger.yaml
organizations:
  - id: org-a
    modbus:
      host: 127.0.0.1
  - id: org-a
    modbus:
      host: 127.0.0.2
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Load(path); err == nil {
		t.Fatal("expected duplicate org id error, got nil")
	}
}
