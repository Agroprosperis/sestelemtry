package edge

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

const bitsIsland = 0x020C // section, ESS breaker, switch in VSG

// islandService is a session after the grid's loss, the bus dead, the
// switch in VSG, the battery stopped and in PQ.
func islandService(t *testing.T, actions bool) *Service {
	t.Helper()
	s := offgridService(t)
	s.cfg.Offgrid.IslandActionsEnabled = actions
	enableForTest(t, s)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		s.stepWrite(ctx, sec(i), supplyTick(s, sec(i), bitsGridPQ, 228, essStopped()), nil)
		if i == 0 {
			drainJobs(s)
			s.onCommandResult(ctx, cmdResult{id: CmdPCSPQ, at: sec(0)})
		}
	}
	for i := 3; i < 12; i++ {
		s.stepWrite(ctx, sec(i), supplyTick(s, sec(i), bitsBlackoutVS, 0, merge(essStopped(), map[string]float64{"soc_percent": 60})), nil)
	}
	if s.ctl.State() != WriteModeStop || s.supplyNow.Mode != SupplyBlackout || s.supplyNow.Switch != SwitchVSG {
		t.Fatalf("setup: %s / %s / %s", s.ctl.State(), s.supplyNow.Mode, s.supplyNow.Switch)
	}
	if j := drainJobs(s); len(j) != 0 {
		t.Fatalf("one-shots before any button: %+v", j)
	}
	return s
}

func stopped60() map[string]float64 {
	return merge(essStopped(), map[string]float64{"soc_percent": 60})
}

func eventCodes(t *testing.T, s *Service) map[string]string {
	t.Helper()
	evs, err := s.bb.PendingEvents(context.Background(), 1000)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, d := range evs.Docs {
		var ev struct{ Code, Message string }
		if err := json.Unmarshal(d, &ev); err != nil {
			t.Fatal(err)
		}
		out[ev.Code] = ev.Message
	}
	return out
}

func TestIslandWaitsForTheVendor(t *testing.T) {
	s := islandService(t, false)
	r := s.onCommandRequest(context.Background(), sec(12), cmdRequest{id: ActIslandStart, by: "t"})
	if r.OK || !strings.Contains(r.Error, "до відповіді вендора") {
		t.Fatalf("flag off: %+v", r)
	}
	for _, id := range []string{CmdPCSVSG, CmdIslandESSStart, CmdIslandESSStop, CmdIslandPVStart} {
		if r := s.onCommandRequest(context.Background(), sec(12), cmdRequest{id: id, by: "t"}); r.OK {
			t.Fatalf("%s pressed directly: %+v", id, r)
		}
	}
}

func TestIslandStartGate(t *testing.T) {
	s := islandService(t, true)
	gate := func(at int, bits uint16, vals map[string]float64) string {
		tk := supplyTick(s, sec(at), bits, 0, vals)
		return s.islandStartGate(sec(at), tk).Reason
	}
	if r := gate(12, bitsIsland, stopped60()); r != "" {
		t.Fatalf("all conditions: %q", r)
	}
	for _, c := range []struct {
		bits uint16
		vals map[string]float64
		want string
	}{
		{bitsIsland | 1<<diT1, stopped60(), "1QF"},
		{bitsIsland | 1<<diGen, stopped60(), "генератора"},
		{bitsIsland &^ (1 << diESS), stopped60(), "вимикач УЗЕ"},
		{bitsIsland, merge(essStopped(), map[string]float64{"soc_percent": 25}), "вище 25"},
		{bitsIsland, merge(essRunning(), map[string]float64{"soc_percent": 60}), "зупинена"},
	} {
		if r := gate(13, c.bits, c.vals); !strings.Contains(r, c.want) {
			t.Errorf("0x%04X %v: %q, want %q", c.bits, c.vals, r, c.want)
		}
	}
	// The switch freshly moved to VSG: wait 5 s.
	s2 := islandService(t, true)
	supplyTick(s2, sec(12), 0x010C, 0, stopped60())
	tk := supplyTick(s2, sec(13), bitsIsland, 0, stopped60())
	if r := s2.islandStartGate(sec(13), tk).Reason; !strings.Contains(r, "щонайменше 5 с") {
		t.Fatalf("switch just moved: %q", r)
	}
}

// The entry stops at the writer's ban on 44365 = 1: the write never
// reaches the logger, the battery is not started, the reason is on the
// console, and a new press waits for the device off time.
func TestIslandEntryStopsAtTheVSGBan(t *testing.T) {
	s := islandService(t, true)
	ctx := context.Background()
	if r := s.onCommandRequest(ctx, sec(12), cmdRequest{id: ActIslandStart, by: "t"}); !r.OK {
		t.Fatalf("press: %+v", r)
	}
	if s.ctl.State() != WriteIslandStart {
		t.Fatalf("state %s", s.ctl.State())
	}
	at := 12
	var vsg oneShotJob
	for ; at < 30; at++ {
		s.stepWrite(ctx, sec(at), supplyTick(s, sec(at), bitsIsland, 0, stopped60()), nil)
		if c := s.writeCmd.Load(); !c.ESSHold || c.PVLimitKw != 0 {
			t.Fatalf("entering: %+v", c)
		}
		if j := drainJobs(s); len(j) > 0 {
			vsg = j[0]
			break
		}
	}
	if vsg.addr != 44365 || vsg.value != 1 || at-12 < 5 {
		t.Fatalf("VSG job %+v at +%d s", vsg, at-12)
	}
	// The writer takes the job: the guard refuses it, nothing is written.
	f := newFakeLogger()
	jobs := make(chan oneShotJob, 1)
	jobs <- vsg
	wctx, cancel := context.WithTimeout(ctx, 80*time.Millisecond)
	s.runWriter(wctx, s.cfg.SmartLogger.Devices[1], regsFor(RoleESS), time.Hour,
		func(context.Context, Device) (registerSession, error) { return f, nil }, jobs)
	cancel()
	res := <-s.cmdResults
	if !errors.Is(res.err, errNotWritable) || len(f.singleWrites()) != 0 {
		t.Fatalf("44365 = 1 reached the logger: %v, %v", res.err, f.singleWrites())
	}
	s.onCommandResult(ctx, res)
	for at++; at < 40; at++ {
		s.stepWrite(ctx, sec(at), supplyTick(s, sec(at), bitsIsland, 0, stopped60()), nil)
		for _, j := range drainJobs(s) {
			t.Fatalf("after the refusal: %+v", j)
		}
	}
	if s.ctl.State() != WriteModeStop || !s.gate.Enabled() {
		t.Fatalf("after the failed entry: %s", s.ctl.State())
	}
	if msg := eventCodes(t, s)[EvIslandFailed]; !strings.Contains(msg, "до погодження порядку тесту") {
		t.Fatalf("failure event %q", msg)
	}
	tk := supplyTick(s, sec(40), bitsIsland, 0, stopped60())
	if r := s.islandStartGate(sec(40), tk).Reason; !strings.Contains(r, "не раніше") {
		t.Fatalf("retry gate: %q", r)
	}
}

// Past the ban (as after E5): the island comes up, holds, warns, and
// leaves on its own triggers only to stop.
func TestIslandRunAndExit(t *testing.T) {
	s := islandService(t, true)
	ctx := context.Background()
	if r := s.onCommandRequest(ctx, sec(12), cmdRequest{id: ActIslandStart, by: "t"}); !r.OK {
		t.Fatal(r.Error)
	}
	at := 12
	step := func(bits uint16, busV float64, vals map[string]float64) []oneShotJob {
		at++
		s.stepWrite(ctx, sec(at), supplyTick(s, sec(at), bits, busV, vals), nil)
		return drainJobs(s)
	}
	for len(step(bitsIsland, 0, stopped60())) == 0 && at < 30 {
	}
	s.onCommandResult(ctx, cmdResult{id: CmdPCSVSG, at: sec(at)}) // as if allowed
	vsgStopped := merge(stopped60(), map[string]float64{"pcs_working_mode": 1})
	var start []oneShotJob
	for i := 0; i < 10 && len(start) == 0; i++ {
		start = step(bitsIsland, 0, vsgStopped)
	}
	if len(start) != 1 || start[0].addr != 40199 {
		t.Fatalf("island start job %+v", start)
	}
	s.onCommandResult(ctx, cmdResult{id: CmdIslandESSStart, at: sec(at)})
	up := map[string]float64{"pcs_shutdown": 0, "pcs_in_operation": 8, "pcs_working_mode": 1, "soc_percent": 60,
		"active_pv_power_kw": 0, "active_ess_power_kw": 120}
	jobs := step(bitsIsland, 230, up)
	if s.ctl.State() != WriteIsland || len(jobs) != 1 || jobs[0].addr != 40197 {
		t.Fatalf("island up: %s, jobs %+v", s.ctl.State(), jobs)
	}
	if c := s.writeCmd.Load(); !c.ESSHold {
		t.Fatalf("40381 written in the island: %+v", c)
	}
	if _, ok := (WriteCommand{ESSHold: true}).valueFor(regESSPower); ok {
		t.Fatal("ESSHold does not hold 40381")
	}
	// Charge 89 %: the sun no higher than the load.
	full := merge(up, map[string]float64{"soc_percent": 89, "active_pv_power_kw": 300, "active_ess_power_kw": -100})
	step(bitsIsland, 230, full)
	if c := s.writeCmd.Load(); c.PVLimitKw != 200 {
		t.Fatalf("PV ceiling at 89 %%: %+v", c)
	}
	// 30 %: warned once; an alarm word: an alarm, not a stop.
	low := merge(up, map[string]float64{"soc_percent": 29, "sl_alarm_3": 4})
	for i := 0; i < 3; i++ {
		if j := step(bitsIsland, 230, low); len(j) != 0 {
			t.Fatalf("30 %% or an alarm sent %+v", j)
		}
	}
	codes := eventCodes(t, s)
	if codes[EvIslandSocWarn] == "" || codes[EvIslandAlarm] == "" || s.ctl.State() != WriteIsland {
		t.Fatalf("warning/alarm: %v, state %s", codes, s.ctl.State())
	}
	// 20 %: the battery is stopped, then put back in PQ, then done.
	jobs = step(bitsIsland, 230, merge(up, map[string]float64{"soc_percent": 20}))
	if len(jobs) != 1 || jobs[0].addr != 40198 || s.ctl.State() != WriteIslandStop {
		t.Fatalf("at 20 %%: %s, %+v", s.ctl.State(), jobs)
	}
	jobs = step(bitsIsland, 0, merge(stopped60(), map[string]float64{"pcs_working_mode": 1, "soc_percent": 20}))
	if len(jobs) != 1 || jobs[0].addr != 44365 || jobs[0].value != 0 {
		t.Fatalf("PQ after the stop: %+v", jobs)
	}
	step(bitsIsland, 0, merge(stopped60(), map[string]float64{"soc_percent": 20}))
	if s.ctl.State() != WriteModeStop || s.writeCmd.Load().ESSHold {
		t.Fatalf("after the exit: %s", s.ctl.State())
	}
}

func TestIslandExitTriggers(t *testing.T) {
	for _, c := range []struct {
		name string
		bits uint16
		want string
	}{
		{"switch away from VSG", 0x010C, "не у VSG"},
		{"incomer closed", bitsIsland | 1<<diT1, "вводу"},
		{"generator breaker closed", bitsIsland | 1<<diGen, "генератора"},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := islandService(t, true)
			s.ctl.IslandBegin(sec(12), "t")
			s.ctl.IslandUp(sec(12))
			s.isl = islandSeq{phase: islRun, since: sec(12), by: "t"}
			run := map[string]float64{"pcs_shutdown": 0, "pcs_in_operation": 8, "pcs_working_mode": 1, "soc_percent": 60}
			ctx := context.Background()
			var jobs []oneShotJob
			for i := 13; i < 22 && s.ctl.State() == WriteIsland; i++ {
				s.stepWrite(ctx, sec(i), supplyTick(s, sec(i), c.bits, 230, run), nil)
				jobs = append(jobs, drainJobs(s)...)
			}
			if s.ctl.State() != WriteIslandStop || len(jobs) != 1 || jobs[0].addr != 40198 ||
				!strings.Contains(s.ctl.Reason(), c.want) {
				t.Fatalf("%s, %q, jobs %+v", s.ctl.State(), s.ctl.Reason(), jobs)
			}
		})
	}
}

// The island stop button, the operator's «0 кВт», turning writing off.
func TestIslandOperatorActions(t *testing.T) {
	s := islandService(t, true)
	ctx := context.Background()
	if r := s.onCommandRequest(ctx, sec(12), cmdRequest{id: ActIslandStop, by: "t"}); r.OK {
		t.Fatal("island stop with no island")
	}
	s.ctl.IslandBegin(sec(12), "t")
	s.ctl.IslandUp(sec(12))
	s.isl = islandSeq{phase: islRun, since: sec(12), by: "t"}
	if r := s.onWriteRequest(ctx, sec(13), writeRequest{action: "stop", by: "t"}); r.OK {
		t.Fatal("«0 кВт» in the island")
	}
	supplyTick(s, sec(13), bitsIsland, 230, map[string]float64{"pcs_shutdown": 0, "pcs_in_operation": 8, "pcs_working_mode": 1})
	if r := s.onCommandRequest(ctx, sec(13), cmdRequest{id: ActIslandStop, by: "t"}); !r.OK {
		t.Fatalf("island stop: %+v", r)
	}
	if j := drainJobs(s); len(j) != 1 || j[0].addr != 40198 || s.ctl.State() != WriteIslandStop {
		t.Fatalf("stop: %s, %+v", s.ctl.State(), j)
	}

	// Turning writing off from the island: no 40381, the stop, then PQ.
	s2 := islandService(t, true)
	s2.ctl.IslandBegin(sec(12), "t")
	s2.ctl.IslandUp(sec(12))
	s2.isl = islandSeq{phase: islRun, since: sec(12), by: "t"}
	s2.onWriteRequest(ctx, sec(13), writeRequest{action: "disable", by: "t"})
	vsgStopped := merge(stopped60(), map[string]float64{"pcs_working_mode": 1})
	var jobs []oneShotJob
	for i := 13; i < 40 && s2.gate.Enabled(); i++ {
		tk := supplyTick(s2, sec(i), bitsIsland, 0, vsgStopped)
		s2.stepWrite(ctx, sec(i), tk, nil)
		if c := s2.writeCmd.Load(); c != nil && !c.ESSHold {
			t.Fatalf("40381 written while turning off from the island: %+v", c)
		}
		s2.storeFeedback(WriteFeedback{Register: "40378", At: sec(i), Kw: 0, OK: true})
		for _, j := range drainJobs(s2) {
			jobs = append(jobs, j)
			s2.onCommandResult(ctx, cmdResult{id: j.id, at: sec(i)})
		}
		if len(jobs) > 0 && jobs[len(jobs)-1].addr == 44365 {
			vsgStopped = stopped60()
		}
	}
	if s2.gate.Enabled() || len(jobs) < 2 || jobs[0].addr != 40198 || jobs[1].addr != 44365 {
		t.Fatalf("turn-off from the island: gate %v, jobs %+v", s2.gate.Enabled(), jobs)
	}
}

// Nothing starts by itself: the bus dead, the switch in VSG, all
// conditions met, ten minutes — not one write.
func TestIslandNothingWithoutTheButton(t *testing.T) {
	s := islandService(t, true)
	ctx := context.Background()
	for i := 12; i < 600; i += 3 {
		s.stepWrite(ctx, sec(i), supplyTick(s, sec(i), bitsIsland, 0, stopped60()), nil)
		if j := drainJobs(s); len(j) != 0 {
			t.Fatalf("at %d s: %+v", i, j)
		}
	}
	if !s.avail.Button(ActIslandStart).OK || s.ctl.State() != WriteModeStop {
		t.Fatalf("the button is open but nothing pressed it: %+v, %s", s.avail.Button(ActIslandStart), s.ctl.State())
	}
}

// An island the console did not start (ze 26.11: Encombi brought it up
// 2 min 20 s after the grid went): the battery's setpoints stay
// unwritten, the sun follows §5.2, and nothing is stopped for it, also
// when writing is turned off.
func TestExternalIslandIsLeftAlone(t *testing.T) {
	s := islandService(t, false)
	ctx := context.Background()
	held := map[string]float64{"pcs_shutdown": 0, "pcs_in_operation": 8, "pcs_working_mode": 1, "soc_percent": 60,
		"active_pv_power_kw": 0, "active_ess_power_kw": 27}
	at := 12
	step := func(vals map[string]float64) []oneShotJob {
		at++
		s.stepWrite(ctx, sec(at), supplyTick(s, sec(at), bitsIsland, 230, vals), nil)
		return drainJobs(s)
	}
	// 44364 = 2 on the way to VSG (ze 26.11 16:04:30): not latched yet.
	for i := 0; i < 4; i++ {
		if j := step(merge(held, map[string]float64{"pcs_working_mode": 2})); len(j) != 0 {
			t.Fatalf("one-shots: %+v", j)
		}
	}
	if s.supplyNow.Mode != SupplyIsland || s.ctl.ExternalIsland() {
		t.Fatalf("44364 = 2: mode %s, external %v", s.supplyNow.Mode, s.ctl.ExternalIsland())
	}
	for i := 0; i < 3; i++ {
		if j := step(held); len(j) != 0 {
			t.Fatalf("one-shots in the island: %+v", j)
		}
	}
	if c := s.writeCmd.Load(); !s.ctl.ExternalIsland() || !c.ESSHold || c.State != WriteModeStop || c.PVLimitKw != 600 {
		t.Fatalf("external island: %v, %+v", s.ctl.ExternalIsland(), c)
	}
	if eventCodes(t, s)[EvIslandExternal] == "" {
		t.Fatal("no ISLAND_EXTERNAL event")
	}
	step(merge(held, map[string]float64{"soc_percent": 89, "active_pv_power_kw": 300, "active_ess_power_kw": -100}))
	if c := s.writeCmd.Load(); c.PVLimitKw != 200 || !c.ESSHold {
		t.Fatalf("PV ceiling at 89 %%: %+v", c)
	}

	if r := s.onWriteRequest(ctx, sec(at), writeRequest{action: "disable", by: "t"}); !r.OK {
		t.Fatal(r.Error)
	}
	for i := 0; i < 20 && s.gate.Enabled(); i++ {
		s.storeFeedback(WriteFeedback{Register: "40378", At: sec(at + 1), Kw: 600, OK: true})
		if j := step(held); len(j) != 0 {
			t.Fatalf("turning off sent %+v", j)
		}
		if c := s.writeCmd.Load(); c != nil && !c.ESSHold {
			t.Fatalf("40381 written while turning off: %+v", c)
		}
	}
	if s.gate.Enabled() || !strings.Contains(s.gate.State().Reason, "зупинку УЗЕ не надсилали") {
		t.Fatalf("turned off: gate %v, %q", s.gate.Enabled(), s.gate.State().Reason)
	}
}

// The bus stays live through the grid's loss, the battery holding it in
// VSG: the mode stop leaves the battery running.
func TestGridLossIntoAnExternalIslandStopsNothing(t *testing.T) {
	s := offgridService(t)
	enableForTest(t, s)
	ctx := context.Background()
	d := func() *Decision { return &Decision{PBessVirtualKw: 300, ReasonCode: "test"} }
	for i := 0; i < 3; i++ {
		s.stepWrite(ctx, sec(i), supplyTick(s, sec(i), bitsGridPQ, 228, essStopped()), d())
	}
	drainJobs(s)
	s.onCommandResult(ctx, cmdResult{id: CmdPCSPQ, at: sec(2)})
	press(t, s, sec(2), CmdESSStart)
	s.stepWrite(ctx, sec(12), supplyTick(s, sec(12), bitsGridPQ, 228, essRunning()), d())
	if s.ctl.State() != WriteHold {
		t.Fatalf("after the start: %s", s.ctl.State())
	}
	held := merge(essRunning(), map[string]float64{"pcs_working_mode": 1})
	var seen []oneShotJob
	for i := 13; i < 22; i++ {
		s.stepWrite(ctx, sec(i), supplyTick(s, sec(i), bitsIsland, 230, held), d())
		seen = append(seen, drainJobs(s)...)
	}
	if s.ctl.State() != WriteModeStop || !s.ctl.ExternalIsland() || len(seen) != 0 {
		t.Fatalf("state %s, external %v, one-shots %+v", s.ctl.State(), s.ctl.ExternalIsland(), seen)
	}
}

// The latch outlives stale Encombi data and ends with a dead bus.
func TestExternalIslandLatch(t *testing.T) {
	cfg := zeCfg(t)
	c := NewController(cfg.Control.Write).WithOffgrid(cfg.Offgrid)
	c.Start(testTS)
	step := func(at int, mode string, vals map[string]float64) WriteCommand {
		st := SupplyState{Mode: mode, Switch: SwitchVSG}
		cmd, _ := c.Step(ControlInputs{Now: sec(at), Tick: tickAt(sec(at), vals), ExportCapKw: 850, ImportLimitKw: 1700,
			Supply: &st, Avail: availabilityFor(st)})
		return cmd
	}
	vsg := merge(essRunning(), map[string]float64{"pcs_working_mode": 1})
	if cmd := step(1, SupplyBlackout, essStopped()); cmd.State != WriteModeStop || cmd.ESSHold {
		t.Fatalf("blackout: %+v", cmd)
	}
	if cmd := step(2, SupplyIsland, vsg); !cmd.ESSHold {
		t.Fatalf("island: %+v", cmd)
	}
	if cmd := step(3, SupplyUnknown, vsg); !cmd.ESSHold {
		t.Fatalf("stale data: %+v", cmd)
	}
	if cmd := step(4, SupplyBlackout, essStopped()); cmd.ESSHold || c.ExternalIsland() {
		t.Fatalf("dead bus: %+v", cmd)
	}
}
