package offgrid

import (
	"fmt"
	"math"
	"sort"
	"time"
)

// Advice at an outage (ems_offgrid_advice.md §6): island for the whole
// outage, the generator at once, or the island and then the generator.
// The system advises and shows the consequences; people decide.

// Variant ids.
const (
	VariantIsland      = "A" // острів на все відключення
	VariantGenerator   = "B" // генератор одразу
	VariantIslandToGen = "C" // острів, потім генератор
)

// Fuel is the tariff version's generator fuel (handoff §4.4), VAT in.
type Fuel struct {
	Set        bool    `json:"set"`
	Method     string  `json:"method"` // per_kwh | per_hour
	UahPerKwh  float64 `json:"uah_per_kwh"`
	UahPerHour float64 `json:"uah_per_hour"`
	LPerKwh    float64 `json:"l_per_kwh"`
	LPerHour   float64 `json:"l_per_hour"`
}

// AdviceParams are the site's figures and money for one advice.
type AdviceParams struct {
	CapacityKwh  float64
	FloorPct     float64
	SwitchSocPct float64 // §6.3: the island hands over to the generator here
	ChargeKw     float64 // the battery's charge power, for the generator's top-up
	IslandMaxKw  float64
	GenRatedKw   float64
	GenMinKw     float64
	// ExtraStopThresholdUah: a variant with one more stop is advised only
	// when it saves at least this much (§6.4 п. 2).
	ExtraStopThresholdUah float64
	// RechargeUahPerKwh is what a kWh taken from the battery costs to put
	// back from the grid after the outage (§6.3).
	RechargeUahPerKwh float64
	RechargeEstimated bool
	Fuel              Fuel
}

// Hour is one stretch of the outage inside a clock hour: from From, for
// Fraction of an hour, with L_розр (the margin in) and the sun.
type Hour struct {
	From     time.Time
	Fraction float64
	LoadKw   float64
	PVKw     float64
}

// HoursOf cuts [start, end) into clock-hour stretches; at gives each
// hour's load and sun.
func HoursOf(start, end time.Time, at func(hour time.Time) (loadKw, pvKw float64)) []Hour {
	var out []Hour
	for h := start.Truncate(time.Hour); h.Before(end); h = h.Add(time.Hour) {
		from, to := maxTime(h, start), minTime(h.Add(time.Hour), end)
		if !to.After(from) {
			continue
		}
		load, pv := at(h)
		out = append(out, Hour{From: from, Fraction: to.Sub(from).Hours(), LoadKw: load, PVKw: pv})
	}
	return out
}

// Variant is one way through the outage.
type Variant struct {
	ID       string     `json:"id"`
	Label    string     `json:"label"`
	Feasible bool       `json:"feasible"`
	Why      string     `json:"why,omitempty"`
	CostUah  *float64   `json:"cost_uah"`
	FuelL    float64    `json:"fuel_l"`
	Stops    int        `json:"stops"`
	EndSoc   float64    `json:"end_soc_pct"`
	SwitchAt *time.Time `json:"switch_at,omitempty"`
	Notes    []string   `json:"notes,omitempty"`
}

// Advice is §6.2–§6.4.
type Advice struct {
	Variants    []Variant `json:"variants"`
	Recommended string    `json:"recommended,omitempty"`
	Why         string    `json:"why,omitempty"`
	// LimitLoadKw: some hour needs more than any variant gives; the load
	// must come down to this.
	LimitLoadKw *float64 `json:"limit_load_kw,omitempty"`
}

var variantLabels = map[string]string{
	VariantIsland:      "Острів на все відключення",
	VariantGenerator:   "Генератор одразу",
	VariantIslandToGen: "Острів, потім генератор",
}

// Advise works out the three variants from the charge at the start.
func Advise(hours []Hour, socPct float64, p AdviceParams) Advice {
	a := Advice{Variants: []Variant{island(hours, socPct, p), generator(hours, socPct, p), islandToGen(hours, socPct, p)}}
	for _, h := range hours {
		if h.LoadKw-h.PVKw > p.GenRatedKw {
			a.LimitLoadKw = ptr(p.GenRatedKw)
			break
		}
	}
	a.Recommended, a.Why = recommend(a.Variants, p.ExtraStopThresholdUah)
	return a
}

func island(hours []Hour, socPct float64, p AdviceParams) Variant {
	v := Variant{ID: VariantIsland, Label: variantLabels[VariantIsland], Stops: 2, Feasible: true}
	e, floorE := socPct/100*p.CapacityKwh, p.FloorPct/100*p.CapacityKwh
	start := e
	for _, h := range hours {
		net := math.Max(0, h.LoadKw-h.PVKw)
		if net > p.IslandMaxKw {
			v.Feasible, v.Why = false, fmt.Sprintf("споживання %.0f кВт вище потужності острова %.0f кВт", net, p.IslandMaxKw)
			break
		}
		e -= net * h.Fraction
		if e < floorE {
			v.Feasible, v.Why = false, fmt.Sprintf("заряду не вистачить до кінця: %.0f %% мало", socPct)
			break
		}
	}
	v.EndSoc = round1(math.Max(e, 0) / p.CapacityKwh * 100)
	if v.Feasible {
		v.CostUah = ptr(math.Round((start - e) * p.RechargeUahPerKwh))
	}
	return v
}

// genRun is the generator over some hours: its fuel and what it puts
// into the battery to stay above its minimum.
type genRun struct {
	kwh, hours, charged float64
	belowMin            bool
	over                float64
}

func runGenerator(hours []Hour, roomKwh float64, p AdviceParams) genRun {
	var g genRun
	for _, h := range hours {
		net := h.LoadKw - h.PVKw
		if net > p.GenRatedKw {
			g.over = math.Max(g.over, net)
		}
		out := math.Max(0, net)
		if out < p.GenMinKw {
			top := math.Min(p.GenMinKw-out, p.ChargeKw)
			if top*h.Fraction > roomKwh {
				top = roomKwh / math.Max(h.Fraction, 1e-9)
			}
			if out+top < p.GenMinKw-0.5 {
				g.belowMin = true
			}
			out += top
			g.charged += top * h.Fraction
			roomKwh -= top * h.Fraction
		}
		g.kwh += out * h.Fraction
		g.hours += h.Fraction
	}
	return g
}

func (g genRun) fuel(f Fuel) (litres, uah float64) {
	if f.Method == "per_hour" {
		return g.hours * f.LPerHour, g.hours * f.UahPerHour
	}
	return g.kwh * f.LPerKwh, g.kwh * f.UahPerKwh
}

func generator(hours []Hour, socPct float64, p AdviceParams) Variant {
	v := Variant{ID: VariantGenerator, Label: variantLabels[VariantGenerator], Stops: 2, Feasible: true}
	room := math.Max(0, (100-socPct)/100*p.CapacityKwh)
	g := runGenerator(hours, room, p)
	if g.over > 0 {
		v.Feasible, v.Why = false, fmt.Sprintf("споживання %.0f кВт вище номіналу генератора %.0f кВт", g.over, p.GenRatedKw)
	}
	if g.belowMin {
		v.Notes = append(v.Notes, "генератор працюватиме нижче мінімуму")
	}
	v.EndSoc = round1(socPct + g.charged/p.CapacityKwh*100)
	litres, uah := g.fuel(p.Fuel)
	v.FuelL = round1(litres)
	if !p.Fuel.Set {
		v.Notes = append(v.Notes, "ціна пального не задана в тарифі")
	} else if v.Feasible {
		v.CostUah = ptr(math.Round(uah - g.charged*p.RechargeUahPerKwh))
	}
	return v
}

func islandToGen(hours []Hour, socPct float64, p AdviceParams) Variant {
	v := Variant{ID: VariantIslandToGen, Label: variantLabels[VariantIslandToGen], Stops: 3}
	e, switchE := socPct/100*p.CapacityKwh, p.SwitchSocPct/100*p.CapacityKwh
	start := e
	if e <= switchE {
		v.Why = fmt.Sprintf("заряд уже не вище %.0f %%", p.SwitchSocPct)
		return v
	}
	var rest []Hour
	for k, h := range hours {
		net := math.Max(0, h.LoadKw-h.PVKw)
		if net > p.IslandMaxKw {
			v.Why = fmt.Sprintf("споживання %.0f кВт вище потужності острова %.0f кВт", net, p.IslandMaxKw)
			return v
		}
		if net == 0 || e-net*h.Fraction > switchE {
			e -= net * h.Fraction
			continue
		}
		// The island's share runs out inside this stretch.
		used := (e - switchE) / net
		at := h.From.Add(time.Duration(used * float64(time.Hour))).Truncate(time.Minute)
		v.SwitchAt = &at
		e = switchE
		rest = append(rest, Hour{From: at, Fraction: h.Fraction - used, LoadKw: h.LoadKw, PVKw: h.PVKw})
		rest = append(rest, hours[k+1:]...)
		break
	}
	if v.SwitchAt == nil {
		v.Why = "заряду вистачає на все відключення: генератор не потрібен"
		return v
	}
	g := runGenerator(rest, math.Max(0, p.CapacityKwh-e), p)
	if g.over > 0 {
		v.Why = fmt.Sprintf("споживання %.0f кВт вище номіналу генератора %.0f кВт", g.over, p.GenRatedKw)
		return v
	}
	if g.belowMin {
		v.Notes = append(v.Notes, "генератор працюватиме нижче мінімуму")
	}
	v.Feasible = true
	v.EndSoc = round1((e + g.charged) / p.CapacityKwh * 100)
	litres, uah := g.fuel(p.Fuel)
	v.FuelL = round1(litres)
	if !p.Fuel.Set {
		v.Notes = append(v.Notes, "ціна пального не задана в тарифі")
		return v
	}
	v.CostUah = ptr(math.Round((start-e)*p.RechargeUahPerKwh + uah - g.charged*p.RechargeUahPerKwh))
	return v
}

// recommend is §6.4: the cheapest feasible; one with an extra stop only
// when it saves at least the threshold over one with fewer.
func recommend(vs []Variant, threshold float64) (string, string) {
	var priced []Variant
	for _, v := range vs {
		if v.Feasible && v.CostUah != nil {
			priced = append(priced, v)
		}
	}
	if len(priced) == 0 {
		return "", "немає варіанта з відомою вартістю"
	}
	sort.SliceStable(priced, func(i, j int) bool { return *priced[i].CostUah < *priced[j].CostUah })
	best := priced[0]
	for _, v := range priced[1:] {
		if v.Stops < best.Stops && *v.CostUah-*best.CostUah < threshold {
			return v.ID, fmt.Sprintf("«%s» дешевший лише на %.0f грн — менше зупинок важливіше", best.Label, *v.CostUah-*best.CostUah)
		}
	}
	return best.ID, "найдешевший із можливих"
}

// RechargePrice is §6.3: the mean all-in price of the cheapest hours
// after the outage needed to put need kWh back, over the round trip,
// plus the battery's wear.
func RechargePrice(buyAfter []float64, needKwh, chargeKw, roundtrip, wearUahPerKwh float64) float64 {
	if len(buyAfter) == 0 {
		return 0
	}
	if roundtrip <= 0 || roundtrip > 1 {
		roundtrip = 0.9
	}
	n := 1
	if chargeKw > 0 {
		n = int(math.Ceil(needKwh / chargeKw))
	}
	n = max(1, min(n, len(buyAfter)))
	sorted := append([]float64(nil), buyAfter...)
	sort.Float64s(sorted)
	sum := 0.0
	for _, v := range sorted[:n] {
		sum += v
	}
	return sum/float64(n)/roundtrip + wearUahPerKwh
}

// GenChargeHint is §7: charge the battery from the generator or later
// from the grid, as the generator's target for the IoT2050.
type GenChargeHint struct {
	TargetKw           float64 `json:"target_kw"`
	Why                string  `json:"why"`
	GenChargeUahPerKwh float64 `json:"gen_charge_uah_per_kwh"`
	RechargeUahPerKwh  float64 `json:"recharge_uah_per_kwh"`
}

// GenCharge decides the generator's target from its power now. extraLPerKwh
// is the extra fuel per kWh of charge; gridInTime tells whether the
// grid can still fill the battery to its target before the next outage.
func GenCharge(genKw float64, p AdviceParams, targetKw, fuelUahPerL, vatMul, extraLPerKwh float64, gridInTime bool) GenChargeHint {
	h := GenChargeHint{RechargeUahPerKwh: math.Round(p.RechargeUahPerKwh*100) / 100}
	h.GenChargeUahPerKwh = math.Round(fuelUahPerL*extraLPerKwh*vatMul*100) / 100
	switch {
	case genKw < p.GenMinKw:
		h.TargetKw, h.Why = p.GenMinKw, fmt.Sprintf("генератор %.0f кВт нижче мінімуму: заряд обов'язковий до %.0f кВт", genKw, p.GenMinKw)
	case h.GenChargeUahPerKwh <= p.RechargeUahPerKwh:
		h.TargetKw, h.Why = targetKw, fmt.Sprintf("заряд від генератора дешевший: %.2f проти %.2f грн/кВт·год", h.GenChargeUahPerKwh, h.RechargeUahPerKwh)
	case !gridInTime:
		h.TargetKw, h.Why = targetKw, "з мережі до наступного відключення УЗЕ не встигне дозарядитись"
	default:
		h.TargetKw, h.Why = p.GenMinKw, fmt.Sprintf("заряд від генератора дорожчий: %.2f проти %.2f грн/кВт·год з мережі — лише до мінімуму", h.GenChargeUahPerKwh, h.RechargeUahPerKwh)
	}
	return h
}

// Available is §8: the power at hand and how long the battery lasts.
type Available struct {
	IslandKw        float64    `json:"island_kw"`
	GeneratorKw     float64    `json:"generator_kw"`
	AutonomyNowMin  *float64   `json:"autonomy_now_min,omitempty"`
	AutonomyPlanMin *float64   `json:"autonomy_plan_min,omitempty"`
	RunsOutAt       *time.Time `json:"runs_out_at,omitempty"`
	// StartGeneratorBy is the runs-out time less the 4 min of the
	// generator's start and 10 min of margin (§6.4).
	StartGeneratorBy *time.Time `json:"start_generator_by,omitempty"`
}

// AvailableNow computes §8 at now; hours are L_розр and the PV forecast
// from now on.
func AvailableNow(now time.Time, socPct, dischargeMaxKw, pvKw, loadKw float64, hours []Hour, p AdviceParams) Available {
	a := Available{
		IslandKw:    round1(math.Min(p.IslandMaxKw, dischargeMaxKw) + pvKw),
		GeneratorKw: round1(p.GenRatedKw + dischargeMaxKw),
	}
	usable := (socPct - p.FloorPct) / 100 * p.CapacityKwh
	if usable <= 0 {
		return a
	}
	if net := loadKw - pvKw; net > 0 {
		m := math.Round(usable / net * 60)
		a.AutonomyNowMin = &m
		at := now.Add(time.Duration(m) * time.Minute).Truncate(time.Minute)
		by := at.Add(-14 * time.Minute)
		a.RunsOutAt, a.StartGeneratorBy = &at, &by
	}
	left, mins := usable, 0.0
	for _, h := range hours {
		net := math.Max(0, h.LoadKw-h.PVKw)
		e := net * h.Fraction
		if net > 0 && e >= left {
			mins += left / net * 60
			a.AutonomyPlanMin = ptr(math.Round(mins))
			return a
		}
		left -= e
		mins += h.Fraction * 60
	}
	return a
}
