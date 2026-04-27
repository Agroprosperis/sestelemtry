package main

import (
	"context"
	"io"
	"log/slog"
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
		ID: "org-a",
		Modbus: config.Modbus{
			RequestTimeout: time.Second,
		},
		SiteID:   "site-1",
		DeviceID: "dev-1",
	}
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
	err := pollAndStore(context.Background(), logger, cfg, org, reader, resolved, chunks, nil)
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
	org := config.Organization{ID: "org-a", Modbus: config.Modbus{RequestTimeout: time.Second}}
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
	if err := pollAndStore(context.Background(), logger, cfg, org, reader, resolved, chunks, nil); err != nil {
		t.Fatalf("pollAndStore error: %v", err)
	}
	if reader.holdingCalls != 0 || reader.inputCalls != 1 {
		t.Fatalf("unexpected calls: holding=%d input=%d", reader.holdingCalls, reader.inputCalls)
	}
}

func TestPollAndStoreMissingSlice(t *testing.T) {
	cfg := &config.Root{ModbusRegisterMap: config.MapHolding}
	org := config.Organization{ID: "org-a", Modbus: config.Modbus{RequestTimeout: time.Second}}
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
	err := pollAndStore(context.Background(), logger, cfg, org, reader, resolved, chunks, nil)
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

func TestReconnectWaitWithBackoff(t *testing.T) {
	org := config.Organization{
		PollInterval: 5 * time.Second,
		Modbus: config.Modbus{
			ReconnectBackoff:    true,
			MaxReconnectBackoff: 20 * time.Second,
		},
	}
	if got := reconnectWait(org, 1); got != 5*time.Second {
		t.Fatalf("attempt 1 wait mismatch: %s", got)
	}
	if got := reconnectWait(org, 2); got != 10*time.Second {
		t.Fatalf("attempt 2 wait mismatch: %s", got)
	}
	if got := reconnectWait(org, 4); got != 20*time.Second {
		t.Fatalf("attempt 4 should cap at max, got %s", got)
	}
}
