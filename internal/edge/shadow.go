package edge

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// deadbandKw absorbs SmartLogger power noise so the virtual command
// does not chatter around zero.
const deadbandKw = 2.0

// Decision is one shadow-engine verdict for one tick: what the EMS
// would have written to the SmartLogger, plus why. Nothing is ever
// written in this build — the outputs are logged and uplinked only.
type Decision struct {
	TS           time.Time
	Mode         Mode
	Preset       string
	StateMachine string
	PlanSource   string // manifest | fallback | config
	DataQuality  string

	// Inputs snapshot.
	SocPercent  *float64
	PVPowerKw   *float64
	ESSPowerKw  *float64
	GridPowerKw *float64
	LoadPowerKw *float64
	PBessPlanKw *float64

	// Outputs (virtual commands).
	PBessVirtualKw    float64 // + discharge / − charge, kW
	PPVLimitVirtualKw float64 // PV active power limit, kW

	ReasonCode string
	Rationale  string

	Degraded bool
	Anomaly  bool
}

// Record renders the canonical MVP §9.3 control record — the exact
// document persisted in the black box and shipped as control_records[].
func (d Decision) Record(siteID string) map[string]any {
	inputs := map[string]any{"data_quality": d.DataQuality}
	putF := func(m map[string]any, k string, v *float64) {
		if v != nil {
			m[k] = round1(*v)
		}
	}
	putF(inputs, "soc_percent", d.SocPercent)
	putF(inputs, "pv_power_kw", d.PVPowerKw)
	putF(inputs, "ess_power_kw", d.ESSPowerKw)
	putF(inputs, "grid_power_kw", d.GridPowerKw)
	putF(inputs, "load_power_kw", d.LoadPowerKw)
	putF(inputs, "p_bess_plan_kw", d.PBessPlanKw)

	return map[string]any{
		"site_id":       siteID,
		"ts":            d.TS.UTC().Format(time.RFC3339),
		"mode":          string(d.Mode),
		"preset":        d.Preset,
		"state_machine": d.StateMachine,
		"plan_source":   d.PlanSource,
		"inputs":        inputs,
		"outputs": map[string]any{
			"p_bess_virtual_kw":     round1(d.PBessVirtualKw),
			"p_pv_limit_virtual_kw": round1(d.PPVLimitVirtualKw),
			"would_write_40381":     round1(d.PBessVirtualKw),
			"would_write_40378":     round1(d.PPVLimitVirtualKw),
		},
		"reason_code": d.ReasonCode,
		"rationale":   d.Rationale,
	}
}

// engineParams are the effective control parameters after merging the
// site config with the active manifest (manifest wins where set).
type engineParams struct {
	preset         string
	planSource     string
	plan           *Plan
	socMinPct      float64
	socMaxPct      float64
	ratedPowerKw   float64
	pvRatedKw      float64
	targetImportKw float64
}

func resolveParams(now time.Time, m *Manifest, cfg *Config) engineParams {
	p := engineParams{
		preset:         cfg.Control.Preset,
		planSource:     "config",
		socMinPct:      cfg.Limits.Bess.SocMinEconomicPct,
		socMaxPct:      cfg.Limits.Bess.SocMaxEconomicPct,
		ratedPowerKw:   cfg.Limits.Bess.RatedPowerKw,
		pvRatedKw:      cfg.Limits.PV.RatedKw,
		targetImportKw: cfg.Limits.Grid.TargetImportKw,
	}
	if m == nil {
		return p
	}
	if !m.ActiveAt(now) {
		// Expired manifest → safe fallback (spec: self_consumption_safe,
		// no arbitrage, no grid charge). Site limits stay in force.
		p.preset = FallbackPreset
		p.planSource = "fallback"
		return p
	}
	p.planSource = "manifest"
	if m.Preset != "" {
		p.preset = m.Preset
	}
	p.plan = m.Plan
	if m.SocPolicy.MinEconomicPct > 0 {
		p.socMinPct = m.SocPolicy.MinEconomicPct
	}
	if m.SocPolicy.MaxEconomicPct > 0 {
		p.socMaxPct = m.SocPolicy.MaxEconomicPct
	}
	if m.GridLimits.PvRatedKw > 0 {
		p.pvRatedKw = m.GridLimits.PvRatedKw
	}
	if m.GridLimits.TargetImportKw > 0 {
		p.targetImportKw = m.GridLimits.TargetImportKw
	}
	return p
}

// Decide computes the virtual dispatch for one tick. Pure function of
// (tick, manifest, config): replay and live shadow share it verbatim.
// Returned events (anomaly/degradation) still need service-side
// deduplication before hitting the black box.
func Decide(t Tick, m *Manifest, cfg *Config) (Decision, []Event) {
	params := resolveParams(t.TS, m, cfg)

	d := Decision{
		TS:           t.TS,
		Mode:         cfg.Control.Mode,
		Preset:       params.preset,
		StateMachine: "ADVISOR",
		PlanSource:   params.planSource,
		DataQuality:  t.DataQuality,
		SocPercent:   t.SocPercent,
		PVPowerKw:    t.PVPowerKw,
		ESSPowerKw:   t.ESSPowerKw,
		GridPowerKw:  t.GridPowerKw,
		LoadPowerKw:  t.LoadPowerKw,
	}
	var events []Event

	// Desired power before safety clamps.
	desired := 0.0
	switch {
	case t.DataQuality == QualityFault:
		d.ReasonCode = "data_fault"
		d.Rationale = "телеметрія несвіжа або відсутня — утримання 0"
		d.Degraded = true
	case t.PCSShutdown != nil && *t.PCSShutdown:
		d.ReasonCode = "pcs_shutdown"
		d.Rationale = "PCS вимкнено (40540) — команда УЗЕ заблокована"
		d.Degraded = true
	default:
		desired = desiredPower(t, params, &d)
	}

	if params.ratedPowerKw > 0 && math.Abs(desired) > params.ratedPowerKw*1.5 {
		d.Anomaly = true
		events = append(events, Event{
			TS: t.TS, Severity: SevWarning, Code: EvShadowAnomaly,
			Message: fmt.Sprintf("virtual command %.1f kW exceeds 1.5x rated %.1f kW", desired, params.ratedPowerKw),
			Context: map[string]any{"reason_code": d.ReasonCode},
		})
	}

	// Safety clamps; each applied clamp is recorded.
	var clamps []string
	desired = clampBess(t, params, desired, &clamps)

	d.PBessVirtualKw = round1(desired)
	d.PPVLimitVirtualKw = round1(params.pvRatedKw)

	if len(clamps) > 0 {
		d.Degraded = true
		d.Rationale = d.Rationale + "; обмежено: " + strings.Join(clamps, ", ")
	}
	if d.Degraded && d.ReasonCode != "data_fault" && d.ReasonCode != "pcs_shutdown" {
		events = append(events, Event{
			TS: t.TS, Severity: SevWarning, Code: EvDispatchDegrade,
			Message: "virtual dispatch clamped: " + strings.Join(clamps, ", "),
			Context: map[string]any{"reason_code": d.ReasonCode},
		})
	}
	return d, events
}

// desiredPower picks the economic target (level A plan or
// self-consumption rules) before clamping.
func desiredPower(t Tick, params engineParams, d *Decision) float64 {
	if params.preset == PresetEconomicArbitrage {
		if iv := params.plan.IntervalAt(t.TS); iv != nil {
			v := iv.EssKw
			d.PBessPlanKw = &v
			switch {
			case v > deadbandKw:
				d.ReasonCode = "plan_discharge"
				d.Rationale = planRationale("розряд за планом", iv)
			case v < -deadbandKw:
				d.ReasonCode = "plan_charge"
				d.Rationale = planRationale("заряд за планом", iv)
			default:
				d.ReasonCode = "plan_hold"
				d.Rationale = "план: утримання"
			}
			return v
		}
		// Arbitrage without a plan interval degrades to self-consumption.
		v := selfConsumptionPower(t, d)
		d.ReasonCode = "no_plan_" + d.ReasonCode
		d.Rationale = "план відсутній — " + d.Rationale
		return v
	}
	return selfConsumptionPower(t, d)
}

func planRationale(action string, iv *PlanInterval) string {
	if iv.PriceUah > 0 {
		return fmt.Sprintf("%s (%.1f кВт, РДН %.2f грн/кВт·год)", action, iv.EssKw, iv.PriceUah)
	}
	return fmt.Sprintf("%s (%.1f кВт)", action, iv.EssKw)
}

// selfConsumptionPower implements the self_consumption preset: charge
// from PV surplus, discharge into the local deficit, never trade.
func selfConsumptionPower(t Tick, d *Decision) float64 {
	if t.PVPowerKw == nil || t.LoadPowerKw == nil {
		d.ReasonCode = "insufficient_data"
		d.Rationale = "немає pv/load — утримання 0"
		return 0
	}
	surplus := *t.PVPowerKw - *t.LoadPowerKw
	switch {
	case surplus > deadbandKw:
		d.ReasonCode = "self_charge"
		d.Rationale = fmt.Sprintf("заряд від надлишку СЕС (%.1f кВт)", surplus)
		return -surplus
	case surplus < -deadbandKw:
		d.ReasonCode = "self_discharge"
		d.Rationale = fmt.Sprintf("розряд на покриття навантаження (%.1f кВт)", -surplus)
		return -surplus
	default:
		d.ReasonCode = "hold"
		d.Rationale = "баланс у межах deadband — утримання"
		return 0
	}
}

// clampBess applies, in order: SOC policy, SmartLogger dynamic limits
// (40490/40492) or rated power, the no-export rule, the
// no-grid-charge rule for self-consumption presets, and the grid
// import target for plan-driven charging.
func clampBess(t Tick, params engineParams, p float64, clamps *[]string) float64 {
	note := func(s string) { *clamps = append(*clamps, s) }

	if p == 0 {
		return 0
	}

	// SOC policy.
	if t.SocPercent == nil {
		note("SOC невідомий — команда 0")
		return 0
	}
	soc := *t.SocPercent
	if p > 0 && soc <= params.socMinPct {
		note(fmt.Sprintf("SOC %.1f%% ≤ min %.0f%% — розряд заборонено", soc, params.socMinPct))
		return 0
	}
	if p < 0 && soc >= params.socMaxPct {
		note(fmt.Sprintf("SOC %.1f%% ≥ max %.0f%% — заряд заборонено", soc, params.socMaxPct))
		return 0
	}

	// Dynamic SmartLogger limits win over the passport rating.
	dischargeCap := params.ratedPowerKw
	if t.ESSDischargeMaxKw != nil {
		dischargeCap = *t.ESSDischargeMaxKw
	}
	chargeCap := params.ratedPowerKw
	if t.ESSChargeMaxKw != nil {
		chargeCap = *t.ESSChargeMaxKw
	}
	if p > 0 && dischargeCap > 0 && p > dischargeCap {
		note(fmt.Sprintf("ліміт розряду %.0f кВт (40492)", dischargeCap))
		p = dischargeCap
	}
	if p < 0 && chargeCap > 0 && -p > chargeCap {
		note(fmt.Sprintf("ліміт заряду %.0f кВт (40490)", chargeCap))
		p = -chargeCap
	}

	pvKnown := t.PVPowerKw != nil && t.LoadPowerKw != nil

	// No export: discharge must not exceed the local deficit.
	if p > 0 && pvKnown {
		maxNoExport := *t.LoadPowerKw - *t.PVPowerKw
		if maxNoExport < 0 {
			maxNoExport = 0
		}
		if p > maxNoExport {
			note("без експорту: розряд обрізано до дефіциту")
			p = maxNoExport
		}
	}

	if p < 0 && pvKnown {
		switch params.preset {
		case PresetEconomicArbitrage:
			// Grid charging is allowed (the plan gated it by price), but
			// the import target still caps it.
			if params.targetImportKw > 0 {
				headroom := params.targetImportKw - (*t.LoadPowerKw - *t.PVPowerKw)
				if headroom < 0 {
					headroom = 0
				}
				if -p > headroom {
					note(fmt.Sprintf("ліміт імпорту %.0f кВт", params.targetImportKw))
					p = -headroom
				}
			}
		default:
			// Self-consumption presets charge from PV surplus only.
			surplus := *t.PVPowerKw - *t.LoadPowerKw
			if surplus < 0 {
				surplus = 0
			}
			if -p > surplus {
				note("заряд лише від надлишку СЕС")
				p = -surplus
			}
		}
	}
	return p
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }
