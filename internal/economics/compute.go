package economics

import "math"

// HourFlows is the per-hour energy state the economics formulas need.
// Port of the TS `HourFlows` type. The four `*To*` directional flows
// come from the energy-flow allocator; pv / gridImport / gridExport are
// SmartLogger accumulator deltas; essCharged / essDischarged come from
// the same allocator so the per-hour balance identity holds.
type HourFlows struct {
	PV            float64
	GridImport    float64
	GridExport    float64
	EssCharged    float64
	EssDischarged float64
	PVToEss       float64
	GridToEss     float64
	EssToLoad     float64
	EssToGrid     float64
}

// HourEconomics is the result of one call to HourEconomicsFor. Monetary
// values are UAH; per-kWh fields are UAH/kWh.
type HourEconomics struct {
	Load        float64
	PVToLoad    float64
	PVToGrid    float64
	GridToLoad  float64
	ImportPrice float64
	ExportPrice float64
	BaselineCost float64
	ActualCost   float64
	Effect       float64
	EssNet       float64
}

// DeriveDerivedFlows computes load, pvToLoad, pvToGrid, gridToLoad from
// the hourly accumulator deltas plus the four directional flows. Each
// clamp is >= 0 because the underlying counters never physically go
// backward. Port of TS `deriveDerivedFlows`.
func DeriveDerivedFlows(in HourFlows) (load, pvToLoad, pvToGrid, gridToLoad float64) {
	load = math.Max(in.PV+in.GridImport+in.EssDischarged-in.GridExport-in.EssCharged, 0)
	pvToGrid = math.Max(in.GridExport-in.EssToGrid, 0)
	pvToLoad = math.Max(in.PV-pvToGrid-in.PVToEss, 0)
	gridToLoad = math.Max(in.GridImport-in.GridToEss, 0)
	return load, pvToLoad, pvToGrid, gridToLoad
}

// HourEconomicsFor turns one hour's energy flows + the RDN price for the
// hour into the monetary KPIs. Port of TS `hourEconomics`.
func HourEconomicsFor(rdnUahPerKwh float64, flow HourFlows, t Tariffs) HourEconomics {
	vatMultiplier := 1.0
	if t.IncludeVat {
		vatMultiplier = 1 + t.VatRate
	}
	importPrice := (rdnUahPerKwh +
		t.DistributionUahPerKwh +
		t.TransmissionUahPerKwh +
		t.SupplierMarginUahPerKwh +
		t.OtherFeesUahPerKwh) * vatMultiplier
	exportPrice := rdnUahPerKwh * (1 - t.ExportDiscount) * vatMultiplier

	load, pvToLoad, pvToGrid, gridToLoad := DeriveDerivedFlows(flow)

	baselineCost := load * importPrice
	actualCost := flow.GridImport*importPrice -
		flow.GridExport*exportPrice +
		flow.EssDischarged*t.DegradationUahPerKwh
	effect := baselineCost - actualCost

	essNet := flow.EssToLoad*importPrice +
		flow.EssToGrid*exportPrice -
		flow.GridToEss*importPrice -
		flow.PVToEss*exportPrice -
		flow.EssDischarged*t.DegradationUahPerKwh

	return HourEconomics{
		Load:        load,
		PVToLoad:    pvToLoad,
		PVToGrid:    pvToGrid,
		GridToLoad:  gridToLoad,
		ImportPrice: importPrice,
		ExportPrice: exportPrice,
		BaselineCost: baselineCost,
		ActualCost:   actualCost,
		Effect:       effect,
		EssNet:       essNet,
	}
}

// DailyTotals folds an hourly slice into the daily KPIs. Port of TS
// `DailyTotals` + `dailyTotals`. Hours with nil RDN (no price) are
// skipped so a partially-priced day is honest.
type DailyTotals struct {
	BaselineCost float64
	ActualCost   float64
	Effect       float64
	EssNet       float64
	Load         float64
	PV           float64
	GridImport   float64
	GridExport   float64
	EssCharged   float64
	EssDischarged float64
	PVToLoad     float64
	PVToEss      float64
	PVToGrid     float64
	GridToLoad   float64
	GridToEss    float64
	EssToLoad    float64
	EssToGrid    float64
	HoursWithData    int
	HoursMissingPrice int
	AvgImportPrice float64
	AvgExportPrice float64
	RevenuePvExport  float64
	RevenuePvSelf    float64
	RevenueEssExport float64
	RevenueEssSelf   float64
	RevenueTotal     float64
	ExpenseGridCharge float64
	ExpenseTotal     float64
	Ebitda           float64
	EssWithdrawnCost   float64
	EssRealizedProfit  float64
	EssDegradationCost float64
	EssAvgCostBasisEod float64
	EssResidualKwhEod  float64
	EssCostBasisUahEod float64
}

// ComputeDailyTotals sums a 24-element row slice into DailyTotals. nil
// entries (no flow data) are skipped. Port of TS `dailyTotals`.
func ComputeDailyTotals(rows []*HourRow) DailyTotals {
	var acc DailyTotals
	var importLoadKwh, exportKwh, importPriceUahSum, exportPriceUahSum float64
	for _, row := range rows {
		if row == nil {
			continue
		}
		if row.Rdn == nil {
			acc.HoursMissingPrice++
			continue
		}
		acc.HoursWithData++
		acc.BaselineCost += row.Econ.BaselineCost
		acc.ActualCost += row.Econ.ActualCost
		acc.Effect += row.Econ.Effect
		acc.EssNet += row.Econ.EssNet
		acc.Load += row.Econ.Load
		acc.PV += row.Flow.PV
		acc.GridImport += row.Flow.GridImport
		acc.GridExport += row.Flow.GridExport
		acc.EssCharged += row.Flow.EssCharged
		acc.EssDischarged += row.Flow.EssDischarged
		acc.PVToLoad += row.Econ.PVToLoad
		acc.PVToEss += row.Flow.PVToEss
		acc.PVToGrid += row.Econ.PVToGrid
		acc.GridToLoad += row.Econ.GridToLoad
		acc.GridToEss += row.Flow.GridToEss
		acc.EssToLoad += row.Flow.EssToLoad
		acc.EssToGrid += row.Flow.EssToGrid

		importPrice := row.Econ.ImportPrice
		exportPrice := row.Econ.ExportPrice
		acc.RevenuePvExport += row.Econ.PVToGrid * exportPrice
		acc.RevenuePvSelf += row.Econ.PVToLoad * importPrice
		acc.RevenueEssExport += row.Flow.EssToGrid * exportPrice
		acc.RevenueEssSelf += row.Flow.EssToLoad * importPrice
		acc.ExpenseGridCharge += row.Flow.GridToEss * importPrice

		importLoadKwh += row.Flow.GridImport
		exportKwh += row.Flow.GridExport
		importPriceUahSum += importPrice * row.Flow.GridImport
		exportPriceUahSum += exportPrice * row.Flow.GridExport

		if row.EssWithdrawnCostUah != nil && !math.IsInf(*row.EssWithdrawnCostUah, 0) && !math.IsNaN(*row.EssWithdrawnCostUah) {
			acc.EssWithdrawnCost += *row.EssWithdrawnCostUah
		}
		if row.EssRealizedProfitUah != nil && !math.IsInf(*row.EssRealizedProfitUah, 0) && !math.IsNaN(*row.EssRealizedProfitUah) {
			acc.EssRealizedProfit += *row.EssRealizedProfitUah
		}
	}
	if importLoadKwh > 0 {
		acc.AvgImportPrice = importPriceUahSum / importLoadKwh
	}
	if exportKwh > 0 {
		acc.AvgExportPrice = exportPriceUahSum / exportKwh
	}
	acc.RevenueTotal = acc.RevenuePvExport + acc.RevenuePvSelf + acc.RevenueEssExport + acc.RevenueEssSelf
	acc.ExpenseTotal = acc.ExpenseGridCharge
	acc.Ebitda = acc.RevenueTotal - acc.ExpenseTotal
	essRevenue := acc.RevenueEssExport + acc.RevenueEssSelf
	acc.EssDegradationCost = essRevenue - acc.EssWithdrawnCost - acc.EssRealizedProfit
	// EOD snapshot: read the END-of-hour state from the last row that
	// ran through rollHour (scan right-to-left so a null-RDN tail
	// falls back to the last priced hour).
	for i := len(rows) - 1; i >= 0; i-- {
		r := rows[i]
		if r != nil && r.EssAvgCostUahPerKwhEnd != nil && r.EssResidualKwhEnd != nil && r.EssCostBasisUahEnd != nil {
			acc.EssAvgCostBasisEod = *r.EssAvgCostUahPerKwhEnd
			acc.EssResidualKwhEod = *r.EssResidualKwhEnd
			acc.EssCostBasisUahEod = *r.EssCostBasisUahEnd
			break
		}
	}
	return acc
}
