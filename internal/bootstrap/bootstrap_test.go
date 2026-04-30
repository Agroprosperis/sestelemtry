package bootstrap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
