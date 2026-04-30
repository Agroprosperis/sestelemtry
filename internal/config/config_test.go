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
	if o.RunAt != "14:00" {
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
		in       string
		hour     int
		minute   int
		ok       bool
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
