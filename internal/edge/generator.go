package edge

import (
	"fmt"
	"math"
	"time"
)

// Work with the generator (ems_offgrid_island_generator.md §6): the
// battery in PQ, the generator holds the bus. The battery charges so the
// generator rises to its target; it discharges only on what goes over
// the generator's rating. The edge never starts or stops the generator.

// Generator events.
const (
	EvGenBelowMin     = "GEN_BELOW_MIN"
	EvGenReversePower = "GEN_REVERSE_POWER"
	EvGenDataStale    = "GEN_DATA_STALE"
)

// genState is the generator control's memory; reset on each start.
type genState struct {
	last         float64
	reverseSince time.Time
}

// genCommand is one second of §6: the battery command and the PV ceiling.
func (c *Controller) genCommand(in ControlInputs, cmd *WriteCommand) []Event {
	t, og, st, now := in.Tick, c.og, in.Supply, in.Now
	note := func(s string) { cmd.Clamps = append(cmd.Clamps, s) }
	var events []Event
	cmd.PVLimitKw, cmd.PVKnown = c.pvCeiling(in, &cmd.Clamps)
	last := c.gen.last

	if st.Mode == SupplyTransition {
		note("перехід живлення: мережа і генератор разом — УЗЕ 0")
		c.gen.last, cmd.ESSKw = 0, 0
		return nil
	}
	if !st.Fresh || st.GenPowerKw == nil {
		// Without the generator meter reverse power is not seen.
		ess := math.Min(last, 0)
		msg := "дані Encombi застаріли — розряд 0, заряд тримається"
		if st.DataAge >= og.GeneratorChargeHold {
			ess, msg = 0, fmt.Sprintf("дані Encombi старші за %.0f с — заряд теж 0", og.GeneratorChargeHold.Seconds())
		}
		note(msg)
		c.gen.reverseSince = time.Time{}
		c.gen.last, cmd.ESSKw = ess, ess
		return []Event{{TS: now, Severity: SevAlarm, Code: EvGenDataStale, Message: "робота з генератором: " + msg}}
	}

	p := *st.GenPowerKw
	ess := 0.0
	if t.ESSPowerKw != nil {
		ess = *t.ESSPowerKw
	}
	socMax := c.cfg.SocMaxPct
	hold := false
	switch in.Decision.ManualType {
	case ManualHold:
		hold = true
	case ManualTarget:
		// «Заряд до рівня»: the charge stops at the block's level.
		if v := in.Decision.ManualTargetPct; v > 0 && v < socMax {
			socMax = v
		}
	}

	var want float64
	switch {
	case hold:
		note("ручна команда: пауза — УЗЕ 0")
	case p > og.GeneratorRatedKw:
		want = ess + (p - og.GeneratorRatedKw)
		note(fmt.Sprintf("генератор %.0f кВт понад номінал %.0f — розряд на перевищення", p, og.GeneratorRatedKw))
	case math.Abs(p-og.GeneratorTargetKw) < og.GeneratorDeadbandKw:
		want = math.Min(last, 0)
	default:
		want = math.Min(0, ess+(p-og.GeneratorTargetKw))
	}

	full := false
	switch soc := t.SocPercent; {
	case soc == nil:
		want = 0
		note("SOC невідомий — 0")
	case want < 0 && *soc >= socMax:
		want, full = 0, true
		note(fmt.Sprintf("заряд %.1f%% ≥ %.0f%% — заряд 0", *soc, socMax))
	case want > 0 && *soc <= c.cfg.SocMinPct:
		want = 0
		note(fmt.Sprintf("SOC %.1f%% ≤ %.0f%% — розряд заборонено", *soc, c.cfg.SocMinPct))
	}
	if t.SocPercent != nil && *t.SocPercent >= socMax {
		full = true
	}

	if p <= 0 {
		if c.gen.reverseSince.IsZero() {
			c.gen.reverseSince = now
		}
	} else {
		c.gen.reverseSince = time.Time{}
	}
	reverse := !c.gen.reverseSince.IsZero() && now.Sub(c.gen.reverseSince) >= og.GeneratorReverseAfter
	if reverse {
		want = math.Min(want, 0)
		note("зворотна потужність генератора — розряд 0, стеля СЕС донизу")
		events = append(events, Event{TS: now, Severity: SevAlarm, Code: EvGenReversePower,
			Message: fmt.Sprintf("зворотна потужність генератора: %.0f кВт — розряд 0, стеля СЕС донизу", p)})
	}

	// A charge or discharge grows by a step per second; anything toward
	// 0 is taken at once.
	if math.Abs(want) > math.Abs(last) && math.Signbit(want) == math.Signbit(last) || last == 0 {
		if d := want - last; math.Abs(d) > og.GeneratorStepKw {
			want = last + math.Copysign(og.GeneratorStepKw, d)
		}
	}
	if want > 0 && t.ESSDischargeMaxKw != nil && want > *t.ESSDischargeMaxKw {
		want = *t.ESSDischargeMaxKw
		note(fmt.Sprintf("доступний розряд %.0f кВт (40492)", want))
	}
	if want < 0 && t.ESSChargeMaxKw != nil && -want > *t.ESSChargeMaxKw {
		want = -*t.ESSChargeMaxKw
		note(fmt.Sprintf("доступний заряд %.0f кВт (40490)", -want))
	}
	if !c.firstConfirmed && math.Abs(want) > c.cfg.FirstCommandMaxKw {
		want = math.Copysign(c.cfg.FirstCommandMaxKw, want)
		note(fmt.Sprintf("перша ненульова команда ≤ %.0f кВт", c.cfg.FirstCommandMaxKw))
	}

	// A full battery (or a pause) cannot lift the generator: the sun
	// gives way until the generator is back at its minimum.
	if (full || hold || reverse) && p < og.GeneratorMinKw && cmd.PVKnown {
		pv := 0.0
		if t.PVPowerKw != nil {
			pv = *t.PVPowerKw
		}
		if ceiling := math.Max(0, pv+(p-og.GeneratorMinKw)); ceiling < cmd.PVLimitKw {
			cmd.PVLimitKw = ceiling
			note(fmt.Sprintf("генератор %.0f кВт нижче мінімуму %.0f — стеля СЕС %.0f кВт", p, og.GeneratorMinKw, ceiling))
		}
		if pv < pvRunningKw && !reverse {
			events = append(events, Event{TS: now, Severity: SevWarning, Code: EvGenBelowMin,
				Message: fmt.Sprintf("батарея повна, генератор нижче мінімуму: %.0f кВт < %.0f", p, og.GeneratorMinKw)})
		}
	}
	c.gen.last, cmd.ESSKw = want, want
	return events
}
