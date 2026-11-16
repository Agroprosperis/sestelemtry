package edge

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/grid-x/modbus"
)

var errException4 = &modbus.Error{FunctionCode: 0x90, ExceptionCode: 4}

func essRunning() map[string]float64 {
	return map[string]float64{"pcs_shutdown": 0, "pcs_in_operation": 8}
}

func essStopped() map[string]float64 {
	return map[string]float64{"pcs_shutdown": 1, "pcs_in_operation": 0}
}

// job takes the job queued for a logger, if any.
func job(s *Service, role DeviceRole) (oneShotJob, bool) {
	select {
	case j := <-s.cmdJobs[role]:
		return j, true
	default:
		return oneShotJob{}, false
	}
}

// press sends a button through the core loop and has the logger take it.
func press(t *testing.T, s *Service, now time.Time, id string) {
	t.Helper()
	if r := s.onCommandRequest(context.Background(), now, cmdRequest{id: id, by: "t"}); !r.OK {
		t.Fatalf("%s: %+v", id, r)
	}
	cmd := oneShots[id]
	j, ok := job(s, cmd.Role)
	if !ok || j.addr != cmd.Addr {
		t.Fatalf("%s: job %+v queued=%v", id, j, ok)
	}
	s.onCommandResult(context.Background(), cmdResult{id: id, at: now, err: nil})
}

func tickAt(at time.Time, values map[string]float64) Tick {
	tk := controlTick(values)
	tk.TS = at
	return tk
}

func merge(ms ...map[string]float64) map[string]float64 {
	out := map[string]float64{}
	for _, m := range ms {
		for k, v := range m {
			out[k] = v
		}
	}
	return out
}

// The rule of rules holds for the buttons too: with the flag off no
// FC06 goes out, whatever the button.
func TestOneShotNeverWithFlagOff(t *testing.T) {
	s := writeService(t, zeCfg(t))
	f := newFakeLogger()
	if err := s.guardedWriteSingle(context.Background(), f, 40199, 0); err != errWriteOff {
		t.Fatalf("err = %v", err)
	}
	if r := s.onCommandRequest(context.Background(), testTS, cmdRequest{id: CmdESSStart, by: "t"}); r.OK {
		t.Fatalf("a button with the flag off: %+v", r)
	}
	jobs := make(chan oneShotJob, 1)
	jobs <- oneShotJob{id: CmdESSStart, addr: 40199}
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	s.runWriter(ctx, s.cfg.SmartLogger.Devices[1], regsFor(RoleESS), time.Hour,
		func(context.Context, Device) (registerSession, error) { return f, nil }, jobs)
	if got := f.singleWrites(); len(got) != 0 {
		t.Fatalf("FC06 with the flag off: %v", got)
	}
	if res := <-s.cmdResults; !errors.Is(res.err, errWriteOff) {
		t.Fatalf("result = %+v", res)
	}
}

func TestOneShotAllowlist(t *testing.T) {
	s := writeService(t, zeCfg(t))
	enableForTest(t, s)
	f := newFakeLogger()
	for _, addr := range []uint16{40202, 40203, 40381, 40195} {
		if err := s.guardedWriteSingle(context.Background(), f, addr, 0); err != errNotWritable {
			t.Fatalf("%d: %v", addr, err)
		}
	}
	if err := s.guardedWriteSingle(context.Background(), f, 40199, 1); err != errNotWritable {
		t.Fatalf("value 1: %v", err)
	}
	if len(f.singleWrites()) != 0 {
		t.Fatal("refused writes reached the logger")
	}
}

// A button goes to its own logger only, as one FC06 of 0.
func TestWriterSendsTheButtonToItsLogger(t *testing.T) {
	s := writeService(t, zeCfg(t))
	enableForTest(t, s)
	f := newFakeLogger()
	jobs := make(chan oneShotJob, 1)
	jobs <- oneShotJob{id: CmdESSStart, addr: 40199}
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	s.runWriter(ctx, s.cfg.SmartLogger.Devices[1], regsFor(RoleESS), time.Hour,
		func(context.Context, Device) (registerSession, error) { return f, nil }, jobs)
	if got := f.singleWrites(); len(got) != 1 || got[0] != 40199 {
		t.Fatalf("FC06 = %v", got)
	}
	if res := <-s.cmdResults; res.err != nil || res.id != CmdESSStart {
		t.Fatalf("result = %+v", res)
	}
	for id, c := range oneShots {
		if (c.Target == "ess") != (c.Role == RoleESS) || !oneShotAddr(c.Addr) {
			t.Fatalf("%s goes to %s / %d", id, c.Role, c.Addr)
		}
	}
}

// Setpoints only after the confirmed battery start; the hold counts from
// it; then the engine's commands.
func TestBatteryStartGatesSetpoints(t *testing.T) {
	s := writeService(t, zeCfg(t))
	enableForTest(t, s)
	ctx := context.Background()
	d := func() *Decision { return &Decision{PBessVirtualKw: 300, ReasonCode: "test"} }
	at := testTS.Add(10 * time.Minute)
	s.stepWrite(ctx, at, tickAt(at, essStopped()), d())
	if c := s.writeCmd.Load(); c.State != WriteAwaitStart || c.ESSKw != 0 {
		t.Fatalf("before the start: %+v", c)
	}
	press(t, s, at, CmdESSStart)
	s.stepWrite(ctx, at.Add(5*time.Second), tickAt(at.Add(5*time.Second), essStopped()), d())
	if st, _ := s.commandStatus(CmdESSStart); st.State != CmdWaiting || st.Live["40540"] != 1 {
		t.Fatalf("waiting: %+v", st)
	}
	started := at.Add(20 * time.Second)
	s.stepWrite(ctx, started, tickAt(started, essRunning()), d())
	if st, _ := s.commandStatus(CmdESSStart); st.State != CmdConfirmed {
		t.Fatalf("confirmed: %+v", st)
	}
	if c := s.writeCmd.Load(); c.State != WriteHold || c.ESSKw != 0 {
		t.Fatalf("hold: %+v", c)
	}
	run := started.Add(s.cfg.Control.Write.InitialHold + time.Second)
	s.stepWrite(ctx, run, tickAt(run, essRunning()), d())
	if c := s.writeCmd.Load(); c.State != WriteRun || c.ESSKw != 100 {
		t.Fatalf("run: %+v, want the first command capped at 100", c)
	}
}

func TestCommandNotConfirmedInThreeMinutes(t *testing.T) {
	s := writeService(t, zeCfg(t))
	enableForTest(t, s)
	press(t, s, testTS, CmdESSStart)
	end := testTS.Add(commandConfirmWindow + time.Second)
	s.stepWrite(context.Background(), end, tickAt(end, essStopped()), nil)
	st, _ := s.commandStatus(CmdESSStart)
	if st.State != CmdNotConfirmed || st.Live["40540"] != 1 {
		t.Fatalf("status = %+v", st)
	}
	if s.ctl.State() != WriteAwaitStart {
		t.Fatalf("state = %s", s.ctl.State())
	}
}

// The logger's refusal shows with its code; a silent writer times out.
func TestCommandAnswers(t *testing.T) {
	s := writeService(t, zeCfg(t))
	enableForTest(t, s)
	ctx := context.Background()
	if r := s.onCommandRequest(ctx, testTS, cmdRequest{id: CmdPVStart, by: "t"}); !r.OK {
		t.Fatal(r.Error)
	}
	s.onCommandResult(ctx, cmdResult{id: CmdPVStart, at: testTS, err: errException4})
	if st, _ := s.commandStatus(CmdPVStart); st.Answer != AnswerRefused || st.Exception != 4 || st.State != CmdFailed {
		t.Fatalf("refused: %+v", st)
	}
	if r := s.onCommandRequest(ctx, testTS, cmdRequest{id: CmdESSStart, by: "t"}); !r.OK {
		t.Fatal(r.Error)
	}
	late := testTS.Add(commandAnswerTimeout + time.Second)
	s.updateCommands(ctx, late, tickAt(late, essStopped()))
	if st, _ := s.commandStatus(CmdESSStart); st.Answer != AnswerNoResponse || st.State != CmdFailed {
		t.Fatalf("silent writer: %+v", st)
	}
}

// A command waiting for its confirmation blocks itself and its opposite;
// nothing is retried by itself.
func TestPendingCommandBlocksItselfAndItsOpposite(t *testing.T) {
	s := writeService(t, zeCfg(t))
	enableForTest(t, s)
	press(t, s, testTS, CmdESSStart)
	for _, id := range []string{CmdESSStart, CmdESSStop} {
		if r := s.onCommandRequest(context.Background(), testTS, cmdRequest{id: id, by: "t"}); r.OK {
			t.Fatalf("%s while the start waits: %+v", id, r)
		}
	}
	if r := s.onCommandRequest(context.Background(), testTS, cmdRequest{id: CmdPVStart, by: "t"}); !r.OK {
		t.Fatalf("another row is free: %+v", r)
	}
}

// A check that stops the test stops what the test started: the battery
// if started, the sun only if the test started it.
func TestAutoStopStopsWhatTheTestStarted(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name      string
		pvAtStart float64
		wantPV    bool
	}{{"sun up all along", 120, false}, {"sun started by the test", 0, true}} {
		t.Run(tc.name, func(t *testing.T) {
			s := writeService(t, zeCfg(t))
			enableForTest(t, s)
			s.beginWriteSession(tickAt(testTS, merge(essStopped(), map[string]float64{"active_pv_power_kw": tc.pvAtStart})))
			press(t, s, testTS, CmdESSStart)
			s.stepWrite(ctx, testTS.Add(time.Second), tickAt(testTS.Add(time.Second), essRunning()), nil)
			if tc.pvAtStart == 0 {
				press(t, s, testTS.Add(2*time.Second), CmdPVStart)
				s.stepWrite(ctx, testTS.Add(3*time.Second), tickAt(testTS.Add(3*time.Second), essRunning()), nil)
			}
			alarm := testTS.Add(time.Minute)
			s.stepWrite(ctx, alarm, tickAt(alarm, merge(essRunning(), map[string]float64{"sl_alarm_2": 0x10})), nil)
			if s.ctl.State() != WriteStopped {
				t.Fatalf("state = %s", s.ctl.State())
			}
			if j, ok := job(s, RoleESS); !ok || j.addr != 40198 {
				t.Fatalf("battery stop not sent: %+v", j)
			}
			j, ok := job(s, RolePV)
			if tc.wantPV != (ok && j.addr == 40196) {
				t.Fatalf("PV stop sent=%v (%+v), want %v", ok, j, tc.wantPV)
			}
			// No restart after a stop.
			if r := s.onCommandRequest(ctx, alarm, cmdRequest{id: CmdESSStart, by: "t"}); r.OK {
				t.Fatal("a start after the stop")
			}
		})
	}
}

// «УЗЕ 0 кВт» is a stop of the setpoints, not of the battery.
func TestOperatorZeroDoesNotStopTheBattery(t *testing.T) {
	s := writeService(t, zeCfg(t))
	enableForTest(t, s)
	press(t, s, testTS, CmdESSStart)
	s.stepWrite(context.Background(), testTS.Add(time.Second), tickAt(testTS.Add(time.Second), essRunning()), nil)
	s.onWriteRequest(context.Background(), testTS.Add(time.Minute), writeRequest{action: "stop", by: "t"})
	s.stepWrite(context.Background(), testTS.Add(61*time.Second), tickAt(testTS.Add(61*time.Second), essRunning()), nil)
	if _, ok := job(s, RoleESS); ok {
		t.Fatal("«0 кВт» sent a battery stop")
	}
}

func safeFeedback(s *Service, at time.Time) {
	s.storeFeedback(WriteFeedback{Register: "40381", At: at, Kw: 0, OK: true})
	s.storeFeedback(WriteFeedback{Register: "40386", At: at, Kw: 0, OK: true})
	s.storeFeedback(WriteFeedback{Register: "40378", At: at, Kw: 600, OK: true})
	s.storeFeedback(WriteFeedback{Register: "40384", At: at, Kw: 0, OK: true})
}

// Turning off: 0 read back, the battery stopped and confirmed, the sun
// stopped only if the test started it, then the flag drops.
func TestDisableStopsTheBatteryThenDropsTheFlag(t *testing.T) {
	s := writeService(t, zeCfg(t))
	enableForTest(t, s)
	ctx := context.Background()
	press(t, s, testTS, CmdESSStart)
	s.stepWrite(ctx, testTS.Add(time.Second), tickAt(testTS.Add(time.Second), essRunning()), nil)

	now := testTS.Add(time.Hour)
	if r := s.onWriteRequest(ctx, now, writeRequest{action: "disable", by: "t"}); !r.OK {
		t.Fatal(r.Error)
	}
	s.stepWrite(ctx, now.Add(time.Second), tickAt(now.Add(time.Second), essRunning()), nil)
	if !s.gate.Enabled() {
		t.Fatal("flag dropped before the 0 was read back")
	}
	if _, ok := job(s, RoleESS); ok {
		t.Fatal("battery stop before the 0 was read back")
	}
	safeFeedback(s, now.Add(2*time.Second))
	s.stepWrite(ctx, now.Add(3*time.Second), tickAt(now.Add(3*time.Second), essRunning()), nil)
	j, ok := job(s, RoleESS)
	if !ok || j.addr != 40198 || !s.gate.Enabled() {
		t.Fatalf("battery stop = %+v queued=%v, flag %v", j, ok, s.gate.Enabled())
	}
	s.onCommandResult(ctx, cmdResult{id: CmdESSStop, at: now.Add(4 * time.Second)})
	s.stepWrite(ctx, now.Add(5*time.Second), tickAt(now.Add(5*time.Second), essRunning()), nil)
	if !s.gate.Enabled() {
		t.Fatal("flag dropped before the battery stop was confirmed")
	}
	s.stepWrite(ctx, now.Add(8*time.Second), tickAt(now.Add(8*time.Second), essStopped()), nil)
	if s.gate.Enabled() || s.ctl.State() != WriteOff {
		t.Fatalf("flag %v, state %s", s.gate.Enabled(), s.ctl.State())
	}
	if _, ok := job(s, RolePV); ok {
		t.Fatal("the sun that ran all along was stopped")
	}
	if st := s.gate.State(); strings.Contains(st.Reason, "не підтверджено") {
		t.Fatalf("reason = %q", st.Reason)
	}
}

// Nothing waits forever: a battery stop that gets no answer is noted
// and the flag still drops.
func TestDisableWithASilentLogger(t *testing.T) {
	s := writeService(t, zeCfg(t))
	enableForTest(t, s)
	ctx := context.Background()
	now := testTS.Add(time.Hour)
	s.onWriteRequest(ctx, now, writeRequest{action: "disable", by: "t"})
	at := now.Add(stoppingTimeout + time.Second)
	s.stepWrite(ctx, at, tickAt(at, essStopped()), nil)
	if !s.gate.Enabled() {
		t.Fatal("dropped without trying the battery stop")
	}
	at = at.Add(commandAnswerTimeout + time.Second)
	s.stepWrite(ctx, at, tickAt(at, essStopped()), nil)
	if s.gate.Enabled() {
		t.Fatal("flag still on")
	}
	if r := s.gate.State().Reason; !strings.Contains(r, "readback") || !strings.Contains(r, "зупинку УЗЕ не підтверджено") {
		t.Fatalf("reason = %q", r)
	}
}

// The PV logger's exception 4 while the sun is down is the expected
// answer; with the sun up, or on the battery logger, it is a fault.
func TestPVLoggerException4(t *testing.T) {
	cases := []struct {
		name  string
		dev   int
		reg   uint16
		pvKw  float64
		fault bool
	}{
		{"PV logger, sun down", 0, 40378, 0, false},
		{"PV logger, sun up", 0, 40378, 120, true},
		{"battery logger", 1, 40381, 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := writeService(t, zeCfg(t))
			enableForTest(t, s)
			tk := tickAt(time.Now().UTC(), map[string]float64{"active_pv_power_kw": tc.pvKw})
			s.lastTick.Store(&tk)
			s.writeCmd.Store(&WriteCommand{At: time.Now().UTC(), State: WriteAwaitStart, PVLimitKw: 600, PVKnown: true})
			f := newFakeLogger()
			f.addrErr[tc.reg] = errException4
			dev := s.cfg.SmartLogger.Devices[tc.dev]
			ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
			defer cancel()
			s.runWriter(ctx, dev, regsFor(dev.Role), 20*time.Millisecond,
				func(context.Context, Device) (registerSession, error) { return f, nil }, nil)
			faulted := len(s.writeFaults) > 0
			if faulted != tc.fault {
				t.Fatalf("fault = %v, want %v", faulted, tc.fault)
			}
			if !tc.fault {
				if fb, _ := s.feedback(regPVLimit); !fb.Tolerated {
					t.Fatalf("feedback = %+v", fb)
				}
				refused := 0
				for len(s.events) > 0 {
					if ev := <-s.events; ev.Code == EvPVWriteRefused {
						refused++
					}
				}
				if refused != 1 {
					t.Fatalf("PV_WRITE_REFUSED events = %d, want one per transition", refused)
				}
			}
		})
	}
}

// The ESS array stop is judged after a while and tells whether the sun
// stayed up.
func TestESSArrayStopRecordsTheSun(t *testing.T) {
	s := writeService(t, zeCfg(t))
	enableForTest(t, s)
	press(t, s, testTS, CmdArrayStopESS)
	early := testTS.Add(10 * time.Second)
	s.updateCommands(context.Background(), early, tickAt(early, essStopped()))
	if st, _ := s.commandStatus(CmdArrayStopESS); st.State != CmdWaiting {
		t.Fatalf("judged too early: %+v", st)
	}
	later := testTS.Add(arrayStopObserveAfter + time.Second)
	s.updateCommands(context.Background(), later, tickAt(later, merge(essStopped(), map[string]float64{"active_pv_power_kw": 140})))
	if st, _ := s.commandStatus(CmdArrayStopESS); st.State != CmdConfirmed {
		t.Fatalf("status = %+v", st)
	}
	if !s.sess.essStopConfirmed {
		t.Fatal("battery stop not recorded")
	}
}

func TestAlarmWordsByFirmware(t *testing.T) {
	keys := []string{"sl_alarm_1", "sl_alarm_6", "sl_alarm_7", "sl_alarm_8", "pcs_shutdown"}
	if got := alarmWordKeys(keys, 6); len(got) != 3 {
		t.Fatalf("six words: %v", got)
	}
	if got := alarmWordKeys(keys, 8); len(got) != 5 {
		t.Fatalf("eight words: %v", got)
	}
	cfg := zeCfg(t)
	cfg.SmartLogger.AlarmWords = 7
	if err := cfg.validate(); err == nil {
		t.Fatal("alarm_words 7 accepted")
	}
}

// The precondition is a stopped battery; a stopped sun needs no 40396.
func TestPreconditionsForTheApprovedHandOver(t *testing.T) {
	s := writeService(t, zeCfg(t))
	now := time.Now().UTC()
	tk := tickAt(now, merge(essStopped(), map[string]float64{"active_pv_power_kw": 0, "pv_rated_kw": 0}))
	s.lastTick.Store(&tk)
	pre := map[string]bool{}
	for _, p := range s.writePreconditions(now) {
		pre[p.ID] = p.OK
	}
	if !pre["ess_stopped"] || !pre["pv_rated"] {
		t.Fatalf("preconditions = %v", pre)
	}
	if _, ok := pre["pcs"]; ok {
		t.Fatal("the old 40540 = 0 precondition is back")
	}
}
