package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nesh/sestelemetry/internal/config"
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
	err := pollAndStore(context.Background(), logger, cfg, org, dev, reader, resolved, chunks, nil)
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
	if err := pollAndStore(context.Background(), logger, cfg, org, dev, reader, resolved, chunks, nil); err != nil {
		t.Fatalf("pollAndStore error: %v", err)
	}
	if reader.holdingCalls != 0 || reader.inputCalls != 1 {
		t.Fatalf("unexpected calls: holding=%d input=%d", reader.holdingCalls, reader.inputCalls)
	}
}

// A meter that is not answering reads as INT32_MAX; that sample is
// dropped, the others of the poll are stored.
func TestPollAndStoreDropsInvalidReadings(t *testing.T) {
	var got []storage.Sample
	prevInsert := insertSamples
	insertSamples = func(_ context.Context, _ *pgxpool.Pool, samples []storage.Sample) error {
		got = append([]storage.Sample(nil), samples...)
		return nil
	}
	t.Cleanup(func() { insertSamples = prevInsert })

	cfg := &config.Root{ModbusRegisterMap: config.MapHolding}
	org := config.Organization{ID: "ke"}
	dev := config.ModbusDevice{Modbus: config.Modbus{RequestTimeout: time.Second}}
	resolved := []registers.ResolvedEntry{
		{Entry: registers.Entry{MetricKey: "soc_percent", DataType: registers.DTUint16, Gain: 0.1}, PDUStart: 0, WordCount: 1, PDUEnd: 0},
		{Entry: registers.Entry{MetricKey: "grid_connected_active_power_kw", DataType: registers.DTInt32, Gain: 0.001}, PDUStart: 1, WordCount: 2, PDUEnd: 2},
	}
	chunks := []modbusclient.ReadChunk{{Start: 0, Quantity: 3}}
	reader := &mockModbusReader{holding: map[uint16][]byte{0: {0x03, 0x5c, 0x7f, 0xff, 0xff, 0xff}}}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := pollAndStore(context.Background(), logger, cfg, org, dev, reader, resolved, chunks, nil); err != nil {
		t.Fatalf("pollAndStore error: %v", err)
	}
	if len(got) != 1 || got[0].MetricKey != "soc_percent" {
		t.Fatalf("samples = %+v", got)
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
	err := pollAndStore(context.Background(), logger, cfg, org, dev, reader, resolved, chunks, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

type failingModbusReader struct{ err error }

func (f failingModbusReader) ReadHolding(context.Context, uint16, uint16) ([]byte, error) {
	return nil, f.err
}

func (f failingModbusReader) ReadInput(context.Context, uint16, uint16) ([]byte, error) {
	return nil, f.err
}

// refusingReader answers the starts it holds and refuses the rest, the
// way a firmware answers a register it does not know.
type refusingReader struct{ holding map[uint16][]byte }

func (r refusingReader) ReadHolding(_ context.Context, start, _ uint16) ([]byte, error) {
	if b, ok := r.holding[start]; ok {
		return b, nil
	}
	return nil, errors.New("modbus exception 2: illegal data address")
}

func (r refusingReader) ReadInput(ctx context.Context, start, quantity uint16) ([]byte, error) {
	return r.ReadHolding(ctx, start, quantity)
}

func TestOptionalReads(t *testing.T) {
	all := []registers.ResolvedEntry{
		{Entry: registers.Entry{MetricKey: "soc_percent", DataType: registers.DTUint16}, PDUStart: 0, WordCount: 1},
		{Entry: registers.Entry{MetricKey: "grid_line_voltage_ab_v", DataType: registers.DTUint16, Gain: 0.1, Scope: registers.ScopeEdge}, PDUStart: 10, WordCount: 1, PDUEnd: 10},
		{Entry: registers.Entry{MetricKey: "grid_line_voltage_bc_v", DataType: registers.DTUint16, Gain: 0.1, Scope: registers.ScopeEdge}, PDUStart: 11, WordCount: 1, PDUEnd: 11},
		{Entry: registers.Entry{MetricKey: "grid_line_voltage_ca_v", DataType: registers.DTUint16, Gain: 0.1, Scope: registers.ScopeEdge}, PDUStart: 12, WordCount: 1, PDUEnd: 12},
		{Entry: registers.Entry{MetricKey: "array_soc_percent", DataType: registers.DTUint16, Gain: 0.1, Scope: registers.ScopeEdge}, PDUStart: 20, WordCount: 1, PDUEnd: 20},
	}
	if reads, err := optionalReadsFor(config.ModbusDevice{MetricKeys: []string{"soc_percent"}}, all); err != nil || len(reads) != 0 {
		t.Fatal("a whitelisted device reads only what it names")
	}
	reads, err := optionalReadsFor(config.ModbusDevice{}, []registers.ResolvedEntry{all[0], all[4]})
	if err == nil || len(reads) != 1 || reads[0].entries[0].MetricKey != "array_soc_percent" {
		t.Fatalf("a catalog without the voltages must say so and keep array SOC: %+v, %v", reads, err)
	}
	reads, err = optionalReadsFor(config.ModbusDevice{}, all)
	if err != nil || len(reads) != 2 || optionalMetricCount(reads) != 4 {
		t.Fatalf("optional reads = %+v, %v", reads, err)
	}
	volts, soc := reads[0], reads[1]
	if len(volts.chunks) != 1 || len(soc.chunks) != 1 {
		t.Fatalf("chunks: voltages %+v, array SOC %+v", volts.chunks, soc.chunks)
	}

	var got []storage.Sample
	prevInsert := insertSamples
	insertSamples = func(_ context.Context, _ *pgxpool.Pool, samples []storage.Sample) error {
		got = append(got, samples...)
		return nil
	}
	t.Cleanup(func() { insertSamples = prevInsert })
	cfg := &config.Root{ModbusRegisterMap: config.MapHolding}
	org := config.Organization{ID: "de"}
	dev := config.ModbusDevice{Modbus: config.Modbus{RequestTimeout: time.Second}}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	voltages := []byte{0x0f, 0xbe, 0x0f, 0xc8, 0x0f, 0xb4}

	reader := &mockModbusReader{holding: map[uint16][]byte{10: voltages, 20: {0x03, 0x84}}}
	if !pollOptionalDue(context.Background(), logger, cfg, org, dev, reader, reads, nil) {
		t.Fatal("good reads keep the session")
	}
	if len(got) != 4 || got[0].Value != 403 || got[1].Value != 404 || got[2].Value != 402 ||
		got[3].MetricKey != "array_soc_percent" || got[3].Value != 90 {
		t.Fatalf("samples = %+v", got)
	}
	now := time.Now()
	if volts.due(now) || volts.due(now.Add(volts.every-time.Second)) {
		t.Fatal("the voltages wait their interval")
	}
	if !soc.due(now) {
		t.Fatal("array SOC is read on every poll")
	}

	// A firmware that refuses 40527 pauses only array SOC; the voltages
	// are still stored and keep their own schedule.
	volts.next, soc.next = time.Time{}, time.Time{}
	got = nil
	refusing := refusingReader{holding: map[uint16][]byte{10: voltages}}
	if !pollOptionalDue(context.Background(), logger, cfg, org, dev, refusing, reads, nil) {
		t.Fatal("a refusal keeps the session")
	}
	if len(got) != 3 {
		t.Fatalf("the voltages must still be stored, got %+v", got)
	}
	if soc.due(time.Now().Add(optionalBackoff - time.Minute)) {
		t.Fatal("a refusal pauses array SOC")
	}
	if !volts.due(time.Now().Add(volts.every)) {
		t.Fatal("the voltages keep their own schedule")
	}

	// A timeout asks for a redial.
	soc.next = time.Time{}
	if pollOptional(context.Background(), logger, cfg, org, dev, failingModbusReader{err: context.DeadlineExceeded}, soc, nil) {
		t.Fatal("a timeout redials the session")
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
