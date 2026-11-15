package economics

import (
	"context"
	"fmt"
	"math"
	"time"
)

// Generator economics (ems-spec generator handoff §4.3–4.5). A day of an
// organization counts the generator only with generator accounting:
// fuel priced in the day's tariff and a generator of its own (not shared
// by a group's sites). Without it every hour is computed exactly as
// before and only GenKwh and the supply minutes are kept for reference.

// SupplyMinutes counts an hour's minutes by supply mode (handoff §4.1);
// grid minutes are not counted.
type SupplyMinutes struct {
	Generator  int
	Island     int
	Blackout   int
	Transition int
	Unknown    int
}

// Offgrid is the minutes without the grid.
func (m SupplyMinutes) Offgrid() int { return m.Generator + m.Island + m.Blackout }

func (m *SupplyMinutes) add(o SupplyMinutes) {
	m.Generator += o.Generator
	m.Island += o.Island
	m.Blackout += o.Blackout
	m.Transition += o.Transition
	m.Unknown += o.Unknown
}

// GenFlowRow is the energy-flow allocator's generator-aware view of one
// hour (the hourly flow row's gen block); nil for an hour without
// generator data. GenKwh is the generator meter's delta, the split
// fields the allocator's generator-aware sums.
type GenFlowRow struct {
	GenKwh    float64
	Estimated bool
	Minutes   SupplyMinutes

	PVToEss       float64
	GenToEss      float64
	GridToEss     float64
	EssToLoad     float64
	EssToGrid     float64
	EssCharged    float64
	EssDischarged float64
	GenToLoad     float64

	LoadOffgrid      float64
	PVToLoadOffgrid  float64
	EssToLoadOffgrid float64
	PVToEssOffgrid   float64
	PVToLoadIsland   float64
	EssToLoadIsland  float64
}

// active reports whether the generator ran or the site was off the grid
// during the hour: the hours generator accounting changes.
func (g *GenFlowRow) active() bool {
	return g != nil && (g.GenKwh > 0 || g.Minutes.Offgrid() > 0)
}

// genAccounting is the rule a day of an organization counts the generator
// by: fuel priced in the day's tariff and a generator of its own.
func genAccounting(t Tariffs, orgID string) bool {
	return t.FuelSet() && !sharedGenerator(orgID)
}

// GenAccounting reports whether the economics count the organization's
// generator on the civil day containing day, for a caller that must pick
// the same flows (the live energy summary).
func (s *Service) GenAccounting(ctx context.Context, orgID string, day time.Time) bool {
	schedule, err := s.backend.TariffSchedule(ctx, orgID)
	if err != nil {
		return false
	}
	t, ok := schedule.ResolveForDay(day)
	return ok && genAccounting(t, orgID)
}

// sharedGenerator reports whether the organization's generator is shared
// with another site of its economic group (Дубовʼязівський's Т1 and Т2
// read one meter). Until the group is computed as one (PR B3) such a
// site keeps today's formulas.
func sharedGenerator(orgID string) bool {
	for _, g := range Groups {
		for _, m := range g.Members {
			if m.ID == orgID {
				return true
			}
		}
	}
	return false
}

// genFlags marks a day's generator data. Without generator accounting it
// says why: gen_shared for a group's shared generator, gen_cost_missing
// when the day's tariff prices no fuel. With it: gen_estimated when an
// hour's generator energy came from the power integral, and the minutes
// of unknown or transition supply mode, counted as grid. A blackout alone
// flags nothing (handoff §4.5): its hours have no load to price.
func genFlags(rows []*HourRow, accounting, shared bool) []string {
	var active, estimated bool
	var mins SupplyMinutes
	for _, r := range rows {
		if r == nil {
			continue
		}
		if r.GenKwh > 0 || r.SupplyMinutes.Generator > 0 || r.SupplyMinutes.Island > 0 {
			active = true
		}
		if r.Flow.Gen.Accounted && r.GenEstimated {
			estimated = true
		}
		mins.add(r.SupplyMinutes)
	}
	if !active {
		return nil
	}
	switch {
	case shared:
		return []string{"gen_shared"}
	case !accounting:
		return []string{"gen_cost_missing"}
	}
	var out []string
	if estimated {
		out = append(out, "gen_estimated")
	}
	if mins.Unknown > 0 {
		out = append(out, fmt.Sprintf("supply_unknown:%d", mins.Unknown))
	}
	if mins.Transition > 0 {
		out = append(out, fmt.Sprintf("supply_transition:%d", mins.Transition))
	}
	return out
}

// GenFuelMixed is the fuel method of a rollup whose days used both.
const GenFuelMixed = "mixed"

// GenTotals is the generator part of a day or a rollup (ems-spec generator
// handoff §4.6). GenKwh and SupplyMinutes describe any day with generator
// data; the flows, the fuel and ExpenseGenCharge are non-zero only on
// hours counted with the generator. GenFuelMethod is the fuel method of
// those hours: "" without any, GenFuelMixed in a rollup of both.
type GenTotals struct {
	GenKwh           float64
	GenToLoad        float64
	GenToEss         float64
	LoadOffgrid      float64
	GenFuelCost      float64
	GenFuelL         float64
	GenFuelMethod    string
	ExpenseGenCharge float64
	SupplyMinutes    SupplyMinutes
}

// add folds another day's or rollup's generator part in.
func (g *GenTotals) add(o GenTotals) {
	g.GenKwh += o.GenKwh
	g.GenToLoad += o.GenToLoad
	g.GenToEss += o.GenToEss
	g.LoadOffgrid += o.LoadOffgrid
	g.GenFuelCost += o.GenFuelCost
	g.GenFuelL += o.GenFuelL
	g.ExpenseGenCharge += o.ExpenseGenCharge
	g.SupplyMinutes.add(o.SupplyMinutes)
	switch {
	case g.GenFuelMethod == "":
		g.GenFuelMethod = o.GenFuelMethod
	case o.GenFuelMethod != "" && o.GenFuelMethod != g.GenFuelMethod:
		g.GenFuelMethod = GenFuelMixed
	}
}

// Add is add for the portfolio, which sums its sites outside the package.
func (g *GenTotals) Add(o GenTotals) { g.add(o) }

// GenFlows is the generator side of an hour's flows, set only on hours a
// day with generator accounting computes with the generator (Accounted).
type GenFlows struct {
	Accounted bool
	GenKwh    float64
	GenToLoad float64
	GenToEss  float64

	LoadOffgrid      float64
	PVToLoadOffgrid  float64
	EssToLoadOffgrid float64
	PVToEssOffgrid   float64
	PVToLoadIsland   float64
	EssToLoadIsland  float64

	Minutes SupplyMinutes
}

// genFlowsFromCounters is essFlowsFromCounters for an hour counted with
// the generator. Magnitudes come from the hour's counters, the direction
// from the allocator's generator-aware split. An hour wholly without the
// grid has no import or export whatever the SmartLogger meter claims; that
// claim is returned as offgridImport for the day's quality flag.
func genFlowsFromCounters(
	a FlowRow,
	hasChg bool, chargeCounter float64,
	hasDis bool, dischargeCounter float64,
	pv, gridImport, gridExport float64,
) (f HourFlows, offgridImport float64) {
	const eps = 1e-9
	g := a.Gen
	if g.Minutes.Offgrid() >= 60 {
		offgridImport = math.Max(gridImport, 0)
		gridImport, gridExport = 0, 0
	}
	charged := g.EssCharged
	if hasChg {
		charged = math.Max(chargeCounter, 0)
	}
	discharged := g.EssDischarged
	if hasDis {
		discharged = math.Max(dischargeCounter, 0)
	}
	f = HourFlows{
		PV:            pv,
		GridImport:    gridImport,
		GridExport:    gridExport,
		EssCharged:    charged,
		EssDischarged: discharged,
	}
	gen := math.Max(g.GenKwh, 0)

	var genToEss, pvToEssOff float64
	switch {
	case g.EssCharged > eps:
		k := charged / g.EssCharged
		f.PVToEss = g.PVToEss * k
		genToEss = g.GenToEss * k
		f.GridToEss = g.GridToEss * k
		pvToEssOff = g.PVToEssOffgrid * k
	case charged > 0:
		f.GridToEss = math.Min(charged, math.Max(gridImport, 0))
		genToEss = math.Min(charged-f.GridToEss, gen)
		f.PVToEss = charged - f.GridToEss - genToEss
	}
	// PV cannot charge more than the hour generated, nor the generator
	// more than it gave: the excess goes to the generator, then the grid.
	if maxPv := math.Max(pv, 0); f.PVToEss > maxPv {
		over := f.PVToEss - maxPv
		f.PVToEss = maxPv
		toGen := math.Min(over, math.Max(gen-genToEss, 0))
		genToEss += toGen
		f.GridToEss += over - toGen
	}
	if genToEss > gen {
		f.GridToEss += genToEss - gen
		genToEss = gen
	}

	var essToLoadOff, essToLoadIsland float64
	switch {
	case g.EssDischarged > eps:
		k := discharged / g.EssDischarged
		f.EssToLoad = g.EssToLoad * k
		f.EssToGrid = g.EssToGrid * k
		essToLoadOff = g.EssToLoadOffgrid * k
		essToLoadIsland = g.EssToLoadIsland * k
	case discharged > 0:
		f.EssToGrid = math.Min(discharged, math.Max(gridExport, 0))
		f.EssToLoad = discharged - f.EssToGrid
	}

	f.Gen = GenFlows{
		Accounted:        true,
		GenKwh:           gen,
		GenToEss:         genToEss,
		GenToLoad:        math.Max(gen-genToEss, 0),
		LoadOffgrid:      g.LoadOffgrid,
		PVToLoadOffgrid:  g.PVToLoadOffgrid,
		EssToLoadOffgrid: math.Min(essToLoadOff, f.EssToLoad),
		PVToEssOffgrid:   math.Min(pvToEssOff, f.PVToEss),
		PVToLoadIsland:   g.PVToLoadIsland,
		EssToLoadIsland:  math.Min(essToLoadIsland, f.EssToLoad),
		Minutes:          g.Minutes,
	}
	return f, offgridImport
}

// offgridParts is an hour's load without the grid and the solar and
// battery parts of it, made to add up with the generator's: L_off =
// gen_to_load + pv_off + ess_off, which keeps EBITDA equal to effect +
// wear. A wholly off-grid hour is off the grid in full. In a mixed hour
// the allocator's off-grid load bounds the solar and battery parts: the
// counter scaling can claim more for them than the off-grid windows saw,
// as when the generator data begins mid-hour.
type offgridParts struct {
	load, pvToLoad, essToLoad, pvToEss float64
}

func offgridPartsOf(e HourEconomics, flow HourFlows) offgridParts {
	g := flow.Gen
	if g.Minutes.Offgrid() >= 60 {
		return offgridParts{
			load:      math.Min(g.GenToLoad+e.PVToLoad+flow.EssToLoad, e.Load),
			pvToLoad:  e.PVToLoad,
			essToLoad: flow.EssToLoad,
			pvToEss:   flow.PVToEss,
		}
	}
	p := offgridParts{
		pvToLoad:  math.Min(g.PVToLoadOffgrid, e.PVToLoad),
		essToLoad: math.Min(g.EssToLoadOffgrid, flow.EssToLoad),
		pvToEss:   math.Min(g.PVToEssOffgrid, flow.PVToEss),
	}
	if room, sum := math.Max(g.LoadOffgrid-g.GenToLoad, 0), p.pvToLoad+p.essToLoad; sum > room {
		p.pvToLoad *= room / sum
		p.essToLoad *= room / sum
	}
	p.load = math.Min(g.GenToLoad+p.pvToLoad+p.essToLoad, e.Load)
	return p
}

// genHourEconomics replaces e's money with handoff §4.5 for an hour
// counted with the generator. e carries the prices and derived flows of
// HourEconomicsFor.
//
//	baseline = (load − L_off) × import + B_off
//	actual   = import_kwh × import − export_kwh × export + ess_dis × wear + F
//	ess_net  = ess_to_load_on × import + V_ess_off + ess_to_grid × export
//	           − grid_to_ess × import − gen_to_ess × c_ess
//	           − pv_to_ess_on × export − ess_dis × wear
//
// per_kwh: F = G·c, B_off = L_off·c, c_ess = c, V_ess_off = ess_to_load_off·c.
// per_hour: F = T_gen·h, B_off = T_off·h, c_ess = 0, and V_ess_off is the
// battery's share of T_island·h. Solar used without the grid gets the
// same valuation (PvOffgridValueUah), so the revenue legs add up to the
// effect.
func genHourEconomics(e HourEconomics, flow HourFlows, t Tariffs) HourEconomics {
	g := flow.Gen
	imp, exp := e.ImportPrice, e.ExportPrice
	off := offgridPartsOf(e, flow)
	lOff, pvOff, essOff := off.load, off.pvToLoad, off.essToLoad
	tGen := float64(g.Minutes.Generator) / 60
	tIsland := float64(g.Minutes.Island) / 60

	method := t.FuelMethod()
	var fuel, bOff, cEss, vEssOff, vPvOff, litres float64
	if method == GenFuelPerHour {
		h := t.FuelCostPerHour()
		fuel = tGen * h
		bOff = (tGen + tIsland) * h
		if d := g.EssToLoadIsland + g.PVToLoadIsland; d > 0 {
			vEssOff = tIsland * h * g.EssToLoadIsland / d
			vPvOff = tIsland * h * g.PVToLoadIsland / d
		}
		litres = tGen * t.GenFuelConsumptionLPerH
	} else {
		c := t.FuelCostPerKwh()
		fuel = g.GenKwh * c
		bOff = lOff * c
		cEss = c
		vEssOff = essOff * c
		vPvOff = pvOff * c
		litres = g.GenKwh * t.GenFuelConsumptionLPerKwh
	}

	e.BaselineCost = (e.Load-lOff)*imp + bOff
	e.ActualCost = flow.GridImport*imp -
		flow.GridExport*exp +
		flow.EssDischarged*t.DegradationUahPerKwh +
		fuel
	e.Effect = e.BaselineCost - e.ActualCost
	e.EssNet = (flow.EssToLoad-essOff)*imp +
		vEssOff +
		flow.EssToGrid*exp -
		flow.GridToEss*imp -
		g.GenToEss*cEss -
		(flow.PVToEss-off.pvToEss)*exp -
		flow.EssDischarged*t.DegradationUahPerKwh
	e.LoadOffgrid = lOff
	e.PVToLoadOffgrid = pvOff
	e.EssToLoadOffgrid = essOff
	e.PVToEssOffgrid = off.pvToEss
	e.GenFuelCostUah = fuel
	e.GenFuelL = litres
	e.GenFuelMethod = method
	e.PvOffgridValueUah = vPvOff
	e.EssOffgridValueUah = vEssOff
	e.GenChargeCostUah = g.GenToEss * cEss
	return e
}
