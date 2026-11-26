package edge

// Avail says whether an action is open now and, if not, why.
type Avail struct {
	OK     bool   `json:"ok"`
	Reason string `json:"reason,omitempty"`
}

var open = Avail{OK: true}

// Console actions besides the one-shot buttons.
const ActWriteEnable = "write_enable"

// EvManualUnavailable: a manual block came in the manifest but its type
// is closed in this power mode (§4.3 п. 5); the edge holds 0 instead.
const EvManualUnavailable = "MANUAL_COMMAND_UNAVAILABLE"

// Availability is ems_offgrid_island_generator.md §4.3 for one tick:
// what the cloud's commands and the console's buttons may do in this
// power mode and switch position. A closed action stays on screen,
// grey, with its reason. Stops are always open.
type Availability struct {
	Restriction string `json:"restriction,omitempty"`
	// Power: the battery may take a non-zero command at all.
	Power   Avail            `json:"power"`
	Plan    Avail            `json:"plan"`
	Manual  map[string]Avail `json:"manual"`
	Buttons map[string]Avail `json:"buttons"`
}

var manualTypes = []string{ManualHold, ManualFixed, ManualCover, ManualSolar, ManualTarget, ManualExport, ManualCap}

// ManualOK reports whether a manual block of this type may run now.
func (a *Availability) ManualOK(typ string) Avail {
	if a == nil {
		return open
	}
	if v, ok := a.Manual[typ]; ok {
		return v
	}
	return open
}

// Button reports whether a console button may be pressed now.
func (a *Availability) Button(id string) Avail {
	if a == nil {
		return open
	}
	if v, ok := a.Buttons[id]; ok {
		return v
	}
	return open
}

func allOpen() *Availability {
	a := &Availability{Power: open, Plan: open, Manual: map[string]Avail{}, Buttons: map[string]Avail{ActWriteEnable: open}}
	for _, t := range manualTypes {
		a.Manual[t] = open
	}
	for id := range oneShots {
		if !internalOneShots[id] {
			a.Buttons[id] = open
		}
	}
	return a
}

// availabilityFor is the §4.3 table. Only stops stay open where an
// action is closed; nothing here starts anything.
func availabilityFor(st SupplyState) *Availability {
	a := allOpen()
	closeAll := func(reason string) {
		a.Restriction = reason
		a.Power, a.Plan = Avail{Reason: reason}, Avail{Reason: reason}
		for _, t := range manualTypes {
			a.Manual[t] = Avail{Reason: reason}
		}
	}
	closeStarts := func(reason string, keepPV bool) {
		for id, c := range oneShots {
			if !c.Start || internalOneShots[id] || (keepPV && c.Target == "pv") {
				continue
			}
			a.Buttons[id] = Avail{Reason: reason}
		}
	}
	gridPQ := st.Mode == SupplyGrid && st.Switch == SwitchPQ
	if !gridPQ {
		a.Buttons[ActWriteEnable] = Avail{Reason: "запис вмикається лише від мережі з перемикачем у PQ"}
	}
	switch st.Mode {
	case SupplyGrid:
		switch st.Switch {
		case SwitchPQ:
		case SwitchVSG:
			closeAll("перемикач у VSG при наявній мережі, поверніть у PQ")
			closeStarts(a.Restriction, false)
		case SwitchOff:
			closeAll("перемикач в OFF")
			closeStarts(a.Restriction, true)
		default:
			closeAll("перемикач на шафі не читається")
			closeStarts(a.Restriction, false)
		}
	case SupplyBlackout:
		switch st.Switch {
		case SwitchVSG:
			closeAll("острів запускається з локальної консолі")
		case SwitchOff:
			closeAll("немає живлення, перемикач в OFF")
		default:
			closeAll("немає живлення; запустіть генератор або переведіть перемикач у VSG")
		}
		closeStarts(a.Restriction, false)
	case SupplyIsland:
		if st.Switch == SwitchVSG {
			closeAll("в острові потужність УЗЕ не задається")
		} else {
			closeAll("перемикач не у VSG, острів зупиняється")
		}
		closeStarts(a.Restriction, false)
	case SupplyGenerator:
		switch st.Switch {
		case SwitchPQ:
			closeAll("робота від генератора")
			a.Manual[ManualHold], a.Manual[ManualTarget] = open, open
			closeStarts("від генератора УЗЕ запускається кнопкою «Пуск УЗЕ з генератором»", false)
		case SwitchVSG:
			closeAll("УЗЕ у VSG не працює разом із генератором, переведіть у PQ")
			closeStarts(a.Restriction, false)
		default:
			closeAll("робота від генератора, перемикач не в PQ")
			closeStarts(a.Restriction, false)
		}
	case SupplyTransition:
		closeAll("перехід живлення: мережа і генератор разом")
		closeStarts(a.Restriction, false)
	default:
		closeAll("немає свіжих даних Encombi")
		closeStarts(a.Restriction, false)
	}
	return a
}
