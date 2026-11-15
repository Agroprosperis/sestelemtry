package main

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nesh/sestelemetry/internal/config"
	"github.com/nesh/sestelemetry/internal/inventory"
	"github.com/nesh/sestelemetry/internal/modbusclient"
	"github.com/nesh/sestelemetry/internal/registers"
)

// The plant passport is read from the SmartLoggers only: the Encombi is
// never dialled for it.
func TestPollOrgInventorySkipsOwnCatalogDevices(t *testing.T) {
	var dialed []string
	prevDial := dialFunc
	dialFunc = func(ctx context.Context, target modbusclient.DialTarget) (*modbusclient.Session, error) {
		dialed = append(dialed, target.Host)
		return modbusclient.Dial(ctx, modbusclient.DialTarget{Host: "mock"})
	}
	t.Cleanup(func() { dialFunc = prevDial })
	prevInsert := insertPlantInventory
	insertPlantInventory = func(context.Context, *pgxpool.Pool, inventory.Snapshot) error { return nil }
	t.Cleanup(func() { insertPlantInventory = prevInsert })

	org := config.Organization{ID: "ke", ModbusDevices: []config.ModbusDevice{
		{Modbus: config.Modbus{Host: "10.24.40.238", RequestTimeout: time.Second}},
		{Modbus: config.Modbus{Host: "10.24.40.239", RequestTimeout: time.Second}, RegisterCatalog: "registers/encombi_ecpvx.yaml"},
	}}
	cat, err := registers.Load("../../registers/huawei_smartlogger.yaml")
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := cat.Resolve(0)
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	_ = pollOrgInventory(context.Background(), logger, &config.Root{ModbusRegisterMap: config.MapHolding}, org, resolved, nil, inventory.PollReasonStartup)
	if len(dialed) != 1 || dialed[0] != "10.24.40.238" {
		t.Fatalf("dialled %v, want the SmartLogger only", dialed)
	}
}

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
