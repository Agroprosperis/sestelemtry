package edge

import (
	"fmt"
	"strings"
	"time"
)

// Health snapshot (diagnostics spec §8.3): the "очікувано vs факт"
// document rendered by both consoles and shipped inside the heartbeat.
// Built from state the service already tracks — no extra Modbus reads.

// Check severities. `info` never fails the root ok.
const (
	CheckOK      = "ok"
	CheckInfo    = "info"
	CheckWarning = "warning"
	CheckAlarm   = "alarm"
)

// HealthCheck is one row of the §4 table.
type HealthCheck struct {
	ID       string `json:"id"`
	OK       bool   `json:"ok"`
	Severity string `json:"severity"` // ok | info | warning | alarm
	Label    string `json:"label"`
	Expected string `json:"expected"`
	Actual   string `json:"actual"`
	Detail   string `json:"detail,omitempty"`
}

// BessHealth is the §7.3 contract. Unavailable fields are null and the
// card stays up.
type BessHealth struct {
	Class            string    `json:"class"`
	ClassLabel       string    `json:"class_label"`
	SocPercent       *float64  `json:"soc_percent"`
	SohPercent       *float64  `json:"soh_percent"`
	SoePercent       *float64  `json:"soe_percent"`
	SocMinPct        float64   `json:"soc_min_pct"`
	SocMaxPct        float64   `json:"soc_max_pct"`
	PKw              *float64  `json:"p_kw"`
	QKvar            *float64  `json:"q_kvar"`
	PPlanKw          *float64  `json:"p_plan_kw"`
	PShadowKw        *float64  `json:"p_shadow_kw"`
	Clamps           []string  `json:"clamps"`
	ChargeMaxKw      *float64  `json:"charge_max_kw"`
	DischargeMaxKw   *float64  `json:"discharge_max_kw"`
	ChargeableKwh    *float64  `json:"chargeable_kwh"`
	DischargeableKwh *float64  `json:"dischargeable_kwh"`
	RatedKw          *float64  `json:"rated_kw"`
	RatedKwh         *float64  `json:"rated_kwh"`
	PassportKw       *float64  `json:"passport_kw"`
	PassportKwh      *float64  `json:"passport_kwh"`
	PassportEssCount *int      `json:"passport_ess_count"`
	NEss             *int      `json:"n_ess"`
	NPcs             *int      `json:"n_pcs"`
	PcsInOperation   *int      `json:"pcs_in_operation"`
	PcsShutdown      *int      `json:"pcs_shutdown"`
	PcsLabel         string    `json:"pcs_label"`
	ChargedKwh       *float64  `json:"charged_kwh"`
	DischargedKwh    *float64  `json:"discharged_kwh"`
	PollOK           bool      `json:"poll_ok"`
	PollError        *string   `json:"poll_error"`
	TS               time.Time `json:"ts"`
}

type HealthAlarms struct {
	Words []string `json:"words"` // six hex words, 50000…50005
}

// HealthSnapshot is the §8.3 root document. `inverters` is absent when
// the 51xxx poll is disabled; `alarms` is absent until the alarm words
// have actually been polled (absence ≠ all clear).
type HealthSnapshot struct {
	TS        time.Time          `json:"ts"`
	OK        bool               `json:"ok"`
	Checks    []HealthCheck      `json:"checks"`
	Bess      *BessHealth        `json:"bess,omitempty"`
	Inverters []InverterSnapshot `json:"inverters,omitempty"`
	Alarms    *HealthAlarms      `json:"alarms,omitempty"`
}

// BESS class labels (§7.1).
var bessClassLabels = map[string]string{
	"discharging": "розряд",
	"charging":    "заряд",
	"hold":        "утримання",
	"shutdown":    "вимкнено (PCS shutdown)",
	"unreachable": "без зв'язку",
	"unknown":     "невідомо",
}

// buildHealth assembles the snapshot from the service's current state.
// Safe from any goroutine: everything it touches is atomics/sync.Map.
func (s *Service) buildHealth(now time.Time) *HealthSnapshot {
	h := &HealthSnapshot{TS: now, OK: true}

	tick := s.lastTick.Load()
	dec := s.lastDecision.Load()
	params := resolveParams(now, s.manifest.Load(), s.cfg)

	essFresh, essSeen := s.roleFresh(now, RoleESS)
	// Single topology: the one box carries both roles.
	if !essSeen {
		essFresh, essSeen = s.roleFresh(now, RoleAll)
	}

	add := func(c HealthCheck) {
		if c.Severity == CheckWarning || c.Severity == CheckAlarm {
			if !c.OK {
				h.OK = false
			}
		}
		h.Checks = append(h.Checks, c)
	}

	// data_quality
	dq := ""
	if tick != nil {
		dq = tick.DataQuality
	}
	switch dq {
	case QualityOK:
		add(HealthCheck{ID: "data_quality", OK: true, Severity: CheckOK, Label: "Якість даних", Expected: "ok", Actual: dq})
	case QualityEstimated:
		add(HealthCheck{ID: "data_quality", OK: true, Severity: CheckInfo, Label: "Якість даних", Expected: "ok", Actual: dq, Detail: "load розраховано з балансу вузла"})
	case QualityStale:
		add(HealthCheck{ID: "data_quality", OK: false, Severity: CheckWarning, Label: "Якість даних", Expected: "ok", Actual: dq})
	default: // fault or no tick yet
		if dq == "" {
			dq = "немає даних"
		}
		add(HealthCheck{ID: "data_quality", OK: false, Severity: CheckAlarm, Label: "Якість даних", Expected: "ok", Actual: dq, Detail: "dispatch заблоковано (data_fault)"})
	}

	// soc_band
	socExp := fmt.Sprintf("%.0f…%.0f %%", params.socMinPct, params.socMaxPct)
	if tick != nil && tick.SocPercent != nil {
		soc := *tick.SocPercent
		if soc < params.socMinPct || soc > params.socMaxPct {
			add(HealthCheck{ID: "soc_band", OK: false, Severity: CheckWarning, Label: "SOC у робочому вікні", Expected: socExp, Actual: fmt.Sprintf("%.1f %%", soc)})
		} else {
			add(HealthCheck{ID: "soc_band", OK: true, Severity: CheckOK, Label: "SOC у робочому вікні", Expected: socExp, Actual: fmt.Sprintf("%.1f %%", soc)})
		}
	} else {
		add(HealthCheck{ID: "soc_band", OK: false, Severity: CheckWarning, Label: "SOC у робочому вікні", Expected: socExp, Actual: "немає даних"})
	}

	// pcs: 40540 shutdown blocks; 40539 = 0 alone is only a warning
	// (the register can legitimately read 0 on some firmwares).
	pcsShut, pcsShutOK := tickInt(tick, "pcs_shutdown")
	pcsOper, pcsOperOK := tickInt(tick, "pcs_in_operation")
	switch {
	case pcsShutOK && pcsShut != 0:
		add(HealthCheck{ID: "pcs", OK: false, Severity: CheckAlarm, Label: "PCS", Expected: "не shutdown", Actual: fmt.Sprintf("40540=%d", pcsShut), Detail: "dispatch заблоковано (pcs_shutdown)"})
	case pcsShutOK && pcsOperOK && pcsOper == 0:
		add(HealthCheck{ID: "pcs", OK: false, Severity: CheckWarning, Label: "PCS", Expected: "в роботі", Actual: "40539=0, 40540=0", Detail: "регістр 0 — не трактувати як shutdown"})
	case pcsShutOK:
		actual := "40540=0"
		if pcsOperOK {
			actual = fmt.Sprintf("40539=%d, 40540=0", pcsOper)
		}
		add(HealthCheck{ID: "pcs", OK: true, Severity: CheckOK, Label: "PCS", Expected: "не shutdown", Actual: actual})
	default:
		add(HealthCheck{ID: "pcs", OK: false, Severity: CheckWarning, Label: "PCS", Expected: "не shutdown", Actual: "немає даних"})
	}

	// sl_alarms
	if tick != nil {
		if words, polled := tick.SLAlarmWords(); polled {
			hex := slAlarmHex(words)
			h.Alarms = &HealthAlarms{Words: hex[:]}
			if tick.SLAlarmActive() {
				add(HealthCheck{ID: "sl_alarms", OK: false, Severity: CheckAlarm, Label: "Аварії SmartLogger", Expected: "усі слова 0", Actual: strings.Join(hex[:], " "), Detail: "dispatch заблоковано (sl_alarm)"})
			} else {
				add(HealthCheck{ID: "sl_alarms", OK: true, Severity: CheckOK, Label: "Аварії SmartLogger", Expected: "усі слова 0", Actual: "усі 0"})
			}
		} else {
			add(HealthCheck{ID: "sl_alarms", OK: false, Severity: CheckWarning, Label: "Аварії SmartLogger", Expected: "усі слова 0", Actual: "не опитуються", Detail: "50000…50005 немає у whitelist"})
		}
	}

	// grid_limit
	limitKw := s.cfg.Limits.Grid.ImportLimitKw
	if m := s.manifest.Load(); m != nil && m.ActiveAt(now) && m.GridLimits.ImportLimitKw > 0 {
		limitKw = m.GridLimits.ImportLimitKw
	}
	if limitKw > 0 && tick != nil && tick.GridPowerKw != nil {
		imp := *tick.GridPowerKw
		if imp > limitKw {
			add(HealthCheck{ID: "grid_limit", OK: false, Severity: CheckAlarm, Label: "Ліміт імпорту", Expected: fmt.Sprintf("≤ %.0f кВт", limitKw), Actual: fmt.Sprintf("%.1f кВт", imp), Detail: "у shadow лише індикація"})
		} else {
			add(HealthCheck{ID: "grid_limit", OK: true, Severity: CheckOK, Label: "Ліміт імпорту", Expected: fmt.Sprintf("≤ %.0f кВт", limitKw), Actual: fmt.Sprintf("%.1f кВт", imp)})
		}
	}

	// plan_vs_shadow: only meaningful while a plan interval is active.
	if dec != nil && dec.PBessPlanKw != nil {
		plan := *dec.PBessPlanKw
		shadow := dec.PBessVirtualKw
		delta := plan - shadow
		if delta < 0 {
			delta = -delta
		}
		if delta > 1 {
			add(HealthCheck{ID: "plan_vs_shadow", OK: false, Severity: CheckWarning, Label: "План vs shadow", Expected: fmt.Sprintf("%.1f кВт", plan), Actual: fmt.Sprintf("%.1f кВт", shadow), Detail: strings.Join(dec.Clamps, "; ")})
		} else {
			add(HealthCheck{ID: "plan_vs_shadow", OK: true, Severity: CheckOK, Label: "План vs shadow", Expected: fmt.Sprintf("%.1f кВт", plan), Actual: fmt.Sprintf("%.1f кВт", shadow)})
		}
	}

	// shadow_vs_fact: always info in shadow — Encombi drives the BESS.
	if dec != nil && tick != nil && tick.ESSPowerKw != nil {
		add(HealthCheck{ID: "shadow_vs_fact", OK: true, Severity: CheckInfo, Label: "Shadow vs факт УЗЕ", Expected: fmt.Sprintf("%.1f кВт", dec.PBessVirtualKw), Actual: fmt.Sprintf("%.1f кВт", *tick.ESSPowerKw), Detail: "керує Encombi — розбіжність очікувана"})
	}

	// sl_{role}: per-SmartLogger link state.
	for _, dev := range s.cfg.SmartLogger.Devices {
		fresh, seen := s.hostFresh(now, dev.Host)
		id := "sl_" + string(dev.Role)
		label := "SmartLogger " + strings.ToUpper(string(dev.Role))
		switch {
		case fresh:
			add(HealthCheck{ID: id, OK: true, Severity: CheckOK, Label: label, Expected: "poll OK", Actual: "OK"})
		case seen:
			add(HealthCheck{ID: id, OK: false, Severity: CheckAlarm, Label: label, Expected: "poll OK", Actual: "останній poll застарів", Detail: dev.Host})
		default:
			add(HealthCheck{ID: id, OK: false, Severity: CheckAlarm, Label: label, Expected: "poll OK", Actual: "немає жодного poll", Detail: dev.Host})
		}
	}

	// inverters: only when the 51xxx poll is configured. Night standby
	// never reddens the fleet — only fault/unreachable do.
	if n := len(s.cfg.Diagnostics.Inverters.DeviceAddresses); n > 0 {
		fleet := s.lastInverters.Load()
		if fleet != nil {
			h.Inverters = fleet.Inverters
			counts := map[string]int{}
			for _, r := range fleet.Inverters {
				counts[r.Class]++
			}
			actual := fmt.Sprintf("%d у мережі · %d пуск · %d standby · %d аварія · %d без зв'язку",
				counts[InvOnGrid], counts[InvStarting], counts[InvStandby], counts[InvFault], counts[InvUnreachable])
			stale := now.Sub(fleet.TS) > 2*s.cfg.Diagnostics.Inverters.PollInterval
			switch {
			case counts[InvFault] > 0 || counts[InvUnreachable] > 0:
				add(HealthCheck{ID: "inverters", OK: false, Severity: CheckAlarm, Label: "Інвертори", Expected: fmt.Sprintf("%d без аварій", n), Actual: actual})
			case stale:
				add(HealthCheck{ID: "inverters", OK: false, Severity: CheckWarning, Label: "Інвертори", Expected: fmt.Sprintf("%d без аварій", n), Actual: actual, Detail: "знімок застарів"})
			default:
				add(HealthCheck{ID: "inverters", OK: true, Severity: CheckOK, Label: "Інвертори", Expected: fmt.Sprintf("%d без аварій", n), Actual: actual})
			}
		} else {
			add(HealthCheck{ID: "inverters", OK: false, Severity: CheckWarning, Label: "Інвертори", Expected: fmt.Sprintf("%d без аварій", n), Actual: "ще немає знімка"})
		}
	}

	// bess card + checks.
	bess := s.buildBessHealth(now, tick, dec, params, essFresh, essSeen)
	h.Bess = bess
	switch bess.Class {
	case "shutdown", "unreachable":
		add(HealthCheck{ID: "bess", OK: false, Severity: CheckAlarm, Label: "УЗЕ", Expected: "PCS не shutdown, SOC у вікні", Actual: bess.ClassLabel, Detail: "dispatch заблоковано"})
	default:
		socOut := bess.SocPercent != nil && (*bess.SocPercent < params.socMinPct || *bess.SocPercent > params.socMaxPct)
		if socOut {
			add(HealthCheck{ID: "bess", OK: false, Severity: CheckWarning, Label: "УЗЕ", Expected: "PCS не shutdown, SOC у вікні", Actual: fmt.Sprintf("%s, SOC поза вікном", bess.ClassLabel)})
		} else {
			add(HealthCheck{ID: "bess", OK: true, Severity: CheckOK, Label: "УЗЕ", Expected: "PCS не shutdown, SOC у вікні", Actual: bess.ClassLabel})
		}
	}

	// bess_inventory: passport vs SL registers; warning only, no block.
	if c := s.bessInventoryCheck(bess); c != nil {
		add(*c)
	}

	return h
}

// buildBessHealth maps the current tick + decision onto the §7.3
// contract.
func (s *Service) buildBessHealth(now time.Time, tick *Tick, dec *Decision, params engineParams, essFresh, essSeen bool) *BessHealth {
	b := &BessHealth{
		Class:     "unknown",
		SocMinPct: params.socMinPct,
		SocMaxPct: params.socMaxPct,
		Clamps:    []string{},
		PollOK:    essFresh,
		TS:        now,
	}
	if !essFresh {
		msg := "ESS SmartLogger poll застарів"
		if !essSeen {
			msg = "ESS SmartLogger: немає жодного poll"
		}
		b.PollError = &msg
		b.Class = "unreachable"
		b.ClassLabel = bessClassLabels[b.Class]
		return b
	}
	if tick == nil {
		b.ClassLabel = bessClassLabels[b.Class]
		return b
	}

	b.TS = tick.TS
	b.SocPercent = tick.SocPercent
	b.PKw = tick.ESSPowerKw
	b.ChargeMaxKw = tick.ESSChargeMaxKw
	b.DischargeMaxKw = tick.ESSDischargeMaxKw
	b.SohPercent = tickF(tick, "ess_soh_pct")
	b.SoePercent = tickF(tick, "ess_soe_pct")
	b.QKvar = tickF(tick, "reactive_ess_power_kvar")
	b.ChargeableKwh = tickF(tick, "ess_chargeable_kwh")
	b.DischargeableKwh = tickF(tick, "ess_dischargeable_kwh")
	b.RatedKw = tickF(tick, "ess_rated_kw")
	b.RatedKwh = tickF(tick, "ess_rated_kwh")
	b.ChargedKwh = tickF(tick, "total_energy_charged_kwh")
	b.DischargedKwh = tickF(tick, "total_energy_discharged_kwh")
	if v, ok := tickInt(tick, "ess_count"); ok {
		b.NEss = &v
	}
	if v, ok := tickInt(tick, "pcs_count"); ok {
		b.NPcs = &v
	}

	if p := s.cfg.Limits.Bess.PassportKw; p > 0 {
		b.PassportKw = f64ptr(p)
	}
	if p := s.cfg.Limits.Bess.PassportKwh; p > 0 {
		b.PassportKwh = f64ptr(p)
	}
	if p := s.cfg.Limits.Bess.PassportEssCount; p > 0 {
		b.PassportEssCount = &p
	}

	if dec != nil {
		b.PPlanKw = dec.PBessPlanKw
		v := dec.PBessVirtualKw
		b.PShadowKw = &v
		if dec.Clamps != nil {
			b.Clamps = dec.Clamps
		}
	}

	pcsShut, pcsShutOK := tickInt(tick, "pcs_shutdown")
	pcsOper, pcsOperOK := tickInt(tick, "pcs_in_operation")
	if pcsShutOK {
		b.PcsShutdown = &pcsShut
	}
	if pcsOperOK {
		b.PcsInOperation = &pcsOper
	}
	switch {
	case pcsShutOK && pcsShut != 0:
		b.PcsLabel = "вимкнено (40540≠0)"
	case pcsOperOK && pcsOper != 0:
		b.PcsLabel = "в роботі"
	case pcsShutOK && pcsOperOK:
		b.PcsLabel = "регістр 0 — не трактувати як shutdown"
	default:
		b.PcsLabel = "немає даних"
	}

	// Class (§7.1): shutdown wins, then the ±2 kW fact deadband.
	switch {
	case pcsShutOK && pcsShut != 0:
		b.Class = "shutdown"
	case b.PKw != nil && *b.PKw > deadbandKw:
		b.Class = "discharging"
	case b.PKw != nil && *b.PKw < -deadbandKw:
		b.Class = "charging"
	case b.PKw != nil:
		b.Class = "hold"
	default:
		b.Class = "unknown"
	}
	b.ClassLabel = bessClassLabels[b.Class]
	return b
}

// bessInventoryCheck compares the site passport with what the
// SmartLogger reports (40398/40484/40488). Mismatch is a warning only.
func (s *Service) bessInventoryCheck(b *BessHealth) *HealthCheck {
	if b.PassportKw == nil && b.PassportKwh == nil && b.PassportEssCount == nil {
		return nil // passport not configured — nothing to compare
	}
	var mismatches []string
	cmpF := func(name string, passport, actual *float64) {
		if passport == nil || actual == nil {
			return
		}
		// SL scaling noise tolerance: 1%.
		if diff := *actual - *passport; diff > *passport*0.01 || diff < -*passport*0.01 {
			mismatches = append(mismatches, fmt.Sprintf("%s: SL %.0f ≠ паспорт %.0f", name, *actual, *passport))
		}
	}
	cmpF("P, кВт", b.PassportKw, b.RatedKw)
	cmpF("E, кВт·год", b.PassportKwh, b.RatedKwh)
	if b.PassportEssCount != nil && b.NEss != nil && *b.NEss != *b.PassportEssCount {
		mismatches = append(mismatches, fmt.Sprintf("шафи: SL %d ≠ паспорт %d", *b.NEss, *b.PassportEssCount))
	}
	expected := passportSummary(b)
	if len(mismatches) > 0 {
		return &HealthCheck{ID: "bess_inventory", OK: false, Severity: CheckWarning, Label: "Комплектація УЗЕ", Expected: expected, Actual: strings.Join(mismatches, "; "), Detail: "розбіжність не блокує dispatch"}
	}
	return &HealthCheck{ID: "bess_inventory", OK: true, Severity: CheckOK, Label: "Комплектація УЗЕ", Expected: expected, Actual: "збігається з паспортом"}
}

func passportSummary(b *BessHealth) string {
	parts := []string{}
	if b.PassportKw != nil {
		parts = append(parts, fmt.Sprintf("%.0f кВт", *b.PassportKw))
	}
	if b.PassportKwh != nil {
		parts = append(parts, fmt.Sprintf("%.0f кВт·год", *b.PassportKwh))
	}
	if b.PassportEssCount != nil {
		parts = append(parts, fmt.Sprintf("%d шаф", *b.PassportEssCount))
	}
	return strings.Join(parts, " / ")
}

// roleFresh reports whether the device serving `role` produced a
// reading recently (same freshness window the console uses). Second
// return is false when the role is not configured at all.
func (s *Service) roleFresh(now time.Time, role DeviceRole) (fresh, configured bool) {
	for _, dev := range s.cfg.SmartLogger.Devices {
		if dev.Role != role {
			continue
		}
		f, _ := s.hostFresh(now, dev.Host)
		return f, true
	}
	return false, false
}

// hostFresh: (fresh, everSeen) for one SmartLogger host.
func (s *Service) hostFresh(now time.Time, host string) (bool, bool) {
	v, ok := s.devPollOK.Load(host)
	if !ok {
		return false, false
	}
	last := time.Unix(v.(int64), 0).UTC()
	return now.Sub(last) < 3*s.cfg.SmartLogger.PollInterval+2*time.Second, true
}

func tickF(t *Tick, key string) *float64 {
	if t == nil {
		return nil
	}
	if v, ok := t.Values[key]; ok {
		return f64ptr(v)
	}
	return nil
}

func tickInt(t *Tick, key string) (int, bool) {
	if t == nil {
		return 0, false
	}
	if v, ok := t.Values[key]; ok {
		return int(v), true
	}
	return 0, false
}
