package edge

import (
	"context"
	"strings"
	"testing"
	"time"
)

const (
	bitsGridPQ     = 0x01CE // T1, section, ESS, RN1, RN2, PQ
	bitsGridVSG    = 0x02CE // the same with the switch in VSG
	bitsBlackoutPQ = 0x010C // section, ESS, PQ: no incomer, no generator
	bitsBlackoutVS = 0x020C // section, ESS, VSG
	bitsGenPQ      = 0x052C // section, ESS, generator breaker, PQ, RN3
)

func offgridService(t *testing.T) *Service {
	t.Helper()
	cfg := zeCfg(t)
	cfg.Encombi.Enabled, cfg.Encombi.Host = true, "10.28.40.103"
	s := writeService(t, cfg)
	s.supply = newSupplyTracker(cfg.Encombi.Debounce)
	return s
}

// supplyTick is one second of the core loop's input side: a fresh
// Encombi reading, the tick, the power mode taken.
func supplyTick(s *Service, at time.Time, bits uint16, busV float64, values map[string]float64) Tick {
	r := encombiReading{At: at, DIBits: bits, Alive: true, BusV: [3]float64{busV, busV, busV}}
	s.lastEncombi.Store(&r)
	tk := tickAt(at, values)
	s.observeSupply(context.Background(), at, &tk)
	s.lastTick.Store(&tk)
	return tk
}

// drainJobs takes every queued one-shot of both loggers.
func drainJobs(s *Service) []oneShotJob {
	var out []oneShotJob
	for _, role := range []DeviceRole{RolePV, RoleESS} {
		for {
			j, ok := job(s, role)
			if !ok {
				break
			}
			out = append(out, j)
		}
	}
	return out
}

func TestAvailabilityTable(t *testing.T) {
	cases := []struct {
		mode, sw   string
		power      bool
		reason     string
		essStart   bool
		pvStart    bool
		manualOpen []string
	}{
		{SupplyGrid, SwitchPQ, true, "", true, true, manualTypes},
		{SupplyGrid, SwitchVSG, false, "перемикач у VSG", false, false, nil},
		{SupplyGrid, SwitchOff, false, "перемикач в OFF", false, true, nil},
		{SupplyGrid, SwitchUnknown, false, "не читається", false, false, nil},
		{SupplyBlackout, SwitchPQ, false, "немає живлення", false, false, nil},
		{SupplyBlackout, SwitchVSG, false, "з локальної консолі", false, false, nil},
		{SupplyIsland, SwitchVSG, false, "не задається", false, false, nil},
		{SupplyIsland, SwitchPQ, false, "острів зупиняється", false, false, nil},
		{SupplyGenerator, SwitchPQ, false, "робота від генератора", false, false, []string{ManualHold, ManualTarget}},
		{SupplyGenerator, SwitchVSG, false, "переведіть у PQ", false, false, nil},
		{SupplyTransition, SwitchPQ, false, "перехід", false, false, nil},
		{SupplyUnknown, SwitchUnknown, false, "Encombi", false, false, nil},
	}
	for _, c := range cases {
		a := availabilityFor(SupplyState{Mode: c.mode, Switch: c.sw})
		name := c.mode + "/" + c.sw
		if a.Power.OK != c.power || a.Plan.OK != c.power || !strings.Contains(a.Restriction, c.reason) {
			t.Errorf("%s: power %v plan %v restriction %q", name, a.Power.OK, a.Plan.OK, a.Restriction)
		}
		if a.Button(CmdESSStart).OK != c.essStart || a.Button(CmdArrayStartESS).OK != c.essStart || a.Button(CmdPVStart).OK != c.pvStart {
			t.Errorf("%s: ess start %v, pv start %v", name, a.Button(CmdESSStart), a.Button(CmdPVStart))
		}
		for id, cmd := range oneShots {
			if !cmd.Start && !a.Button(id).OK {
				t.Errorf("%s: stop %s closed", name, id)
			}
		}
		if got := a.Button(ActWriteEnable).OK; got != (c.mode == SupplyGrid && c.sw == SwitchPQ) {
			t.Errorf("%s: write enable %v", name, got)
		}
		openSet := map[string]bool{}
		for _, m := range c.manualOpen {
			openSet[m] = true
		}
		for _, m := range manualTypes {
			if a.ManualOK(m).OK != openSet[m] {
				t.Errorf("%s: manual %s open=%v", name, m, a.ManualOK(m).OK)
			}
		}
	}
	var none *Availability
	if !none.Button(CmdESSStart).OK || !none.ManualOK(ManualFixed).OK {
		t.Fatal("encombi off must not close anything")
	}
}

// The plan's rule of rules (offgrid §2, «нічого не запускається само»):
// the grid goes and comes back with no button pressed — the battery is
// stopped, the session goes on, and not one start or 44365 goes out.
func TestGridLossKeepsTheSessionAndStartsNothing(t *testing.T) {
	s := offgridService(t)
	enableForTest(t, s)
	ctx := context.Background()
	d := func() *Decision { return &Decision{PBessVirtualKw: 300, ReasonCode: "test"} }
	at := testTS
	for i := 0; i < 3; i++ {
		at = testTS.Add(time.Duration(i) * time.Second)
		s.stepWrite(ctx, at, supplyTick(s, at, bitsGridPQ, 228, essStopped()), d())
	}
	if j := drainJobs(s); len(j) != 1 || j[0].id != CmdPCSPQ {
		t.Fatalf("enabling queued %+v, want 44365 = 0 once", j)
	}
	s.onCommandResult(ctx, cmdResult{id: CmdPCSPQ, at: at})
	press(t, s, at, CmdESSStart)
	at = at.Add(10 * time.Second)
	s.stepWrite(ctx, at, supplyTick(s, at, bitsGridPQ, 228, essRunning()), d())
	if s.ctl.State() != WriteHold {
		t.Fatalf("after the start: %s", s.ctl.State())
	}
	run := at.Add(s.cfg.Control.Write.InitialHold + time.Second)
	s.stepWrite(ctx, run, supplyTick(s, run, bitsGridPQ, 228, essRunning()), d())
	if c := s.writeCmd.Load(); c.State != WriteRun || c.ESSKw == 0 {
		t.Fatalf("run: %+v", c)
	}

	// The grid goes: the PCS trips, the bus is dead.
	var seen []oneShotJob
	for i := 1; i <= 5; i++ {
		at = run.Add(time.Duration(i) * time.Second)
		s.stepWrite(ctx, at, supplyTick(s, at, bitsBlackoutPQ, 0, essStopped()), d())
		seen = append(seen, drainJobs(s)...)
	}
	if s.ctl.State() != WriteModeStop || !s.gate.Enabled() {
		t.Fatalf("after the grid went: state %s, gate %v", s.ctl.State(), s.gate.Enabled())
	}
	if c := s.writeCmd.Load(); c.ESSKw != 0 {
		t.Fatalf("battery command with the grid gone: %+v", c)
	}
	if len(seen) != 1 || seen[0].addr != 40198 {
		t.Fatalf("one-shots on the grid's loss: %+v, want the battery stop only", seen)
	}
	s.onCommandResult(ctx, cmdResult{id: CmdESSStop, at: at})

	// The grid comes back and stays: still nothing starts.
	back := at
	for i := 1; i <= 15*60; i += 5 {
		at = back.Add(time.Duration(i) * time.Second)
		s.stepWrite(ctx, at, supplyTick(s, at, bitsGridPQ, 228, essStopped()), d())
		seen = append(seen, drainJobs(s)...)
	}
	if len(seen) != 1 {
		t.Fatalf("one-shots after the grid came back: %+v", seen[1:])
	}
	if c := s.writeCmd.Load(); c.State != WriteAwaitStart || c.ESSKw != 0 {
		t.Fatalf("after the grid came back: %+v", c)
	}
	// A person starts it again: the start goes out, then the hold.
	press(t, s, at, CmdESSStart)
	at = at.Add(10 * time.Second)
	s.stepWrite(ctx, at, supplyTick(s, at, bitsGridPQ, 228, essRunning()), d())
	if s.ctl.State() != WriteHold {
		t.Fatalf("restart by the button: %s", s.ctl.State())
	}
}

func TestStartsClosedByThePowerMode(t *testing.T) {
	s := offgridService(t)
	enableForTest(t, s)
	ctx := context.Background()
	at := testTS
	for i := 0; i < 3; i++ {
		at = testTS.Add(time.Duration(i) * time.Second)
		supplyTick(s, at, bitsBlackoutPQ, 0, essStopped())
	}
	if r := s.onCommandRequest(ctx, at, cmdRequest{id: CmdESSStart, by: "t"}); r.OK || !strings.Contains(r.Error, "немає живлення") {
		t.Fatalf("start in a blackout: %+v", r)
	}
	if r := s.onCommandRequest(ctx, at, cmdRequest{id: CmdESSStop, by: "t"}); !r.OK {
		t.Fatalf("a stop is always open: %+v", r)
	}
	for i := 3; i < 6; i++ {
		at = testTS.Add(time.Duration(i) * time.Second)
		supplyTick(s, at, bitsGridVSG, 228, essStopped())
	}
	for _, id := range []string{CmdESSStart, CmdPVStart, CmdArrayStartESS} {
		if r := s.onCommandRequest(ctx, at, cmdRequest{id: id, by: "t"}); r.OK || !strings.Contains(r.Error, "VSG") {
			t.Fatalf("%s with the switch in VSG: %+v", id, r)
		}
	}
	if r := s.onCommandRequest(ctx, at, cmdRequest{id: CmdPVStop, by: "t"}); !r.OK {
		t.Fatalf("PV stop: %+v", r)
	}
}

// §4.3: switch away from PQ, unknown data: the battery at 0, no stop.
func TestRunHoldsZeroWhenThePowerModeClosesIt(t *testing.T) {
	cfg := zeCfg(t).Control.Write
	c := NewController(cfg)
	c.Start(testTS)
	c.ESSStarted(testTS)
	at := testTS.Add(cfg.InitialHold + time.Second)
	in := func(at time.Time, st SupplyState, a *Availability, dec Decision) ControlInputs {
		tk := controlTick(nil)
		tk.TS = at
		return ControlInputs{Now: at, Tick: tk, Decision: dec, ExportCapKw: 850, ImportLimitKw: 1700, Supply: &st, Avail: a}
	}
	grid := SupplyState{Mode: SupplyGrid, Switch: SwitchPQ}
	cmd, _ := c.Step(in(at, grid, availabilityFor(grid), Decision{PBessVirtualKw: 50}))
	if cmd.State != WriteRun || cmd.ESSKw != 50 {
		t.Fatalf("grid/PQ: %+v", cmd)
	}
	vsg := SupplyState{Mode: SupplyGrid, Switch: SwitchVSG}
	cmd, evs := c.Step(in(at.Add(time.Second), vsg, availabilityFor(vsg), Decision{PBessVirtualKw: 50}))
	if cmd.State != WriteRun || cmd.ESSKw != 0 || len(evs) != 0 || !strings.Contains(strings.Join(cmd.Clamps, ";"), "VSG") {
		t.Fatalf("grid/VSG: %+v %v", cmd, evs)
	}
	unknown := SupplyState{Mode: SupplyUnknown, Switch: SwitchUnknown}
	cmd, _ = c.Step(in(at.Add(2*time.Second), unknown, availabilityFor(unknown), Decision{PBessVirtualKw: 50}))
	if cmd.State != WriteRun || cmd.ESSKw != 0 {
		t.Fatalf("unknown: %+v", cmd)
	}
	closedCover := allOpen()
	closedCover.Manual[ManualCover] = Avail{Reason: "тест"}
	cmd, _ = c.Step(in(at.Add(3*time.Second), grid, closedCover, Decision{PBessVirtualKw: 50, ManualType: ManualCover}))
	if cmd.ESSKw != 0 || !strings.Contains(strings.Join(cmd.Clamps, ";"), "«cover» недоступна") {
		t.Fatalf("closed manual: %+v", cmd)
	}
}

func TestModeStopAndGridBack(t *testing.T) {
	cfg := zeCfg(t).Control.Write
	c := NewController(cfg)
	c.Start(testTS)
	c.ESSStarted(testTS)
	step := func(at time.Time, mode string, vals map[string]float64) (WriteCommand, []Event) {
		tk := tickAt(at, vals)
		st := SupplyState{Mode: mode, Switch: SwitchPQ}
		return c.Step(ControlInputs{Now: at, Tick: tk, Decision: Decision{PBessVirtualKw: 80}, ExportCapKw: 850, ImportLimitKw: 1700,
			Supply: &st, Avail: availabilityFor(st)})
	}
	cmd, evs := step(testTS.Add(time.Minute), SupplyGenerator, essStopped())
	if cmd.State != WriteModeStop || cmd.ESSKw != 0 || len(evs) != 1 || evs[0].Code != EvWriteModeStop || !c.StopAuto() {
		t.Fatalf("generator: %+v %v", cmd, evs)
	}
	if c.ESSStartedInSession() {
		t.Fatal("the confirmed start survived the mode stop: «УЗЕ знову зупинилась» would fire")
	}
	cmd, _ = step(testTS.Add(2*time.Minute), SupplyGrid, essStopped())
	if cmd.State != WriteAwaitStart || cmd.ESSKw != 0 || c.StopAuto() {
		t.Fatalf("grid back: %+v", cmd)
	}
	c.ESSStarted(testTS.Add(3 * time.Minute))
	if c.State() != WriteHold {
		t.Fatalf("start after the grid came back: %s", c.State())
	}
}

func TestEnableNeedsGridAndPQ(t *testing.T) {
	s := offgridService(t)
	for i := 0; i < 3; i++ {
		at := testTS.Add(time.Duration(i) * time.Second)
		s.publishSupply(supplyTick(s, at, bitsGridVSG, 228, essStopped()))
	}
	pre := func() WritePrecondition {
		for _, p := range s.writePreconditions(testTS.Add(3 * time.Second)) {
			if p.ID == "supply" {
				return p
			}
		}
		t.Fatal("no supply precondition")
		return WritePrecondition{}
	}
	if p := pre(); p.OK || !strings.Contains(p.Actual, "VSG") {
		t.Fatalf("VSG: %+v", p)
	}
	for i := 3; i < 6; i++ {
		at := testTS.Add(time.Duration(i) * time.Second)
		s.publishSupply(supplyTick(s, at, bitsGridPQ, 228, essStopped()))
	}
	if p := pre(); !p.OK {
		t.Fatalf("grid/PQ: %+v", p)
	}
}
