package edge

import (
	"fmt"
	"math"
	"time"
)

// Power modes of the site (ems_offgrid_island_generator.md §4.2).
const (
	SupplyGrid       = "grid"
	SupplyGenerator  = "generator"
	SupplyIsland     = "island"
	SupplyBlackout   = "blackout"
	SupplyTransition = "transition"
	SupplyUnknown    = "unknown"
)

// Positions of the PQ / OFF / VSG switch on the Encombi cabinet.
const (
	SwitchPQ      = "pq"
	SwitchOff     = "off"
	SwitchVSG     = "vsg"
	SwitchUnknown = "unknown"
)

// Who drives the SmartLoggers, for the status line.
const (
	ControllerEMS     = "ems"
	ControllerEncombi = "encombi"
	ControllerNone    = "none"
)

// Inputs of 20500 (§4.1): bit N is input N, bit 0 is not used.
const (
	diT1      = 1
	diSection = 2
	diESS     = 3
	diT2      = 4
	diGen     = 5
	diRN1     = 6
	diRN2     = 7
	diPQ      = 8
	diVSG     = 9
	diRN3     = 10
)

// Tick keys of the Encombi reading; present only while it is fresh.
const (
	keyEncombiDI   = "encombi_di_bits"
	keyGenPowerKw  = "gen_meter_power_kw"
	keyGenAlive    = "gen_meter_alive"
	keyBusVoltageA = "gen_bus_voltage_a_v"
	keyBusVoltageB = "gen_bus_voltage_b_v"
	keyBusVoltageC = "gen_bus_voltage_c_v"
)

var busVoltageKeys = [3]string{keyBusVoltageA, keyBusVoltageB, keyBusVoltageC}

var supplyModeLabels = map[string]string{
	SupplyGrid:       "Мережа",
	SupplyGenerator:  "Генератор",
	SupplyIsland:     "Острів",
	SupplyBlackout:   "Знеструмлено",
	SupplyTransition: "Перехід",
	SupplyUnknown:    "Невідомо",
}

var switchLabels = map[string]string{
	SwitchPQ:      "PQ",
	SwitchOff:     "OFF",
	SwitchVSG:     "VSG",
	SwitchUnknown: "не читається",
}

var essModeLabels = map[string]string{
	"pq":      "PQ",
	"vsg":     "VSG",
	"mixed":   "частина PCS у VSG",
	"stopped": "зупинена",
	"unknown": "невідомо",
}

var controllerLabels = map[string]string{
	ControllerEMS:     "EMS (запис увімкнено)",
	ControllerEncombi: "Encombi",
	ControllerNone:    "ніхто",
}

// supplyInputs is what the power mode is decided from, read off a tick.
type supplyInputs struct {
	fresh      bool
	bits       uint16
	alive      bool
	genPowerKw *float64
	busV       *float64 // highest phase read
}

func supplyInputsOf(t Tick) supplyInputs {
	v, ok := t.Values[keyEncombiDI]
	if !ok {
		return supplyInputs{}
	}
	in := supplyInputs{fresh: true, bits: uint16(v), alive: t.Values[keyGenAlive] == 1}
	if p, ok := t.Values[keyGenPowerKw]; ok {
		in.genPowerKw = f64ptr(p)
	}
	for _, k := range busVoltageKeys {
		if u, ok := t.Values[k]; ok && (in.busV == nil || u > *in.busV) {
			in.busV = f64ptr(u)
		}
	}
	return in
}

func diOn(bits uint16, n int) bool { return bits>>n&1 == 1 }

// classifySupply is §4.2. The grid is on the bus only through a closed
// incomer whose relay sees voltage: the relays sit before 1QF and 2QF.
// The generator meter reads the bus, not the generator, so «the
// generator feeds the bus» is its breaker closed and the bus live.
func classifySupply(in supplyInputs, busLiveMinV float64) string {
	if !in.fresh || !in.alive {
		return SupplyUnknown
	}
	grid := (diOn(in.bits, diT1) && diOn(in.bits, diRN1)) || (diOn(in.bits, diT2) && diOn(in.bits, diRN2))
	busLive := in.busV != nil && *in.busV >= busLiveMinV
	gen := diOn(in.bits, diGen) && busLive
	switch {
	case grid && gen:
		return SupplyTransition
	case grid:
		return SupplyGrid
	case gen:
		return SupplyGenerator
	case busLive:
		return SupplyIsland
	default:
		return SupplyBlackout
	}
}

// switchPosition reads the cabinet switch off inputs 8 (PQ) and 9 (VSG).
func switchPosition(in supplyInputs) string {
	if !in.fresh {
		return SwitchUnknown
	}
	pq, vsg := diOn(in.bits, diPQ), diOn(in.bits, diVSG)
	switch {
	case pq && vsg:
		return SwitchUnknown
	case pq:
		return SwitchPQ
	case vsg:
		return SwitchVSG
	default:
		return SwitchOff
	}
}

// supplyTracker takes a new power mode once it has held for the
// debounce, and remembers since when the cabinet switch is where it is.
// Core loop only.
type supplyTracker struct {
	debounce time.Duration

	mode      string
	modeSince time.Time
	cand      string
	candSince time.Time
	// pendSince: since when the raw mode differs from the taken one.
	pendSince time.Time
	settling  bool

	sw      string
	swSince time.Time
}

// supplySettleExtra bounds how long a mode without the grid may stay
// pending past the debounce before the checks run again: a reading that
// flickers must not hold them off for good.
const supplySettleExtra = 3 * time.Second

func newSupplyTracker(debounce time.Duration) *supplyTracker {
	return &supplyTracker{debounce: debounce, mode: SupplyUnknown, sw: SwitchUnknown}
}

// observe feeds one tick's raw classification. changed reports a newly
// taken mode; swChanged a new switch position.
func (tr *supplyTracker) observe(now time.Time, raw, sw string) (changed, swChanged bool, prevMode, prevSw string) {
	if raw != tr.cand {
		tr.cand, tr.candSince = raw, now
	}
	prevMode = tr.mode
	if tr.cand != tr.mode && now.Sub(tr.candSince) >= tr.debounce {
		tr.mode, tr.modeSince, changed = tr.cand, tr.candSince, true
	}
	if tr.modeSince.IsZero() {
		tr.modeSince = now
	}
	switch {
	case raw == tr.mode:
		tr.pendSince = time.Time{}
	case tr.pendSince.IsZero():
		tr.pendSince = now
	}
	tr.settling = offGridMode(raw) && raw != tr.mode && now.Sub(tr.pendSince) < tr.debounce+supplySettleExtra
	prevSw = tr.sw
	if sw != tr.sw {
		tr.sw, tr.swSince, swChanged = sw, now, true
	}
	if tr.swSince.IsZero() {
		tr.swSince = now
	}
	return changed, swChanged, prevMode, prevSw
}

// SupplyState is the taken power mode for the controller and consoles.
type SupplyState struct {
	Mode        string
	ModeSince   time.Time
	Switch      string
	SwitchSince time.Time
	// Settling: the readings show a mode without the grid that is not
	// taken yet. The PCS trips the moment the grid goes, well inside
	// the debounce.
	Settling bool
	// DataAge is how old the last Encombi reading is.
	DataAge     time.Duration
	Bits        uint16
	Fresh       bool
	GenPowerKw  *float64
	BusVoltageV *float64
}

func (tr *supplyTracker) state(in supplyInputs) SupplyState {
	return SupplyState{
		Mode: tr.mode, ModeSince: tr.modeSince, Switch: tr.sw, SwitchSince: tr.swSince, Settling: tr.settling,
		Bits: in.bits, Fresh: in.fresh, GenPowerKw: in.genPowerKw, BusVoltageV: in.busV,
	}
}

// SwitchHeld reports the switch at pos for at least d.
func (st SupplyState) SwitchHeld(now time.Time, pos string, d time.Duration) bool {
	return st.Switch == pos && now.Sub(st.SwitchSince) >= d
}

// Breaker reports one input of 20500 on fresh data.
func (st SupplyState) Breaker(n int) bool { return st.Fresh && diOn(st.Bits, n) }

// SupplyStatus is the status line of both consoles (§4.3).
type SupplyStatus struct {
	Mode            string          `json:"mode"`
	ModeLabel       string          `json:"mode_label"`
	ModeSince       time.Time       `json:"mode_since"`
	Switch          string          `json:"switch"`
	SwitchLabel     string          `json:"switch_label"`
	ESSMode         string          `json:"ess_mode"`
	ESSModeLabel    string          `json:"ess_mode_label"`
	Controller      string          `json:"controller"`
	ControllerLabel string          `json:"controller_label"`
	Restriction     string          `json:"restriction,omitempty"`
	DIBits          *int            `json:"di_bits,omitempty"`
	Inputs          map[string]bool `json:"inputs,omitempty"`
	GenPowerKw      *float64        `json:"gen_power_kw,omitempty"`
	GenLoadPct      *float64        `json:"gen_load_pct,omitempty"`
	GenMinPct       float64         `json:"gen_min_pct"`
	GenBestPct      [2]float64      `json:"gen_best_pct"`
	BusVoltageV     *float64        `json:"bus_voltage_v,omitempty"`
	SocPercent      *float64        `json:"soc_percent,omitempty"`
	SocFloorPct     float64         `json:"soc_floor_pct"`
	LoadKw          *float64        `json:"load_kw,omitempty"`
	// AutonomyMin: minutes to the floor at the current load less the sun.
	AutonomyMin *float64 `json:"autonomy_min,omitempty"`
	// Actions is what the cloud's commands and the buttons may do now.
	Actions *Availability `json:"actions,omitempty"`
}

var inputNames = map[int]string{
	diT1: "t1", diSection: "section", diESS: "ess", diT2: "t2", diGen: "gen",
	diRN1: "rn1", diRN2: "rn2", diPQ: "pq", diVSG: "vsg", diRN3: "rn3",
}

// essModeOf is the battery for the status line: stopped (40540), else
// 44364.
func essModeOf(t Tick) string {
	if essStoppedOn(t) {
		return "stopped"
	}
	return pcsModeName(t)
}

// offgridLoadKw is the site load without the grid meter: the generator,
// the sun and the battery's discharge feed it. On the grid it is the
// tick's own load.
func offgridLoadKw(mode string, t Tick, genKw *float64) *float64 {
	switch mode {
	case SupplyGenerator, SupplyIsland:
		if t.PVPowerKw == nil || t.ESSPowerKw == nil {
			return nil
		}
		load := *t.PVPowerKw + *t.ESSPowerKw
		if mode == SupplyGenerator {
			if genKw == nil {
				return nil
			}
			load += *genKw
		}
		return f64ptr(round1(math.Max(0, load)))
	}
	return t.LoadPowerKw
}

// autonomyMin is how long the battery lasts down to the floor at the
// given net load; nil when it is not draining.
func autonomyMin(socPct *float64, floorPct, capacityKwh float64, netKw float64) *float64 {
	if socPct == nil || capacityKwh <= 0 || netKw <= 0 || *socPct <= floorPct {
		return nil
	}
	return f64ptr(math.Round((*socPct - floorPct) / 100 * capacityKwh / netKw * 60))
}

// buildSupplyStatus fills the status line; restriction comes from the
// availability of actions.
func buildSupplyStatus(st SupplyState, t Tick, controller string, cfg *Config, capacityKwh float64) *SupplyStatus {
	og := cfg.Offgrid
	out := &SupplyStatus{
		Mode: st.Mode, ModeLabel: supplyModeLabels[st.Mode], ModeSince: st.ModeSince,
		Switch: st.Switch, SwitchLabel: switchLabels[st.Switch],
		Controller: controller, ControllerLabel: controllerLabels[controller],
		GenMinPct:   math.Round(og.GeneratorMinKw / og.GeneratorRatedKw * 100),
		GenBestPct:  [2]float64{60, 70},
		SocPercent:  t.SocPercent,
		SocFloorPct: og.IslandSocFloorPct,
		BusVoltageV: st.BusVoltageV,
		GenPowerKw:  st.GenPowerKw,
	}
	out.ESSMode = essModeOf(t)
	out.ESSModeLabel = essModeLabels[out.ESSMode]
	if st.Fresh {
		bits := int(st.Bits)
		out.DIBits = &bits
		out.Inputs = map[string]bool{}
		for n, name := range inputNames {
			out.Inputs[name] = diOn(st.Bits, n)
		}
	}
	if st.GenPowerKw != nil && og.GeneratorRatedKw > 0 {
		out.GenLoadPct = f64ptr(math.Round(*st.GenPowerKw / og.GeneratorRatedKw * 100))
	}
	out.LoadKw = offgridLoadKw(st.Mode, t, st.GenPowerKw)
	if out.LoadKw != nil {
		net := *out.LoadKw
		if t.PVPowerKw != nil {
			net -= *t.PVPowerKw
		}
		out.AutonomyMin = autonomyMin(t.SocPercent, og.IslandSocFloorPct, capacityKwh, net)
	}
	if st.Mode == SupplyUnknown {
		out.Restriction = "немає свіжих даних Encombi"
	}
	return out
}

// supplyEvent journals a newly taken power mode.
func supplyEvent(now time.Time, prev, mode string, st SupplyState) Event {
	sev := SevWarning
	if mode == SupplyGrid {
		sev = SevInfo
	}
	msg := fmt.Sprintf("режим живлення: %s → %s", supplyModeLabels[prev], supplyModeLabels[mode])
	ctx := map[string]any{"from": prev, "to": mode}
	if st.Fresh {
		ctx["di_bits"] = fmt.Sprintf("0x%04X", st.Bits)
	}
	if st.BusVoltageV != nil {
		ctx["bus_voltage_v"] = *st.BusVoltageV
	}
	return Event{TS: now, Severity: sev, Code: EvSupplyMode, Message: msg, Context: ctx}
}

func switchEvent(now time.Time, prev, sw string) Event {
	return Event{
		TS: now, Severity: SevInfo, Code: EvCabinetSwitch,
		Message: fmt.Sprintf("перемикач на шафі: %s → %s", switchLabels[prev], switchLabels[sw]),
		Context: map[string]any{"from": prev, "to": sw},
	}
}

// Power-mode events.
const (
	EvSupplyMode    = "SUPPLY_MODE_CHANGED"
	EvCabinetSwitch = "CABINET_SWITCH_CHANGED"
)
