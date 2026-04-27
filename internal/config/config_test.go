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
