// Package dispatch is the planning core of the manual-control desk
// (ems-spec docs/specs/ems_manual_control_mvp.md): an hourly linear
// program over the known horizon with lexicographic priorities —
// physics and the import limit first, then the operator's manual goals
// in chronological order, then the economics of AUTO. It is a port of
// optimizeDispatch / simulate from the approved desk mockup
// (ems-dispatch-desk-browser-v2.html, ems-spec fffb893); the same code
// prices the desk preview and the rolling plan published to the edge.
package dispatch

import (
	"errors"
	"math"
	"strconv"
	"strings"

	"github.com/nesh/sestelemetry/internal/economics"
)

// CommandType is one of the eight operator intents (demo guide §4).
type CommandType string

const (
	CmdCover  CommandType = "cover"  // покривати споживання: Value = max discharge, kW
	CmdSolar  CommandType = "solar"  // заряд надлишками СЕС: Value = max charge, kW
	CmdTarget CommandType = "target" // зарядити до SOC: Value = SOC %, at the end of the block
	CmdCap    CommandType = "cap"    // AUTO з лімітом імпорту: Value = max net PCC import, kW
	CmdExport CommandType = "export" // експорт через PCC: Value = net PCC export, kW
	CmdFixed  CommandType = "fixed"  // потужність УЗЕ: Value kW, Direction charge|discharge
	CmdHold   CommandType = "hold"   // пауза 0 кВт
	CmdAuto   CommandType = "auto"   // повернути в AUTO (never stored on an hour)
)

// Command is the manual intent on one hour. ID names the block it came
// from: consecutive hours of one "target" block share an ID, and only
// the block's last hour carries the SOC goal.
type Command struct {
	Type      CommandType `json:"type"`
	Value     float64     `json:"value"`
	Direction string      `json:"direction,omitempty"`
	ID        string      `json:"id,omitempty"`
}

// Constraints are the horizon-wide limits of a draft («Обмеження на
// весь горизонт»). ExportCapKw nil = no agreed export limit: export is
// impossible until one is set (active_consumer_export_power_cap.md).
type Constraints struct {
	ReservePct  float64  `json:"reserve_pct"`
	GridCharge  bool     `json:"grid_charge"`
	EssSale     bool     `json:"ess_sale"`
	BlockExport bool     `json:"block_export"`
	ImportCapKw float64  `json:"import_cap_kw"`
	ExportCapKw *float64 `json:"export_cap_kw"`
}

// EffectiveExportCap is the PCC export ceiling in force.
func (c Constraints) EffectiveExportCap() float64 {
	if c.BlockExport || c.ExportCapKw == nil {
		return 0
	}
	return *c.ExportCapKw
}

// Site is the physical envelope: passport / «Обмеження» ladder.
type Site struct {
	CapacityKwh float64
	ChargeKw    float64
	DischargeKw float64
	ImportKw    float64 // hard PCC import limit
	SocMinPct   float64 // hard SOC window
	SocMaxPct   float64
	Tariffs     economics.Tariffs
}

// Eta is the one-way efficiency: √(round trip), 0.9 when unset.
func (s Site) Eta() float64 {
	rt := s.Tariffs.RoundtripEfficiency
	if rt <= 0 || rt > 1 {
		rt = 0.9
	}
	return math.Sqrt(rt)
}

// Prices returns the all-in buy and net sell price for an RDN price.
func (s Site) Prices(rdn float64) (buy, sell float64) {
	return economics.ImportExportPrices(s.Tariffs, rdn)
}

// Model is one version of the operator's plan over the future hours
// (index 0 = the current hour). Loads nil = unknown; Commands nil = AUTO.
type Model struct {
	Loads    []*float64  `json:"loads"`
	Commands []*Command  `json:"commands"`
	Cfg      Constraints `json:"cfg"`
}

// Inputs are the non-editable inputs of a run.
type Inputs struct {
	Site     Site
	StartKwh float64    // energy in the battery now
	PV       []float64  // PV forecast per future hour, kW
	Rdn      []*float64 // RDN price per future hour, UAH/kWh; nil = not published
}

// HourPlan is the LP decision for one hour.
type HourPlan struct {
	Charge    float64 `json:"charge_kw"`
	Discharge float64 `json:"discharge_kw"`
	Curtailed float64 `json:"curtailed_kw"`
}

// KnownHours is the contiguous run of hours from now with a known load
// and a published RDN price: the economics cannot look past a gap.
func KnownHours(in Inputs, m Model) int {
	for i, v := range m.Loads {
		if v == nil || math.IsNaN(*v) || math.IsInf(*v, 0) || *v < 0 {
			return i
		}
		if i >= len(in.Rdn) || in.Rdn[i] == nil {
			return i
		}
	}
	return len(m.Loads)
}

func pvAt(in Inputs, i int) float64 {
	if i < len(in.PV) {
		return in.PV[i]
	}
	return 0
}

// lpBuilder accumulates A·x ≤ b rows over a growing variable set.
type lpBuilder struct {
	cost []float64
	rows []map[int]float64
	rhs  []float64
}

func (b *lpBuilder) variable(weight float64) int {
	b.cost = append(b.cost, weight)
	return len(b.cost) - 1
}

func (b *lpBuilder) limit(terms map[int]float64, rhs float64) {
	b.rows = append(b.rows, terms)
	b.rhs = append(b.rhs, rhs)
}

func (b *lpBuilder) upper(v int, max float64) {
	b.limit(map[int]float64{v: 1}, math.Max(0, max))
}

func (b *lpBuilder) equal(terms map[int]float64, rhs float64) {
	b.limit(terms, rhs)
	b.limit(negate(terms), -rhs)
}

func (b *lpBuilder) solve(objective []float64) ([]float64, error) {
	n := len(b.cost)
	A := make([][]float64, len(b.rows))
	for i, terms := range b.rows {
		row := make([]float64, n)
		for v, k := range terms {
			row[v] = k
		}
		A[i] = row
	}
	obj := make([]float64, n)
	copy(obj, objective)
	x, err := solveLP(A, b.rhs, obj)
	if err != nil {
		return nil, err
	}
	if x == nil {
		return nil, ErrInfeasible
	}
	return x, nil
}

// preserveMinimum minimises Σ variables and pins that minimum, so later
// stages cannot trade it away (the mockup's lexicographic step).
func (b *lpBuilder) preserveMinimum(variables []int) error {
	objective := make([]float64, len(b.cost))
	for _, v := range variables {
		objective[v] = -1
	}
	x, err := b.solve(objective)
	if err != nil {
		return err
	}
	minimum := 0.0
	terms := make(map[int]float64, len(variables))
	for _, v := range variables {
		minimum += math.Max(0, x[v])
		terms[v] = 1
	}
	b.limit(terms, minimum+1e-7)
	return nil
}

func negate(terms map[int]float64) map[int]float64 {
	out := make(map[int]float64, len(terms))
	for v, k := range terms {
		out[v] = -k
	}
	return out
}

func copyTerms(terms map[int]float64) map[int]float64 {
	out := make(map[int]float64, len(terms)+1)
	for v, k := range terms {
		out[v] = k
	}
	return out
}

// ErrInfeasible means the LP has no solution at all (never expected:
// every hard limit has a slack); callers surface it as a plan error.
var ErrInfeasible = errors.New("dispatch: plan is infeasible")

// Optimize solves the first `count` hours (see KnownHours).
func Optimize(in Inputs, m Model, count int) ([]HourPlan, error) {
	if count <= 0 {
		return nil, nil
	}
	site, cfg := in.Site, m.Cfg
	eta := site.Eta()
	floor := site.CapacityKwh * math.Max(site.SocMinPct, cfg.ReservePct) / 100
	ceiling := site.CapacityKwh * site.SocMaxPct / 100
	// A live SOC can sit outside the window (below a freshly raised
	// reserve): keep the LP feasible by never asking it to jump inside —
	// the battery just may not discharge below where it already is.
	floor = math.Min(floor, in.StartKwh)
	ceiling = math.Max(ceiling, in.StartKwh)
	exportCap := cfg.EffectiveExportCap()

	type hourVars struct{ charge, discharge, buy, sell, curtail int }
	var (
		b           lpBuilder
		hours       []hourVars
		manualGoals [][]int
		overflows   []int
	)
	aim := func(v int, target float64) {
		b.upper(v, target)
		gap := b.variable(0)
		b.limit(map[int]float64{v: -1, gap: -1}, -target)
		manualGoals = append(manualGoals, []int{gap})
	}
	state := map[int]float64{}
	for i := 0; i < count; i++ {
		buyPrice, sellPrice := site.Prices(*in.Rdn[i])
		pv := pvAt(in, i)
		net := *m.Loads[i] - pv
		var cmd *Command
		if i < len(m.Commands) {
			cmd = m.Commands[i]
		}
		charge := b.variable(0)
		discharge := b.variable(-site.Tariffs.DegradationUahPerKwh)
		buy := b.variable(-buyPrice)
		sell := b.variable(sellPrice)
		curtail := b.variable(0)
		overflow := b.variable(0)
		overflows = append(overflows, overflow)
		hours = append(hours, hourVars{charge, discharge, buy, sell, curtail})

		chargeRoom := math.Max(0, -net)
		if cfg.GridCharge {
			chargeRoom = math.Max(0, cfg.ImportCapKw-net)
		}
		b.upper(charge, math.Min(site.ChargeKw, chargeRoom))
		saleRoom := 0.0
		if cfg.EssSale {
			saleRoom = exportCap
		}
		b.upper(discharge, math.Min(site.DischargeKw, math.Max(0, net+saleRoom)))
		b.upper(sell, exportCap)
		b.upper(curtail, pv)
		b.limit(map[int]float64{buy: 1, overflow: -1}, cfg.ImportCapKw)
		b.equal(map[int]float64{buy: 1, sell: -1, charge: -1, discharge: 1, curtail: -1}, net)
		state[charge] = eta
		state[discharge] = -1 / eta
		b.limit(copyTerms(state), ceiling-in.StartKwh)
		b.limit(negate(state), in.StartKwh-floor)

		if cmd == nil {
			continue
		}
		switch cmd.Type {
		case CmdHold:
			b.upper(charge, 0)
			b.upper(discharge, 0)
		case CmdFixed:
			if cmd.Direction == "charge" {
				b.upper(discharge, 0)
				aim(charge, cmd.Value)
			} else {
				b.upper(charge, 0)
				aim(discharge, cmd.Value)
			}
		case CmdCover:
			b.upper(charge, 0)
			aim(discharge, math.Min(math.Max(0, net), cmd.Value))
		case CmdSolar:
			b.upper(discharge, 0)
			aim(charge, math.Min(math.Max(0, -net), cmd.Value))
		case CmdCap:
			gap := b.variable(0)
			manualGoals = append(manualGoals, []int{gap})
			b.limit(map[int]float64{buy: 1, sell: -1, gap: -1}, cmd.Value)
		case CmdExport:
			b.upper(sell, cmd.Value)
			gap := b.variable(0)
			manualGoals = append(manualGoals, []int{gap})
			b.limit(map[int]float64{buy: 1, sell: -1, gap: -1}, -cmd.Value)
			b.upper(charge, math.Max(0, -net-cmd.Value))
			b.upper(discharge, math.Max(0, net+cmd.Value))
		case CmdTarget:
			b.upper(discharge, 0)
			if isBlockEnd(m, i) {
				gap := b.variable(0)
				excess := b.variable(0)
				manualGoals = append(manualGoals, []int{gap, excess})
				target := site.CapacityKwh * cmd.Value / 100
				low := negate(state)
				low[gap] = -1
				b.limit(low, in.StartKwh-target)
				high := copyTerms(state)
				high[excess] = -1
				b.limit(high, target-in.StartKwh)
			}
		}
	}

	// Physical feasibility first, then attainable manual goals in time
	// order: a later command cannot trade away an earlier manual goal
	// for a higher RDN price.
	if err := b.preserveMinimum(overflows); err != nil {
		return nil, err
	}
	for _, goal := range manualGoals {
		if err := b.preserveMinimum(goal); err != nil {
			return nil, err
		}
	}
	// Terminal value (SHADOW_SOC): energy left
	// above the floor is worth the cheapest all-in import of the horizon.
	shadowPrice := math.Inf(1)
	for i := 0; i < count; i++ {
		buyPrice, _ := site.Prices(*in.Rdn[i])
		shadowPrice = math.Min(shadowPrice, buyPrice)
	}
	for _, h := range hours {
		b.cost[h.charge] += eta * shadowPrice
		b.cost[h.discharge] -= shadowPrice / eta
	}
	x, err := b.solve(b.cost)
	if err != nil {
		return nil, err
	}
	clean := func(v float64) float64 {
		if math.Abs(v) < 1e-6 {
			return 0
		}
		return math.Max(0, v)
	}
	plan := make([]HourPlan, len(hours))
	for i, h := range hours {
		plan[i] = HourPlan{Charge: clean(x[h.charge]), Discharge: clean(x[h.discharge]), Curtailed: clean(x[h.curtail])}
	}
	return plan, nil
}

// isBlockEnd reports whether hour i is the last hour of its command
// block (the next hour carries another block or no command).
func isBlockEnd(m Model, i int) bool {
	cmd := m.Commands[i]
	if i+1 >= len(m.Commands) {
		return true
	}
	next := m.Commands[i+1]
	return next == nil || next.Type != cmd.Type || next.ID != cmd.ID
}

// HourResult is one simulated future hour. Pointer fields are nil past
// the known horizon (no forecast — never read as zero).
type HourResult struct {
	P         *float64 `json:"p_kw"`      // + discharge / − charge
	Wanted    *float64 `json:"wanted_kw"` // what the manual command asked for
	Soc       *float64 `json:"soc_pct"`   // at the end of the hour
	Before    *float64 `json:"soc_before_pct"`
	Import    *float64 `json:"import_kw"`
	Export    *float64 `json:"export_kw"`
	Curtailed *float64 `json:"curtailed_kw"`
	Charge    float64  `json:"charge_kw"`
	Discharge float64  `json:"discharge_kw"`
	Reasons   []string `json:"reasons"`
	Unknown   bool     `json:"unknown"`
}

// Issue is a manual goal or limit the plan cannot meet in one hour.
type Issue struct {
	Hour     int      `json:"hour"`
	Text     string   `json:"text"`
	Wanted   *float64 `json:"wanted_kw"`
	P        float64  `json:"p_kw"`
	Source   string   `json:"source"` // manual | network
	Blocking bool     `json:"blocking"`
}

// Result is a full run: the LP plan replayed hour by hour.
type Result struct {
	Hours      []HourResult `json:"hours"`
	Issues     []Issue      `json:"issues"`
	KnownHours int          `json:"known_hours"`
	EndSoc     *float64     `json:"end_soc_pct"`
	MinSoc     *float64     `json:"min_soc_pct"`
	PeakImport *float64     `json:"peak_import_kw"`
}

func f64(v float64) *float64 { return &v }

// Simulate runs the LP over the known horizon and replays it (the
// mockup's simulate): SOC, PCC exchange, and the reasons any manual
// command or limit falls short. len(Hours) = len(m.Loads).
func Simulate(in Inputs, m Model) (Result, error) {
	site, cfg := in.Site, m.Cfg
	future := len(m.Loads)
	count := KnownHours(in, m)
	plan, err := Optimize(in, m, count)
	if err != nil {
		return Result{}, err
	}
	eta := site.Eta()
	capKwh := site.CapacityKwh
	energy := in.StartKwh
	exportLimit := cfg.EffectiveExportCap()
	floor := capKwh * math.Max(site.SocMinPct, cfg.ReservePct) / 100

	res := Result{KnownHours: count, Hours: make([]HourResult, 0, future), Issues: []Issue{}}
	for i := 0; i < future; i++ {
		if i >= count {
			h := HourResult{Unknown: true, Reasons: []string{}}
			if i == count {
				h.Before = f64(energy / capKwh * 100)
			}
			res.Hours = append(res.Hours, h)
			continue
		}
		var cmd *Command
		if i < len(m.Commands) {
			cmd = m.Commands[i]
		}
		s := CmdAuto
		if cmd != nil {
			s = cmd.Type
		}
		before := energy
		pv := pvAt(in, i)
		net := *m.Loads[i] - pv
		var reasons []string
		p := plan[i].Discharge - plan[i].Charge
		wanted := p
		switch s {
		case CmdCover:
			wanted = math.Min(math.Max(0, net), cmd.Value)
		case CmdSolar:
			wanted = -math.Min(math.Max(0, -net), cmd.Value)
		case CmdFixed:
			if cmd.Direction == "charge" {
				wanted = -cmd.Value
			} else {
				wanted = cmd.Value
			}
		case CmdExport:
			wanted = net + cmd.Value
		}
		energy += plan[i].Charge*eta - plan[i].Discharge/eta
		curtailed := plan[i].Curtailed
		grid := net - p + curtailed
		importPower := math.Max(0, grid)
		exportPower := math.Max(0, -grid)
		incomplete := (s == CmdCover || s == CmdSolar || s == CmdFixed) && math.Abs(p-wanted) > .05
		exportMiss := s == CmdExport && exportPower < cmd.Value-.05
		if incomplete || exportMiss {
			if wanted > 0 && energy <= floor+.05 {
				reasons = append(reasons, "резерв SOC "+Fmt(cfg.ReservePct)+"%")
			}
			if wanted < 0 && energy >= capKwh*site.SocMaxPct/100-.05 {
				reasons = append(reasons, "максимум SOC "+Fmt(site.SocMaxPct)+"%")
			}
			if wanted > site.DischargeKw+.05 || -wanted > site.ChargeKw+.05 {
				reasons = append(reasons, "ліміт потужності УЗЕ")
			}
			if wanted > 0 && !cfg.EssSale && wanted > math.Max(0, net)+.05 {
				reasons = append(reasons, "продаж енергії УЗЕ вимкнено")
			}
			if wanted < 0 && !cfg.GridCharge && -wanted > math.Max(0, -net)+.05 {
				reasons = append(reasons, "бракує надлишку СЕС; заряд із мережі вимкнено")
			}
			if cfg.BlockExport && wanted > math.Max(0, net)+.05 {
				reasons = append(reasons, "повна заборона експорту: СЕС та УЗЕ")
			}
			if s == CmdExport && cmd.Value > exportLimit+.05 && !cfg.BlockExport {
				reasons = append(reasons, "ціль експорту перевищує ліміт PCC")
			}
			if len(reasons) == 0 {
				reasons = append(reasons, "недостатньо доступної енергії або потужності для всіх ручних команд")
			}
		}
		if s == CmdTarget && isBlockEnd(m, i) && energy < capKwh*cmd.Value/100-.05 {
			reasons = append(reasons, "ціль SOC "+Fmt(cmd.Value)+"% недосяжна")
		}
		if importPower > cfg.ImportCapKw+.05 {
			reasons = append(reasons, "імпорт "+Fmt(importPower)+" кВт перевищує ліміт "+Fmt(cfg.ImportCapKw)+" кВт")
		}
		if s == CmdCap && importPower > cmd.Value+.05 {
			if energy <= floor+.05 {
				reasons = append(reasons, "резерв SOC "+Fmt(cfg.ReservePct)+"%")
			}
			if net-cmd.Value > site.DischargeKw+.05 {
				reasons = append(reasons, "ліміт потужності УЗЕ")
			}
			reasons = append(reasons, "імпорт PCC "+Fmt(importPower)+" кВт перевищує задані "+Fmt(cmd.Value)+" кВт")
		}
		if exportMiss {
			reasons = append(reasons, "експорт PCC "+Fmt(exportPower)+" із запитаних "+Fmt(cmd.Value)+" кВт")
		}
		reasons = uniqueStrings(reasons)
		if len(reasons) > 0 {
			source := "manual"
			if s == CmdAuto {
				source = "network"
			}
			w := wanted
			res.Issues = append(res.Issues, Issue{Hour: i, Text: strings.Join(reasons, "; "), Wanted: &w, P: p, Source: source, Blocking: true})
		}
		w := wanted
		res.Hours = append(res.Hours, HourResult{
			P: f64(p), Wanted: &w,
			Soc: f64(energy / capKwh * 100), Before: f64(before / capKwh * 100),
			Import: f64(importPower), Export: f64(exportPower), Curtailed: f64(curtailed),
			Charge: plan[i].Charge, Discharge: plan[i].Discharge,
			Reasons: nonNil(reasons),
		})
	}
	if count == future {
		res.EndSoc = f64(energy / capKwh * 100)
	}
	if count > 0 {
		minSoc := in.StartKwh / capKwh * 100
		peak := 0.0
		for i := 0; i < count; i++ {
			minSoc = math.Min(minSoc, *res.Hours[i].Soc)
			if i == 0 || *res.Hours[i].Import > peak {
				peak = *res.Hours[i].Import
			}
		}
		res.MinSoc = f64(minSoc)
		res.PeakImport = f64(peak)
	}
	return res, nil
}

func uniqueStrings(in []string) []string {
	seen := map[string]bool{}
	out := in[:0]
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// FlowPart is one coloured piece of a BESS bar.
type FlowPart struct {
	Key   string  `json:"key"` // solar | grid | load | export
	Power float64 `json:"power_kw"`
}

// SplitFlow attributes the BESS power by balance (PV supplies the
// local load first): charge from PV surplus vs grid, discharge into the
// load vs export. The mockup's splitBessFlow.
func SplitFlow(p, pv, load float64) []FlowPart {
	var parts []FlowPart
	if p < 0 {
		solar := math.Min(-p, math.Max(0, pv-load))
		parts = []FlowPart{{"solar", -solar}, {"grid", p + solar}}
	} else {
		local := math.Min(p, math.Max(0, load-pv))
		parts = []FlowPart{{"load", local}, {"export", p - local}}
	}
	out := parts[:0]
	for _, part := range parts {
		if math.Abs(part.Power) > .000001 {
			out = append(out, part)
		}
	}
	return out
}

// Fmt renders a number like the mockup (uk-UA, at most one decimal,
// no-break space between thousands).
func Fmt(x float64) string {
	neg := x < 0
	v := math.Round(math.Abs(x)*10) / 10
	whole := math.Floor(v)
	frac := int(math.Round((v - whole) * 10))
	digits := strconv.FormatFloat(whole, 'f', 0, 64)
	var groups []string
	for len(digits) > 3 {
		groups = append([]string{digits[len(digits)-3:]}, groups...)
		digits = digits[:len(digits)-3]
	}
	groups = append([]string{digits}, groups...)
	s := strings.Join(groups, "\u00a0")
	if frac > 0 {
		s += "," + strconv.Itoa(frac)
	}
	if neg && (whole > 0 || frac > 0) {
		s = "-" + s
	}
	return s
}
