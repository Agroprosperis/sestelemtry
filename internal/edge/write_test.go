package edge

import (
	"context"
	"encoding/binary"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// fakeLogger records writes and answers readbacks from what it holds.
type fakeLogger struct {
	mu       sync.Mutex
	regs     map[uint16][]byte
	writes   []uint16
	readback func(addr uint16, written []byte) []byte // nil = echo
	writeErr error
	closed   int
}

func newFakeLogger() *fakeLogger { return &fakeLogger{regs: map[uint16][]byte{}} }

func (f *fakeLogger) WriteMultiple(_ context.Context, start uint16, payload []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes = append(f.writes, start)
	if f.writeErr != nil {
		return f.writeErr
	}
	f.regs[start] = append([]byte(nil), payload...)
	return nil
}

func (f *fakeLogger) ReadHolding(_ context.Context, start, _ uint16) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b := f.regs[start]
	if f.readback != nil {
		return f.readback(start, b), nil
	}
	if b == nil {
		return make([]byte, 4), nil
	}
	return b, nil
}

func (f *fakeLogger) Close() error {
	f.mu.Lock()
	f.closed++
	f.mu.Unlock()
	return nil
}

func (f *fakeLogger) written() []uint16 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]uint16(nil), f.writes...)
}

func zeCfg(t *testing.T) *Config {
	cfg := &Config{
		SiteID:   "ze",
		Timezone: "Europe/Kyiv",
		SmartLogger: SmartLogger{
			Topology:     TopologyDual,
			PollInterval: time.Second,
			Devices: []Device{
				{Role: RolePV, Host: "10.28.40.101", RequestTimeout: time.Second},
				{Role: RoleESS, Host: "10.28.40.102", RequestTimeout: time.Second},
			},
		},
		Edge:     EdgeIdentity{EdgeID: "iot2050-ze-test"},
		Manifest: ManifestConfig{CachePath: filepath.Join(t.TempDir(), "active_manifest.json")},
		Control:  ControlConfig{Mode: ModeShadow, Preset: PresetSelfConsumption, Interval: time.Second},
		Limits: Limits{
			Grid: GridLimits{ImportLimitKw: 1700, TargetImportKw: 1600, ExportCapKw: 850},
			Bess: BessLimits{SocMinEconomicPct: 20, SocMaxEconomicPct: 90},
		},
	}
	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func writeService(t *testing.T, cfg *Config) *Service {
	t.Helper()
	gate, _, err := OpenWriteGate(cfg.Control.Write.StatePath, testTS)
	if err != nil {
		t.Fatal(err)
	}
	bb, err := OpenBlackbox(BlackboxConfig{
		Enabled: true, DBPath: filepath.Join(t.TempDir(), "bb.db"), RetentionDays: 1, DiskCriticalPct: 95,
	}, cfg.SiteID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bb.Close() })
	return &Service{
		cfg: cfg, log: slog.Default(), bb: bb, gate: gate, ctl: NewController(cfg.Control.Write),
		writeFaults: make(chan writeFault, 8), eventLastWritten: map[string]time.Time{},
	}
}

func enableForTest(t *testing.T, s *Service) {
	t.Helper()
	if _, err := s.gate.Enable("test", testTS); err != nil {
		t.Fatal(err)
	}
	s.ctl.Start(testTS)
}

// The shipped example loads with the write section and its defaults.
func TestExampleConfigLoads(t *testing.T) {
	t.Setenv("EDGE_SL_HOST", "mock")
	t.Setenv("EDGE_CLOUD_URL", "http://127.0.0.1:18080")
	cfg, err := LoadConfig("../../config.edge.example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	w := cfg.Control.Write
	if w.InitialHold != 15*time.Minute || w.FirstCommandMaxKw != 100 || w.SocMinPct != 20 || w.SocMaxPct != 90 ||
		w.PVInterval != 2*time.Second || w.ESSInterval != 3*time.Second || w.StatePath == "" {
		t.Fatalf("write config = %+v", w)
	}
	if cfg.Limits.Grid.ExportCapKw <= 0 {
		t.Fatalf("export cap = %v", cfg.Limits.Grid.ExportCapKw)
	}
}

// The one rule that must never break: with the flag off nothing is
// written, whoever asks and whatever the command.
func TestGuardedWriteNeverWritesWithFlagOff(t *testing.T) {
	s := writeService(t, zeCfg(t))
	f := newFakeLogger()
	payload, _, _ := regESSPower.encode(-500)
	if err := s.guardedWrite(context.Background(), f, regESSPower, payload); err != errWriteOff {
		t.Fatalf("err = %v, want errWriteOff", err)
	}
	fb := s.writeOne(context.Background(), f, s.cfg.SmartLogger.Devices[1], regESSPower, -500)
	if fb.OK || len(f.written()) != 0 {
		t.Fatalf("flag off wrote: %+v, writes %v", fb, f.written())
	}
	enableForTest(t, s)
	if err := s.guardedWrite(context.Background(), f, writeReg{Addr: 40197, Name: "40197"}, payload); err != errNotWritable {
		t.Fatalf("40197 err = %v, want errNotWritable", err)
	}
	if err := s.guardedWrite(context.Background(), f, regESSPower, payload); err != nil {
		t.Fatal(err)
	}
	if got := f.written(); len(got) != 1 || got[0] != 40381 {
		t.Fatalf("writes = %v", got)
	}
}

// With the flag off a writer does not even connect.
func TestWriterIdleWhileFlagOff(t *testing.T) {
	s := writeService(t, zeCfg(t))
	dials := 0
	var mu sync.Mutex
	dial := func(context.Context, Device) (registerSession, error) {
		mu.Lock()
		dials++
		mu.Unlock()
		return newFakeLogger(), nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()
	s.runWriter(ctx, s.cfg.SmartLogger.Devices[1], regsFor(RoleESS), 10*time.Millisecond, dial)
	mu.Lock()
	defer mu.Unlock()
	if dials != 0 {
		t.Fatalf("dials = %d with the flag off", dials)
	}
}

// Each logger gets its own pair, with the cutover's encoding.
func TestWriterWritesItsOwnRegisters(t *testing.T) {
	s := writeService(t, zeCfg(t))
	enableForTest(t, s)
	s.writeCmd.Store(&WriteCommand{At: time.Now().UTC(), State: WriteRun, ESSKw: -100, PVLimitKw: 600, PVKnown: true})
	pv, ess := newFakeLogger(), newFakeLogger()
	run := func(dev Device, f *fakeLogger) {
		ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
		defer cancel()
		s.runWriter(ctx, dev, regsFor(dev.Role), 20*time.Millisecond,
			func(context.Context, Device) (registerSession, error) { return f, nil })
	}
	run(s.cfg.SmartLogger.Devices[0], pv)
	run(s.cfg.SmartLogger.Devices[1], ess)
	for _, a := range pv.written() {
		if a != 40378 && a != 40384 {
			t.Fatalf("PV logger got a write to %d", a)
		}
	}
	for _, a := range ess.written() {
		if a != 40381 && a != 40386 {
			t.Fatalf("ESS logger got a write to %d", a)
		}
	}
	if got := int32(binary.BigEndian.Uint32(ess.regs[40381])); got != -1000 {
		t.Fatalf("40381 raw = %d, want -1000 (charge 100 kW)", got)
	}
	if got := binary.BigEndian.Uint32(pv.regs[40378]); got != 6000 {
		t.Fatalf("40378 raw = %d, want 6000 (600 kW)", got)
	}
	if got := int32(binary.BigEndian.Uint32(ess.regs[40386])); got != 0 {
		t.Fatalf("40386 raw = %d, want 0", got)
	}
}

// A readback that differs is a fault, reported to the core loop.
func TestWriterReadbackMismatchFaults(t *testing.T) {
	s := writeService(t, zeCfg(t))
	enableForTest(t, s)
	s.writeCmd.Store(&WriteCommand{At: time.Now().UTC(), State: WriteRun, ESSKw: 50, PVKnown: false})
	f := newFakeLogger()
	f.readback = func(uint16, []byte) []byte { return []byte{0, 0, 0, 7} }
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	s.runWriter(ctx, s.cfg.SmartLogger.Devices[1], regsFor(RoleESS), 20*time.Millisecond,
		func(context.Context, Device) (registerSession, error) { return f, nil })
	select {
	case fault := <-s.writeFaults:
		if fault.msg == "" {
			t.Fatal("empty fault")
		}
	default:
		t.Fatal("no fault reported")
	}
	if fb, ok := s.feedback(regESSPower); !ok || fb.OK {
		t.Fatalf("feedback = %+v", fb)
	}
}

func TestWriteGateComesUpOffAfterRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "write_gate.json")
	g, ev, err := OpenWriteGate(path, testTS)
	if err != nil || ev != nil || g.Enabled() {
		t.Fatalf("fresh gate: %v %v %v", err, ev, g.Enabled())
	}
	if _, err := g.Enable("test", testTS); err != nil {
		t.Fatal(err)
	}
	g2, ev, err := OpenWriteGate(path, testTS.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if g2.Enabled() || ev == nil || ev.Code != EvWriteOffAfterReboot {
		t.Fatalf("after restart: enabled=%v event=%+v", g2.Enabled(), ev)
	}
	raw, _ := os.ReadFile(path)
	if string(raw) == "" || g2.State().Enabled {
		t.Fatalf("persisted state %s", raw)
	}
}

func controlTick(values map[string]float64) Tick {
	base := map[string]float64{
		"active_pv_power_kw": 100, "load_power_kw": 300, "grid_connected_active_power_kw": 200,
		"active_ess_power_kw": 0, "soc_percent": 50, "ess_charge_max_kw": 800, "ess_discharge_max_kw": 800,
		"pcs_shutdown": 0, "pv_rated_kw": 600,
		"sl_alarm_1": 0, "sl_alarm_2": 0, "sl_alarm_3": 0, "sl_alarm_4": 0, "sl_alarm_5": 0, "sl_alarm_6": 0,
		"meter_phase_a_voltage_v": 231, "meter_phase_b_voltage_v": 231, "meter_phase_c_voltage_v": 231,
		"pv_active_power_control_mode": 4, "ess_active_power_control_mode": 4,
	}
	for k, v := range values {
		base[k] = v
	}
	return buildTickFromValues("ze", TopologyDual, 1, 1, testTS, base, QualityOK)
}

func step(c *Controller, at time.Time, tick Tick, desired float64) (WriteCommand, []Event) {
	tick.TS = at
	return c.Step(ControlInputs{
		Now: at, Tick: tick, Decision: Decision{PBessVirtualKw: desired, ReasonCode: "test"},
		ExportCapKw: 850, ImportLimitKw: 1700,
	})
}

func TestControllerHoldsThenCapsTheFirstCommand(t *testing.T) {
	cfg := zeCfg(t).Control.Write
	c := NewController(cfg)
	c.Start(testTS)
	cmd, _ := step(c, testTS.Add(time.Minute), controlTick(nil), 300)
	if cmd.State != WriteHold || cmd.ESSKw != 0 || cmd.PVLimitKw != 600 {
		t.Fatalf("hold: %+v", cmd)
	}
	at := testTS.Add(cfg.InitialHold)
	cmd, _ = step(c, at, controlTick(nil), 200)
	if cmd.State != WriteRun || cmd.ESSKw != 100 {
		t.Fatalf("first command: %+v, want 100 kW", cmd)
	}
	// The battery follows 100 kW for 10 s: the cap lifts.
	for i := 1; i <= 12; i++ {
		cmd, _ = step(c, at.Add(time.Duration(i)*time.Second), controlTick(map[string]float64{"active_ess_power_kw": 100}), 200)
	}
	if cmd.ESSKw != 200 {
		t.Fatalf("after following: %+v, want 200 kW", cmd)
	}
}

func runController(t *testing.T) (*Controller, time.Time) {
	cfg := zeCfg(t).Control.Write
	c := NewController(cfg)
	c.Start(testTS)
	at := testTS.Add(cfg.InitialHold)
	step(c, at, controlTick(nil), 0)
	return c, at
}

func TestControllerHardSocWindow(t *testing.T) {
	c, at := runController(t)
	cmd, _ := step(c, at.Add(time.Second), controlTick(map[string]float64{"soc_percent": 19}), 50)
	if cmd.ESSKw != 0 {
		t.Fatalf("discharge at SOC 19%%: %+v", cmd)
	}
	cmd, _ = step(c, at.Add(2*time.Second), controlTick(map[string]float64{"soc_percent": 91}), -50)
	if cmd.ESSKw != 0 {
		t.Fatalf("charge at SOC 91%%: %+v", cmd)
	}
}

// Over the ceiling: first less discharge, then a lower PV ceiling.
func TestControllerExportCeiling(t *testing.T) {
	c, at := runController(t)
	c.firstConfirmed = true
	// PV 600, load 100: exporting 500 already; discharging 400 more would
	// make 900 > 850 → discharge cut to 350.
	tick := controlTick(map[string]float64{"active_pv_power_kw": 600, "load_power_kw": 100, "grid_connected_active_power_kw": -500})
	cmd, _ := step(c, at.Add(time.Second), tick, 400)
	if cmd.ESSKw != 350 || cmd.PVLimitKw != 600 {
		t.Fatalf("discharge cut: %+v", cmd)
	}
	// Exporting 900 with the battery idle: PV ceiling lowered by 50.
	tick = controlTick(map[string]float64{"active_pv_power_kw": 600, "load_power_kw": -300, "grid_connected_active_power_kw": -900})
	cmd, _ = step(c, at.Add(2*time.Second), tick, 0)
	if cmd.ESSKw != 0 || cmd.PVLimitKw != 550 {
		t.Fatalf("PV ceiling: %+v", cmd)
	}
}

func TestControllerStops(t *testing.T) {
	cases := []struct {
		name   string
		values map[string]float64
		hold   time.Duration
	}{
		{"alarm word", map[string]float64{"sl_alarm_3": 0x10}, 0},
		{"PCS shutdown", map[string]float64{"pcs_shutdown": 1}, 0},
		{"control mode", map[string]float64{"ess_active_power_control_mode": 1}, 0},
		{"low phase voltage", map[string]float64{"meter_phase_a_voltage_v": 150}, 11 * time.Second},
		{"import near the limit", map[string]float64{"grid_connected_active_power_kw": 1650}, 6 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, at := runController(t)
			var cmd WriteCommand
			var events []Event
			for d := time.Duration(0); d <= tc.hold; d += time.Second {
				var evs []Event
				cmd, evs = step(c, at.Add(time.Second+d), controlTick(tc.values), 0)
				events = append(events, evs...)
			}
			if cmd.State != WriteStopped || cmd.ESSKw != 0 || cmd.PVLimitKw != 600 {
				t.Fatalf("cmd = %+v", cmd)
			}
			if len(events) != 1 || events[0].Code != EvWriteStop {
				t.Fatalf("events = %+v", events)
			}
			// Latched: a clean reading does not resume.
			cmd, _ = step(c, at.Add(time.Minute), controlTick(nil), 200)
			if cmd.State != WriteStopped || cmd.ESSKw != 0 {
				t.Fatalf("resumed: %+v", cmd)
			}
		})
	}
	t.Run("short voltage dip", func(t *testing.T) {
		c, at := runController(t)
		cmd, _ := step(c, at.Add(time.Second), controlTick(map[string]float64{"meter_phase_a_voltage_v": 150}), 0)
		cmd, _ = step(c, at.Add(5*time.Second), controlTick(nil), 0)
		if cmd.State != WriteRun {
			t.Fatalf("a 4 s dip stopped writing: %+v", cmd)
		}
	})
}

// The battery not following the command for 30 s stops writing.
func TestControllerStopsWhenTheBatteryDoesNotFollow(t *testing.T) {
	c, at := runController(t)
	var cmd WriteCommand
	for i := 1; i <= 45; i++ {
		cmd, _ = step(c, at.Add(time.Duration(i)*time.Second), controlTick(map[string]float64{"active_ess_power_kw": 0}), 80)
	}
	if cmd.State != WriteStopped {
		t.Fatalf("cmd = %+v", cmd)
	}
}

func TestControllerLowersPVCeilingForFaultyInverters(t *testing.T) {
	c, at := runController(t)
	tick := controlTick(nil)
	tick.TS = at.Add(time.Second)
	cmd, _ := c.Step(ControlInputs{Now: tick.TS, Tick: tick, ExportCapKw: 850, ImportLimitKw: 1700, InverterCount: 12, InverterFaults: 2})
	if cmd.PVLimitKw != 500 {
		t.Fatalf("PV ceiling = %v, want 500 (10/12 of 600)", cmd.PVLimitKw)
	}
}

func TestManualSetpoints(t *testing.T) {
	tick := testTick(map[string]float64{
		"active_pv_power_kw": 200, "load_power_kw": 500, "soc_percent": 40,
		"grid_connected_active_power_kw": 300, "active_ess_power_kw": 0,
	}, QualityOK)
	until := tick.TS.Add(2 * time.Hour)
	cases := []struct {
		cmd  ManualCommand
		want float64
	}{
		{ManualCommand{Type: ManualHold}, 0},
		{ManualCommand{Type: ManualFixed, Value: 120, Direction: "charge"}, -120},
		{ManualCommand{Type: ManualFixed, Value: 120, Direction: "discharge"}, 120},
		{ManualCommand{Type: ManualCover, Value: 250}, 250},
		{ManualCommand{Type: ManualCover, Value: 400}, 300},
		{ManualCommand{Type: ManualSolar, Value: 100}, 0},
		{ManualCommand{Type: ManualExport, Value: 100}, 400},
		{ManualCommand{Type: ManualTarget, Value: 60, Until: until}, -172},
	}
	for _, tc := range cases {
		s, ok := manualSetpoint(tick, tc.cmd, 1720)
		if !ok || s.kw != tc.want {
			t.Errorf("%s %v: kw = %v (ok %v), want %v", tc.cmd.Type, tc.cmd.Value, s.kw, ok, tc.want)
		}
	}
	if _, ok := manualSetpoint(tick, ManualCommand{Type: ManualCap, Value: 400}, 1720); ok {
		t.Error("cap must fall through to the automat")
	}
}

func TestManifestManualCommandsAndSequence(t *testing.T) {
	m := arbitrageManifest(0)
	m.ManifestSeq = 10
	m.ManualCommands = []ManualCommand{
		{ID: "a", Type: ManualFixed, Value: 100, Direction: "discharge", From: testTS, Until: testTS.Add(time.Hour)},
	}
	if err := m.ValidateForEdge("ab"); err != nil {
		t.Fatal(err)
	}
	if c := m.ManualAt(testTS.Add(time.Minute)); c == nil || c.ID != "a" {
		t.Fatalf("ManualAt = %+v", c)
	}
	// While a block is in force it drives the decision, not the plan.
	d, _ := Decide(testTick(map[string]float64{
		"active_pv_power_kw": 100, "load_power_kw": 300, "soc_percent": 50,
		"ess_charge_max_kw": 300, "ess_discharge_max_kw": 300,
	}, QualityOK), m, testCfg())
	if d.ReasonCode != "manual_fixed" || d.ManualCommandID != "a" {
		t.Fatalf("decision = %s / %s", d.ReasonCode, d.ManualCommandID)
	}

	bad := *m
	bad.ManualCommands = append(append([]ManualCommand(nil), m.ManualCommands...),
		ManualCommand{ID: "b", Type: ManualHold, From: testTS.Add(30 * time.Minute), Until: testTS.Add(2 * time.Hour)})
	if err := bad.ValidateForEdge("ab"); err == nil {
		t.Error("overlapping blocks accepted")
	}
	bad.ManualCommands = []ManualCommand{{ID: "c", Type: "reboot", From: testTS, Until: testTS.Add(time.Hour)}}
	if err := bad.ValidateForEdge("ab"); err == nil {
		t.Error("unknown type accepted")
	}
	w := *m
	w.WriteEnabled = true
	if err := w.ValidateForEdge("ab"); err == nil {
		t.Error("write_enabled=true accepted from a manifest")
	}

	if err := m.AcceptAfter(10, false); err == nil {
		t.Error("same seq accepted")
	}
	if err := m.AcceptAfter(9, true); err != nil {
		t.Errorf("newer seq rejected: %v", err)
	}
	old := *m
	old.ManifestSeq = 0
	if err := old.AcceptAfter(9, true); err == nil {
		t.Error("unnumbered manifest accepted while writing")
	}
	if err := old.AcceptAfter(9, false); err != nil {
		t.Errorf("unnumbered manifest rejected in shadow: %v", err)
	}
}

func TestEnableNeedsConfirmationAndPreconditions(t *testing.T) {
	s := writeService(t, zeCfg(t))
	now := time.Now().UTC()
	tick := controlTick(nil)
	tick.TS = now
	s.lastTick.Store(&tick)
	for _, d := range s.cfg.SmartLogger.Devices {
		s.devPollOK.Store(d.Host, now.Unix())
	}
	ctx := context.Background()
	if r := s.onWriteRequest(ctx, now, writeRequest{action: "enable", by: "t", confirm: "ab"}); r.OK || s.gate.Enabled() {
		t.Fatalf("wrong confirmation enabled writing: %+v", r)
	}
	broken := controlTick(map[string]float64{"pcs_shutdown": 1})
	broken.TS = now
	s.lastTick.Store(&broken)
	if r := s.onWriteRequest(ctx, now, writeRequest{action: "enable", by: "t", confirm: "ze"}); r.OK || s.gate.Enabled() {
		t.Fatalf("PCS shutdown enabled writing: %+v", r)
	}
	s.lastTick.Store(&tick)
	r := s.onWriteRequest(ctx, now, writeRequest{action: "enable", by: "t", confirm: "ze"})
	if !r.OK || !s.gate.Enabled() || s.ctl.State() != WriteHold {
		t.Fatalf("enable: %+v, gate %v, state %s", r, s.gate.Enabled(), s.ctl.State())
	}
}

// Turning off writes the safe values, waits for the readback, then drops
// the flag; without a readback it drops it after the timeout.
func TestDisableWaitsForTheSafeReadback(t *testing.T) {
	s := writeService(t, zeCfg(t))
	enableForTest(t, s)
	now := testTS.Add(time.Hour)
	tick := controlTick(nil)
	s.ctl.BeginStopping(now, "test")
	s.stepWrite(context.Background(), now.Add(time.Second), tick, nil)
	if !s.gate.Enabled() {
		t.Fatal("flag dropped before the safe values were read back")
	}
	after := now.Add(2 * time.Second)
	s.storeFeedback(WriteFeedback{Register: "40381", At: after, Kw: 0, OK: true})
	s.storeFeedback(WriteFeedback{Register: "40386", At: after, Kw: 0, OK: true})
	s.storeFeedback(WriteFeedback{Register: "40378", At: after, Kw: 600, OK: true})
	s.storeFeedback(WriteFeedback{Register: "40384", At: after, Kw: 0, OK: true})
	s.stepWrite(context.Background(), now.Add(3*time.Second), tick, nil)
	if s.gate.Enabled() || s.ctl.State() != WriteOff {
		t.Fatalf("flag still on: %v / %s", s.gate.Enabled(), s.ctl.State())
	}

	s2 := writeService(t, zeCfg(t))
	enableForTest(t, s2)
	s2.ctl.BeginStopping(now, "test")
	s2.stepWrite(context.Background(), now.Add(stoppingTimeout+time.Second), tick, nil)
	if s2.gate.Enabled() {
		t.Fatal("flag still on after the stopping timeout")
	}
}
