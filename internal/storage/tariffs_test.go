package storage

import (
	"context"
	"encoding/json"
	"testing"
)

func TestInitTariffsSchemaRequiresPool(t *testing.T) {
	if err := InitTariffsSchema(context.Background(), nil); err == nil {
		t.Fatal("expected error for nil pool")
	}
}

func TestGetOrgTariffsRequiresPool(t *testing.T) {
	if _, _, err := GetOrgTariffs(context.Background(), nil, "ze"); err == nil {
		t.Fatal("expected error for nil pool")
	}
}

func TestGetOrgTariffsRequiresOrgID(t *testing.T) {
	// Empty org id is rejected before we reach the pool, so a nil
	// pool is fine here — the guard fires first.
	if _, _, err := GetOrgTariffs(context.Background(), nil, ""); err == nil {
		t.Fatal("expected error for empty organization_id")
	}
}

func TestUpsertOrgTariffsRequiresPool(t *testing.T) {
	payload, _ := json.Marshal(map[string]float64{"vat_rate": 0.2})
	if err := UpsertOrgTariffs(context.Background(), nil, "ze", payload); err == nil {
		t.Fatal("expected error for nil pool")
	}
}

func TestUpsertOrgTariffsRejectsEmptyPayload(t *testing.T) {
	// Empty payloads short-circuit before the pool, mirroring the
	// org-id guard. This keeps callers from accidentally clobbering
	// an existing row with a JSONB `null` literal.
	if err := UpsertOrgTariffs(context.Background(), nil, "ze", nil); err == nil {
		t.Fatal("expected error for empty payload")
	}
}

func TestUpsertOrgTariffsRequiresOrgID(t *testing.T) {
	payload, _ := json.Marshal(map[string]float64{"vat_rate": 0.2})
	if err := UpsertOrgTariffs(context.Background(), nil, "", payload); err == nil {
		t.Fatal("expected error for empty organization_id")
	}
}
