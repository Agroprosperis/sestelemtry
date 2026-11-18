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
	mu        sync.Mutex
	regs      map[uint16][]byte
	writes    []uint16
	singles   []uint16
	readback  func(addr uint16, written []byte) []byte // nil = echo
	writeErr  error
	addrErr   map[uint16]error // per register, FC16 and FC06
	singleErr error
	closed    int
}

func newFakeLogger() *fakeLogger {
	return &fakeLogger{regs: map[uint16][]byte{}, addrErr: map[uint16]error{}}
}

func (f *fakeLogger) WriteMultiple(_ context.Context, start uint16, payload []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes = append(f.writes, start)
	if f.writeErr != nil {
		return f.writeErr
	}
	if err := f.addrErr[start]; err != nil {
		return err
	}
	f.regs[start] = append([]byte(nil), payload...)
	return nil
}

func (f *fakeLogger) WriteSingle(_ context.Context, addr, _ uint16) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.singles = append(f.singles, addr)
	if f.singleErr != nil {
		return f.singleErr
	}
	return f.addrErr[addr]
}

func (f *fakeLogger) singleWrites() []uint16 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]uint16(nil), f.singles...)
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
		// PinPath defaults next to the write flag, in the same temp dir.
		Control: ControlConfig{Mode: ModeShadow, Preset: PresetSelfConsumption, Interval: time.Second},
		Limits: Limits{
			Grid: GridLimits{ImportLimitKw: 1700, TargetImportKw: 1600},
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
	s := &Service{
		cfg: cfg, log: slog.Default(), bb: bb, gate: gate, ctl: NewController(cfg.Control.Write),
		pin:         &pinGate{path: cfg.Control.Write.PinPath},
		writeFaults: make(chan writeFault, 8), writeRequests: make(chan writeRequest), events: make(chan Event, 8),
		eventLastWritten: map[string]time.Time{},
		cmds:             map[string]*CommandStatus{}, cmdRequests: make(chan cmdRequest),
		cmdResults: make(chan cmdResult, 16), cmdJobs: map[DeviceRole]chan oneShotJob{},
	}
	for _, d := range cfg.SmartLogger.Devices {
		s.cmdJobs[d.Role] = make(chan oneShotJob, 4)
	}
	return s
}

func enableForTest(t *testing.T, s *Service) {
	t.Helper()
	if _, err := s.gate.Enable("test", testTS); err != nil {
		t.Fatal(err)
	}
	tk := controlTick(nil)
	s.lastTick.Store(&tk)
	s.beginWriteSession(tk)
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
}

// zeManifest is an active, numbered manifest carrying ze's ceiling from
// the cloud's «Налаштування».
func zeManifest(now time.Time, exportCapKw float64) *Manifest {
	return &Manifest{
		ManifestID: "ze-test", SiteID: "ze", Mode: ModeShadow, Preset: PresetSelfConsumption, ManifestSeq: 1,
		ValidFrom: now.Add(-time.Hour), ValidUntil: now.Add(time.Hour),
		GridLimits: ManifestGridLimits{ImportLimitKw: 1700, TargetImportKw: 1600, ExportCapKw: exportCapKw},
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
	s.runWriter(ctx, s.cfg.SmartLogger.Devices[1], regsFor(RoleESS), 10*time.Millisecond, dial, nil)
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
			func(context.Context, Device) (registerSession, error) { return f, nil }, nil)
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

// shortSettle lets a readback that keeps differing count as such within
// a test's run.
func shortSettle(t *testing.T) {
	t.Helper()
	oldFor, oldStep := settleFor, settleStep
	settleFor, settleStep = 40*time.Millisecond, 10*time.Millisecond
	t.Cleanup(func() { settleFor, settleStep = oldFor, oldStep })
}

// laggingLogger answers the first `lag` readbacks after a write with the
// value held before it, as the ze loggers did (15:25: 660 read for 605).
type laggingLogger struct {
	*fakeLogger
	lag  int
	prev map[uint16][]byte
	left map[uint16]int
}

func newLaggingLogger(lag int) *laggingLogger {
	return &laggingLogger{fakeLogger: newFakeLogger(), lag: lag, prev: map[uint16][]byte{}, left: map[uint16]int{}}
}

func (l *laggingLogger) WriteMultiple(ctx context.Context, start uint16, payload []byte) error {
	l.mu.Lock()
	l.prev[start], l.left[start] = l.regs[start], l.lag
	l.mu.Unlock()
	return l.fakeLogger.WriteMultiple(ctx, start, payload)
}

func (l *laggingLogger) ReadHolding(ctx context.Context, start, qty uint16) ([]byte, error) {
	l.mu.Lock()
	if l.left[start] > 0 {
		l.left[start]--
		b := l.prev[start]
		l.mu.Unlock()
		if b == nil {
			return make([]byte, 4), nil
		}
		return b, nil
	}
	l.mu.Unlock()
	return l.fakeLogger.ReadHolding(ctx, start, qty)
}

// The loggers apply a new setpoint with a delay: the readback is read
// again before it counts as differing, a higher PV ceiling and the
// battery's setpoint alike.
func TestWriterWaitsForTheLoggerToApply(t *testing.T) {
	oldFor, oldStep := settleFor, settleStep
	settleFor, settleStep = 300*time.Millisecond, 10*time.Millisecond
	t.Cleanup(func() { settleFor, settleStep = oldFor, oldStep })
	s := writeService(t, zeCfg(t))
	enableForTest(t, s)
	s.writeCmd.Store(&WriteCommand{At: time.Now().UTC().Add(time.Minute), State: WriteRun, ESSKw: 100, PVLimitKw: 605, PVKnown: true})
	for _, dev := range s.cfg.SmartLogger.Devices {
		f := newLaggingLogger(3)
		f.regs[40378] = []byte{0, 0, 0x19, 0xC8} // 660 kW held before
		ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
		s.runWriter(ctx, dev, regsFor(dev.Role), 50*time.Millisecond,
			func(context.Context, Device) (registerSession, error) { return f, nil }, nil)
		cancel()
	}
	if len(s.writeFaults) != 0 {
		t.Fatalf("fault: %+v", <-s.writeFaults)
	}
	for _, reg := range []writeReg{regPVLimit, regESSPower} {
		if fb, ok := s.feedback(reg); !ok || !fb.OK || fb.Clipped {
			t.Fatalf("%s: %+v", reg.Name, fb)
		}
	}
}

// A readback that differs is a fault, reported to the core loop.
func TestWriterReadbackMismatchFaults(t *testing.T) {
	shortSettle(t)
	s := writeService(t, zeCfg(t))
	enableForTest(t, s)
	s.writeCmd.Store(&WriteCommand{At: time.Now().UTC(), State: WriteRun, ESSKw: 50, PVKnown: false})
	f := newFakeLogger()
	f.readback = func(uint16, []byte) []byte { return []byte{0, 0, 0, 7} }
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	s.runWriter(ctx, s.cfg.SmartLogger.Devices[1], regsFor(RoleESS), 20*time.Millisecond,
		func(context.Context, Device) (registerSession, error) { return f, nil }, nil)
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
		"pcs_working_mode": 0,
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

// Until the battery start is confirmed only 0 kW goes out, however long;
// the hold is counted from the confirmed start.
func TestControllerHoldsThenCapsTheFirstCommand(t *testing.T) {
	cfg := zeCfg(t).Control.Write
	c := NewController(cfg)
	c.Start(testTS)
	stopped := map[string]float64{"pcs_shutdown": 1}
	cmd, _ := step(c, testTS.Add(time.Hour), controlTick(stopped), 300)
	if cmd.State != WriteAwaitStart || cmd.ESSKw != 0 || cmd.PVLimitKw != 600 {
		t.Fatalf("awaiting the start: %+v", cmd)
	}
	started := testTS.Add(time.Hour + time.Minute)
	c.ESSStarted(started)
	cmd, _ = step(c, started.Add(cfg.InitialHold-time.Second), controlTick(nil), 300)
	if cmd.State != WriteHold || cmd.ESSKw != 0 {
		t.Fatalf("hold: %+v", cmd)
	}
	at := started.Add(cfg.InitialHold)
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
	c.ESSStarted(testTS)
	at := testTS.Add(cfg.InitialHold)
	step(c, at, controlTick(nil), 0)
	return c, at
}

// A stopped PCS is the expected state until the start is confirmed.
func TestControllerStoppedPCSBeforeTheStart(t *testing.T) {
	c := NewController(zeCfg(t).Control.Write)
	c.Start(testTS)
	cmd, evs := step(c, testTS.Add(time.Minute), controlTick(map[string]float64{"pcs_shutdown": 1}), 0)
	if cmd.State != WriteAwaitStart || len(evs) != 0 {
		t.Fatalf("stopped before the start: %+v %+v", cmd, evs)
	}
	cmd, _ = step(c, testTS.Add(2*time.Minute), controlTick(map[string]float64{"pcs_shutdown": 1, "sl_alarm_2": 0x10}), 0)
	if cmd.State != WriteStopped || !c.StopAuto() || c.ESSStartedInSession() {
		t.Fatalf("an alarm before the start: %+v", cmd)
	}
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

func controlTickWithout(values map[string]float64, drop ...string) Tick {
	base := controlTick(values).Values
	for _, k := range drop {
		delete(base, k)
	}
	return buildTickFromValues("ze", TopologyDual, 1, 1, testTS, base, QualityOK)
}

var essLines = map[string]float64{"ess_grid_line_voltage_ab_v": 387, "ess_grid_line_voltage_bc_v": 388, "ess_grid_line_voltage_ca_v": 387}

// ze 17.11.2026 13:48: the PCC meter stopped answering the logger. No
// 40505, the meter's phases 0 V, the loggers' lines still 387…388 V.
func silentMeterTick() Tick {
	values := map[string]float64{
		"meter_phase_a_voltage_v": 0, "meter_phase_b_voltage_v": 0, "meter_phase_c_voltage_v": 0,
		"meter_active_power_kw": 0,
	}
	for k, v := range essLines {
		values[k] = v
	}
	return controlTickWithout(values, "grid_connected_active_power_kw")
}

// From 14:20 the logger gave the meter's phases no value at all.
func meterNoValueTick(drop ...string) Tick {
	return controlTickWithout(essLines, append([]string{
		"meter_phase_a_voltage_v", "meter_phase_b_voltage_v", "meter_phase_c_voltage_v",
	}, drop...)...)
}

// Without 40505 neither the import limit nor the export ceiling is
// watched, without 40392 the battery's following: writing stops after
// 10 s, as on a lost poll. A blackout stays an undervoltage.
func TestControllerStopsWithoutPCCOrBatteryPower(t *testing.T) {
	blackout := controlTickWithout(map[string]float64{
		"meter_phase_a_voltage_v": 0, "meter_phase_b_voltage_v": 0, "meter_phase_c_voltage_v": 0,
	}, "grid_connected_active_power_kw")
	cases := []struct {
		name   string
		tick   Tick
		reason string
	}{
		{"silent PCC meter", silentMeterTick(), "потужність PCC 40505: немає даних — лічильник PCC не відповідає"},
		{"meter gives no value", meterNoValueTick("grid_connected_active_power_kw"), "потужність PCC 40505: немає даних — лічильник PCC не відповідає"},
		{"no 40505", controlTickWithout(nil, "grid_connected_active_power_kw"), "потужність PCC 40505: немає даних"},
		{"no 40392", controlTickWithout(nil, "active_ess_power_kw"), "потужність УЗЕ 40392: немає даних"},
		{"blackout", blackout, "низька напруга мережі: фаза 0 В; фаза 0 В; фаза 0 В"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, at := runController(t)
			step(c, at.Add(time.Second), tc.tick, 0)
			if cmd, _ := step(c, at.Add(9*time.Second), tc.tick, 0); cmd.State != WriteRun {
				t.Fatalf("stopped within 10 s: %+v", cmd)
			}
			cmd, evs := step(c, at.Add(11*time.Second), tc.tick, 0)
			if cmd.State != WriteStopped || len(evs) != 1 || c.Reason() != tc.reason {
				t.Fatalf("cmd = %+v, events = %+v, reason %q, want %q", cmd, evs, c.Reason(), tc.reason)
			}
		})
	}
}

func preconditionsFor(t *testing.T, cfg *Config, tick Tick) map[string]WritePrecondition {
	t.Helper()
	s := writeService(t, cfg)
	now := time.Now().UTC()
	tick.TS = now
	s.lastTick.Store(&tick)
	got := map[string]WritePrecondition{}
	for _, p := range s.writePreconditions(now) {
		got[p.ID] = p
	}
	return got
}

// The console names a silent PCC meter instead of 2 147 484 kW of
// import and three 0 V phases, and the loggers' lines alone do not
// pass the grid check (ze 17.11 14:28: ✅ «мережа є» with no meter).
func TestPreconditionsNameASilentPCCMeter(t *testing.T) {
	for name, tick := range map[string]Tick{
		"phases 0 V":      silentMeterTick(),
		"phases no value": meterNoValueTick("grid_connected_active_power_kw"),
	} {
		got := preconditionsFor(t, zeCfg(t), tick)
		if p := got["import_now"]; p.OK || p.Label != "потужність PCC 40505 відома" || p.Actual != "немає даних — лічильник PCC не відповідає" {
			t.Fatalf("%s: import_now = %+v", name, p)
		}
		if p := got["voltage"]; p.OK || p.Actual != "лічильник PCC не відповідає: мережу в точці приєднання не підтверджено" {
			t.Fatalf("%s: voltage = %+v", name, p)
		}
	}

	withMeter := zeCfg(t)
	withMeter.SmartLogger.Devices[0].Meter = &MeterConfig{UnitID: 11, Interval: 2 * time.Second}
	if p := preconditionsFor(t, withMeter, meterNoValueTick())["voltage"]; p.OK || p.Actual != "фази лічильника PCC не прочитано: мережу в точці приєднання не підтверджено" {
		t.Fatalf("meter polled, 40505 read, no phases: voltage = %+v", p)
	}
	if p := preconditionsFor(t, withMeter, controlTick(nil))["voltage"]; !p.OK {
		t.Fatalf("meter phases 231 V: voltage = %+v", p)
	}
}

// Right after the confirmed start a zero command is followed within
// 50 kW for two minutes (ze 18.11: −34…+20 kW while the cabinets came
// up); past that, or beyond 50 kW, the brief's 2 kW stops writing.
func TestControllerStartGrace(t *testing.T) {
	cfg := zeCfg(t).Control.Write
	run := func(fact float64, until time.Duration) WriteCommand {
		c := NewController(cfg)
		c.Start(testTS)
		c.ESSStarted(testTS)
		var cmd WriteCommand
		for d := time.Second; d <= until; d += time.Second {
			cmd, _ = step(c, testTS.Add(d), controlTick(map[string]float64{"active_ess_power_kw": fact}), 0)
		}
		return cmd
	}
	for _, fact := range []float64{20, -34} {
		if cmd := run(fact, 110*time.Second); cmd.State != WriteHold {
			t.Fatalf("%v kW in the first two minutes: %+v", fact, cmd)
		}
	}
	if cmd := run(60, 40*time.Second); cmd.State != WriteStopped {
		t.Fatalf("60 kW right after the start: %+v", cmd)
	}
	if cmd := run(20, 160*time.Second); cmd.State != WriteStopped {
		t.Fatalf("20 kW past the grace: %+v", cmd)
	}
}

// Above the SOC window the battery balances its cabinets on its own (ze
// 18.11, SOC 94.6 %: about −20 kW at command 0, 25 kW for 45 kW): the
// fact is followed within 50 kW there, the brief's tolerance below it.
func TestControllerHighSocTolerance(t *testing.T) {
	run := func(soc, desired, fact float64) WriteCommand {
		c, at := runController(t)
		c.firstConfirmed = true
		var cmd WriteCommand
		for i := 1; i <= 45; i++ {
			cmd, _ = step(c, at.Add(time.Duration(i)*time.Second),
				controlTick(map[string]float64{"soc_percent": soc, "active_ess_power_kw": fact}), desired)
		}
		return cmd
	}
	if cmd := run(94.6, 45, 25); cmd.State != WriteRun {
		t.Fatalf("SOC 94.6 %%, 25 kW for 45: %+v", cmd)
	}
	if cmd := run(94.6, 0, -34); cmd.State != WriteRun {
		t.Fatalf("SOC 94.6 %%, −34 kW for 0: %+v", cmd)
	}
	if cmd := run(94.6, 45, -20); cmd.State != WriteStopped {
		t.Fatalf("SOC 94.6 %%, −20 kW for 45: %+v", cmd)
	}
	if cmd := run(85, 45, 25); cmd.State != WriteStopped {
		t.Fatalf("SOC 85 %%, 25 kW for 45: %+v", cmd)
	}
}

// Cutover §12.7: after the start 44364 off PQ for 10 s stops writing.
func TestControllerStopsWhenThePCSLeavesPQ(t *testing.T) {
	c, at := runController(t)
	vsg := controlTick(map[string]float64{"pcs_working_mode": 1})
	var cmd WriteCommand
	for i := 1; i <= 9; i++ {
		cmd, _ = step(c, at.Add(time.Duration(i)*time.Second), vsg, 0)
	}
	if cmd.State != WriteRun {
		t.Fatalf("stopped within 10 s: %+v", cmd)
	}
	cmd, _ = step(c, at.Add(11*time.Second), vsg, 0)
	if cmd.State != WriteStopped || c.Reason() != "режим PCS не PQ (44364 = 1)" {
		t.Fatalf("cmd = %+v, reason %q", cmd, c.Reason())
	}
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

// 40378 never goes above the 40396 read now: it grows inverter by
// inverter while PV starts and falls as they stop; at 0 PV is not
// written, running or stopped.
func TestControllerPVCeilingFollowsTheCurrentRating(t *testing.T) {
	c, at := runController(t)
	for i, tc := range []struct {
		rated float64
		known bool
	}{{165, true}, {550, true}, {0, false}, {660, true}} {
		cmd, _ := step(c, at.Add(time.Duration(i+1)*time.Second), controlTick(map[string]float64{"pv_rated_kw": tc.rated}), 0)
		if cmd.PVKnown != tc.known || (tc.known && cmd.PVLimitKw != tc.rated) {
			t.Fatalf("40396 = %v: %+v", tc.rated, cmd)
		}
	}
	c.Stop(at.Add(5*time.Second), "test", false)
	for i, tc := range []struct {
		rated float64
		known bool
	}{{495, true}, {110, true}, {0, false}} {
		cmd, _ := step(c, at.Add(time.Duration(i+6)*time.Second), controlTick(map[string]float64{"pv_rated_kw": tc.rated}), 0)
		if cmd.State != WriteStopped || cmd.PVKnown != tc.known || (tc.known && cmd.PVLimitKw != tc.rated) {
			t.Fatalf("stopped, 40396 = %v: %+v", tc.rated, cmd)
		}
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
	// The installers stopped the battery before the hand-over.
	tick := controlTick(map[string]float64{"pcs_shutdown": 1})
	tick.TS = now
	s.lastTick.Store(&tick)
	for _, d := range s.cfg.SmartLogger.Devices {
		s.devPollOK.Store(d.Host, now.Unix())
	}
	ctx := context.Background()
	enable := func(confirm string, pinOK bool) writeReply {
		return s.onWriteRequest(ctx, now, writeRequest{action: "enable", by: "t", confirm: confirm, pinOK: pinOK})
	}
	s.manifest.Store(zeManifest(now, 850))
	if r := enable("ze", true); r.OK || s.gate.Enabled() || !failed(r, "pin") {
		t.Fatalf("enabled without a PIN on the device: %+v", r)
	}
	if err := SetWritePin(s.cfg.Control.Write.PinPath, "246813", now); err != nil {
		t.Fatal(err)
	}
	s.manifest.Store(nil)
	if r := enable("ab", true); r.OK || s.gate.Enabled() {
		t.Fatalf("wrong confirmation enabled writing: %+v", r)
	}
	if r := enable("ze", false); r.OK || s.gate.Enabled() || r.Error != "потрібен PIN" {
		t.Fatalf("enabled without a matched PIN: %+v", r)
	}
	running := controlTick(nil)
	running.TS = now
	s.lastTick.Store(&running)
	if r := enable("ze", true); r.OK || s.gate.Enabled() || !failed(r, "ess_stopped") {
		t.Fatalf("enabled with the battery not stopped before the hand-over: %+v", r)
	}
	s.lastTick.Store(&tick)
	// No ceiling from the cloud: the device config has none to offer.
	if r := enable("ze", true); r.OK || s.gate.Enabled() || !failed(r, "export_cap") {
		t.Fatalf("enabled without an export ceiling in the manifest: %+v", r)
	}
	s.manifest.Store(zeManifest(now, 0))
	if r := enable("ze", true); r.OK || s.gate.Enabled() || !failed(r, "export_cap") {
		t.Fatalf("enabled with a manifest that has no ceiling: %+v", r)
	}
	s.manifest.Store(zeManifest(now, 850))
	r := enable("ze", true)
	if !r.OK || !s.gate.Enabled() || s.ctl.State() != WriteAwaitStart {
		t.Fatalf("enable: %+v, gate %v, state %s", r, s.gate.Enabled(), s.ctl.State())
	}
	if got := s.exportCapKw(now); got != 850 {
		t.Fatalf("export ceiling = %v, want the manifest's 850", got)
	}
}

// A manifest cached by an older build has no number and lost the new
// fields: the same id comes again in full and replaces it.
func TestManifestRefetchAfterOldCache(t *testing.T) {
	old := &Manifest{ManifestID: "ze-1"}
	if manifestETag(old) != "" || manifestETag(nil) != "" {
		t.Fatal("an unnumbered or missing manifest must be fetched in full")
	}
	fresh := &Manifest{ManifestID: "ze-1", ManifestSeq: 42, GridLimits: ManifestGridLimits{ExportCapKw: 850}}
	if sameManifest(fresh, old) || sameManifest(fresh, nil) {
		t.Fatal("the numbered copy must replace the old cache")
	}
	if manifestETag(fresh) != "ze-1" || !sameManifest(&Manifest{ManifestID: "ze-1", ManifestSeq: 42}, fresh) {
		t.Fatal("an applied numbered manifest must be skipped")
	}
	if !sameManifest(&Manifest{ManifestID: "ze-1"}, &Manifest{ManifestID: "ze-1"}) {
		t.Fatal("an unnumbering cloud's unchanged manifest must be skipped")
	}
}

// An active manifest's ceiling wins even when it has none; the device
// config fills in only while no manifest is active.
func TestExportCeilingManifestElseConfig(t *testing.T) {
	cfg := zeCfg(t)
	cfg.Limits.Grid.ExportCapKw = 850
	s := writeService(t, cfg)
	now := time.Now().UTC()
	if got := s.exportCapKw(now); got != 850 {
		t.Fatalf("no manifest: ceiling = %v, want the config's 850", got)
	}
	s.manifest.Store(zeManifest(now, 600))
	if got := s.exportCapKw(now); got != 600 {
		t.Fatalf("manifest 600: ceiling = %v", got)
	}
	s.manifest.Store(zeManifest(now, 0))
	if got := s.exportCapKw(now); got != 0 {
		t.Fatalf("manifest without a ceiling: %v, want 0 (no config fallback)", got)
	}
	if got := s.exportCapKw(now.Add(2 * time.Hour)); got != 850 {
		t.Fatalf("expired manifest: ceiling = %v, want the config's 850", got)
	}
}

// The 40505 cross-check follows the meter's configured sign: ze's meter
// reports import as positive, as 40505 does.
func TestPCCSignPreconditionFollowsMeterSign(t *testing.T) {
	pccOK := func(cfg *Config, meter float64) (bool, bool) {
		s := writeService(t, cfg)
		now := time.Now().UTC()
		tick := controlTick(map[string]float64{"grid_connected_active_power_kw": 1275.3, "meter_active_power_kw": meter})
		tick.TS = now
		s.lastTick.Store(&tick)
		for _, p := range s.writePreconditions(now) {
			if p.ID == "pcc_sign" {
				return p.OK, true
			}
		}
		return false, false
	}
	huawei := zeCfg(t)
	if ok, found := pccOK(huawei, -1278.1); !found || !ok {
		t.Fatalf("Huawei map, opposite signs: ok=%v found=%v", ok, found)
	}
	if ok, _ := pccOK(huawei, +1278.1); ok {
		t.Fatal("Huawei map, same signs passed")
	}
	ze := zeCfg(t)
	ze.MeterImportSign = 1
	if ok, _ := pccOK(ze, +1278.1); !ok {
		t.Fatal("meter_import_sign 1, same signs failed")
	}
	if ok, _ := pccOK(ze, -1278.1); ok {
		t.Fatal("meter_import_sign 1, opposite signs passed")
	}
}
