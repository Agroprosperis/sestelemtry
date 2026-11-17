package edge

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"
)

// stepWrite runs the controller for this tick while the write flag is on
// and records the write side of the decision. d is nil when no decision
// was made this tick.
func (s *Service) stepWrite(ctx context.Context, now time.Time, tick Tick, d *Decision) {
	if !s.gate.Enabled() || s.ctl == nil {
		if s.ctl != nil && s.ctl.State() != WriteOff {
			s.ctl.Off()
		}
		s.writeCmd.Store(nil)
		return
	}
	s.updateCommands(ctx, now, tick)
	if s.ctl.State() == WriteStopping && s.advanceStopping(ctx, now, tick) {
		return
	}
	decision := Decision{TS: now, ReasonCode: "no_decision"}
	if d != nil {
		decision = *d
	}
	n, faults := s.inverterCounts(now)
	cmd, evs := s.ctl.Step(ControlInputs{
		Now:            now,
		Tick:           tick,
		Decision:       decision,
		ExportCapKw:    s.exportCapKw(now),
		ImportLimitKw:  s.effectiveImportLimitKw(now),
		InverterCount:  n,
		InverterFaults: faults,
	})
	s.writeCmd.Store(&cmd)
	for _, ev := range evs {
		s.writeEventDeduped(ctx, ev)
	}
	s.sendAutoStops(ctx, now)
	s.checkDaytimePV(ctx, now, tick)
	s.sessView.Store(&WriteEquipment{ESSStarted: s.ctl.ESSStartedInSession(), PVStartedByTest: s.sess.pvStartedByTest})
	if d == nil {
		return
	}
	d.WriteEnabled = true
	d.StateMachine = "WRITE_" + strings.ToUpper(cmd.State)
	d.WriteState, d.WriteReason, d.WriteClamps = cmd.State, cmd.Reason, cmd.Clamps
	ess := cmd.ESSKw
	d.CommandESSKw = &ess
	if cmd.PVKnown {
		pv := cmd.PVLimitKw
		d.CommandPVLimitKw = &pv
	}
	if fb, ok := s.feedback(regESSPower); ok && fb.Err == "" {
		kw := fb.Kw
		d.Written40381 = &kw
		if fb.ReadbackKw != nil {
			d.Readback40381 = fb.ReadbackKw
		}
	}
	if fb, ok := s.feedback(regPVLimit); ok && fb.Err == "" {
		kw := fb.Kw
		d.Written40378 = &kw
		if fb.ReadbackKw != nil {
			d.Readback40378 = fb.ReadbackKw
		}
	}
}

// onWriteFault handles a failed write or a readback that differs
// (handoff §9.1 п. 2): battery 0, then the flag drops.
func (s *Service) onWriteFault(ctx context.Context, f writeFault) {
	if !s.gate.Enabled() || s.ctl.State() == WriteStopping {
		return
	}
	s.writeEventDeduped(ctx, Event{
		TS: f.at, Severity: SevAlarm, Code: EvWriteFault,
		Message: "відмова запису: " + f.msg + " — УЗЕ 0 кВт, запис вимикається",
	})
	s.beginStopping(f.at, "відмова запису: "+f.msg, false)
}

// beginStopping starts turning writing off. byOperator: the battery is
// stopped whatever happened before (cutover §7 step 5); on a fault only
// if the test started it.
func (s *Service) beginStopping(now time.Time, reason string, byOperator bool) {
	s.sess.disableByOperator, s.sess.stopPhase, s.sess.stopNotes = byOperator, "", nil
	s.ctl.BeginStopping(now, reason)
}

// onWriteRequest serves a console action in the core loop.
func (s *Service) onWriteRequest(ctx context.Context, now time.Time, req writeRequest) writeReply {
	switch req.action {
	case "enable":
		if s.gate.Enabled() {
			return writeReply{Error: "запис уже ввімкнено"}
		}
		if strings.TrimSpace(req.confirm) != s.cfg.SiteID {
			return writeReply{Error: fmt.Sprintf("для підтвердження введіть код об'єкта (%s)", s.cfg.SiteID)}
		}
		if !req.pinOK {
			return writeReply{Error: "потрібен PIN"}
		}
		pre := s.writePreconditions(now)
		for _, p := range pre {
			if !p.OK {
				return writeReply{Error: "не виконані передумови запису", Preconditions: pre}
			}
		}
		ev, err := s.gate.Enable(req.by, now)
		if err != nil {
			return writeReply{Error: err.Error(), Preconditions: pre}
		}
		var tick Tick
		if t := s.lastTick.Load(); t != nil {
			tick = *t
		}
		s.beginWriteSession(tick)
		s.ctl.Start(now)
		s.writeEventDeduped(ctx, ev)
		return writeReply{OK: true, Preconditions: pre}
	case "stop":
		if !s.gate.Enabled() {
			return writeReply{Error: "запис не ввімкнено"}
		}
		if ev, ok := s.ctl.Stop(now, "оператор: УЗЕ 0 кВт ("+req.by+")", false); ok {
			s.writeEventDeduped(ctx, ev)
		}
		return writeReply{OK: true}
	case "disable":
		if !s.gate.Enabled() {
			return writeReply{Error: "запис не ввімкнено"}
		}
		if s.ctl.State() == WriteStopping {
			return writeReply{Error: "запис уже вимикається: УЗЕ 0, зупинка УЗЕ, потім прапорець"}
		}
		s.beginStopping(now, "вимкнено оператором ("+req.by+")", true)
		return writeReply{OK: true}
	}
	return writeReply{Error: "action must be enable, stop or disable"}
}

// requestWrite hands a console action to the core loop. pinOK only
// after the caller matched the PIN.
func (s *Service) requestWrite(ctx context.Context, action, by, confirm string, pinOK bool) writeReply {
	req := writeRequest{action: action, by: by, confirm: confirm, pinOK: pinOK, reply: make(chan writeReply, 1)}
	select {
	case s.writeRequests <- req:
	case <-ctx.Done():
		return writeReply{Error: ctx.Err().Error()}
	case <-time.After(5 * time.Second):
		return writeReply{Error: "сервіс не відповів"}
	}
	select {
	case r := <-req.reply:
		return r
	case <-ctx.Done():
		return writeReply{Error: ctx.Err().Error()}
	}
}

// requestCommand hands a start/stop button to the core loop.
func (s *Service) requestCommand(ctx context.Context, id, by string) cmdReply {
	req := cmdRequest{id: id, by: by, reply: make(chan cmdReply, 1)}
	select {
	case s.cmdRequests <- req:
	case <-ctx.Done():
		return cmdReply{Error: ctx.Err().Error()}
	case <-time.After(5 * time.Second):
		return cmdReply{Error: "сервіс не відповів"}
	}
	select {
	case r := <-req.reply:
		return r
	case <-ctx.Done():
		return cmdReply{Error: ctx.Err().Error()}
	}
}

// exportCapKw is the PCC export ceiling in force: the active manifest's
// (the site's «Налаштування» in the cloud), even when it has none; the
// device config only while no manifest is active. 0 = none.
func (s *Service) exportCapKw(now time.Time) float64 {
	if m := s.manifest.Load(); m != nil && m.ActiveAt(now) {
		return m.GridLimits.ExportCapKw
	}
	return s.cfg.Limits.Grid.ExportCapKw
}

// inverterCounts sizes the PV ceiling: configured inverters and those in
// fault in a fresh sweep. A stale sweep counts no faults.
func (s *Service) inverterCounts(now time.Time) (int, int) {
	n := len(s.cfg.Diagnostics.Inverters.DeviceAddresses)
	fleet := s.lastInverters.Load()
	if n == 0 || fleet == nil || now.Sub(fleet.TS) > 3*s.cfg.Diagnostics.Inverters.PollInterval {
		return n, 0
	}
	faults := 0
	for _, inv := range fleet.Inverters {
		if inv.PollOK && inv.Class == inverterFaultClass {
			faults++
		}
	}
	return n, faults
}

// WritePrecondition is one condition that must hold to turn writing on
// (ems_sl_write_cutover.md §6, §12.6).
type WritePrecondition struct {
	ID     string `json:"id"`
	OK     bool   `json:"ok"`
	Label  string `json:"label"`
	Actual string `json:"actual"`
}

// writePreconditions checks the live state against the cutover's
// start conditions. Every one must hold.
func (s *Service) writePreconditions(now time.Time) []WritePrecondition {
	var out []WritePrecondition
	add := func(id string, ok bool, label, actual string) {
		out = append(out, WritePrecondition{ID: id, OK: ok, Label: label, Actual: actual})
	}
	add("mode", s.cfg.Control.Mode == ModeShadow, "режим контролера shadow", string(s.cfg.Control.Mode))
	pinSet := s.pin.isSet()
	add("pin", pinSet, "PIN задано на пристрої (ems-edge -set-write-pin)", map[bool]string{true: "так", false: "ні"}[pinSet])
	ov := s.override.Load()
	add("override", !ov.activeAt(now), "немає локального override", map[bool]string{true: "діє override", false: "немає"}[ov.activeAt(now)])

	tick := s.lastTick.Load()
	if tick == nil || now.Sub(tick.TS) > 5*time.Second {
		add("telemetry", false, "свіжа телеметрія", "немає")
		return out
	}
	t := *tick
	add("telemetry", t.DataQuality == QualityOK || t.DataQuality == QualityEstimated, "якість телеметрії ok", t.DataQuality)
	for _, dev := range s.cfg.SmartLogger.Devices {
		fresh, _ := s.hostFresh(now, dev.Host)
		add("poll_"+string(dev.Role), fresh, "SmartLogger "+dev.Host+" на зв'язку", map[bool]string{true: "так", false: "ні"}[fresh])
	}

	words, ok := t.SLAlarmWords()
	clear := ok
	for _, w := range words {
		clear = clear && w == 0
	}
	alarmActual := "не опитуються"
	if ok {
		alarmActual = strings.Join(slAlarmHex(words), " ")
	}
	add("alarms", clear, "слова аварій 50000… усі 0", alarmActual)
	// Cutover 04.11.2026 §6, §7 step 2: the installers stop the battery
	// before the hand-over; the test starts it with «Пуск УЗЕ».
	pcsActual := "немає даних"
	if t.PCSShutdown != nil {
		pcsActual = fmt.Sprintf("40540=%v", map[bool]int{true: 1, false: 0}[*t.PCSShutdown])
	}
	add("ess_stopped", essStoppedOn(t), "УЗЕ зупинена до стику (40540 ≠ 0)", pcsActual)

	for _, k := range s.controlModeKeys() {
		v, has := t.Values[k]
		actual := "не прочитано"
		if has {
			actual = fmt.Sprintf("%g", v)
		}
		add("control_mode_"+k, has && v == 4, "40737 = 4 ("+k+")", actual)
	}

	// A stopped sun may read 0 here; its ceiling is written once 40396 is
	// read after «Пуск СЕС».
	rated, hasRated := t.Values["pv_rated_kw"]
	ratedOK := (hasRated && rated > 0) || !pvRunningOn(t)
	ratedActual := fmtKw(rated, hasRated)
	if !(hasRated && rated > 0) && ratedOK {
		ratedActual += " (СЕС не в роботі)"
	}
	add("pv_rated", ratedOK, "номінал СЕС 40396 прочитано, якщо СЕС у роботі", ratedActual)
	add("soc", t.SocPercent != nil, "SOC відомий", fmtPtrPct(t.SocPercent))
	add("ess_limits", t.ESSChargeMaxKw != nil && t.ESSDischargeMaxKw != nil, "40490/40492 відомі",
		fmt.Sprintf("%s / %s", fmtPtrKw(t.ESSChargeMaxKw), fmtPtrKw(t.ESSDischargeMaxKw)))

	capKw := s.exportCapKw(now)
	add("export_cap", capKw > 0, "стеля відпуску задана (manifest: «Налаштування» → режим відпуску; без manifest — конфіг)", fmtKw(capKw, capKw > 0))
	limit := s.effectiveImportLimitKw(now)
	add("import_limit", limit > 0, "ліміт імпорту заданий", fmtKw(limit, limit > 0))
	if t.GridPowerKw != nil && limit > 0 {
		add("import_now", *t.GridPowerKw < importStopShare*limit, "імпорт не біля ліміту", fmt.Sprintf("%.0f кВт", *t.GridPowerKw))
	} else {
		add("import_now", false, "потужність PCC 40505 відома", "немає")
	}
	low := lowVoltages(&t)
	add("voltage", len(low) == 0 && hasVoltage(t), "мережа є, напруги в нормі", voltageActual(t, low))
	if meter, has := t.Values["meter_active_power_kw"]; has && t.GridPowerKw != nil &&
		math.Abs(meter) >= pccSignDeadbandKw && math.Abs(*t.GridPowerKw) >= pccSignDeadbandKw {
		add("pcc_sign", pccSignsAgree(*t.GridPowerKw, meter*s.cfg.EffectiveMeterSign()),
			"40505 і лічильник 32278 показують один напрям (grid_import_sign / meter_import_sign)",
			fmt.Sprintf("імпорт за 40505 %+.1f · за 32278 %+.1f кВт", *t.GridPowerKw, meter*s.cfg.EffectiveMeterSign()))
	}
	if m := s.manifest.Load(); m != nil && m.ActiveAt(now) {
		add("manifest_seq", m.ManifestSeq > 0, "чинний manifest пронумерований", fmt.Sprintf("seq %d", m.ManifestSeq))
	}
	return out
}

// controlModeKeys are the 40737 readings expected on this topology.
func (s *Service) controlModeKeys() []string {
	var out []string
	for _, d := range s.cfg.SmartLogger.Devices {
		out = append(out, DeviceMetricKey(d.Role, "active_power_control_mode"))
	}
	return out
}

func hasVoltage(t Tick) bool {
	for _, k := range []string{
		"meter_phase_a_voltage_v", "grid_line_voltage_ab_v", "pv_grid_line_voltage_ab_v", "ess_grid_line_voltage_ab_v",
	} {
		if v, ok := t.Values[k]; ok && v > 0 {
			return true
		}
	}
	return false
}

func voltageActual(t Tick, low []string) string {
	if len(low) > 0 {
		return strings.Join(low, "; ")
	}
	if !hasVoltage(t) {
		return "напруги не прочитано"
	}
	if v, ok := t.Values["meter_phase_a_voltage_v"]; ok {
		return fmt.Sprintf("фаза A %.0f В", v)
	}
	return "ok"
}

func fmtKw(v float64, ok bool) string {
	if !ok {
		return "немає"
	}
	return fmt.Sprintf("%.0f кВт", v)
}

func fmtPtrKw(v *float64) string {
	if v == nil {
		return "—"
	}
	return fmt.Sprintf("%.0f кВт", *v)
}

func fmtPtrPct(v *float64) string {
	if v == nil {
		return "немає"
	}
	return fmt.Sprintf("%.1f %%", *v)
}

// WriteHealth is the write block of the health snapshot, for the cloud
// console: is the IoT2050 writing, what, and what the loggers accepted.
type WriteHealth struct {
	Enabled            bool            `json:"enabled"`
	Writer             string          `json:"writer"` // iot2050 | other
	State              string          `json:"state"`
	Reason             string          `json:"reason,omitempty"`
	Gate               GateState       `json:"gate"`
	PinSet             bool            `json:"pin_set"`
	Command            *WriteCommand   `json:"command,omitempty"`
	Registers          []WriteFeedback `json:"registers,omitempty"`
	AppliedManifestSeq int64           `json:"applied_manifest_seq,omitempty"`
	Equipment          *WriteEquipment `json:"equipment,omitempty"`
	Commands           []CommandStatus `json:"commands,omitempty"`
}

func (s *Service) writeHealth() *WriteHealth {
	h := &WriteHealth{
		Enabled:            s.gate.Enabled(),
		Writer:             "other",
		State:              WriteOff,
		Gate:               s.gate.State(),
		PinSet:             s.pin.isSet(),
		AppliedManifestSeq: s.appliedSeq.Load(),
		Equipment:          s.equipmentView(s.lastTick.Load()),
		Commands:           s.commandStatuses(),
	}
	if h.Enabled {
		h.Writer = "iot2050"
		if c := s.writeCmd.Load(); c != nil {
			cc := *c
			h.Command = &cc
			h.State, h.Reason = cc.State, cc.Reason
		}
		for _, reg := range s.configuredWriteRegs() {
			if fb, ok := s.feedback(reg); ok {
				h.Registers = append(h.Registers, fb)
			}
		}
	}
	return h
}
