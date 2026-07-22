package main

import (
	"testing"

	"github.com/nesh/sestelemetry/internal/inventory"
	"github.com/nesh/sestelemetry/internal/registers"
)

func TestExcludeInventoryKeys(t *testing.T) {
	in := []registers.ResolvedEntry{
		{Entry: registers.Entry{MetricKey: "soc_percent"}},
		{Entry: registers.Entry{MetricKey: inventory.MetricPVRatedKw}},
		{Entry: registers.Entry{MetricKey: "active_pv_power_kw"}},
		{Entry: registers.Entry{MetricKey: inventory.MetricESSCount}},
	}
	out := excludeInventoryKeys(in)
	if len(out) != 2 {
		t.Fatalf("got %d entries: %#v", len(out), out)
	}
	if out[0].MetricKey != "soc_percent" || out[1].MetricKey != "active_pv_power_kw" {
		t.Fatalf("unexpected: %#v", out)
	}
}
