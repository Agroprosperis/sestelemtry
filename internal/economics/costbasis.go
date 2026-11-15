package economics

import "math"

// EssState is the running cost-basis pair (Weighted Average Cost). Kwh
// is residual energy as known to the bookkeeper; Uah is the total UAH
// stored. Port of TS `EssState`.
type EssState struct {
	Kwh float64
	Uah float64
}

// HourCostBasis is the per-hour observable layer of the WAC roll. Port
// of TS `HourCostBasis`.
type HourCostBasis struct {
	Prev               EssState
	Next               EssState
	AvgCostStart       float64
	AvgCostEnd         float64
	WithdrawnCostUah   float64
	RealizedProfitUah  float64
}

// RollHour applies one hour of charges and discharges to prev using
// charge-then-discharge ordering. PV→ESS enters at 0 UAH (sunlight is
// free), Grid→ESS at the import price; discharges withdraw at the
// post-charge average. Port of TS `rollHour`.
func RollHour(prev EssState, flow HourFlows, importPrice, exportPrice, degradationUahPerKwh float64) HourCostBasis {
	return rollHour(prev, flow, rollPrices{importPrice: importPrice, exportPrice: exportPrice, degradation: degradationUahPerKwh})
}

// rollPrices are what one hour's cost-basis roll prices energy at. The
// generator terms come from genHourEconomics and are zero otherwise:
// what the generator's charge cost, and the diesel valuation of the
// discharge used without the grid.
type rollPrices struct {
	importPrice        float64
	exportPrice        float64
	degradation        float64
	genChargeCostUah   float64
	essOffgridValueUah float64
}

func rollHour(prev EssState, flow HourFlows, p rollPrices) HourCostBasis {
	importPrice, exportPrice, degradationUahPerKwh := p.importPrice, p.exportPrice, p.degradation
	avgCostStart := 0.0
	if prev.Kwh > 0 {
		avgCostStart = prev.Uah / prev.Kwh
	}

	chargedKwh := math.Max(flow.PVToEss, 0) + math.Max(flow.GridToEss, 0) + math.Max(flow.Gen.GenToEss, 0)
	chargedUah := math.Max(flow.GridToEss, 0)*importPrice + p.genChargeCostUah
	afterChargeKwh := prev.Kwh + chargedKwh
	afterChargeUah := prev.Uah + chargedUah

	dischargedKwh := math.Max(flow.EssToLoad, 0) + math.Max(flow.EssToGrid, 0)
	avgCostMid := 0.0
	if afterChargeKwh > 0 {
		avgCostMid = afterChargeUah / afterChargeKwh
	}
	withdrawnCostUah := avgCostMid * dischargedKwh

	nextKwh := afterChargeKwh - dischargedKwh
	nextUah := afterChargeUah - withdrawnCostUah
	if nextKwh <= 0 {
		nextKwh = 0
		nextUah = 0
	} else if nextUah < 0 {
		nextUah = 0
	}
	next := EssState{Kwh: nextKwh, Uah: nextUah}
	avgCostEnd := 0.0
	if next.Kwh > 0 {
		avgCostEnd = next.Uah / next.Kwh
	}

	// Discharge used without the grid displaced diesel, not import.
	dischargeRevenueUah := math.Max(flow.EssToLoad-flow.Gen.EssToLoadOffgrid, 0)*importPrice +
		p.essOffgridValueUah +
		math.Max(flow.EssToGrid, 0)*exportPrice
	degradationUah := math.Max(flow.EssDischarged, 0) * degradationUahPerKwh
	realizedProfitUah := dischargeRevenueUah - withdrawnCostUah - degradationUah

	return HourCostBasis{
		Prev:              prev,
		Next:              next,
		AvgCostStart:      avgCostStart,
		AvgCostEnd:        avgCostEnd,
		WithdrawnCostUah:  withdrawnCostUah,
		RealizedProfitUah: realizedProfitUah,
	}
}
