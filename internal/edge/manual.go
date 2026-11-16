package edge

import (
	"fmt"
	"math"
	"time"
)

// Manual command types: the desk's operator intents
// (internal/dispatch CommandType, ems_manual_control_mvp.md §1).
const (
	ManualHold   = "hold"   // УЗЕ 0 кВт
	ManualFixed  = "fixed"  // потужність УЗЕ: Value кВт, Direction charge|discharge
	ManualCover  = "cover"  // покривати споживання, розряд до Value кВт
	ManualSolar  = "solar"  // заряд надлишком СЕС до Value кВт
	ManualTarget = "target" // SOC Value % до кінця блоку
	ManualExport = "export" // відпуск у точці приєднання Value кВт
	ManualCap    = "cap"    // автомат, імпорт не вище Value кВт
)

// ManualCommand is one operator block delivered in the manifest
// (spec-remarks manifest_manual_commands.md §1). The edge carries out
// its intent every second from the live readings, through the same
// clamps as the plan; while a block is in force the plan is not used.
type ManualCommand struct {
	ID          string    `json:"command_id"`
	Type        string    `json:"type"`
	Value       float64   `json:"value,omitempty"`
	Direction   string    `json:"direction,omitempty"`
	From        time.Time `json:"from"`
	Until       time.Time `json:"until"`
	ConfirmedBy string    `json:"confirmed_by,omitempty"`
	ConfirmedAt time.Time `json:"confirmed_at,omitempty"`
}

func (c ManualCommand) activeAt(t time.Time) bool {
	return !t.Before(c.From) && t.Before(c.Until)
}

func (c ManualCommand) validate() error {
	switch c.Type {
	case ManualHold, ManualCover, ManualSolar, ManualTarget, ManualExport, ManualCap:
	case ManualFixed:
		if c.Direction != "charge" && c.Direction != "discharge" {
			return fmt.Errorf("manual command %s: fixed needs direction charge|discharge, got %q", c.ID, c.Direction)
		}
	default:
		return fmt.Errorf("manual command %s: unknown type %q", c.ID, c.Type)
	}
	if !c.From.Before(c.Until) {
		return fmt.Errorf("manual command %s: from %s is not before until %s", c.ID, c.From, c.Until)
	}
	if c.Value < 0 || math.IsNaN(c.Value) || math.IsInf(c.Value, 0) {
		return fmt.Errorf("manual command %s: value %v out of range", c.ID, c.Value)
	}
	if c.Type == ManualTarget && c.Value > 100 {
		return fmt.Errorf("manual command %s: SOC target %v%% out of range", c.ID, c.Value)
	}
	return nil
}

// ManualAt returns the manual block in force at t, or nil. Overlaps are
// rejected at validation, so at most one matches.
func (m *Manifest) ManualAt(t time.Time) *ManualCommand {
	if m == nil {
		return nil
	}
	for i := range m.ManualCommands {
		if m.ManualCommands[i].activeAt(t) {
			return &m.ManualCommands[i]
		}
	}
	return nil
}

func validateManualCommands(cmds []ManualCommand) error {
	for i, c := range cmds {
		if err := c.validate(); err != nil {
			return err
		}
		for _, o := range cmds[i+1:] {
			if c.From.Before(o.Until) && o.From.Before(c.Until) {
				return fmt.Errorf("manual commands %s and %s overlap", c.ID, o.ID)
			}
		}
	}
	return nil
}

// manualSetpoint turns a block into this second's command. cap returns
// false: it is the automat with an import limit, not a setpoint.
func manualSetpoint(t Tick, c ManualCommand, capacityKwh float64) (setpoint, bool) {
	s := setpoint{reason: "manual_" + c.Type}
	pv, load := t.PVPowerKw, t.LoadPowerKw
	switch c.Type {
	case ManualHold:
		s.rationale = "ручна команда: пауза, УЗЕ 0 кВт"
	case ManualFixed:
		s.kw = c.Value
		if c.Direction == "charge" {
			s.kw = -c.Value
		}
		s.rationale = fmt.Sprintf("ручна команда: потужність УЗЕ %.1f кВт", s.kw)
	case ManualCover:
		if pv == nil || load == nil {
			s.rationale = "ручна команда «покрити споживання»: немає pv/load — 0"
			break
		}
		s.kw = math.Min(c.Value, math.Max(0, *load-*pv))
		s.rationale = fmt.Sprintf("ручна команда: покрити споживання, до %.0f кВт", c.Value)
	case ManualSolar:
		if pv == nil || load == nil {
			s.rationale = "ручна команда «заряд від СЕС»: немає pv/load — 0"
			break
		}
		s.kw = -math.Min(c.Value, math.Max(0, *pv-*load))
		s.rationale = fmt.Sprintf("ручна команда: заряд надлишком СЕС, до %.0f кВт", c.Value)
	case ManualTarget:
		left := c.Until.Sub(t.TS).Hours()
		switch {
		case t.SocPercent == nil || capacityKwh <= 0:
			s.rationale = "ручна команда «ціль SOC»: немає SOC або ємності — 0"
		case *t.SocPercent >= c.Value:
			s.rationale = fmt.Sprintf("ручна команда: ціль SOC %.0f%% досягнута", c.Value)
		case left <= 0:
			s.rationale = "ручна команда «ціль SOC»: блок закінчився"
		default:
			missing := (c.Value - *t.SocPercent) / 100 * capacityKwh
			s.kw = -missing / left
			s.rationale = fmt.Sprintf("ручна команда: заряд до SOC %.0f%% (бракує %.0f кВт·год)", c.Value, missing)
		}
	case ManualExport:
		if pv == nil || load == nil {
			s.rationale = "ручна команда «відпуск у мережу»: немає pv/load — 0"
			break
		}
		s.kw = *load - *pv + c.Value
		s.rationale = fmt.Sprintf("ручна команда: відпуск у точці приєднання %.0f кВт", c.Value)
	case ManualCap:
		return setpoint{}, false
	}
	return s, true
}

// clampImportCap keeps a charging command from pushing the PCC import
// above capKw (the «cap» block); it never adds discharge.
func clampImportCap(t Tick, p, capKw float64, note func(string)) float64 {
	if p >= 0 || capKw <= 0 || t.GridPowerKw == nil || t.ESSPowerKw == nil {
		return p
	}
	// Charging harder by Δ raises the import by Δ.
	predicted := *t.GridPowerKw + (*t.ESSPowerKw - p)
	if predicted <= capKw {
		return p
	}
	p += predicted - capKw
	if p > 0 {
		p = 0
	}
	note(fmt.Sprintf("ліміт імпорту ручної команди %.0f кВт", capKw))
	return p
}
