package edge

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// The island (ems_offgrid_island_generator.md §5): the battery forms
// the bus alone, in VSG. Entered and left only by the console's buttons
// with the PIN; the exit also runs on its own triggers (§5.3), which
// only ever stop. The order and pauses of §5.1 wait for the vendor, so
// they are settings; the writer refuses 44365 = 1 until the test order
// is approved, and the entry then fails at that step with its reason.

// Island phases.
const (
	islPVZero   = "pv_zero"   // 40378 = 0, the pause
	islESSStop  = "ess_stop"  // 40198 if the battery still runs
	islVSG      = "vsg"       // 44365 = 1, until 44364 = 1
	islStart    = "start"     // 40199, until 40540 = 0 and the bus is live
	islRun      = "run"       // the battery holds the bus
	islExitStop = "exit_stop" // 40198, until 40540 ≠ 0
	islExitPQ   = "exit_pq"   // 44365 = 0, until 44364 = 0
)

// Island events.
const (
	EvIslandStart   = "ISLAND_START"
	EvIslandStep    = "ISLAND_STEP"
	EvIslandFailed  = "ISLAND_START_FAILED"
	EvIslandUp      = "ISLAND_UP"
	EvIslandSocWarn = "ISLAND_SOC_WARN"
	EvIslandSocLow  = "ISLAND_SOC_LOW"
	EvIslandAlarm   = "ISLAND_ALARM"
	EvIslandExit    = "ISLAND_EXIT"
	EvIslandDone    = "ISLAND_DONE"
)

var islandPhaseLabels = map[string]string{
	islPVZero:   "стеля СЕС 0, пауза",
	islESSStop:  "зупинка УЗЕ",
	islVSG:      "режим PCS VSG (44365 = 1)",
	islStart:    "пуск УЗЕ (40199), чекаємо напругу шини",
	islRun:      "острів",
	islExitStop: "зупинка УЗЕ (40198)",
	islExitPQ:   "режим PCS PQ (44365 = 0)",
}

// islandSeq is the island's sequence. Core loop only.
type islandSeq struct {
	phase    string
	since    time.Time
	by       string
	exitWhy  string
	warned   bool
	failedAt time.Time
	failWhy  string
}

// islandStartGate is «Пуск острова» now (§4.4).
func (s *Service) islandStartGate(now time.Time, t Tick) Avail {
	og, sup := s.cfg.Offgrid, s.supplyNow
	switch {
	case !og.IslandActionsEnabled || s.supply == nil:
		return Avail{Reason: "дії острова вимкнені до відповіді вендора (offgrid.island_actions_enabled)"}
	case islandState(s.ctl.State()):
		return Avail{Reason: "острів уже запущено"}
	case !sup.Fresh:
		return Avail{Reason: "немає свіжих даних Encombi"}
	case sup.Mode != SupplyBlackout:
		return Avail{Reason: "лише на знеструмленій шині (живлення «Знеструмлено»)"}
	case !sup.SwitchHeld(now, SwitchVSG, og.IslandSwitchHold):
		return Avail{Reason: fmt.Sprintf("перемикач у VSG щонайменше %.0f с", og.IslandSwitchHold.Seconds())}
	case sup.Breaker(diT1) || sup.Breaker(diT2):
		return Avail{Reason: "вимикачі вводів 1QF і 2QF мають бути розімкнені"}
	case sup.Breaker(diGen):
		return Avail{Reason: "вимикач генератора має бути розімкнений"}
	case !sup.Breaker(diESS):
		return Avail{Reason: "вимикач УЗЕ має бути замкнений"}
	case t.SocPercent == nil || *t.SocPercent <= og.IslandStartMinSocPct:
		return Avail{Reason: fmt.Sprintf("заряд має бути вище %.0f %%", og.IslandStartMinSocPct)}
	case !essStoppedOn(t):
		return Avail{Reason: "УЗЕ має бути зупинена (40540)"}
	case s.ctl.State() != WriteModeStop:
		return Avail{Reason: "спершу запис і зупинка УЗЕ після зникнення мережі"}
	case !s.isl.failedAt.IsZero() && now.Sub(s.isl.failedAt) < og.IslandRetryAfter:
		at := s.isl.failedAt.Add(og.IslandRetryAfter)
		if s.tz != nil {
			at = at.In(s.tz)
		}
		return Avail{Reason: "після невдалого пуску — не раніше " + at.Format("15:04")}
	}
	return open
}

// islandStopGate is «Зупинка острова» now: the island started by its
// button, in or on its way.
func (s *Service) islandStopGate() Avail {
	if st := s.ctl.State(); st == WriteIslandStart || st == WriteIsland {
		return open
	}
	return Avail{Reason: "острів не запущено цією кнопкою"}
}

// islandAction serves the two island buttons.
func (s *Service) islandAction(ctx context.Context, now time.Time, id, by string) cmdReply {
	t := s.lastTick.Load()
	if t == nil {
		return cmdReply{Error: "немає телеметрії"}
	}
	switch id {
	case ActIslandStart:
		if g := s.islandStartGate(now, *t); !g.OK {
			return cmdReply{Error: g.Reason}
		}
		if !s.ctl.IslandBegin(now, by) {
			return cmdReply{Error: "стан запису не дозволяє пуск острова"}
		}
		s.isl = islandSeq{phase: islPVZero, since: now, by: by}
		s.writeEventDeduped(ctx, Event{TS: now, Severity: SevWarning, Code: EvIslandStart,
			Message: "«Пуск острова» (" + by + "): стеля СЕС 0, далі кроки §5.1"})
	case ActIslandStop:
		if g := s.islandStopGate(); !g.OK {
			return cmdReply{Error: g.Reason}
		}
		s.islandExit(ctx, now, *t, "«Зупинка острова» ("+by+")")
	}
	return cmdReply{OK: true}
}

func (s *Service) islandPhase(ctx context.Context, now time.Time, phase string) {
	s.isl.phase, s.isl.since = phase, now
	s.writeEventDeduped(ctx, Event{TS: now, Severity: SevInfo, Code: EvIslandStep,
		Message: "острів: " + islandPhaseLabels[phase]})
}

// islandSend hands one of the sequence's writes to the logger; a write
// not sent fails the step.
func (s *Service) islandSend(ctx context.Context, now time.Time, id string) bool {
	r := s.sendOneShot(ctx, now, oneShots[id], "edge: острів ("+s.isl.by+")", true)
	return r.OK
}

// islandFailed: the step's command came back refused or unanswered.
func (s *Service) islandFailed(id string) (string, bool) {
	st, ok := s.commandStatus(id)
	if !ok || st.State != CmdFailed {
		return "", false
	}
	return st.Error, true
}

// islandFail ends an entry that did not come up (§5.1 п. 6): the battery
// stopped, back in PQ, the reason on the console; a new press only after
// the device off time.
func (s *Service) islandFail(ctx context.Context, now time.Time, t Tick, why string) {
	s.isl.failedAt, s.isl.failWhy = now, why
	s.writeEventDeduped(ctx, Event{TS: now, Severity: SevAlarm, Code: EvIslandFailed,
		Message: "острів не зібрано: " + why})
	s.islandExit(ctx, now, t, "невдалий пуск: "+why)
}

// islandExit runs §5.3: 40198, then 44365 = 0, then the power mode.
func (s *Service) islandExit(ctx context.Context, now time.Time, t Tick, why string) {
	s.ctl.IslandExit(now, why)
	s.isl.exitWhy = why
	s.writeEventDeduped(ctx, Event{TS: now, Severity: SevWarning, Code: EvIslandExit, Message: "вихід з острова: " + why})
	if !essStoppedOn(t) {
		s.islandSend(ctx, now, CmdIslandESSStop)
		s.islandPhase(ctx, now, islExitStop)
		return
	}
	s.islandToPQ(ctx, now, t)
}

func (s *Service) islandToPQ(ctx context.Context, now time.Time, t Tick) {
	if !pcsModePQ(t) {
		s.islandSend(ctx, now, CmdPCSPQ)
	}
	s.islandPhase(ctx, now, islExitPQ)
}

func (s *Service) islandDone(ctx context.Context, now time.Time, note string) {
	reason := "острів завершено: " + s.isl.exitWhy
	if note != "" {
		reason += " (" + note + ")"
	}
	s.ctl.IslandDone(now, reason)
	s.isl = islandSeq{failedAt: s.isl.failedAt, failWhy: s.isl.failWhy}
	s.writeEventDeduped(ctx, Event{TS: now, Severity: SevInfo, Code: EvIslandDone, Message: reason})
}

// islandExitWhy is a §5.3 trigger while the island runs, or "".
func (s *Service) islandExitWhy(now time.Time, t Tick) string {
	og, sup := s.cfg.Offgrid, s.supplyNow
	switch {
	case sup.Fresh && sup.Switch != SwitchVSG && sup.Switch != SwitchUnknown && now.Sub(sup.SwitchSince) >= og.IslandSwitchHold:
		return "перемикач на шафі не у VSG"
	case sup.Breaker(diT1) || sup.Breaker(diT2):
		return "замкнувся вимикач вводу"
	case sup.Breaker(diGen):
		return "замкнувся вимикач генератора"
	case t.SocPercent != nil && *t.SocPercent <= og.IslandSocFloorPct:
		return fmt.Sprintf("заряд %.1f %% дійшов до %.0f %%", *t.SocPercent, og.IslandSocFloorPct)
	case essStoppedOn(t):
		return "УЗЕ зупинилась (40540)"
	}
	return ""
}

// islandStep advances the sequence once a second, before the controller.
func (s *Service) islandStep(ctx context.Context, now time.Time, t Tick) {
	if !islandState(s.ctl.State()) {
		return
	}
	og, sup := s.cfg.Offgrid, s.supplyNow
	elapsed := now.Sub(s.isl.since)
	busLive := sup.Fresh && sup.BusVoltageV != nil && *sup.BusVoltageV >= s.cfg.Encombi.BusLiveMinV
	switch s.isl.phase {
	case islPVZero, islESSStop, islVSG, islStart:
		// A breaker closed onto the bus or the switch taken away while
		// coming up: stop at once.
		if why := s.islandExitWhy(now, t); why != "" && !(why == "УЗЕ зупинилась (40540)") {
			s.islandFail(ctx, now, t, why)
			return
		}
	}
	switch s.isl.phase {
	case islPVZero:
		if elapsed < og.IslandStepPause {
			return
		}
		if !essStoppedOn(t) {
			if s.islandSend(ctx, now, CmdIslandESSStop) {
				s.islandPhase(ctx, now, islESSStop)
			} else {
				s.islandFail(ctx, now, t, "зупинку УЗЕ не надіслано")
			}
			return
		}
		s.islandToVSG(ctx, now, t)
	case islESSStop:
		switch {
		case essStoppedOn(t):
			s.islandToVSG(ctx, now, t)
		case elapsed > og.IslandStopTimeout:
			s.islandFail(ctx, now, t, "УЗЕ не зупинилась")
		}
	case islVSG:
		if err, failed := s.islandFailed(CmdPCSVSG); failed {
			why := "44365 = 1 не записано: " + err
			if err == errNotWritable.Error() {
				why = "запис 44365 = 1 (VSG) заборонений до погодження порядку тесту"
			}
			s.islandFail(ctx, now, t, why)
			return
		}
		switch {
		case pcsModeVSG(t) && elapsed >= og.IslandStepPause:
			if s.islandSend(ctx, now, CmdIslandESSStart) {
				s.islandPhase(ctx, now, islStart)
			} else {
				s.islandFail(ctx, now, t, "пуск УЗЕ не надіслано")
			}
		case !pcsModeVSG(t) && elapsed > og.IslandVSGTimeout:
			s.islandFail(ctx, now, t, fmt.Sprintf("44364 не став 1 за %.0f с (%s)", og.IslandVSGTimeout.Seconds(), pcsModeText(t)))
		}
	case islStart:
		if err, failed := s.islandFailed(CmdIslandESSStart); failed {
			s.islandFail(ctx, now, t, "пуск УЗЕ: "+err)
			return
		}
		switch {
		case !essStoppedOn(t) && busLive:
			if !pvRunningOn(t) {
				s.islandSend(ctx, now, CmdIslandPVStart)
			}
			s.ctl.IslandPVOn()
			s.ctl.IslandUp(now)
			s.isl.since = now
			s.isl.phase = islRun
			s.writeEventDeduped(ctx, Event{TS: now, Severity: SevInfo, Code: EvIslandUp,
				Message: fmt.Sprintf("острів зібрано: шина %.0f В, навантаження вмикати частинами, разом не більше %.0f кВт",
					*sup.BusVoltageV, og.IslandPowerMaxKw)})
		case elapsed > og.IslandStartTimeout:
			s.islandFail(ctx, now, t, fmt.Sprintf("за %.0f хв немає 40540 = 0 і напруги шини", og.IslandStartTimeout.Minutes()))
		}
	case islRun:
		s.islandRun(ctx, now, t)
	case islExitStop:
		switch {
		case essStoppedOn(t):
			s.islandToPQ(ctx, now, t)
		case elapsed > og.IslandStopTimeout:
			s.writeEventDeduped(ctx, Event{TS: now, Severity: SevAlarm, Code: EvIslandAlarm,
				Message: "вихід з острова: УЗЕ не зупинилась за " + og.IslandStopTimeout.String() + " — потрібна людина"})
		}
	case islExitPQ:
		switch {
		case pcsModePQ(t):
			s.islandDone(ctx, now, "")
		case elapsed > og.IslandVSGTimeout:
			s.islandDone(ctx, now, "44364 не став 0: "+pcsModeText(t))
		}
	}
}

// islandRun is §5.2: watch, warn, and leave only on a §5.3 trigger.
func (s *Service) islandRun(ctx context.Context, now time.Time, t Tick) {
	og, sup := s.cfg.Offgrid, s.supplyNow
	if why := s.islandExitWhy(now, t); why != "" {
		if t.SocPercent != nil && *t.SocPercent <= og.IslandSocFloorPct {
			s.writeEventDeduped(ctx, Event{TS: now, Severity: SevAlarm, Code: EvIslandSocLow,
				Message: "острів: " + why + " — зупинка УЗЕ"})
		}
		s.islandExit(ctx, now, t, why)
		return
	}
	if t.SocPercent != nil && *t.SocPercent <= og.IslandSocWarnPct && !s.isl.warned {
		s.isl.warned = true
		s.writeEventDeduped(ctx, Event{TS: now, Severity: SevWarning, Code: EvIslandSocWarn,
			Message: fmt.Sprintf("острів: заряд %.1f %% — запускайте генератор", *t.SocPercent)})
	}
	// Alarms, not stops: stopping the island would cut the elevator off.
	var alarms []string
	if !sup.Fresh {
		alarms = append(alarms, "немає свіжих даних Encombi")
	}
	if t.SLAlarmActive() {
		words, _ := t.SLAlarmWords()
		alarms = append(alarms, "аварія SmartLogger ["+strings.Join(slAlarmHex(words), " ")+"]")
	}
	if !pcsModeVSG(t) {
		alarms = append(alarms, "режим PCS не VSG ("+pcsModeText(t)+")")
	}
	if len(alarms) > 0 {
		s.writeEventDeduped(ctx, Event{TS: now, Severity: SevAlarm, Code: EvIslandAlarm,
			Message: "острів: " + strings.Join(alarms, "; ") + " — рішення за людиною"})
	}
}

func (s *Service) islandToVSG(ctx context.Context, now time.Time, t Tick) {
	if pcsModeVSG(t) {
		s.islandPhase(ctx, now, islVSG)
		return
	}
	if !s.islandSend(ctx, now, CmdPCSVSG) {
		s.islandFail(ctx, now, t, "44365 = 1 не надіслано")
		return
	}
	s.islandPhase(ctx, now, islVSG)
}
