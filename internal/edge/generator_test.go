package edge

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// genController is a controller already running with the generator.
func genController(t *testing.T) *Controller {
	t.Helper()
	cfg := zeCfg(t)
	c := NewController(cfg.Control.Write).WithOffgrid(cfg.Offgrid)
	c.Start(testTS)
	c.modeStop(testTS, SupplyBlackout)
	c.GenStarted(testTS)
	c.firstConfirmed = true
	if c.State() != WriteGenRun {
		t.Fatalf("state %s", c.State())
	}
	return c
}

type genIn struct {
	at       time.Time
	p        float64 // generator meter
	ess      float64 // battery fact, + discharge
	soc      float64
	pv       float64
	fresh    bool
	age      time.Duration
	mode     string
	settling bool
	dec      Decision
}

func genStep(c *Controller, g genIn) (WriteCommand, []Event) {
	if g.mode == "" {
		g.mode = SupplyGenerator
	}
	tk := tickAt(g.at, map[string]float64{
		"active_ess_power_kw": g.ess, "soc_percent": g.soc, "active_pv_power_kw": g.pv,
		"pcs_shutdown": 0, "pcs_in_operation": 8,
	})
	// With the generator the PCC meter drops out of the logger.
	delete(tk.Values, "grid_connected_active_power_kw")
	tk.GridPowerKw = nil
	for _, k := range meterPhaseKeys {
		tk.Values[k] = 0
	}
	st := SupplyState{Mode: g.mode, Switch: SwitchPQ, Fresh: g.fresh, DataAge: g.age, Settling: g.settling}
	if g.fresh {
		p := g.p
		st.GenPowerKw = &p
	}
	return c.Step(ControlInputs{Now: g.at, Tick: tk, Decision: g.dec, Supply: &st, Avail: availabilityFor(st)})
}

func sec(s int) time.Time { return testTS.Add(time.Duration(s) * time.Second) }

func TestGeneratorLiftsToTheTarget(t *testing.T) {
	c := genController(t)
	// 150 kW: below the minimum; charge so the generator rises to 480,
	// 50 kW more per second.
	cmd, _ := genStep(c, genIn{at: sec(1), p: 150, soc: 50, fresh: true})
	if cmd.ESSKw != -50 {
		t.Fatalf("first second: %+v", cmd)
	}
	cmd, _ = genStep(c, genIn{at: sec(2), p: 200, ess: -50, soc: 50, fresh: true})
	if cmd.ESSKw != -100 {
		t.Fatalf("second second: %+v", cmd)
	}
	// The battery charges 300, the generator reads 470: inside the band.
	c.gen.last = -300
	cmd, _ = genStep(c, genIn{at: sec(3), p: 470, ess: -300, soc: 50, fresh: true})
	if cmd.ESSKw != -300 {
		t.Fatalf("deadband: %+v", cmd)
	}
	// The load fell: 600 kW of generator with 300 of charge → charge 180.
	cmd, _ = genStep(c, genIn{at: sec(4), p: 600, ess: -300, soc: 50, fresh: true})
	if cmd.ESSKw != -180 {
		t.Fatalf("less charge: %+v", cmd)
	}
	if cmd.State != WriteGenRun || cmd.PVLimitKw != 600 {
		t.Fatalf("state/PV: %+v", cmd)
	}
}

func TestGeneratorDischargesOnlyOverTheRating(t *testing.T) {
	c := genController(t)
	cmd, _ := genStep(c, genIn{at: sec(1), p: 800, soc: 50, fresh: true})
	if cmd.ESSKw != 50 {
		t.Fatalf("over the rating: %+v", cmd)
	}
	cmd, _ = genStep(c, genIn{at: sec(2), p: 760, ess: 50, soc: 50, fresh: true})
	if cmd.ESSKw != 76 {
		t.Fatalf("over the rating, second: %+v", cmd)
	}
	cmd, _ = genStep(c, genIn{at: sec(3), p: 900, ess: 76, soc: 19, fresh: true})
	if cmd.ESSKw != 0 || !strings.Contains(strings.Join(cmd.Clamps, ";"), "розряд заборонено") {
		t.Fatalf("at the SOC floor: %+v", cmd)
	}
}

func TestGeneratorFullBatteryLowersThePV(t *testing.T) {
	c := genController(t)
	cmd, evs := genStep(c, genIn{at: sec(1), p: 150, soc: 90, pv: 100, fresh: true})
	if cmd.ESSKw != 0 || cmd.PVLimitKw != 30 || len(evs) != 0 {
		t.Fatalf("full, sun up: %+v %v", cmd, evs)
	}
	_, evs = genStep(c, genIn{at: sec(2), p: 150, soc: 90, pv: 0, fresh: true})
	if len(evs) != 1 || evs[0].Code != EvGenBelowMin {
		t.Fatalf("full, no sun: %v", evs)
	}
}

func TestGeneratorReversePower(t *testing.T) {
	c := genController(t)
	c.gen.last = 40
	genStep(c, genIn{at: sec(1), p: -3, ess: 40, soc: 50, pv: 200, fresh: true})
	cmd, evs := genStep(c, genIn{at: sec(3), p: -5, ess: 40, soc: 50, pv: 200, fresh: true})
	if cmd.ESSKw > 0 || cmd.PVLimitKw >= 200 || len(evs) == 0 || evs[0].Code != EvGenReversePower {
		t.Fatalf("reverse power: %+v %v", cmd, evs)
	}
}

// §6: stale data — discharge 0 at once, the charge holds; after 30 s
// the charge too; back — the control goes on, nothing stops.
func TestGeneratorStaleData(t *testing.T) {
	c := genController(t)
	c.gen.last = -200
	cmd, evs := genStep(c, genIn{at: sec(1), soc: 50, fresh: false, age: 8 * time.Second, mode: SupplyUnknown})
	if cmd.ESSKw != -200 || cmd.State != WriteGenRun || len(evs) != 1 || evs[0].Code != EvGenDataStale {
		t.Fatalf("stale: %+v %v", cmd, evs)
	}
	c.gen.last = 60
	cmd, _ = genStep(c, genIn{at: sec(2), soc: 50, fresh: false, age: 9 * time.Second, mode: SupplyUnknown})
	if cmd.ESSKw != 0 {
		t.Fatalf("stale discharge: %+v", cmd)
	}
	c.gen.last = -200
	cmd, _ = genStep(c, genIn{at: sec(30), soc: 50, fresh: false, age: 31 * time.Second, mode: SupplyUnknown})
	if cmd.ESSKw != 0 || cmd.State != WriteGenRun {
		t.Fatalf("stale 31 s: %+v", cmd)
	}
	cmd, _ = genStep(c, genIn{at: sec(31), p: 300, soc: 50, fresh: true})
	if cmd.ESSKw != -50 || cmd.State != WriteGenRun {
		t.Fatalf("data back: %+v", cmd)
	}
}

func TestGeneratorManualPauseAndTarget(t *testing.T) {
	c := genController(t)
	cmd, _ := genStep(c, genIn{at: sec(1), p: 300, soc: 50, fresh: true, dec: Decision{ManualType: ManualHold}})
	if cmd.ESSKw != 0 {
		t.Fatalf("pause: %+v", cmd)
	}
	cmd, _ = genStep(c, genIn{at: sec(2), p: 300, soc: 60, fresh: true, dec: Decision{ManualType: ManualTarget, ManualTargetPct: 60}})
	if cmd.ESSKw != 0 {
		t.Fatalf("target reached: %+v", cmd)
	}
	cmd, _ = genStep(c, genIn{at: sec(3), p: 300, soc: 55, fresh: true, dec: Decision{ManualType: ManualTarget, ManualTargetPct: 60}})
	if cmd.ESSKw != -50 {
		t.Fatalf("target below: %+v", cmd)
	}
}

func TestGeneratorModeChanges(t *testing.T) {
	c := genController(t)
	cmd, _ := genStep(c, genIn{at: sec(1), p: 300, soc: 50, fresh: true, settling: true})
	if cmd.ESSKw != 0 || cmd.State != WriteGenRun {
		t.Fatalf("settling: %+v", cmd)
	}
	cmd, evs := genStep(c, genIn{at: sec(2), p: 300, soc: 50, fresh: true, mode: SupplyTransition})
	if cmd.ESSKw != 0 || cmd.State != WriteGenRun || len(evs) != 0 {
		t.Fatalf("transition: %+v %v", cmd, evs)
	}
	// No PCC meter and dead meter phases for a minute: no grid check
	// stops the battery that runs with the generator.
	for s := 3; s < 70; s++ {
		cmd, evs = genStep(c, genIn{at: sec(s), p: 480, soc: 50, fresh: true})
		if cmd.State != WriteGenRun {
			t.Fatalf("a grid check stopped the generator run at %d s: %+v %v", s, cmd, evs)
		}
	}
	cmd, evs = genStep(c, genIn{at: sec(70), p: 0, soc: 50, fresh: true, mode: SupplyBlackout})
	if cmd.State != WriteModeStop || cmd.ESSKw != 0 || len(evs) != 1 || !strings.Contains(evs[0].Message, "генератор") || !c.StopAuto() {
		t.Fatalf("generator off the bus: %+v %v", cmd, evs)
	}
}

func genService(t *testing.T) *Service {
	t.Helper()
	s := offgridService(t)
	s.cfg.Offgrid.GeneratorEnabled = true
	enableForTest(t, s)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		s.stepWrite(ctx, sec(i), supplyTick(s, sec(i), bitsGridPQ, 228, essStopped()), nil)
		if i == 0 {
			drainJobs(s)
			s.onCommandResult(ctx, cmdResult{id: CmdPCSPQ, at: sec(0)})
		}
	}
	if st, _ := s.commandStatus(CmdPCSPQ); st.State != CmdConfirmed {
		t.Fatalf("PQ at enabling: %+v", st)
	}
	for i := 3; i < 6; i++ {
		s.stepWrite(ctx, sec(i), supplyTick(s, sec(i), bitsBlackoutPQ, 0, essStopped()), nil)
	}
	for i := 6; i < 9; i++ {
		s.stepWrite(ctx, sec(i), supplyTick(s, sec(i), bitsGenPQ, 231, essStopped()), nil)
	}
	if s.ctl.State() != WriteModeStop || s.supplyNow.Mode != SupplyGenerator {
		t.Fatalf("setup: %s / %s", s.ctl.State(), s.supplyNow.Mode)
	}
	if j := drainJobs(s); len(j) != 0 {
		t.Fatalf("one-shots before any button: %+v", j)
	}
	return s
}

func TestStartWithGenerator(t *testing.T) {
	s := genService(t)
	ctx := context.Background()
	if r := s.onCommandRequest(ctx, sec(9), cmdRequest{id: CmdESSStart, by: "t"}); r.OK || !strings.Contains(r.Error, "з генератором") {
		t.Fatalf("plain start with the generator: %+v", r)
	}
	if r := s.onCommandRequest(ctx, sec(9), cmdRequest{id: CmdGenESSStart, by: "t"}); !r.OK {
		t.Fatalf("gen start: %+v", r)
	}
	if j := drainJobs(s); len(j) != 1 || j[0].addr != 40199 {
		t.Fatalf("gen start jobs %+v", j)
	}
	s.onCommandResult(ctx, cmdResult{id: CmdGenESSStart, at: sec(9)})
	gen := merge(essRunning(), map[string]float64{"active_ess_power_kw": 0})
	s.stepWrite(ctx, sec(20), supplyTick(s, sec(20), bitsGenPQ, 231, gen), nil)
	if s.ctl.State() != WriteGenRun {
		t.Fatalf("after the confirmed start: %s", s.ctl.State())
	}
	// Data gone 40 s, then back: the control holds and goes on; nothing
	// is started or stopped.
	for i := 21; i < 60; i++ {
		tk := tickAt(sec(i), gen)
		s.observeSupply(ctx, sec(i), &tk)
		s.stepWrite(ctx, sec(i), tk, nil)
	}
	if s.ctl.State() != WriteGenRun {
		t.Fatalf("stale data stopped the run: %s", s.ctl.State())
	}
	for i := 60; i < 64; i++ {
		s.stepWrite(ctx, sec(i), supplyTick(s, sec(i), bitsGenPQ, 231, gen), nil)
	}
	if j := drainJobs(s); len(j) != 0 || s.ctl.State() != WriteGenRun {
		t.Fatalf("after the data came back: %s, jobs %+v", s.ctl.State(), j)
	}
	// The generator's breaker opens: battery stop, the session goes on.
	for i := 64; i < 68; i++ {
		s.stepWrite(ctx, sec(i), supplyTick(s, sec(i), bitsBlackoutPQ, 0, gen), nil)
	}
	if j := drainJobs(s); len(j) != 1 || j[0].addr != 40198 || s.ctl.State() != WriteModeStop || !s.gate.Enabled() {
		t.Fatalf("generator off: %s, jobs %+v", s.ctl.State(), j)
	}
}

// The button's own sequence: PQ first, its confirmation, then the start.
func TestStartWithGeneratorSetsPQFirst(t *testing.T) {
	s := genService(t)
	ctx := context.Background()
	vsg := merge(essStopped(), map[string]float64{"pcs_working_mode": 1})
	s.stepWrite(ctx, sec(9), supplyTick(s, sec(9), bitsGenPQ, 231, vsg), nil)
	if r := s.onCommandRequest(ctx, sec(9), cmdRequest{id: CmdGenESSStart, by: "t"}); !r.OK {
		t.Fatalf("gen start: %+v", r)
	}
	if j := drainJobs(s); len(j) != 1 || j[0].addr != 44365 {
		t.Fatalf("PQ first: %+v", j)
	}
	s.onCommandResult(ctx, cmdResult{id: CmdPCSPQ, at: sec(9)})
	if r := s.onCommandRequest(ctx, sec(10), cmdRequest{id: CmdGenESSStart, by: "t"}); r.OK {
		t.Fatal("a second press while PQ is pending")
	}
	s.stepWrite(ctx, sec(11), supplyTick(s, sec(11), bitsGenPQ, 231, essStopped()), nil)
	if j := drainJobs(s); len(j) != 1 || j[0].addr != 40199 {
		t.Fatalf("start after PQ: %+v", j)
	}
}

// The off-grid buttons take the device PIN; a wrong one never reaches
// the core loop.
func TestOffgridButtonNeedsThePIN(t *testing.T) {
	s := genService(t)
	if err := SetWritePin(s.cfg.Control.Write.PinPath, "246813", testTS); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{`{"command":"gen_ess_start"}`, `{"command":"gen_ess_start","pin":"111111"}`} {
		rec := httptest.NewRecorder()
		s.uiCommand(rec, httptest.NewRequest(http.MethodPost, "/api/command", strings.NewReader(body)))
		if rec.Code != http.StatusConflict {
			t.Fatalf("%s: status %d %s", body, rec.Code, rec.Body.String())
		}
	}
	if j := drainJobs(s); len(j) != 0 {
		t.Fatalf("jobs %+v", j)
	}
}

func TestStartWithGeneratorNeedsTheOwnersSwitch(t *testing.T) {
	s := genService(t)
	s.cfg.Offgrid.GeneratorEnabled = false
	ctx := context.Background()
	s.stepWrite(ctx, sec(9), supplyTick(s, sec(9), bitsGenPQ, 231, essStopped()), nil)
	if r := s.onCommandRequest(ctx, sec(9), cmdRequest{id: CmdGenESSStart, by: "t"}); r.OK || !strings.Contains(r.Error, "generator_enabled") {
		t.Fatalf("flag off: %+v", r)
	}
	plain := writeService(t, zeCfg(t))
	enableForTest(t, plain)
	if r := plain.onCommandRequest(ctx, testTS, cmdRequest{id: CmdGenESSStart, by: "t"}); r.OK {
		t.Fatalf("encombi off: %+v", r)
	}
	if j := drainJobs(s); len(j) != 0 {
		t.Fatalf("jobs %+v", j)
	}
}
