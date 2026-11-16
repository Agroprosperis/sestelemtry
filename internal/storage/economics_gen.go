package storage

import (
	"fmt"
	"strings"
)

// EconomicsGenHour is the generator part of one economics_hourly row
// (ems-spec generator handoff §4.6). GenKwh, Estimated and the minutes
// describe any hour with generator data; the flows and money are
// non-zero only when the hour was counted with the generator (Accounted).
type EconomicsGenHour struct {
	GenKwh            float64
	Estimated         bool
	MinutesGenerator  int
	MinutesIsland     int
	MinutesBlackout   int
	MinutesTransition int
	MinutesUnknown    int
	Accounted         bool
	GenToLoad         float64
	GenToEss          float64
	LoadOffgrid       float64
	PVToLoadOffgrid   float64
	EssToLoadOffgrid  float64
	PVToEssOffgrid    float64
	PVToLoadIsland    float64
	EssToLoadIsland   float64
	FuelCost          float64
	FuelL             float64
	FuelMethod        string
	PvOffgridValue    float64
	EssOffgridValue   float64
	ChargeCost        float64
}

// genHourColumns are economics_hourly's generator columns, in the order
// of values and scanTargets.
var genHourColumns = []string{
	"gen_kwh", "gen_estimated",
	"minutes_generator", "minutes_island", "minutes_blackout", "minutes_transition", "minutes_unknown",
	"gen_accounted", "gen_to_load_kwh", "gen_to_ess_kwh",
	"load_offgrid_kwh", "pv_to_load_offgrid_kwh", "ess_to_load_offgrid_kwh", "pv_to_ess_offgrid_kwh",
	"pv_to_load_island_kwh", "ess_to_load_island_kwh",
	"gen_fuel_cost_uah", "gen_fuel_l", "gen_fuel_method",
	"pv_offgrid_value_uah", "ess_offgrid_value_uah", "gen_charge_cost_uah",
}

func (g EconomicsGenHour) values() []any {
	return []any{
		g.GenKwh, g.Estimated,
		g.MinutesGenerator, g.MinutesIsland, g.MinutesBlackout, g.MinutesTransition, g.MinutesUnknown,
		g.Accounted, g.GenToLoad, g.GenToEss,
		g.LoadOffgrid, g.PVToLoadOffgrid, g.EssToLoadOffgrid, g.PVToEssOffgrid,
		g.PVToLoadIsland, g.EssToLoadIsland,
		g.FuelCost, g.FuelL, g.FuelMethod,
		g.PvOffgridValue, g.EssOffgridValue, g.ChargeCost,
	}
}

func (g *EconomicsGenHour) scanTargets() []any {
	return []any{
		&g.GenKwh, &g.Estimated,
		&g.MinutesGenerator, &g.MinutesIsland, &g.MinutesBlackout, &g.MinutesTransition, &g.MinutesUnknown,
		&g.Accounted, &g.GenToLoad, &g.GenToEss,
		&g.LoadOffgrid, &g.PVToLoadOffgrid, &g.EssToLoadOffgrid, &g.PVToEssOffgrid,
		&g.PVToLoadIsland, &g.EssToLoadIsland,
		&g.FuelCost, &g.FuelL, &g.FuelMethod,
		&g.PvOffgridValue, &g.EssOffgridValue, &g.ChargeCost,
	}
}

// EconomicsGenDay is the generator part of one economics_daily row.
type EconomicsGenDay struct {
	GenKwh            float64
	GenToLoad         float64
	GenToEss          float64
	LoadOffgrid       float64
	FuelCost          float64
	FuelL             float64
	FuelMethod        string
	ExpenseGenCharge  float64
	MinutesGenerator  int
	MinutesIsland     int
	MinutesBlackout   int
	MinutesTransition int
	MinutesUnknown    int
	EffectOffgrid     float64
}

// genDayColumns are economics_daily's generator columns, in the order of
// values and scanTargets.
var genDayColumns = []string{
	"gen_kwh", "gen_to_load_kwh", "gen_to_ess_kwh", "load_offgrid_kwh",
	"gen_fuel_cost_uah", "gen_fuel_l", "gen_fuel_method", "expense_gen_charge_uah",
	"minutes_generator", "minutes_island", "minutes_blackout", "minutes_transition", "minutes_unknown",
	"effect_offgrid_uah",
}

func (g EconomicsGenDay) values() []any {
	return []any{
		g.GenKwh, g.GenToLoad, g.GenToEss, g.LoadOffgrid,
		g.FuelCost, g.FuelL, g.FuelMethod, g.ExpenseGenCharge,
		g.MinutesGenerator, g.MinutesIsland, g.MinutesBlackout, g.MinutesTransition, g.MinutesUnknown,
		g.EffectOffgrid,
	}
}

func (g *EconomicsGenDay) scanTargets() []any {
	return []any{
		&g.GenKwh, &g.GenToLoad, &g.GenToEss, &g.LoadOffgrid,
		&g.FuelCost, &g.FuelL, &g.FuelMethod, &g.ExpenseGenCharge,
		&g.MinutesGenerator, &g.MinutesIsland, &g.MinutesBlackout, &g.MinutesTransition, &g.MinutesUnknown,
		&g.EffectOffgrid,
	}
}

// economicsGenSchema adds the generator columns in place, so rows written
// before them read as a day without generator data. Mirrored by
// migrations/025_generator_economics.sql and 026_generator_effect_offgrid.sql.
func economicsGenSchema() []string {
	add := func(table string, cols []string) []string {
		var out []string
		for _, c := range cols {
			typ := "double precision NOT NULL DEFAULT 0"
			switch {
			case strings.HasPrefix(c, "minutes_"):
				typ = "integer NOT NULL DEFAULT 0"
			case c == "gen_estimated" || c == "gen_accounted":
				typ = "boolean NOT NULL DEFAULT false"
			case c == "gen_fuel_method":
				typ = "text NOT NULL DEFAULT ''"
			}
			out = append(out, fmt.Sprintf("ALTER TABLE %s ADD COLUMN IF NOT EXISTS %s %s", table, c, typ))
		}
		return out
	}
	return append(add("economics_hourly", genHourColumns), add("economics_daily", genDayColumns)...)
}

// columnList joins columns for a SELECT or INSERT list.
func columnList(cols []string) string { return strings.Join(cols, ", ") }

// placeholders renders $first..$first+n-1.
func placeholders(first, n int) string {
	p := make([]string, n)
	for i := range p {
		p[i] = fmt.Sprintf("$%d", first+i)
	}
	return strings.Join(p, ",")
}

// excludedSet renders the ON CONFLICT update of cols.
func excludedSet(cols []string) string {
	p := make([]string, len(cols))
	for i, c := range cols {
		p[i] = c + " = EXCLUDED." + c
	}
	return strings.Join(p, ",\n\t\t\t")
}
