package main

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nesh/sestelemetry/internal/config"
	"github.com/nesh/sestelemetry/internal/energyflow"
	"github.com/nesh/sestelemetry/internal/modbusclient"
	"github.com/nesh/sestelemetry/internal/registers"
	"github.com/nesh/sestelemetry/internal/storage"
)

type mockModbusReader struct {
	holdingCalls int
	inputCalls   int
	holding      map[uint16][]byte
	input        map[uint16][]byte
}

func (m *mockModbusReader) ReadHolding(_ context.Context, start, _ uint16) ([]byte, error) {
	m.holdingCalls++
	return m.holding[start], nil
}

func (m *mockModbusReader) ReadInput(_ context.Context, start, _ uint16) ([]byte, error) {
	m.inputCalls++
	return m.input[start], nil
}

func TestPollAndStoreHoldingSuccess(t *testing.T) {
	var got []storage.Sample
	prevInsert := insertSamples
	insertSamples = func(_ context.Context, _ *pgxpool.Pool, samples []storage.Sample) error {
		got = append([]storage.Sample(nil), samples...)
		return nil
	}
	t.Cleanup(func() { insertSamples = prevInsert })

	cfg := &config.Root{ModbusRegisterMap: config.MapHolding}
	org := config.Organization{
		ID:       "org-a",
		SiteID:   "site-1",
		DeviceID: "dev-1",
	}
	dev := config.ModbusDevice{Modbus: config.Modbus{RequestTimeout: time.Second}}
	resolved := []registers.ResolvedEntry{
		{
			Entry: registers.Entry{
				MetricKey: "soc_percent",
				DataType:  registers.DTUint16,
				Gain:      0.1,
			},
			PDUStart:  0,
			WordCount: 1,
			PDUEnd:    0,
		},
		{
			Entry: registers.Entry{
				MetricKey: "grid_connected_active_power_kw",
				DataType:  registers.DTInt32,
				Gain:      0.001,
			},
			PDUStart:  1,
			WordCount: 2,
			PDUEnd:    2,
		},
	}
	chunks := []modbusclient.ReadChunk{{Start: 0, Quantity: 3}}

	// Bytes: UINT16(860) + INT32(-1120)
	reader := &mockModbusReader{
		holding: map[uint16][]byte{
			0: {0x03, 0x5c, 0xff, 0xff, 0xfb, 0xa0},
		},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	err := pollAndStore(context.Background(), logger, cfg, org, dev, reader, resolved, chunks, nil, nil, energyflow.RoleNone)
	if err != nil {
		t.Fatalf("pollAndStore error: %v", err)
	}
	if reader.holdingCalls != 1 || reader.inputCalls != 0 {
		t.Fatalf("unexpected calls: holding=%d input=%d", reader.holdingCalls, reader.inputCalls)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 samples, got %d", len(got))
	}
	if got[0].MetricKey != "soc_percent" || got[0].Value != 86 {
		t.Fatalf("unexpected sample 0: %+v", got[0])
	}
	if got[1].MetricKey != "grid_connected_active_power_kw" || got[1].Value != -1.12 {
		t.Fatalf("unexpected sample 1: %+v", got[1])
	}
	if got[0].OrganizationID != "org-a" || got[0].Labels["site_id"] != "site-1" || got[0].Labels["device_id"] != "dev-1" {
		t.Fatalf("unexpected labels/org: %+v", got[0])
	}
}

func TestPollAndStoreUsesInputMap(t *testing.T) {
	prevInsert := insertSamples
	insertSamples = func(_ context.Context, _ *pgxpool.Pool, _ []storage.Sample) error { return nil }
	t.Cleanup(func() { insertSamples = prevInsert })

	cfg := &config.Root{ModbusRegisterMap: config.MapInput}
	org := config.Organization{ID: "org-a"}
	dev := config.ModbusDevice{Modbus: config.Modbus{RequestTimeout: time.Second}}
	resolved := []registers.ResolvedEntry{
		{
			Entry:     registers.Entry{MetricKey: "soc_percent", DataType: registers.DTUint16, Gain: 0.1},
			PDUStart:  0,
			WordCount: 1,
			PDUEnd:    0,
		},
	}
	chunks := []modbusclient.ReadChunk{{Start: 0, Quantity: 1}}
	reader := &mockModbusReader{input: map[uint16][]byte{0: {0x03, 0x5c}}}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := pollAndStore(context.Background(), logger, cfg, org, dev, reader, resolved, chunks, nil, nil, energyflow.RoleNone); err != nil {
		t.Fatalf("pollAndStore error: %v", err)
	}
	if reader.holdingCalls != 0 || reader.inputCalls != 1 {
		t.Fatalf("unexpected calls: holding=%d input=%d", reader.holdingCalls, reader.inputCalls)
	}
}

func TestPollAndStoreMissingSlice(t *testing.T) {
	cfg := &config.Root{ModbusRegisterMap: config.MapHolding}
	org := config.Organization{ID: "org-a"}
	dev := config.ModbusDevice{Modbus: config.Modbus{RequestTimeout: time.Second}}
	resolved := []registers.ResolvedEntry{
		{
			Entry:     registers.Entry{MetricKey: "soc_percent", DataType: registers.DTUint16, Gain: 0.1},
			PDUStart:  4,
			WordCount: 1,
			PDUEnd:    4,
		},
	}
	chunks := []modbusclient.ReadChunk{{Start: 0, Quantity: 1}}
	reader := &mockModbusReader{holding: map[uint16][]byte{0: {0x00, 0x01}}}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	err := pollAndStore(context.Background(), logger, cfg, org, dev, reader, resolved, chunks, nil, nil, energyflow.RoleNone)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestReadBudgetForPoll(t *testing.T) {
	got := readBudgetForPoll(2*time.Second, 3)
	if got != 8*time.Second {
		t.Fatalf("expected 8s read budget, got %s", got)
	}
}

// TestRunOrgFanOutPerDevice verifies that an organization with two
// modbus_devices spawns a goroutine per device, dials each device once,
// and writes only the per-device whitelisted metric_keys.
func TestRunOrgFanOutPerDevice(t *testing.T) {
	dialed := make(map[string]int)
	var dialCount int32
	var dialMu sync.Mutex
	prevDial := dialFunc
	dialFunc = func(ctx context.Context, target modbusclient.DialTarget) (*modbusclient.Session, error) {
		dialMu.Lock()
		dialed[target.Host]++
		dialMu.Unlock()
		atomic.AddInt32(&dialCount, 1)
		// Route every dial to the in-process mock session inside
		// modbusclient so we don't touch the network.
		return modbusclient.Dial(ctx, modbusclient.DialTarget{
			Host:           "mock",
			Port:           target.Port,
			UnitID:         target.UnitID,
			ConnectTimeout: target.ConnectTimeout,
			RequestTimeout: target.RequestTimeout,
		})
	}
	t.Cleanup(func() { dialFunc = prevDial })

	var (
		mu      sync.Mutex
		samples []storage.Sample
		seenA   bool
		seenC   bool
	)
	bothSeen := make(chan struct{})
	var bothOnce sync.Once
	prevInsert := insertSamples
	insertSamples = func(_ context.Context, _ *pgxpool.Pool, batch []storage.Sample) error {
		mu.Lock()
		defer mu.Unlock()
		samples = append(samples, batch...)
		for _, s := range batch {
			switch s.MetricKey {
			case "a":
				seenA = true
			case "c":
				seenC = true
			}
		}
		if seenA && seenC {
			bothOnce.Do(func() { close(bothSeen) })
		}
		return nil
	}
	t.Cleanup(func() { insertSamples = prevInsert })

	cfg := &config.Root{ModbusRegisterMap: config.MapHolding}
	org := config.Organization{
		ID:           "ze",
		PollInterval: 5 * time.Millisecond,
		ModbusDevices: []config.ModbusDevice{
			{
				Modbus:     config.Modbus{Host: "10.0.0.1", RequestTimeout: time.Second, ConnectTimeout: time.Second},
				MetricKeys: []string{"a", "b"},
			},
			{
				Modbus:     config.Modbus{Host: "10.0.0.2", RequestTimeout: time.Second, ConnectTimeout: time.Second},
				MetricKeys: []string{"c"},
			},
		},
	}
	resolved := []registers.ResolvedEntry{
		{Entry: registers.Entry{MetricKey: "a", DataType: registers.DTUint16, Gain: 1}, PDUStart: 0, WordCount: 1, PDUEnd: 0},
		{Entry: registers.Entry{MetricKey: "b", DataType: registers.DTUint16, Gain: 1}, PDUStart: 1, WordCount: 1, PDUEnd: 1},
		{Entry: registers.Entry{MetricKey: "c", DataType: registers.DTUint16, Gain: 1}, PDUStart: 0, WordCount: 1, PDUEnd: 0},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	doneRun := make(chan struct{})
	go func() {
		runOrg(ctx, logger, cfg, org, resolved, nil)
		close(doneRun)
	}()

	select {
	case <-bothSeen:
	case <-ctx.Done():
		t.Fatal("timed out waiting for samples from both devices")
	}
	cancel()
	<-doneRun

	if dc := atomic.LoadInt32(&dialCount); dc < 2 {
		t.Fatalf("expected at least 2 Dial calls (one per device), got %d", dc)
	}
	dialMu.Lock()
	if dialed["10.0.0.1"] == 0 || dialed["10.0.0.2"] == 0 {
		t.Fatalf("expected both device hosts to be dialed, got %+v", dialed)
	}
	dialMu.Unlock()

	mu.Lock()
	defer mu.Unlock()
	keys := map[string]int{}
	for _, s := range samples {
		keys[s.MetricKey]++
		if s.OrganizationID != "ze" {
			t.Fatalf("sample written for wrong org: %+v", s)
		}
	}
	if keys["a"] == 0 || keys["b"] == 0 {
		t.Fatalf("device 1 should produce samples for both a and b: %+v", keys)
	}
	if keys["c"] == 0 {
		t.Fatalf("device 2 should produce samples for c: %+v", keys)
	}
}

// TestPollAndStoreFeedsEnergyFlow verifies that decoded samples are
// forwarded to the energy-flow aggregator when the device has a role.
// We use a single SmartLogger (RoleSingle) carrying a minimal
// accumulator set, run two polls one window apart and assert the
// aggregator emits a sample on the second flush.
func TestPollAndStoreFeedsEnergyFlow(t *testing.T) {
	prevInsert := insertSamples
	insertSamples = func(_ context.Context, _ *pgxpool.Pool, _ []storage.Sample) error { return nil }
	t.Cleanup(func() { insertSamples = prevInsert })

	cfg := &config.Root{ModbusRegisterMap: config.MapHolding}
	org := config.Organization{ID: "org-a"}
	dev := config.ModbusDevice{Modbus: config.Modbus{RequestTimeout: time.Second}}

	resolved := []registers.ResolvedEntry{
		{Entry: registers.Entry{MetricKey: energyflow.SrcAccumulatedPVYieldKwh, DataType: registers.DTInt64, Gain: 0.01}, PDUStart: 0, WordCount: 4, PDUEnd: 3},
		{Entry: registers.Entry{MetricKey: energyflow.SrcAccumulatedPurchasedKwh, DataType: registers.DTInt64, Gain: 0.01}, PDUStart: 4, WordCount: 4, PDUEnd: 7},
		{Entry: registers.Entry{MetricKey: energyflow.SrcAccumulatedSoldKwh, DataType: registers.DTInt64, Gain: 0.01}, PDUStart: 8, WordCount: 4, PDUEnd: 11},
		{Entry: registers.Entry{MetricKey: energyflow.SrcTotalEssChargedKwh, DataType: registers.DTInt64, Gain: 0.01}, PDUStart: 12, WordCount: 4, PDUEnd: 15},
		{Entry: registers.Entry{MetricKey: energyflow.SrcTotalEssDischargedKwh, DataType: registers.DTUint64, Gain: 0.01}, PDUStart: 16, WordCount: 4, PDUEnd: 19},
	}
	chunks := []modbusclient.ReadChunk{{Start: 0, Quantity: 20}}

	// Fixed payloads encoding 0/0/0/0/0 and then 100/0/0/0/200
	// (raw register values; gain 0.01 → 1.0 kWh / 2.0 kWh).
	zero := make([]byte, 40)
	first := make([]byte, 40)
	first[7] = 100  // accumulated_pv_energy_yield raw = 100 → 1.0 kWh
	first[39] = 200 // total_energy_discharged raw = 200 → 2.0 kWh

	reader := &mockModbusReader{holding: map[uint16][]byte{0: zero}}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	emitted := make(chan []energyflow.EmittedSample, 4)
	emit := func(_ context.Context, samples []energyflow.EmittedSample) error {
		emitted <- samples
		return nil
	}
	agg := energyflow.New(org.ID, energyflow.Options{
		AllocationWindowSeconds: 1,
		MaxGapSeconds:           5,
	}, emit, logger)

	if err := pollAndStore(context.Background(), logger, cfg, org, dev, reader, resolved, chunks, nil, agg, energyflow.RoleSingle); err != nil {
		t.Fatalf("pollAndStore err: %v", err)
	}
	if err := agg.Flush(context.Background()); err != nil {
		t.Fatalf("first flush err: %v", err)
	}

	reader.holding[0] = first
	if err := pollAndStore(context.Background(), logger, cfg, org, dev, reader, resolved, chunks, nil, agg, energyflow.RoleSingle); err != nil {
		t.Fatalf("pollAndStore err: %v", err)
	}
	if err := agg.Flush(context.Background()); err != nil {
		t.Fatalf("second flush err: %v", err)
	}

	select {
	case got := <-emitted:
		if len(got) != len(energyflow.SyntheticMetricKeys) {
			t.Fatalf("emitted %d samples, want %d", len(got), len(energyflow.SyntheticMetricKeys))
		}
	case <-time.After(time.Second):
		t.Fatal("no samples emitted by aggregator")
	}
}

func TestReconnectWaitWithBackoff(t *testing.T) {
	org := config.Organization{PollInterval: 5 * time.Second}
	dev := config.ModbusDevice{
		Modbus: config.Modbus{
			ReconnectBackoff:    true,
			MaxReconnectBackoff: 20 * time.Second,
		},
	}
	if got := reconnectWait(org, dev, 1); got != 5*time.Second {
		t.Fatalf("attempt 1 wait mismatch: %s", got)
	}
	if got := reconnectWait(org, dev, 2); got != 10*time.Second {
		t.Fatalf("attempt 2 wait mismatch: %s", got)
	}
	if got := reconnectWait(org, dev, 4); got != 20*time.Second {
		t.Fatalf("attempt 4 should cap at max, got %s", got)
	}
}
