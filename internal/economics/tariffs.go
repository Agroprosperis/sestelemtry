// Package economics is the server-side port of the daily economics
// model (see daily_economic_model_mvp.md and the hourly EMS table in
// fusionsolar_api_import_agent_handoff_uk.md). It mirrors the former
// client-side pipeline (web/src/economics/{compute,costBasis}.ts and
// the assembly in useEconomicsData.ts) one-to-one so stored archive
// economics and live economics are produced by a single engine.
package economics

import "time"

// Tariffs encapsulates every per-kWh number the model needs. All values
// are UAH/kWh except ExportDiscount and VatRate (unitless 0..1
// fractions) and the IncludeVat toggle. EssCapacityKwh is the battery
// capacity used to anchor the SOC residual. Mirrors the frontend
// `Tariffs` type and the API `OrgTariffs` DTO field-for-field.
type Tariffs struct {
	DistributionUahPerKwh   float64
	TransmissionUahPerKwh   float64
	SupplierMarginUahPerKwh float64
	OtherFeesUahPerKwh      float64
	ExportDiscount          float64
	DegradationUahPerKwh    float64
	IncludeVat              bool
	VatRate                 float64
	EssCapacityKwh          float64
}

// ScheduleEntry is one effective-dated tariff version. EffectiveFrom is
// the calendar day (midnight) from which Tariffs applies.
type ScheduleEntry struct {
	EffectiveFrom time.Time
	Tariffs       Tariffs
}

// Schedule is a date-versioned set of tariffs for one organization,
// sorted ascending by EffectiveFrom. ResolveForDay picks the version in
// effect for a given day.
type Schedule []ScheduleEntry

// ResolveForDay returns the tariff version in effect for `day` — the
// entry with the greatest EffectiveFrom that is on or before `day`. The
// bool is false when the schedule is empty or `day` precedes the
// earliest entry (caller falls back to defaults).
//
// Comparison is on the CIVIL date only (year/month/day in each value's
// own location), never the absolute instant. effective_from is a civil
// date (parsed in UTC) while `day` is a local-midnight timestamp, so an
// instant comparison would mis-rank a tariff that starts on the same
// civil date as the target day in a positive-offset timezone.
func (s Schedule) ResolveForDay(day time.Time) (Tariffs, bool) {
	var (
		best   Tariffs
		found  bool
		bestAt time.Time
	)
	for _, e := range s {
		if civilLessOrEqual(e.EffectiveFrom, day) {
			if !found || civilLessOrEqual(bestAt, e.EffectiveFrom) {
				best = e.Tariffs
				bestAt = e.EffectiveFrom
				found = true
			}
		}
	}
	return best, found
}

// civilLessOrEqual reports whether a's civil date is on or before b's
// civil date, each read in its own location.
func civilLessOrEqual(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	if ay != by {
		return ay < by
	}
	if am != bm {
		return am < bm
	}
	return ad <= bd
}
