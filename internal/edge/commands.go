package edge

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/nesh/sestelemetry/internal/modbusclient"
)

// One-shot start/stop commands (ems_sl_write_cutover.md, approved
// 04.11.2026, §3, §4, §12.1): one button, one FC06 write of 0, once,
// never from the manual mode or the automaton. The registers are
// write-only, so the feedback is the logger's answer, then the
// equipment's state.
const (
	CmdPVStart       = "pv_start"
	CmdPVStop        = "pv_stop"
	CmdESSStart      = "ess_start"
	CmdESSStop       = "ess_stop"
	CmdArrayStartPV  = "array_start_pv"
	CmdArrayStopPV   = "array_stop_pv"
	CmdArrayStartESS = "array_start_ess"
	CmdArrayStopESS  = "array_stop_ess"
	// CmdPCSPQ sets the battery PCS to PQ (44365 = 0) before its start;
	// the edge sends it itself (cutover §12.7).
	CmdPCSPQ = "pcs_pq"
)

const (
	commandConfirmWindow = 3 * time.Minute
	// 44364 must read PQ this soon after 44365 = 0, or no start goes out.
	pcsModeConfirmWindow = 30 * time.Second
	// A writer that has not answered by then (stuck dialing) counts as
	// no answer, so nothing waits on it forever.
	commandAnswerTimeout = 15 * time.Second
	// An ESS array stop is judged this long after the logger took it:
	// it also tells whether the sun stayed up (cutover §3).
	arrayStopObserveAfter = 30 * time.Second
	pvRunningKw           = 2.0 // 40388 above this: PV in operation (cutover §12.1)
	pvStoppedKw           = 0.5 // 40388 at 0
)

// Stage 1, the logger's answer to the write.
const (
	AnswerPending    = "pending"
	AnswerAccepted   = "accepted"
	AnswerRefused    = "refused"
	AnswerNoResponse = "no_response"
	AnswerNotSent    = "not_sent"
)

// Stage 2, the equipment's state.
const (
	CmdWaiting      = "waiting"
	CmdConfirmed    = "confirmed"
	CmdNotConfirmed = "not_confirmed"
	CmdFailed       = "failed"
)

const (
	EvCmdSent          = "SL_COMMAND_SENT"
	EvCmdRefused       = "SL_COMMAND_REFUSED"
	EvCmdNoResponse    = "SL_COMMAND_NO_RESPONSE"
	EvCmdConfirmed     = "SL_COMMAND_CONFIRMED"
	EvCmdNotConfirmed  = "SL_COMMAND_NOT_CONFIRMED"
	EvArrayStopPVCheck = "ESS_ARRAY_STOP_PV_CHECK"
	EvPVWriteRefused   = "PV_WRITE_REFUSED"
	EvPVWriteAccepted  = "PV_WRITE_ACCEPTED"
	EvESSWriteRefused  = "ESS_WRITE_REFUSED"
	EvESSWriteAccepted = "ESS_WRITE_ACCEPTED"
	EvPVNotRunning     = "PV_NOT_RUNNING"
)

type oneShot struct {
	ID       string
	Addr     uint16
	Role     DeviceRole
	Label    string
	Opposite string
	Start    bool
	Target   string // "pv" | "ess": the equipment its confirmation watches
}

var oneShots = map[string]oneShot{
	CmdPVStart:       {CmdPVStart, 40197, RolePV, "Пуск СЕС", CmdPVStop, true, "pv"},
	CmdPVStop:        {CmdPVStop, 40196, RolePV, "Зупинка СЕС", CmdPVStart, false, "pv"},
	CmdESSStart:      {CmdESSStart, 40199, RoleESS, "Пуск УЗЕ", CmdESSStop, true, "ess"},
	CmdESSStop:       {CmdESSStop, 40198, RoleESS, "Зупинка УЗЕ", CmdESSStart, false, "ess"},
	CmdArrayStartPV:  {CmdArrayStartPV, 40200, RolePV, "Пуск масиву (логер СЕС)", CmdArrayStopPV, true, "pv"},
	CmdArrayStopPV:   {CmdArrayStopPV, 40201, RolePV, "Зупинка масиву (логер СЕС)", CmdArrayStartPV, false, "pv"},
	CmdArrayStartESS: {CmdArrayStartESS, 40200, RoleESS, "Пуск масиву (логер УЗЕ)", CmdArrayStopESS, true, "ess"},
	CmdArrayStopESS:  {CmdArrayStopESS, 40201, RoleESS, "Зупинка масиву (логер УЗЕ)", CmdArrayStartESS, false, "ess"},
	CmdPCSPQ:         {CmdPCSPQ, regPCSMode, RoleESS, "Режим PCS: PQ", "", false, "pcs"},
}

// oneShotAddr: the start/stop registers an FC06 may go to. 40202/40203
// (the same start/stop pair with a confusing 0/1) never.
func oneShotAddr(addr uint16) bool { return addr >= 40196 && addr <= 40201 }

// window is how long a command's confirmation is waited for.
func (c oneShot) window() time.Duration {
	if c.Target == "pcs" {
		return pcsModeConfirmWindow
	}
	return commandConfirmWindow
}

// pcsModePQ: 44364 reads 0, the PCS works with the grid (cutover §12.7).
func pcsModePQ(t Tick) bool {
	v, ok := t.Values["pcs_working_mode"]
	return ok && v == 0
}

// pcsModeName names 44364 for the consoles: 0 PQ, 1 VSG, 2 and 3 a part
// of the PCS in VSG, 65535 or no reading unknown.
func pcsModeName(t Tick) string {
	v, ok := t.Values["pcs_working_mode"]
	switch {
	case !ok:
		return "unknown"
	case v == 0:
		return "pq"
	case v == 1:
		return "vsg"
	case v == 2 || v == 3:
		return "mixed"
	}
	return "unknown"
}

func pcsModeText(t Tick) string {
	if v, ok := t.Values["pcs_working_mode"]; ok {
		return fmt.Sprintf("44364 = %g", v)
	}
	return "44364 не прочитано"
}

// CommandStatus is one button's last command, for the consoles.
type CommandStatus struct {
	ID         string         `json:"id"`
	Label      string         `json:"label"`
	Register   uint16         `json:"register"`
	Host       string         `json:"host"`
	By         string         `json:"by"`
	SentAt     time.Time      `json:"sent_at"`
	Answer     string         `json:"answer"`
	Exception  int            `json:"exception,omitempty"`
	Error      string         `json:"error,omitempty"`
	AcceptedAt *time.Time     `json:"accepted_at,omitempty"`
	State      string         `json:"state"`
	Deadline   *time.Time     `json:"deadline,omitempty"`
	DoneAt     *time.Time     `json:"done_at,omitempty"`
	Live       map[string]any `json:"live,omitempty"`
	Note       string         `json:"note,omitempty"`
}

func (c *CommandStatus) open() bool { return c != nil && c.State == CmdWaiting }

func essRunningOn(t Tick) bool {
	if t.PCSShutdown == nil || *t.PCSShutdown {
		return false
	}
	v, ok := t.Values["pcs_in_operation"]
	return ok && v > 0
}

func essStoppedOn(t Tick) bool { return t.PCSShutdown != nil && *t.PCSShutdown }

func pvRunningOn(t Tick) bool { return t.PVPowerKw != nil && *t.PVPowerKw > pvRunningKw }

func pvStoppedOn(t Tick, onGrid int, fleetKnown bool) bool {
	if t.PVPowerKw == nil || *t.PVPowerKw > pvStoppedKw {
		return false
	}
	return !fleetKnown || onGrid == 0
}

func liveValues(target string, t Tick, onGrid int, fleetKnown bool) map[string]any {
	out := map[string]any{}
	if target == "pcs" {
		if v, ok := t.Values["pcs_working_mode"]; ok {
			out["44364"] = v
		}
		return out
	}
	if target == "ess" {
		if t.PCSShutdown != nil {
			out["40540"] = map[bool]int{true: 1, false: 0}[*t.PCSShutdown]
		}
		if v, ok := t.Values["pcs_in_operation"]; ok {
			out["40539"] = v
		}
		if t.ESSChargeMaxKw != nil {
			out["40490"] = round1(*t.ESSChargeMaxKw)
		}
		if t.ESSDischargeMaxKw != nil {
			out["40492"] = round1(*t.ESSDischargeMaxKw)
		}
		return out
	}
	if t.PVPowerKw != nil {
		out["40388"] = round1(*t.PVPowerKw)
	}
	if fleetKnown {
		out["inverters_on_grid"] = onGrid
	}
	return out
}

// confirmedOn reports that the equipment reached the command's state.
func (c oneShot) confirmedOn(t Tick, now, acceptedAt time.Time, onGrid int, fleetKnown bool) bool {
	switch {
	case c.Target == "pcs":
		return pcsModePQ(t)
	case c.Target == "ess" && c.Start:
		return essRunningOn(t)
	case c.Target == "ess":
		return essStoppedOn(t) && (c.ID != CmdArrayStopESS || now.Sub(acceptedAt) >= arrayStopObserveAfter)
	case c.Start:
		return pvRunningOn(t)
	default:
		return pvStoppedOn(t, onGrid, fleetKnown)
	}
}

// --- core loop ---------------------------------------------------------

type cmdRequest struct {
	id    string
	by    string
	reply chan cmdReply
}

type cmdReply struct {
	OK     bool           `json:"ok"`
	Error  string         `json:"error,omitempty"`
	Status *CommandStatus `json:"status,omitempty"`
}

type oneShotJob struct {
	id   string
	addr uint16
}

type cmdResult struct {
	id  string
	at  time.Time
	err error
}

// writeSession is what one write session knows about the start/stop
// commands. Core loop only.
type writeSession struct {
	pvRunningAtEnable bool
	pvStartedByTest   bool // PV was not running at the hand-over and the logger took a start from the test
	essStopConfirmed  bool // a battery stop was confirmed after the last start the logger took
	autoStopSent      bool
	disableByOperator bool
	stopPhase         string // turning off: zero | ess_stop | pv_stop
	essStopAwaited    bool   // turning off waits for a battery stop
	pvStopAwaited     bool   // turning off waits for a PV stop
	pqSent            bool   // 44365 = 0 went out in this session
	stopNotes         []string
	pvCheckDay        string
}

// essStartedByTest reports that the test started the battery: the
// logger took a start in this session, confirmed or not yet.
func (s *Service) essStartedByTest() bool {
	return s.essStartSent.Load() || s.ctl.ESSStartedInSession()
}

// WriteEquipment is the equipment side of the write block.
type WriteEquipment struct {
	ESS             string `json:"ess"` // running | stopped | unknown
	PV              string `json:"pv"`  // running | not_running | unknown
	ESSStarted      bool   `json:"ess_started"`
	PVStartedByTest bool   `json:"pv_started_by_test"`
	PVRefusing      bool   `json:"pv_refusing"`
	// PCSMode is 44364 by name: pq | vsg | mixed | unknown (§12.7).
	PCSMode    string   `json:"pcs_mode"`
	PCSModeRaw *float64 `json:"pcs_mode_raw,omitempty"`
}

func (s *Service) deviceFor(role DeviceRole) (Device, bool) {
	for _, d := range s.cfg.SmartLogger.Devices {
		if d.Role == role || d.Role == RoleAll {
			return d, true
		}
	}
	return Device{}, false
}

// commandStatuses is a sorted copy for the consoles.
func (s *Service) commandStatuses() []CommandStatus {
	s.cmdMu.Lock()
	defer s.cmdMu.Unlock()
	out := make([]CommandStatus, 0, len(s.cmds))
	for _, c := range s.cmds {
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SentAt.Before(out[j].SentAt) })
	return out
}

func (s *Service) commandStatus(id string) (CommandStatus, bool) {
	s.cmdMu.Lock()
	defer s.cmdMu.Unlock()
	c, ok := s.cmds[id]
	if !ok {
		return CommandStatus{}, false
	}
	return *c, true
}

// onCommandRequest serves a console button in the core loop.
func (s *Service) onCommandRequest(ctx context.Context, now time.Time, req cmdRequest) cmdReply {
	cmd, ok := oneShots[req.id]
	if !ok {
		return cmdReply{Error: "невідома команда"}
	}
	if !s.gate.Enabled() {
		return cmdReply{Error: "запис не ввімкнено: команди пуску й зупинки йдуть лише з увімкненим записом"}
	}
	if a := s.avail.Button(req.id); !a.OK {
		return cmdReply{Error: a.Reason}
	}
	if state := s.ctl.State(); cmd.Start && (state == WriteStopped || state == WriteStopping) {
		return cmdReply{Error: "після зупинки повторний пуск не надсилається — вимкніть запис і почніть заново"}
	}
	// Cutover §12.7: the battery starts only in PQ, and PQ is set only
	// on the stopped battery.
	t := s.lastTick.Load()
	if cmd.Target == "ess" && cmd.Start && (t == nil || !pcsModePQ(*t)) {
		return cmdReply{Error: s.pcsStartRefusal(t)}
	}
	if cmd.ID == CmdPCSPQ && (t == nil || !essStoppedOn(*t)) {
		return cmdReply{Error: "режим PCS задається лише на зупиненій УЗЕ (40540 ≠ 0)"}
	}
	return s.sendOneShot(ctx, now, cmd, req.by, false)
}

// pcsStartRefusal says why «Пуск УЗЕ» waits for PQ.
func (s *Service) pcsStartRefusal(t *Tick) string {
	mode := "44364 не прочитано"
	if t != nil {
		mode = pcsModeText(*t)
	}
	if st, ok := s.commandStatus(CmdPCSPQ); ok {
		switch {
		case st.open() || st.Answer == AnswerPending:
			return "режим PCS ще переходить у PQ (" + mode + ") — пуск після підтвердження"
		case st.State == CmdNotConfirmed || st.State == CmdFailed:
			return "режим PCS не став PQ після 44365 = 0 (" + mode + ") — пуск не надсилається; вимкніть запис і почніть заново"
		}
	}
	return "режим PCS не PQ (" + mode + ") — пуск не надсилається"
}

// sendPQ writes 44365 = 0 once a session: after writing is turned on,
// on the stopped battery, before «Пуск УЗЕ» (cutover §12.7).
func (s *Service) sendPQ(ctx context.Context, now time.Time, t Tick) {
	if s.sess.pqSent || s.ctl.State() != WriteAwaitStart || !essStoppedOn(t) || s.essStartedByTest() {
		return
	}
	for _, id := range []string{CmdESSStart, CmdArrayStartESS} {
		if _, pressed := s.commandStatus(id); pressed {
			return
		}
	}
	s.sess.pqSent = true
	s.sendOneShot(ctx, now, oneShots[CmdPCSPQ], "edge: увімкнення запису", false)
}

// sendOneShot hands one command to its logger's writer. A pending
// command blocks itself and its opposite; nothing is ever retried.
// force is the edge's own stop (a latched stop, turning writing off):
// it goes out over a start that still waits for its confirmation, and
// is not doubled when the same stop is already on its way.
func (s *Service) sendOneShot(ctx context.Context, now time.Time, cmd oneShot, by string, force bool) cmdReply {
	dev, ok := s.deviceFor(cmd.Role)
	if !ok {
		return cmdReply{Error: "немає логера для «" + cmd.Label + "»"}
	}
	s.cmdMu.Lock()
	if c := s.cmds[cmd.ID]; c.open() {
		snap := *c
		s.cmdMu.Unlock()
		if force {
			return cmdReply{OK: true, Status: &snap}
		}
		return cmdReply{Error: "«" + snap.Label + "» ще чекає підтвердження"}
	}
	if c := s.cmds[cmd.Opposite]; c.open() {
		if !force {
			s.cmdMu.Unlock()
			return cmdReply{Error: "«" + c.Label + "» ще чекає підтвердження"}
		}
		at := now
		c.State, c.DoneAt, c.Note = CmdNotConfirmed, &at, "перервано зупинкою"
	}
	st := &CommandStatus{
		ID: cmd.ID, Label: cmd.Label, Register: cmd.Addr, Host: dev.Host, By: by,
		SentAt: now, Answer: AnswerPending, State: CmdWaiting,
	}
	s.cmds[cmd.ID] = st
	s.cmdMu.Unlock()

	select {
	case s.cmdJobs[dev.Role] <- oneShotJob{id: cmd.ID, addr: cmd.Addr}:
	default:
		s.onCommandResult(ctx, cmdResult{id: cmd.ID, at: now, err: errors.New("writer зайнятий")})
		snap, _ := s.commandStatus(cmd.ID)
		return cmdReply{Error: "логер зайнятий, команду не надіслано", Status: &snap}
	}
	s.writeEventDeduped(ctx, Event{
		TS: now, Severity: SevWarning, Code: EvCmdSent,
		Message: fmt.Sprintf("«%s»: %d = 0 на %s (%s)", cmd.Label, cmd.Addr, dev.Host, by),
		Context: map[string]any{"command": cmd.ID, "register": cmd.Addr, "host": dev.Host, "by": by},
	})
	// The operator stopping a running battery ends the day's commands
	// as an operator stop: nothing else is stopped for it.
	if !cmd.Start && cmd.Target == "ess" && s.ctl.ESSStartedInSession() {
		if state := s.ctl.State(); state == WriteHold || state == WriteRun {
			if ev, ok := s.ctl.Stop(now, "«"+cmd.Label+"» ("+by+")", false); ok {
				s.writeEventDeduped(ctx, ev)
			}
		}
	}
	snap, _ := s.commandStatus(cmd.ID)
	return cmdReply{OK: true, Status: &snap}
}

// onCommandResult records the logger's answer to a command.
func (s *Service) onCommandResult(ctx context.Context, res cmdResult) {
	s.cmdMu.Lock()
	st := s.cmds[res.id]
	if st == nil || st.Answer != AnswerPending {
		s.cmdMu.Unlock()
		return
	}
	at := res.at
	switch {
	case res.err == nil:
		dl := at.Add(oneShots[res.id].window())
		st.Answer, st.AcceptedAt, st.Deadline = AnswerAccepted, &at, &dl
	case errors.Is(res.err, errWriteOff):
		st.Answer, st.State, st.Error, st.DoneAt = AnswerNotSent, CmdFailed, res.err.Error(), &at
	default:
		if code, ok := modbusclient.ExceptionCode(res.err); ok {
			st.Answer, st.Exception = AnswerRefused, int(code)
		} else {
			st.Answer = AnswerNoResponse
		}
		st.State, st.Error, st.DoneAt = CmdFailed, res.err.Error(), &at
	}
	snap := *st
	s.cmdMu.Unlock()
	// A start the logger took counts as started by the test from here
	// on, before its confirmation: a stop must follow it either way.
	if cmd := oneShots[snap.ID]; snap.Answer == AnswerAccepted && cmd.Start {
		if cmd.Target == "ess" {
			s.essStartSent.Store(true)
			s.sess.essStopConfirmed = false
		} else if !s.sess.pvRunningAtEnable {
			s.sess.pvStartedByTest = true
		}
	}
	switch snap.Answer {
	case AnswerRefused:
		s.writeEventDeduped(ctx, Event{TS: at, Severity: SevAlarm, Code: EvCmdRefused,
			Message: fmt.Sprintf("«%s»: логер відмовив, exception %d", snap.Label, snap.Exception),
			Context: map[string]any{"command": snap.ID, "register": snap.Register, "host": snap.Host, "exception": snap.Exception}})
	case AnswerNoResponse, AnswerNotSent:
		s.writeEventDeduped(ctx, Event{TS: at, Severity: SevAlarm, Code: EvCmdNoResponse,
			Message: fmt.Sprintf("«%s»: немає відповіді логера — %s", snap.Label, snap.Error),
			Context: map[string]any{"command": snap.ID, "register": snap.Register, "host": snap.Host}})
	}
}

// updateCommands checks the accepted commands against the equipment's
// state: confirmed, or not confirmed after the window.
func (s *Service) updateCommands(ctx context.Context, now time.Time, t Tick) {
	var stuck []string
	s.cmdMu.Lock()
	for id, st := range s.cmds {
		if st.Answer == AnswerPending && now.Sub(st.SentAt) > commandAnswerTimeout {
			stuck = append(stuck, id)
		}
	}
	s.cmdMu.Unlock()
	for _, id := range stuck {
		s.onCommandResult(ctx, cmdResult{id: id, at: now, err: errors.New("логер не відповів за 15 с")})
	}
	onGrid, fleetKnown := s.invertersOnGrid(now)
	type done struct {
		cmd       oneShot
		snap      CommandStatus
		confirmed bool
	}
	var finished []done
	s.cmdMu.Lock()
	for id, st := range s.cmds {
		if st.Answer != AnswerAccepted || st.State != CmdWaiting {
			continue
		}
		cmd := oneShots[id]
		st.Live = liveValues(cmd.Target, t, onGrid, fleetKnown)
		switch {
		case cmd.confirmedOn(t, now, *st.AcceptedAt, onGrid, fleetKnown):
			at := now
			st.State, st.DoneAt = CmdConfirmed, &at
			finished = append(finished, done{cmd, *st, true})
		case now.After(*st.Deadline):
			at := now
			st.State, st.DoneAt = CmdNotConfirmed, &at
			finished = append(finished, done{cmd, *st, false})
		}
	}
	s.cmdMu.Unlock()
	for _, d := range finished {
		if !d.confirmed {
			within := "3 хв"
			if d.cmd.window() < time.Minute {
				within = fmt.Sprintf("%.0f с", d.cmd.window().Seconds())
			}
			s.writeEventDeduped(ctx, Event{TS: now, Severity: SevAlarm, Code: EvCmdNotConfirmed,
				Message: fmt.Sprintf("«%s» не підтверджено за %s: %s", d.cmd.Label, within, fmtLive(d.snap.Live)),
				Context: map[string]any{"command": d.cmd.ID, "live": d.snap.Live}})
			continue
		}
		s.writeEventDeduped(ctx, Event{TS: now, Severity: SevInfo, Code: EvCmdConfirmed,
			Message: fmt.Sprintf("«%s» підтверджено: %s", d.cmd.Label, fmtLive(d.snap.Live)),
			Context: map[string]any{"command": d.cmd.ID, "live": d.snap.Live}})
		switch {
		case d.cmd.Target == "ess" && d.cmd.Start:
			s.ctl.ESSStarted(now)
			s.sess.essStopConfirmed = false
		case d.cmd.Target == "ess":
			s.sess.essStopConfirmed = true
			if d.cmd.ID == CmdArrayStopESS {
				pv := "немає даних"
				if t.PVPowerKw != nil {
					pv = fmt.Sprintf("%.1f кВт", *t.PVPowerKw)
				}
				verdict := "сонце згасло"
				if pvRunningOn(t) {
					verdict = "сонце лишилось"
				}
				s.writeEventDeduped(ctx, Event{TS: now, Severity: SevInfo, Code: EvArrayStopPVCheck,
					Message: "після зупинки масиву УЗЕ " + verdict + " (40388 " + pv + ")",
					Context: map[string]any{"pv_running": pvRunningOn(t), "live": liveValues("pv", t, onGrid, fleetKnown)}})
			}
		case d.cmd.Start:
			if !s.sess.pvRunningAtEnable {
				s.sess.pvStartedByTest = true
			}
		}
	}
}

// invertersOnGrid counts the inverters on grid in a fresh sweep.
func (s *Service) invertersOnGrid(now time.Time) (int, bool) {
	fleet := s.lastInverters.Load()
	if fleet == nil || len(s.cfg.Diagnostics.Inverters.DeviceAddresses) == 0 ||
		now.Sub(fleet.TS) > 3*s.cfg.Diagnostics.Inverters.PollInterval {
		return 0, false
	}
	n := 0
	for _, inv := range fleet.Inverters {
		if inv.PollOK && inv.Class == InvOnGrid {
			n++
		}
	}
	return n, true
}

// sendAutoStops stops what the test started once a check latched a
// stop (cutover §9, §12.4): the battery if the test started it, the sun
// only if the test started it. The battery stop goes out even when the
// PCS already reads as shut down: a PCS that tripped comes back by
// itself, a stopped one does not.
func (s *Service) sendAutoStops(ctx context.Context, now time.Time) {
	if s.sess.autoStopSent || !s.ctl.StopAuto() {
		return
	}
	s.sess.autoStopSent = true
	if s.essStartedByTest() && !s.sess.essStopConfirmed {
		s.sendOneShot(ctx, now, oneShots[CmdESSStop], "edge: зупинка", true)
	}
	if s.sess.pvStartedByTest {
		s.sendOneShot(ctx, now, oneShots[CmdPVStop], "edge: зупинка", true)
	}
}

// advanceStopping walks turning writing off (cutover §7 step 5): the
// battery at 0 and read back, then the battery stopped and confirmed,
// then the sun stopped if the test started it, then the flag drops.
// Each step that fails or times out is noted and the next one runs, so
// the flag always drops. true = the flag dropped.
func (s *Service) advanceStopping(ctx context.Context, now time.Time, t Tick) bool {
	since := s.ctl.StoppingSince()
	if s.sess.stopPhase == "" {
		s.sess.stopPhase = "zero"
	}
	for {
		switch s.sess.stopPhase {
		case "zero":
			confirmed := s.stoppingConfirmed(since)
			if !confirmed && !s.ctl.StoppingTimedOut(now) {
				return false
			}
			if !confirmed {
				s.sess.stopNotes = append(s.sess.stopNotes, "readback безпечних уставок не підтверджено")
			}
			s.sess.stopPhase = "ess_stop"
			// At the end the battery is stopped always; after a fault,
			// if the test started it. A stop already on its way (sent by
			// a latched stop) is waited for, not doubled.
			if (s.sess.disableByOperator || s.essStartedByTest()) && !s.sess.essStopConfirmed {
				if r := s.sendOneShot(ctx, now, oneShots[CmdESSStop], "edge: вимкнення запису", true); r.OK {
					s.sess.essStopAwaited = true
				} else {
					s.sess.stopNotes = append(s.sess.stopNotes, "зупинку УЗЕ не надіслано: "+r.Error)
				}
			}
		case "ess_stop":
			if s.sess.essStopAwaited {
				if st, ok := s.commandStatus(CmdESSStop); ok && st.open() {
					return false
				}
				if !s.sess.essStopConfirmed {
					s.sess.stopNotes = append(s.sess.stopNotes, "зупинку УЗЕ не підтверджено")
				}
			}
			s.sess.stopPhase = "pv_stop"
			if s.sess.pvStartedByTest {
				if r := s.sendOneShot(ctx, now, oneShots[CmdPVStop], "edge: вимкнення запису", true); r.OK {
					s.sess.pvStopAwaited = true
				} else {
					s.sess.stopNotes = append(s.sess.stopNotes, "зупинку СЕС не надіслано: "+r.Error)
				}
			}
		case "pv_stop":
			if s.sess.pvStopAwaited {
				st, ok := s.commandStatus(CmdPVStop)
				if ok && st.open() {
					return false
				}
				if !ok || st.State != CmdConfirmed {
					s.sess.stopNotes = append(s.sess.stopNotes, "зупинку СЕС не підтверджено")
				}
			}
			reason := s.ctl.Reason()
			if len(s.sess.stopNotes) > 0 {
				reason += " (" + joinNotes(s.sess.stopNotes) + ")"
			}
			ev, err := s.gate.Disable("edge", reason, now)
			if err != nil {
				s.log.Error("edge_write_gate_persist", "err", err)
			}
			s.writeEventDeduped(ctx, ev)
			s.endWriteSession(now)
			return true
		}
	}
}

func joinNotes(notes []string) string { return strings.Join(notes, "; ") }

// fmtLive renders the watched values as "40540=1, 40539=0".
func fmtLive(live map[string]any) string {
	keys := make([]string, 0, len(live))
	for k := range live {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%v", k, live[k]))
	}
	if len(parts) == 0 {
		return "немає даних"
	}
	return strings.Join(parts, ", ")
}

// beginWriteSession starts a session's command state at enabling.
func (s *Service) beginWriteSession(t Tick) {
	s.sess = writeSession{pvRunningAtEnable: pvRunningOn(t)}
	s.essStartSent.Store(false)
	s.cmdMu.Lock()
	s.cmds = map[string]*CommandStatus{}
	s.cmdMu.Unlock()
}

// endWriteSession closes what is still open once the flag is off.
func (s *Service) endWriteSession(now time.Time) {
	s.ctl.Off()
	s.writeCmd.Store(nil)
	s.cmdMu.Lock()
	for _, c := range s.cmds {
		if c.open() {
			at := now
			c.State, c.DoneAt, c.Note = CmdNotConfirmed, &at, "запис вимкнено"
		}
	}
	s.cmdMu.Unlock()
	s.sess = writeSession{}
	s.essStartSent.Store(false)
}

// checkDaytimePV notes once a day at 10:00 that the sun is not in
// operation while writing is on: there is no autostart, the operator
// presses «Пуск СЕС» (handoff §9.1 п. 2).
func (s *Service) checkDaytimePV(ctx context.Context, now time.Time, t Tick) {
	if s.tz == nil {
		loc, err := time.LoadLocation(s.cfg.Timezone)
		if err != nil {
			return
		}
		s.tz = loc
	}
	local := now.In(s.tz)
	day := local.Format("2006-01-02")
	if local.Hour() != 10 || s.sess.pvCheckDay == day {
		return
	}
	s.sess.pvCheckDay = day
	if pvRunningOn(t) {
		return
	}
	s.writeEventDeduped(ctx, Event{TS: now, Severity: SevWarning, Code: EvPVNotRunning,
		Message: "о 10:00 СЕС не в роботі — якщо сонце є, оператор натискає «Пуск СЕС»"})
}

// equipmentView is the equipment block for the consoles.
func (s *Service) equipmentView(t *Tick) *WriteEquipment {
	e := &WriteEquipment{ESS: "unknown", PV: "unknown", PVRefusing: s.pvRefusing.Load(), PCSMode: "unknown"}
	if t != nil {
		e.PCSMode = pcsModeName(*t)
		if v, ok := t.Values["pcs_working_mode"]; ok {
			e.PCSModeRaw = &v
		}
		switch {
		case essRunningOn(*t):
			e.ESS = "running"
		case essStoppedOn(*t):
			e.ESS = "stopped"
		}
		if t.PVPowerKw != nil {
			e.PV = map[bool]string{true: "running", false: "not_running"}[pvRunningOn(*t)]
		}
	}
	if v := s.sessView.Load(); v != nil {
		e.ESSStarted, e.PVStartedByTest = v.ESSStarted, v.PVStartedByTest
	}
	return e
}
