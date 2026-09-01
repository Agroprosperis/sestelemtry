package edge

import (
	"context"
	"encoding/binary"
	"log/slog"
	"testing"
	"time"
)

func TestInverterRegisterBase(t *testing.T) {
	// encombi §3.1: 51275→addr 12 … 51550→addr 23; addr 1 → 51000.
	for addr, want := range map[int]int{1: 51000, 12: 51275, 13: 51300, 23: 51550} {
		if got := inverterRegisterBase(addr); got != want {
			t.Errorf("base(%d) = %d, want %d", addr, got, want)
		}
	}
}

// invBlock builds a 25-register payload with the given offset words.
func invBlock(words map[int]uint16) []byte {
	b := make([]byte, inverterBlockQty*2)
	for off, v := range words {
		binary.BigEndian.PutUint16(b[off*2:], v)
	}
	return b
}

func TestDecodeInverterBlock(t *testing.T) {
	ts := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	b := invBlock(map[int]uint16{
		1:  41200, // P = 41.2 kW (I32 low word)
		3:  800,   // Q = 0.8 kVar
		4:  1840,  // I DC = 18.40 A
		6:  42000, // P DC = 42.0 kW (U32 low word)
		7:  12500, // insulation 12.5 MΩ
		8:  990,   // pf 0.990
		9:  0x0200,
		11: 381, // 38.1 °C
	})
	r := decodeInverterBlock(12, "INV-12", b, ts)
	if r.RegisterBase != 51275 || r.Label != "INV-12" {
		t.Fatalf("base/label wrong: %+v", r)
	}
	if r.Class != InvOnGrid || r.StatusRaw != "0x0200" {
		t.Fatalf("class = %s status=%s, want on_grid 0x0200", r.Class, r.StatusRaw)
	}
	checks := map[string][2]float64{
		"p":    {*r.PKw, 41.2},
		"q":    {*r.QKvar, 0.8},
		"idc":  {*r.IDcA, 18.4},
		"pdc":  {*r.PDcKw, 42.0},
		"iso":  {*r.InsulationMohm, 12.5},
		"pf":   {*r.Pf, 0.99},
		"temp": {*r.TempC, 38.1},
	}
	for name, pair := range checks {
		if diff := pair[0] - pair[1]; diff > 0.0001 || diff < -0.0001 {
			t.Errorf("%s = %v, want %v", name, pair[0], pair[1])
		}
	}
	if r.MajorFault != "0x0" || r.MinorFault != "0x0" || r.Warning != "0x0" {
		t.Errorf("fault words: %s/%s/%s, want 0x0", r.MajorFault, r.MinorFault, r.Warning)
	}
	if !r.PollOK || r.PollError != nil {
		t.Errorf("poll_ok=%v err=%v", r.PollOK, r.PollError)
	}
}

func TestClassifyInverter(t *testing.T) {
	cases := []struct {
		status uint16
		major  uint32
		want   string
	}{
		{0x0200, 0, InvOnGrid},
		{0x0201, 0, InvOnGrid},
		{0x0100, 0, InvStarting},
		{0x0002, 0, InvStandby},
		{0x0700, 0, InvStandby}, // немає опромінення
		{0x0300, 0, InvFault},   // unexpected shutdown
		{0x0301, 0, InvShutdown},
		{0x0200, 0x123, InvFault}, // major fault wins over on-grid status
		{0xC800, 0, InvUnknown},
	}
	for _, c := range cases {
		if got, _ := classifyInverter(c.status, c.major); got != c.want {
			t.Errorf("classify(0x%04x, %#x) = %s, want %s", c.status, c.major, got, c.want)
		}
	}
}

func TestInverterEventsOnlyOnTransition(t *testing.T) {
	s := &Service{log: slog.Default()}
	events := make(chan Event, 16)
	prev := map[int]string{}
	drain := func() []Event {
		var out []Event
		for {
			select {
			case ev := <-events:
				out = append(out, ev)
			default:
				return out
			}
		}
	}
	row := func(class string) []InverterSnapshot {
		return []InverterSnapshot{{
			DeviceAddress: 12, RegisterBase: 51275, Class: class,
			StatusRaw: "0x0300", StatusLabel: class, TS: time.Now().UTC(),
		}}
	}

	// Healthy first snapshot → no event.
	s.emitInverterTransitions(context.Background(), row(InvOnGrid), prev, events)
	if evs := drain(); len(evs) != 0 {
		t.Fatalf("healthy first snapshot emitted %v", evs)
	}
	// Enter fault → exactly one INVERTER_FAULT.
	s.emitInverterTransitions(context.Background(), row(InvFault), prev, events)
	evs := drain()
	if len(evs) != 1 || evs[0].Code != EvInverterFault || evs[0].Severity != SevAlarm {
		t.Fatalf("fault transition: %v", evs)
	}
	// Stay in fault → silence (не кожні 30 с, spec §6.2).
	s.emitInverterTransitions(context.Background(), row(InvFault), prev, events)
	if evs := drain(); len(evs) != 0 {
		t.Fatalf("repeated fault emitted %v", evs)
	}
	// fault → unreachable: both are "bad", no new event.
	s.emitInverterTransitions(context.Background(), row(InvUnreachable), prev, events)
	if evs := drain(); len(evs) != 0 {
		t.Fatalf("fault→unreachable emitted %v", evs)
	}
	// Recover → INVERTER_RECOVERED.
	s.emitInverterTransitions(context.Background(), row(InvOnGrid), prev, events)
	evs = drain()
	if len(evs) != 1 || evs[0].Code != EvInverterRecovered {
		t.Fatalf("recovery: %v", evs)
	}
}

func TestSweepInvertersMockKeepsFleetLength(t *testing.T) {
	cfg := testCfg()
	cfg.Diagnostics.Inverters = InverterDiagnostics{
		DeviceAddresses: []int{12, 13, 14},
		PollInterval:    30 * time.Second,
		Labels:          map[int]string{12: "INV-12"},
	}
	s := &Service{cfg: cfg, log: slog.Default()}
	events := make(chan Event, 64)
	dev := Device{Role: RoleAll, Host: "mock", Port: 502,
		ConnectTimeout: time.Second, RequestTimeout: time.Second}

	sess := s.sweepInverters(context.Background(), nil, dev, []int{12, 13, 14}, cfg.Diagnostics.Inverters.Labels, map[int]string{}, events)
	if sess != nil {
		_ = sess.Close()
	}
	fleet := s.lastInverters.Load()
	if fleet == nil || len(fleet.Inverters) != 3 {
		t.Fatalf("fleet length = %v, want 3", fleet)
	}
	if fleet.Inverters[0].Label != "INV-12" || fleet.Inverters[0].RegisterBase != 51275 {
		t.Fatalf("row 0 wrong: %+v", fleet.Inverters[0])
	}
	for _, r := range fleet.Inverters {
		if !r.PollOK {
			t.Fatalf("mock poll must succeed: %+v", r)
		}
	}
}

func TestSweepInvertersUnreachableKeepsRows(t *testing.T) {
	cfg := testCfg()
	cfg.Diagnostics.Inverters = InverterDiagnostics{
		DeviceAddresses: []int{12, 13},
		PollInterval:    30 * time.Second,
	}
	s := &Service{cfg: cfg, log: slog.Default()}
	events := make(chan Event, 64)
	// TEST-NET-3 is unroutable; the dial fails (or times out in 200 ms)
	// and the cached error fills every remaining row.
	dev := Device{Role: RoleAll, Host: "203.0.113.1", Port: 502,
		ConnectTimeout: 200 * time.Millisecond, RequestTimeout: 200 * time.Millisecond}

	sess := s.sweepInverters(context.Background(), nil, dev, []int{12, 13}, nil, map[int]string{}, events)
	if sess != nil {
		_ = sess.Close()
	}
	fleet := s.lastInverters.Load()
	if fleet == nil || len(fleet.Inverters) != 2 {
		t.Fatalf("fleet length = %v, want 2 rows even when unreachable", fleet)
	}
	for _, r := range fleet.Inverters {
		if r.Class != InvUnreachable || r.PollOK || r.PollError == nil {
			t.Fatalf("row must be unreachable with error: %+v", r)
		}
		if r.PKw != nil {
			t.Fatalf("numeric fields must stay nil when unreachable: %+v", r)
		}
	}
}
