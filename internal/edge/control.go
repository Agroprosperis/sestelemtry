package edge

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// Write states of the controller.
const (
	WriteOff        = "off"         // flag off: nothing is written
	WriteAwaitStart = "await_start" // flag on, the battery not started yet: 0 kW
	WriteHold       = "hold"        // battery start confirmed: 0 kW for the hold
	WriteRun        = "run"         // the engine's commands go out
	WriteStopped    = "stopped"     // a stop latched: battery 0, PV at its rating
	WriteStopping   = "stopping"    // turning off: safe values, then the flag drops
	// The grid went (offgrid §2): battery 0 and a stop of what the test
	// started, like a latched stop, but the session goes on. The new
	// mode's actions are buttons; the grid's return waits for «Пуск УЗЕ».
	WriteModeStop = "mode_stop"
	// The battery runs with the generator (offgrid §6), started by
	// «Пуск УЗЕ з генератором».
	WriteGenRun = "gen_run"
	// The island (offgrid §5): its entry sequence, the battery holding
	// the bus, the exit sequence. 40381 is not written in any of them.
	WriteIslandStart = "island_start"
	WriteIsland      = "island"
	WriteIslandStop  = "island_stop"
)

// islandState: the battery is (being) put in VSG; its power is its own.
func islandState(s string) bool {
	return s == WriteIslandStart || s == WriteIsland || s == WriteIslandStop
}

// EvWriteModeStop journals the stop on the grid's loss.
const EvWriteModeStop = "WRITE_MODE_STOP"

// Thresholds of the stops (ems_sl_write_cutover.md §9, §12.3, §12.4).
const (
	stopVoltageAfter  = 10 * time.Second // phase < 200 V or line < 350 V
	stopPollLossAfter = 10 * time.Second // telemetry stale or faulted
	stopExportAfter   = time.Minute      // PCC export above the ceiling
	stopImportAfter   = 5 * time.Second  // PCC import at 95 % of the limit
	stopPCSModeAfter  = 10 * time.Second // 44364 not PQ after the start (§12.7)
	importStopShare   = 0.95
	factWindow        = 10 * time.Second // commands the fact is compared with
	factStopAfter     = 30 * time.Second // outside the tolerance this long
	factConfirmAfter  = 10 * time.Second // a non-zero command followed this long
	factZeroTolKw     = 2.0
	factMinTolKw      = 10.0
	// Right after the confirmed battery start, at command 0: ze 18.11
	// 12:19 and 12:37, SOC 95 %, the battery wandered −34…+20 kW for half
	// a minute while its cabinets came up one by one (owner's decision).
	startGrace     = 2 * time.Minute
	startZeroTolKw = 50.0
	// Above the SOC window the battery balances its cabinets on its own:
	// ze 18.11, SOC 94.6 %, about −20 kW at command 0 and 25 kW for 45 kW
	// (owner's decision).
	highSocTolKw       = 50.0
	factTolShare       = 0.05
	stoppingTimeout    = 15 * time.Second
	commandStaleAfter  = 5 * time.Second // writers fall back to safe values
	firstCommandEpsKw  = deadbandKw
	inverterFaultClass = InvFault
)

// WriteCommand is what the writers put into the SmartLoggers.
type WriteCommand struct {
	At        time.Time `json:"at"`
	State     string    `json:"state"`
	ESSKw     float64   `json:"ess_kw"`      // 40381: + discharge / − charge
	PVLimitKw float64   `json:"pv_limit_kw"` // 40378: PV ceiling
	PVKnown   bool      `json:"pv_known"`    // false: 40396 never read, PV is not written
	// ESSHold: the battery logger's setpoints are not written at all
	// (offgrid §5.2: in the island the battery's power is its own).
	ESSHold bool     `json:"ess_hold,omitempty"`
	Reason  string   `json:"reason"`
	Clamps  []string `json:"clamps,omitempty"`
}

// ControlInputs is one tick's view for the controller.
type ControlInputs struct {
	Now      time.Time
	Tick     Tick
	Decision Decision
	// ExportCapKw/ImportLimitKw are the PCC limits in force (manifest,
	// else device config). 0 = unknown.
	ExportCapKw   float64
	ImportLimitKw float64
	// InverterCount/InverterFaults size the PV ceiling (1/N per fault).
	InverterCount  int
	InverterFaults int
	// Supply/Avail are the power mode and what it allows; nil while
	// encombi is off, and then nothing depends on them.
	Supply *SupplyState
	Avail  *Availability
}

// offGridMode: a power mode in which the grid is not on the bus, taken
// on fresh data.
func offGridMode(mode string) bool {
	return mode == SupplyBlackout || mode == SupplyIsland || mode == SupplyGenerator
}

type cmdSample struct {
	at time.Time
	kw float64
}

// Controller turns the engine's verdict into what goes into the
// SmartLoggers while the write flag is on, and decides when to stop.
// It lives in the core loop only (no locks).
type Controller struct {
	cfg WriteConfig
	og  OffgridConfig
	gen genState
	// islandPV: the island's PV start went out; essHold: the battery's
	// setpoints stay unwritten, also while turning off from the island.
	islandPV bool
	essHold  bool
	// extIsland: the battery holds the bus in VSG, but «Пуск острова»
	// did not start it (another controller, a hand on the logger).
	extIsland bool

	state      string
	since      time.Time
	reason     string
	stoppingAt time.Time
	// essStarted: a battery start was confirmed in this write session
	// (cutover 04.11.2026 §4). stopAuto: the latched stop came from a
	// check, not from the operator's «0 кВт».
	essStarted   bool
	essStartedAt time.Time
	stopAuto     bool

	firstConfirmed bool
	followSince    time.Time
	lowVoltSince   time.Time
	pollLossSince  time.Time
	pccLossSince   time.Time
	essLossSince   time.Time
	pcsModeSince   time.Time
	exportSince    time.Time
	importSince    time.Time
	deviationSince time.Time
	history        []cmdSample
	lastPVRatedKw  float64
}

func NewController(cfg WriteConfig) *Controller {
	return &Controller{cfg: cfg, state: WriteOff}
}

// WithOffgrid gives the controller the site figures without the grid.
func (c *Controller) WithOffgrid(og OffgridConfig) *Controller {
	c.og = og
	return c
}

// State and Reason describe the controller for the journal and consoles.
func (c *Controller) State() string  { return c.state }
func (c *Controller) Reason() string { return c.reason }
func (c *Controller) Since() time.Time {
	return c.since
}

// Start waits for the battery start after the flag went on: the
// battery was stopped before the hand-over, so only 0 kW goes out.
func (c *Controller) Start(now time.Time) {
	*c = Controller{cfg: c.cfg, og: c.og, lastPVRatedKw: c.lastPVRatedKw}
	c.state, c.since, c.reason = WriteAwaitStart, now, "запис увімкнено — УЗЕ не запущена, уставка 0"
}

// ESSStarted enters the hold once the battery start is confirmed; the
// hold is counted from here, not from turning writing on.
func (c *Controller) ESSStarted(now time.Time) {
	if c.state != WriteAwaitStart {
		return
	}
	c.essStarted, c.essStartedAt = true, now
	c.state, c.since, c.reason = WriteHold, now, "пуск УЗЕ підтверджено — утримання на 0"
}

// ESSStartedInSession reports a confirmed battery start since writing
// went on.
func (c *Controller) ESSStartedInSession() bool { return c.essStarted }

// StopAuto reports a stop that also stops what the test started: a
// latched stop from a check, or the grid's loss.
func (c *Controller) StopAuto() bool {
	return (c.state == WriteStopped || c.state == WriteModeStop) && c.stopAuto
}

// modeStop: the power mode changed under a battery that was waiting,
// holding or running: the grid went, or the generator left the bus.
func (c *Controller) modeStop(now time.Time, mode string) Event {
	label := supplyModeLabels[mode]
	what := "мережа зникла (" + label + ")"
	if c.state == WriteGenRun {
		what = "генератор більше не живить шину (" + label + ")"
	}
	c.state, c.since, c.stopAuto = WriteModeStop, now, true
	c.reason = "живлення: " + label + " — УЗЕ 0, дії режиму лише кнопками"
	c.essStarted, c.essStartedAt, c.firstConfirmed = false, time.Time{}, false
	c.gen = genState{}
	c.resetChecks()
	return Event{
		TS: now, Severity: SevAlarm, Code: EvWriteModeStop,
		Message: what + ": УЗЕ 0 кВт, зупинка того, що запускав тест; запис лишається, далі — кнопками",
		Context: map[string]any{"mode": mode},
	}
}

// gridBack: the grid is on the bus again; the battery stays at 0 until
// «Пуск УЗЕ» is pressed and confirmed, then the hold, then the plan.
func (c *Controller) gridBack(now time.Time) {
	c.state, c.since, c.stopAuto = WriteAwaitStart, now, false
	c.reason = "мережа повернулась — УЗЕ 0 до «Пуск УЗЕ»"
	c.resetChecks()
}

func (c *Controller) resetChecks() {
	c.followSince, c.lowVoltSince, c.pollLossSince, c.pccLossSince = time.Time{}, time.Time{}, time.Time{}, time.Time{}
	c.essLossSince, c.pcsModeSince, c.exportSince, c.importSince = time.Time{}, time.Time{}, time.Time{}, time.Time{}
	c.deviationSince = time.Time{}
}

// Off forgets everything once the flag is off.
func (c *Controller) Off() {
	*c = Controller{cfg: c.cfg, og: c.og, state: WriteOff, lastPVRatedKw: c.lastPVRatedKw}
}

// GenStarted: «Пуск УЗЕ з генератором» is confirmed; the generator
// control takes over.
func (c *Controller) GenStarted(now time.Time) {
	if c.state != WriteModeStop {
		return
	}
	c.essStarted, c.essStartedAt = true, now
	c.state, c.since, c.stopAuto = WriteGenRun, now, false
	c.reason = fmt.Sprintf("УЗЕ з генератором: дозавантаження до %.0f кВт", c.og.GeneratorTargetKw)
	c.gen = genState{}
	c.resetChecks()
}

// IslandBegin: «Пуск острова» pressed on the dead bus after the grid's
// loss; the entry sequence runs.
func (c *Controller) IslandBegin(now time.Time, by string) bool {
	if c.state != WriteModeStop {
		return false
	}
	c.state, c.since, c.stopAuto = WriteIslandStart, now, false
	c.reason = "«Пуск острова» (" + by + "): вхід в острів"
	c.islandPV, c.essHold = false, true
	c.resetChecks()
	return true
}

// TurningOffFromIsland: writing is being turned off while the battery
// may still be in VSG; turning off puts it back in PQ after its stop.
func (c *Controller) TurningOffFromIsland() bool { return c.state == WriteStopping && c.essHold }

// ExternalIsland: the battery holds the bus in VSG and «Пуск острова»
// did not start it. Nothing is stopped for it automatically: the stop
// would black the site out (§5.2).
func (c *Controller) ExternalIsland() bool { return c.extIsland }

// observeExtIsland latches an island the console did not start: the bus
// live without the grid and the generator, 44364 = 1. The grid, the
// generator or a dead bus end it; data gone stale does not.
func (c *Controller) observeExtIsland(in ControlInputs) []Event {
	switch in.Supply.Mode {
	case SupplyIsland:
		if c.extIsland || islandState(c.state) || !pcsModeVSG(in.Tick) {
			return nil
		}
		c.extIsland = true
		return []Event{{
			TS: in.Now, Severity: SevAlarm, Code: EvIslandExternal,
			Message: "острів тримає УЗЕ у VSG, але не «Пуск острова»: уставки УЗЕ не пишуться, стеля СЕС за зарядом, сама система нічого не зупиняє",
		}}
	case SupplyGrid, SupplyBlackout, SupplyGenerator:
		c.extIsland = false
	}
	return nil
}

// IslandPVOn: the island's PV start went out; its ceiling follows §5.2.
func (c *Controller) IslandPVOn() { c.islandPV = true }

// IslandUp: the battery holds the bus.
func (c *Controller) IslandUp(now time.Time) {
	if c.state != WriteIslandStart {
		return
	}
	c.state, c.since, c.reason = WriteIsland, now, "острів: УЗЕ тримає шину"
	c.essStarted, c.essStartedAt = true, now
}

// IslandExit starts the exit sequence (§5.3).
func (c *Controller) IslandExit(now time.Time, why string) {
	if c.state != WriteIslandStart && c.state != WriteIsland {
		return
	}
	c.state, c.since, c.reason = WriteIslandStop, now, "вихід з острова: "+why
	c.islandPV = false
}

// IslandDone: the battery is stopped and back in PQ; the session goes
// on, the power mode decides what is next.
func (c *Controller) IslandDone(now time.Time, reason string) {
	if !islandState(c.state) {
		return
	}
	c.state, c.since, c.stopAuto, c.reason = WriteModeStop, now, false, reason
	c.essStarted, c.essStartedAt, c.islandPV, c.essHold = false, time.Time{}, false, false
	c.resetChecks()
}

// islandPVCeiling is 40378 in the island (§5.2): 0 until the island's
// PV start, then islandPVRule.
func (c *Controller) islandPVCeiling(in ControlInputs, clamps *[]string) (float64, bool) {
	if c.state != WriteIsland || !c.islandPV {
		return 0, true
	}
	return c.islandPVRule(in, clamps)
}

// islandPVRule is the sun in an island (§5.2): the rating while the
// charge is below 88 %, above it no more than the load, so the sun does
// not overcharge the battery.
func (c *Controller) islandPVRule(in ControlInputs, clamps *[]string) (float64, bool) {
	ceiling, known := c.pvCeiling(in, clamps)
	t := in.Tick
	if !known || t.SocPercent == nil || *t.SocPercent < c.og.IslandPVFullSocPct || t.PVPowerKw == nil || t.ESSPowerKw == nil {
		return ceiling, known
	}
	load := math.Max(0, *t.PVPowerKw+*t.ESSPowerKw)
	if load < ceiling {
		*clamps = append(*clamps, fmt.Sprintf("заряд %.1f%% ≥ %.0f%% — стеля СЕС до споживання %.0f кВт", *t.SocPercent, c.og.IslandPVFullSocPct, load))
		ceiling = load
	}
	return ceiling, true
}

// OperatorStopOffgrid: the operator stopped the battery that runs
// without the grid. The session goes on; the mode's start button is
// open again, nothing else is stopped for it.
func (c *Controller) OperatorStopOffgrid(now time.Time, reason string) {
	if c.state != WriteGenRun {
		return
	}
	c.state, c.since, c.stopAuto, c.reason = WriteModeStop, now, false, reason+" — УЗЕ зупинено, запис лишається"
	c.essStarted, c.essStartedAt = false, time.Time{}
	c.resetChecks()
}

// Stop latches a stop: from now on only safe values go out. auto marks
// a stop from a check (it also stops what the test started).
func (c *Controller) Stop(now time.Time, reason string, auto bool) (Event, bool) {
	if c.state == WriteOff || c.state == WriteStopped || c.state == WriteStopping {
		return Event{}, false
	}
	c.state, c.since, c.reason, c.stopAuto = WriteStopped, now, reason, auto
	return Event{
		TS: now, Severity: SevAlarm, Code: EvWriteStop,
		Message: "зупинка запису: " + reason + " — УЗЕ 0 кВт, СЕС на номіналі",
		Context: map[string]any{"reason": reason},
	}, true
}

// BeginStopping starts turning off: safe values until both loggers
// confirm them, then the flag drops.
func (c *Controller) BeginStopping(now time.Time, reason string) {
	if c.state == WriteOff || c.state == WriteStopping {
		return
	}
	c.state, c.since, c.reason, c.stoppingAt = WriteStopping, now, reason, now
}

// StoppingSince is when turning off began; zero otherwise.
func (c *Controller) StoppingSince() time.Time {
	if c.state != WriteStopping {
		return time.Time{}
	}
	return c.stoppingAt
}

// StoppingTimedOut reports that the loggers did not confirm the safe
// values in time; the flag drops anyway.
func (c *Controller) StoppingTimedOut(now time.Time) bool {
	return c.state == WriteStopping && now.Sub(c.stoppingAt) > stoppingTimeout
}

// Step computes this tick's command and any stop. It is called only
// while the flag is on.
func (c *Controller) Step(in ControlInputs) (WriteCommand, []Event) {
	var events []Event
	t := in.Tick
	if v, ok := t.Values["pv_rated_kw"]; ok && v > 0 {
		c.lastPVRatedKw = v
	}
	if c.state == WriteOff {
		c.Start(in.Now)
	}
	if c.state == WriteHold && in.Now.Sub(c.since) >= c.cfg.InitialHold {
		c.state, c.since, c.reason = WriteRun, in.Now, "робота"
	}
	if in.Supply != nil {
		switch m := in.Supply.Mode; {
		case offGridMode(m) && (c.state == WriteAwaitStart || c.state == WriteHold || c.state == WriteRun):
			events = append(events, c.modeStop(in.Now, m))
		case m == SupplyGrid && c.state == WriteModeStop:
			c.gridBack(in.Now)
		case c.state == WriteGenRun && (m == SupplyGrid || m == SupplyBlackout || m == SupplyIsland):
			// §6: the generator's breaker opened or the bus went dead.
			events = append(events, c.modeStop(in.Now, m))
		}
		events = append(events, c.observeExtIsland(in)...)
	}
	settling := in.Supply != nil && in.Supply.Settling
	gridStates := c.state == WriteAwaitStart || c.state == WriteHold || c.state == WriteRun
	if (gridStates || c.state == WriteGenRun) && !settling {
		if reason := c.checkStops(in, gridStates); reason != "" {
			if ev, ok := c.Stop(in.Now, reason, true); ok {
				events = append(events, ev)
			}
		}
	}

	cmd := WriteCommand{At: in.Now, State: c.state, Reason: c.reason}
	switch c.state {
	case WriteRun:
		cmd.PVLimitKw, cmd.PVKnown = c.pvCeiling(in, &cmd.Clamps)
		cmd.ESSKw = c.essCommand(in, &cmd.Clamps)
		cmd.Reason = in.Decision.ReasonCode
	case WriteAwaitStart, WriteHold:
		cmd.PVLimitKw, cmd.PVKnown = c.pvCeiling(in, &cmd.Clamps)
	case WriteGenRun:
		events = append(events, c.genCommand(in, &cmd)...)
		if settling {
			cmd.ESSKw, c.gen.last = 0, 0
			cmd.Clamps = append(cmd.Clamps, "живлення змінюється — УЗЕ 0")
		}
	case WriteIslandStart, WriteIsland, WriteIslandStop:
		cmd.ESSHold = true
		cmd.PVLimitKw, cmd.PVKnown = c.islandPVCeiling(in, &cmd.Clamps)
	default:
		if c.extIsland {
			cmd.ESSHold = true
			cmd.PVLimitKw, cmd.PVKnown = c.islandPVRule(in, &cmd.Clamps)
			cmd.Clamps = append(cmd.Clamps, "острів не від «Пуск острова» — уставки УЗЕ не пишуться")
			break
		}
		// Stopped, turning off or the grid gone: battery 0, PV on its
		// rating (§12.4).
		rated := c.pvRated(t)
		cmd.PVLimitKw, cmd.PVKnown = rated, rated > 0
		cmd.ESSHold = c.state == WriteStopping && c.essHold
	}
	if c.state == WriteRun && cmd.ESSKw != 0 && settling {
		cmd.ESSKw = 0
		cmd.Clamps = append(cmd.Clamps, "живлення змінюється — УЗЕ 0")
	}
	if c.state == WriteRun && cmd.ESSKw != 0 && in.Avail != nil {
		if a := in.Avail.Power; !a.OK {
			cmd.ESSKw = 0
			cmd.Clamps = append(cmd.Clamps, a.Reason+" — УЗЕ 0")
		} else if typ := in.Decision.ManualType; typ != "" {
			if a := in.Avail.ManualOK(typ); !a.OK {
				cmd.ESSKw = 0
				cmd.Clamps = append(cmd.Clamps, "ручна команда «"+typ+"» недоступна: "+a.Reason+" — УЗЕ 0")
			}
		}
	}
	if c.state != WriteGenRun && !islandState(c.state) && !c.extIsland {
		// No PCC on the generator or in the island: the ceiling of
		// export does not apply.
		cmd.ESSKw, cmd.PVLimitKw = c.exportCeiling(in, cmd.ESSKw, cmd.PVLimitKw, cmd.PVKnown, &cmd.Clamps)
	}
	cmd.ESSKw = round1(cmd.ESSKw)
	cmd.PVLimitKw = round1(cmd.PVLimitKw)

	c.track(in, cmd.ESSKw)
	return cmd, events
}

// essCommand applies the write-side clamps to the engine's battery
// command: the hard SOC window, the logger's available power and the
// first-command cap. The engine already applied its own chain; these
// hold whatever the manifest said.
func (c *Controller) essCommand(in ControlInputs, clamps *[]string) float64 {
	t := in.Tick
	p := in.Decision.PBessVirtualKw
	note := func(s string) { *clamps = append(*clamps, s) }
	if p == 0 {
		return 0
	}
	if t.SocPercent == nil {
		note("SOC невідомий — 0")
		return 0
	}
	soc := *t.SocPercent
	if p > 0 && soc <= c.cfg.SocMinPct {
		note(fmt.Sprintf("SOC %.1f%% ≤ %.0f%% — розряд заборонено", soc, c.cfg.SocMinPct))
		return 0
	}
	if p < 0 && soc >= c.cfg.SocMaxPct {
		note(fmt.Sprintf("SOC %.1f%% ≥ %.0f%% — заряд заборонено", soc, c.cfg.SocMaxPct))
		return 0
	}
	if p > 0 {
		if t.ESSDischargeMaxKw == nil {
			note("40492 невідомий — розряд 0")
			return 0
		}
		if p > *t.ESSDischargeMaxKw {
			note(fmt.Sprintf("доступний розряд %.0f кВт (40492)", *t.ESSDischargeMaxKw))
			p = *t.ESSDischargeMaxKw
		}
	} else {
		if t.ESSChargeMaxKw == nil {
			note("40490 невідомий — заряд 0")
			return 0
		}
		if -p > *t.ESSChargeMaxKw {
			note(fmt.Sprintf("доступний заряд %.0f кВт (40490)", *t.ESSChargeMaxKw))
			p = -*t.ESSChargeMaxKw
		}
	}
	if !c.firstConfirmed && math.Abs(p) > c.cfg.FirstCommandMaxKw {
		note(fmt.Sprintf("перша ненульова команда ≤ %.0f кВт", c.cfg.FirstCommandMaxKw))
		p = math.Copysign(c.cfg.FirstCommandMaxKw, p)
	}
	return p
}

// pvRated is the PV rating to write: the last 40396 read, never above
// the one read now. While PV starts the logger's 40396 grows inverter
// by inverter (ze 17.11: 165, 550, 660 kW); with every inverter off it
// reads 0 and PV is not written.
func (c *Controller) pvRated(t Tick) float64 {
	rated := c.lastPVRatedKw
	if v, ok := t.Values["pv_rated_kw"]; ok && v < rated {
		rated = v
	}
	return rated
}

// pvCeiling is 40378 before the export ceiling: the rating from 40396,
// lowered by the share of the inverters in fault. false when 40396 has
// never been read or reads 0: PV is then not written at all.
func (c *Controller) pvCeiling(in ControlInputs, clamps *[]string) (float64, bool) {
	rated := c.pvRated(in.Tick)
	if rated <= 0 {
		return 0, false
	}
	if n, f := in.InverterCount, in.InverterFaults; n > 0 && f > 0 {
		if f > n {
			f = n
		}
		*clamps = append(*clamps, fmt.Sprintf("стеля СЕС − %d/%d інверторів у несправності", f, n))
		return rated * float64(n-f) / float64(n), true
	}
	return rated, true
}

// exportCeiling keeps the PCC export at or under the ceiling: first less
// discharge, then a lower PV ceiling (cutover §4, §12.3 п. 3).
func (c *Controller) exportCeiling(in ControlInputs, ess, pvLimit float64, pvKnown bool, clamps *[]string) (float64, float64) {
	t := in.Tick
	capKw := in.ExportCapKw
	if capKw <= 0 || t.GridPowerKw == nil || t.ESSPowerKw == nil {
		// Unknown ceiling or PCC: no battery export at all.
		if ess > 0 && t.LoadPowerKw != nil && t.PVPowerKw != nil {
			if deficit := math.Max(0, *t.LoadPowerKw-*t.PVPowerKw); ess > deficit {
				*clamps = append(*clamps, "PCC або стеля експорту невідомі — розряд до дефіциту")
				ess = deficit
			}
		} else if ess > 0 {
			*clamps = append(*clamps, "PCC або стеля експорту невідомі — розряд 0")
			ess = 0
		}
		return ess, pvLimit
	}
	// More discharge by Δ exports Δ more.
	export := -*t.GridPowerKw + (ess - *t.ESSPowerKw)
	if export <= capKw {
		return ess, pvLimit
	}
	excess := export - capKw
	if ess > 0 {
		cut := math.Min(ess, excess)
		ess -= cut
		excess -= cut
		*clamps = append(*clamps, fmt.Sprintf("стеля експорту %.0f кВт — розряд зменшено", capKw))
	}
	if excess > 0 && pvKnown && t.PVPowerKw != nil {
		limit := math.Max(0, *t.PVPowerKw-excess)
		if limit < pvLimit {
			pvLimit = limit
			*clamps = append(*clamps, fmt.Sprintf("стеля експорту %.0f кВт — СЕС обмежено", capKw))
		}
	}
	return ess, pvLimit
}

// checkStops returns why writing must stop now, or "". grid adds the
// checks of the grid (cutover §9): its voltages, the PCC meter, import
// and export. With the generator the PCC meter drops out of the logger
// and the grid thresholds do not apply (offgrid §6 п. 3).
func (c *Controller) checkStops(in ControlInputs, grid bool) string {
	t, now := in.Tick, in.Now
	if words, ok := t.SLAlarmWords(); ok {
		for _, w := range words {
			if w != 0 {
				return "аварія SmartLogger [" + strings.Join(slAlarmHex(words), " ") + "]"
			}
		}
	}
	// Before the confirmed start a stopped PCS is the expected state.
	if c.essStarted && t.PCSShutdown != nil && *t.PCSShutdown {
		return "УЗЕ знову зупинилась після пуску (40540)"
	}
	// Cutover §12.7: after the start the PCS stays in PQ (44364 = 0).
	if c.essStarted && !pcsModePQ(t) {
		if c.pcsModeSince.IsZero() {
			c.pcsModeSince = now
		}
		if now.Sub(c.pcsModeSince) >= stopPCSModeAfter {
			return "режим PCS не PQ (" + pcsModeText(t) + ")"
		}
	} else {
		c.pcsModeSince = time.Time{}
	}
	if mode, bad := controlModeOff(t); bad {
		return "режим керування 40737 = " + mode + ", має бути 4"
	}
	if t.DataQuality == QualityStale || t.DataQuality == QualityFault {
		if c.pollLossSince.IsZero() {
			c.pollLossSince = now
		}
		if now.Sub(c.pollLossSince) >= stopPollLossAfter {
			return "втрата зв'язку з SmartLogger (" + t.DataQuality + ")"
		}
	} else {
		c.pollLossSince = time.Time{}
	}
	if low := lowVoltages(&t); grid && len(low) > 0 {
		if c.lowVoltSince.IsZero() {
			c.lowVoltSince = now
		}
		if now.Sub(c.lowVoltSince) >= stopVoltageAfter {
			return "низька напруга мережі: " + strings.Join(low, "; ")
		}
	} else {
		c.lowVoltSince = time.Time{}
	}
	// Without 40505 neither the import limit nor the export ceiling is
	// watched; without 40392, the battery's following.
	if !grid {
		c.pccLossSince, c.importSince, c.exportSince = time.Time{}, time.Time{}, time.Time{}
	} else if t.GridPowerKw == nil {
		c.importSince, c.exportSince = time.Time{}, time.Time{}
		if c.pccLossSince.IsZero() {
			c.pccLossSince = now
		}
		if now.Sub(c.pccLossSince) >= stopPollLossAfter {
			return "потужність PCC 40505: " + pccMissing(&t)
		}
	} else {
		c.pccLossSince = time.Time{}
	}
	if t.ESSPowerKw == nil {
		if c.essLossSince.IsZero() {
			c.essLossSince = now
		}
		if now.Sub(c.essLossSince) >= stopPollLossAfter {
			return "потужність УЗЕ 40392: немає даних"
		}
	} else {
		c.essLossSince = time.Time{}
	}
	if t.GridPowerKw != nil && grid {
		grid := *t.GridPowerKw
		if limit := in.ImportLimitKw; limit > 0 && grid >= importStopShare*limit {
			if c.importSince.IsZero() {
				c.importSince = now
			}
			if now.Sub(c.importSince) >= stopImportAfter {
				return fmt.Sprintf("імпорт %.0f кВт біля ліміту %.0f кВт", grid, limit)
			}
		} else {
			c.importSince = time.Time{}
		}
		if capKw := in.ExportCapKw; capKw > 0 && -grid > capKw {
			if c.exportSince.IsZero() {
				c.exportSince = now
			}
			if now.Sub(c.exportSince) >= stopExportAfter {
				return fmt.Sprintf("відпуск %.0f кВт понад стелю %.0f кВт довше хвилини", -grid, capKw)
			}
		} else {
			c.exportSince = time.Time{}
		}
	}
	if reason := c.factStop(in); reason != "" {
		return reason
	}
	return ""
}

// factStop compares the battery's actual power with the commands of the
// last factWindow (cutover §12.3 п. 6): zero within 2 kW, otherwise
// within 10 kW or 5 %, against the logger's available power when that
// is lower. For startGrace after the confirmed start zero is within
// startZeroTolKw; above the SOC window any command is within
// highSocTolKw. Out of tolerance for factStopAfter stops writing.
func (c *Controller) factStop(in ControlInputs) string {
	t, now := in.Tick, in.Now
	if t.ESSPowerKw == nil || len(c.history) == 0 {
		return ""
	}
	lo, hi := c.commandRange(now)
	lo, hi = availableRange(t, lo), availableRange(t, hi)
	tol := factZeroTolKw
	switch {
	case math.Max(math.Abs(lo), math.Abs(hi)) > factZeroTolKw:
		tol = math.Max(factMinTolKw, factTolShare*math.Max(math.Abs(lo), math.Abs(hi)))
	case c.essStarted && now.Sub(c.essStartedAt) < startGrace:
		tol = startZeroTolKw
	}
	if t.SocPercent != nil && *t.SocPercent > c.cfg.SocMaxPct && tol < highSocTolKw {
		tol = highSocTolKw
	}
	actual := *t.ESSPowerKw
	inside := actual >= lo-tol && actual <= hi+tol
	if inside {
		c.deviationSince = time.Time{}
		if !c.firstConfirmed && math.Max(math.Abs(lo), math.Abs(hi)) > firstCommandEpsKw {
			if c.followSince.IsZero() {
				c.followSince = now
			}
			if now.Sub(c.followSince) >= factConfirmAfter {
				c.firstConfirmed = true
			}
		}
		return ""
	}
	c.followSince = time.Time{}
	if c.deviationSince.IsZero() {
		c.deviationSince = now
	}
	if now.Sub(c.deviationSince) >= factStopAfter {
		return fmt.Sprintf("УЗЕ не виконує команду: факт %.1f кВт, команда %.1f…%.1f кВт", actual, lo, hi)
	}
	return ""
}

// availableRange caps an expected power by what the logger allows now.
func availableRange(t Tick, p float64) float64 {
	if p > 0 && t.ESSDischargeMaxKw != nil && p > *t.ESSDischargeMaxKw {
		return *t.ESSDischargeMaxKw
	}
	if p < 0 && t.ESSChargeMaxKw != nil && -p > *t.ESSChargeMaxKw {
		return -*t.ESSChargeMaxKw
	}
	return p
}

func (c *Controller) commandRange(now time.Time) (lo, hi float64) {
	first := true
	for _, s := range c.history {
		if now.Sub(s.at) > factWindow {
			continue
		}
		if first || s.kw < lo {
			lo = s.kw
		}
		if first || s.kw > hi {
			hi = s.kw
		}
		first = false
	}
	return lo, hi
}

func (c *Controller) track(in ControlInputs, kw float64) {
	c.history = append(c.history, cmdSample{at: in.Now, kw: kw})
	cut := 0
	for cut < len(c.history) && in.Now.Sub(c.history[cut].at) > factWindow {
		cut++
	}
	c.history = c.history[cut:]
}

// SafeCommand is what goes out while holding, stopped or stopping.
func (c *Controller) SafeCommand(now time.Time) WriteCommand {
	return WriteCommand{
		At: now, State: c.state, ESSKw: 0,
		PVLimitKw: round1(c.lastPVRatedKw), PVKnown: c.lastPVRatedKw > 0,
		Reason: c.reason,
	}
}

// controlModeOff reports a 40737 reading other than 4 (remote
// communication scheduling) on any logger.
func controlModeOff(t Tick) (string, bool) {
	for _, k := range []string{"active_power_control_mode", "pv_active_power_control_mode", "ess_active_power_control_mode"} {
		if v, ok := t.Values[k]; ok && v != 4 {
			return fmt.Sprintf("%g", v), true
		}
	}
	return "", false
}

var meterPhaseKeys = [3]string{"meter_phase_a_voltage_v", "meter_phase_b_voltage_v", "meter_phase_c_voltage_v"}

func lineVoltageKeys(prefix string) [3]string {
	return [3]string{prefix + "grid_line_voltage_ab_v", prefix + "grid_line_voltage_bc_v", prefix + "grid_line_voltage_ca_v"}
}

// pccMeterSilent: the loggers answer, but the PV logger has no reading
// of its PCC meter: no 40505, the meter's phases 0 V or no value. On ze
// 17.11.2026 13:48–14:25 the phases read 0 V, then no value, while the
// bus kept 222–226 V.
func pccMeterSilent(t *Tick) bool {
	if t.GridPowerKw != nil || (t.DataQuality != QualityOK && t.DataQuality != QualityEstimated) {
		return false
	}
	zero, missing := 0, 0
	for _, k := range meterPhaseKeys {
		switch v, ok := t.Values[k]; {
		case !ok:
			missing++
		case v == 0:
			zero++
		}
	}
	return zero == len(meterPhaseKeys) || missing == len(meterPhaseKeys)
}

// busLive: a logger's line voltages read the bus. In a blackout they
// read 0 too, and the meter's 0 V phases stay an undervoltage.
func busLive(t *Tick) bool {
	for _, prefix := range []string{"", "pv_", "ess_"} {
		for _, k := range lineVoltageKeys(prefix) {
			if t.Values[k] > 0 {
				return true
			}
		}
	}
	return false
}

func meterPhasesRead(t *Tick) bool {
	for _, k := range meterPhaseKeys {
		if _, ok := t.Values[k]; !ok {
			return false
		}
	}
	return true
}

// pccMissing says why 40505 has no value, for stops and consoles.
func pccMissing(t *Tick) string {
	if pccMeterSilent(t) {
		return "немає даних — лічильник PCC не відповідає"
	}
	return "немає даних"
}

// lowVoltages lists the readings below the stop thresholds (phase
// < 200 V, line < 350 V). Line voltages that all read 0 are an idle
// inverter group, not an undervoltage. The 0 V phases of a silent meter
// on a live bus are no reading either: the missing 40505 stops writing.
func lowVoltages(t *Tick) []string {
	type group struct {
		label string
		keys  [3]string
		min   float64
		idle0 bool
	}
	groups := []group{
		{"лінія", lineVoltageKeys(""), 350, true},
		{"лінія СЕС", lineVoltageKeys("pv_"), 350, true},
		{"лінія УЗЕ", lineVoltageKeys("ess_"), 350, true},
	}
	if !pccMeterSilent(t) || !busLive(t) {
		groups = append([]group{{"фаза", meterPhaseKeys, 200, false}}, groups...)
	}
	var low []string
	for _, g := range groups {
		var below []string
		zero, seen := true, false
		for _, k := range g.keys {
			v, ok := t.Values[k]
			if !ok {
				continue
			}
			seen = true
			if v != 0 {
				zero = false
			}
			if v < g.min {
				below = append(below, fmt.Sprintf("%s %.0f В", g.label, v))
			}
		}
		if !seen || (g.idle0 && zero) {
			continue
		}
		low = append(low, below...)
	}
	return low
}
