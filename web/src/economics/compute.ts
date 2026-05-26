import type { Tariffs } from './tariffs'

// HourFlows is the per-hour energy state the economics formulas need.
// The four `*To*` directional flows come straight from the new
// /api/v1/energy-flow-hourly endpoint; `pv`, `gridImport`, `gridExport`
// are SmartLogger accumulator deltas pulled from /api/v1/timeseries
// with `aggregation=delta`. `essCharged` / `essDischarged` come from
// the same hourly endpoint so that the per-hour balance identity
// `load = pv + gridImport + essDis - gridExport - essCh` holds bit-
// for-bit (Allocate's interval-validation logic clamps any rolled-back
// counter to zero, which the deltas-from-timeseries path doesn't).
export type HourFlows = {
  pv: number
  gridImport: number
  gridExport: number
  essCharged: number
  essDischarged: number
  pvToEss: number
  gridToEss: number
  essToLoad: number
  essToGrid: number
}

// HourEconomics is the result of one call to `hourEconomics`. All
// monetary values are UAH; per-kWh fields are UAH/kWh. `effect` is
// project-level (baseline minus actual) and `essNet` isolates the
// ESS-specific contribution so the dashboard can show "did the
// battery alone pay off this hour" separately from the whole
// PV+ESS bundle.
export type HourEconomics = {
  load: number
  pvToLoad: number
  pvToGrid: number
  gridToLoad: number
  importPriceUahPerKwh: number
  exportPriceUahPerKwh: number
  baselineCost: number
  actualCost: number
  effect: number
  essNet: number
}

// derivePvToLoad computes the four flows the new backend doesn't
// return (pvToLoad, pvToGrid, gridToLoad) and `load` itself from
// the hourly accumulator deltas plus the four directional flows
// it does. The algebra mirrors `applyApplianceConsumptionRule`
// in [web/src/dashboard/transforms/buckets.ts] and `Allocate`'s
// `deltaAppliances` rule, so summing 24 hourly `load` values
// reproduces the daily "Споживання навантаження" card on the main
// dashboard.
//
// Each clamp is `>= 0` because the underlying counters never
// physically go backward; a negative result here can only come
// from numerical noise (a 0.001 kWh artefact from rounded source
// values), which we want to render as zero rather than as a
// negative load.
export function deriveDerivedFlows(input: HourFlows): {
  load: number
  pvToLoad: number
  pvToGrid: number
  gridToLoad: number
} {
  const load = Math.max(
    input.pv + input.gridImport + input.essDischarged - input.gridExport - input.essCharged,
    0,
  )
  const pvToGrid = Math.max(input.gridExport - input.essToGrid, 0)
  const pvToLoad = Math.max(input.pv - pvToGrid - input.pvToEss, 0)
  const gridToLoad = Math.max(input.gridImport - input.gridToEss, 0)
  return { load, pvToLoad, pvToGrid, gridToLoad }
}

// hourEconomics turns one hour's worth of energy flows + the RDN
// price for that hour into the four monetary KPIs. `rdnUahPerKwh`
// is the hourly Day-Ahead Market price (already converted from
// UAH/MWh). `null` means the price isn't available — caller is
// expected to surface a partial-day warning rather than passing
// 0, which would silently inflate the project effect.
export function hourEconomics(
  rdnUahPerKwh: number,
  flow: HourFlows,
  tariffs: Tariffs,
): HourEconomics {
  const vatMultiplier = tariffs.includeVat ? 1 + tariffs.vatRate : 1
  // Import price stack: market + distribution + transmission +
  // supplier margin + other fees, then VAT (matching ТЗ §3).
  const importPriceUahPerKwh =
    (rdnUahPerKwh +
      tariffs.distributionUahPerKwh +
      tariffs.transmissionUahPerKwh +
      tariffs.supplierMarginUahPerKwh +
      tariffs.otherFeesUahPerKwh) *
    vatMultiplier
  // Export price: market price after the export discount, then VAT.
  // The discount is a fraction (0.05 = 5%); we apply it as
  // (1 - discount) so 0 = full price, 1 = no revenue.
  const exportPriceUahPerKwh = rdnUahPerKwh * (1 - tariffs.exportDiscount) * vatMultiplier

  const { load, pvToLoad, pvToGrid, gridToLoad } = deriveDerivedFlows(flow)

  // Baseline = "what would the load have cost if 100% of it came
  // from the grid at full import price". This is the counterfactual
  // ТЗ §4 uses to define the project's daily effect.
  const baselineCost = load * importPriceUahPerKwh
  // Actual = grid imports * import price - grid exports * export
  // price + ESS-degradation cost (УЗЕ wear-and-tear on every
  // discharge). The degradation term is a per-kWh proxy for cycle
  // depreciation; setting it to 0 in the UI returns the textbook
  // "without battery wear" number.
  const actualCost =
    flow.gridImport * importPriceUahPerKwh -
    flow.gridExport * exportPriceUahPerKwh +
    flow.essDischarged * tariffs.degradationUahPerKwh
  const effect = baselineCost - actualCost

  // ESS-net is what the battery alone added: essToLoad avoids
  // import-priced kWh, essToGrid earns export-priced kWh, the two
  // charge legs cost what they cost, and degradation eats into the
  // result. This isolates the УЗЕ contribution from the PV array's
  // contribution so the operator can see whether the battery is
  // pulling its weight on its own.
  const essNet =
    flow.essToLoad * importPriceUahPerKwh +
    flow.essToGrid * exportPriceUahPerKwh -
    flow.gridToEss * importPriceUahPerKwh -
    flow.pvToEss * exportPriceUahPerKwh -
    flow.essDischarged * tariffs.degradationUahPerKwh

  return {
    load,
    pvToLoad,
    pvToGrid,
    gridToLoad,
    importPriceUahPerKwh,
    exportPriceUahPerKwh,
    baselineCost,
    actualCost,
    effect,
    essNet,
  }
}

// dailyTotals folds an hourly slice into the daily KPIs the main
// page displays at the top. Hours with `null` rdn or no flow data
// (`null` row) are skipped so a partially-loaded day is honest:
// the result's `hoursWithData` field surfaces the actual coverage.
export type DailyTotals = {
  baselineCost: number
  actualCost: number
  effect: number
  essNet: number
  load: number
  pv: number
  gridImport: number
  gridExport: number
  essCharged: number
  essDischarged: number
  pvToLoad: number
  pvToEss: number
  pvToGrid: number
  gridToLoad: number
  gridToEss: number
  essToLoad: number
  essToGrid: number
  hoursWithData: number
  hoursMissingPrice: number
  avgImportPriceUahPerKwh: number
  avgExportPriceUahPerKwh: number
  // EBITDA framing: each component is summed per hour as
  // `flowₕ · priceₕ` so it reproduces the table's Σ column rather
  // than the (less precise) `flow · weighted-avg-price`. Hours with
  // null RDN are skipped, matching the existing baseline/actual
  // logic. EBITDA = revenueTotal − expenseTotal, and equals
  // `effect` when `degradationUahPerKwh = 0`.
  revenuePvExport: number
  revenuePvSelf: number
  revenueEssExport: number
  revenueEssSelf: number
  revenueTotal: number
  expenseGridCharge: number
  expenseTotal: number
  ebitda: number
  // Cost-basis aggregates from `costBasis.rollHour`. They are
  // independent of the EBITDA framing above (which uses spot
  // prices everywhere) and represent the ESS-only cash effect
  // when each discharge is matched to the price at which the
  // energy was originally charged. `essRealizedProfitUah`
  // equals `revenueEssExport + revenueEssSelf − essWithdrawnCostUah
  // − essDegradationCostUah` (and `essDegradationCostUah` is
  // included as an explicit total for transparency, even though
  // operators usually read it via the existing degradation × kWh
  // calculation). `essAvgCostBasisUahPerKwhEod` is the average
  // cost-per-kWh of whatever residual is left at hour 24 — useful
  // as a "we'll start tomorrow with X грн/кВт·год inside the
  // battery" indicator on the page.
  essWithdrawnCostUah: number
  essRealizedProfitUah: number
  essDegradationCostUah: number
  // End-of-day snapshot of the cost-basis tracker. `Eod` is the
  // state AFTER hour 23 (or the last priced hour, when later hours
  // had no RDN). Three views of the same scalar pair: avg cost per
  // kWh, residual kWh, and total UAH "stuck" inside the battery
  // (= avg · residual). The KPI strip uses residual === 0 as the
  // "battery emptied today" trigger, and `essCostBasisUahEod` as
  // the "deferred profit" hint that explains why
  // `essRealizedProfitUah + pvLegs` may differ from `effect` on
  // carry-over days.
  essAvgCostBasisUahPerKwhEod: number
  essResidualKwhEod: number
  essCostBasisUahEod: number
}

export function dailyTotals(rows: Array<HourEconomicsRow | null>): DailyTotals {
  const acc: DailyTotals = {
    baselineCost: 0,
    actualCost: 0,
    effect: 0,
    essNet: 0,
    load: 0,
    pv: 0,
    gridImport: 0,
    gridExport: 0,
    essCharged: 0,
    essDischarged: 0,
    pvToLoad: 0,
    pvToEss: 0,
    pvToGrid: 0,
    gridToLoad: 0,
    gridToEss: 0,
    essToLoad: 0,
    essToGrid: 0,
    hoursWithData: 0,
    hoursMissingPrice: 0,
    avgImportPriceUahPerKwh: 0,
    avgExportPriceUahPerKwh: 0,
    revenuePvExport: 0,
    revenuePvSelf: 0,
    revenueEssExport: 0,
    revenueEssSelf: 0,
    revenueTotal: 0,
    expenseGridCharge: 0,
    expenseTotal: 0,
    ebitda: 0,
    essWithdrawnCostUah: 0,
    essRealizedProfitUah: 0,
    essDegradationCostUah: 0,
    essAvgCostBasisUahPerKwhEod: 0,
    essResidualKwhEod: 0,
    essCostBasisUahEod: 0,
  }
  let importLoadKwh = 0
  let exportKwh = 0
  let importPriceUahSum = 0
  let exportPriceUahSum = 0
  for (const row of rows) {
    if (!row) continue
    if (row.rdnUahPerKwh === null) {
      acc.hoursMissingPrice++
      continue
    }
    acc.hoursWithData++
    acc.baselineCost += row.economics.baselineCost
    acc.actualCost += row.economics.actualCost
    acc.effect += row.economics.effect
    acc.essNet += row.economics.essNet
    acc.load += row.economics.load
    acc.pv += row.flow.pv
    acc.gridImport += row.flow.gridImport
    acc.gridExport += row.flow.gridExport
    acc.essCharged += row.flow.essCharged
    acc.essDischarged += row.flow.essDischarged
    acc.pvToLoad += row.economics.pvToLoad
    acc.pvToEss += row.flow.pvToEss
    acc.pvToGrid += row.economics.pvToGrid
    acc.gridToLoad += row.economics.gridToLoad
    acc.gridToEss += row.flow.gridToEss
    acc.essToLoad += row.flow.essToLoad
    acc.essToGrid += row.flow.essToGrid

    const importPrice = row.economics.importPriceUahPerKwh
    const exportPrice = row.economics.exportPriceUahPerKwh
    acc.revenuePvExport += row.economics.pvToGrid * exportPrice
    acc.revenuePvSelf += row.economics.pvToLoad * importPrice
    acc.revenueEssExport += row.flow.essToGrid * exportPrice
    acc.revenueEssSelf += row.flow.essToLoad * importPrice
    acc.expenseGridCharge += row.flow.gridToEss * importPrice

    importLoadKwh += row.flow.gridImport
    exportKwh += row.flow.gridExport
    importPriceUahSum += importPrice * row.flow.gridImport
    exportPriceUahSum += exportPrice * row.flow.gridExport

    if (row.essWithdrawnCostUah !== null && Number.isFinite(row.essWithdrawnCostUah)) {
      acc.essWithdrawnCostUah += row.essWithdrawnCostUah
    }
    if (row.essRealizedProfitUah !== null && Number.isFinite(row.essRealizedProfitUah)) {
      acc.essRealizedProfitUah += row.essRealizedProfitUah
    }
  }
  acc.avgImportPriceUahPerKwh = importLoadKwh > 0 ? importPriceUahSum / importLoadKwh : 0
  acc.avgExportPriceUahPerKwh = exportKwh > 0 ? exportPriceUahSum / exportKwh : 0
  acc.revenueTotal =
    acc.revenuePvExport + acc.revenuePvSelf + acc.revenueEssExport + acc.revenueEssSelf
  acc.expenseTotal = acc.expenseGridCharge
  acc.ebitda = acc.revenueTotal - acc.expenseTotal
  // Realized profit obeys the identity
  //   profit = essRevenue − withdrawnCost − degradationCost
  // so we back-solve degradation from the totals to keep all four
  // numbers internally consistent (same convention `dailyTotals`
  // already uses for `revenueTotal = Σ legs`).
  const essRevenue = acc.revenueEssExport + acc.revenueEssSelf
  acc.essDegradationCostUah =
    essRevenue - acc.essWithdrawnCostUah - acc.essRealizedProfitUah
  // EOD snapshot — read the END-of-hour state from the last row
  // that ran through `rollHour`. Scanning right-to-left handles
  // the typical "tail of the day has no DAM yet" case: hours with
  // null RDN don't carry End fields, so we naturally fall back to
  // the last hour that did. Three views of the same scalar pair:
  // avg cost per kWh (for the "tomorrow's basis" KPI), residual
  // kWh (so the KPI can say "battery empty" instead of formatting
  // 0 as em-dash), total UAH "stuck" inside (= avg · residual),
  // which we surface as deferred-profit context.
  for (let i = rows.length - 1; i >= 0; i--) {
    const r = rows[i]
    // Loose `!= null` so `undefined` (older test fixtures, partial
    // hydration) is treated the same as `null`. This way only rows
    // that were explicitly populated by `assembleHourlyRows` count
    // as the EOD source.
    if (
      r &&
      r.essAvgCostUahPerKwhEnd != null &&
      r.essResidualKwhEnd != null &&
      r.essCostBasisUahEnd != null
    ) {
      acc.essAvgCostBasisUahPerKwhEod = r.essAvgCostUahPerKwhEnd
      acc.essResidualKwhEod = r.essResidualKwhEnd
      acc.essCostBasisUahEod = r.essCostBasisUahEnd
      break
    }
  }
  return acc
}

// HourEconomicsRow couples one hour's input flows with its computed
// economics so the table / charts can render both columns from the
// same data structure. `rdnUahPerKwh` may be null when the DAM
// hasn't published a price for that hour (typically future hours
// near "today's" anchor or zone outages); the dashboard surfaces
// these via `hoursMissingPrice`.
export type HourEconomicsRow = {
  hour: number
  // ISO timestamp at the start of the hour, in the request tz.
  // Useful for chart x-axis labels without re-parsing the response.
  hourStart: string
  rdnUahPerKwh: number | null
  flow: HourFlows
  economics: HourEconomics
  // ESS residual energy at the START of the hour, in kWh. Hour 0
  // is anchored from `(soc_percent / 100) · tariffs.essCapacityKwh`
  // observed at the start of the day; every subsequent hour is
  // rolled forward by the previous hour's net charge / discharge:
  //   residual[h+1] = residual[h] + pvToEss[h] + gridToEss[h]
  //                              − essToLoad[h] − essToGrid[h]
  // The cumulative formula keeps the table arithmetic
  // self-consistent (the running line is exactly the four ESS-flow
  // rows added above it). null when the anchor SOC was missing or
  // a preceding hour had no flow data — we propagate null forward
  // rather than fabricating a value mid-day.
  essRemainingKwhStart: number | null
  // Cost-basis snapshot at the start of the hour, computed by
  // `costBasis.rollHour` over a 48-hour window (yesterday seeds
  // today). `essCostBasisUahStart` is the total UAH inside the
  // battery before this hour's charges/discharges; the avg
  // (UAH / kWh) is derived for display. `essWithdrawnCostUah` is
  // the cost basis removed this hour to back the discharges
  // (ESS→Load + ESS→Grid). `essRealizedProfitUah` is the ESS-side
  // cash effect of the hour: discharge revenue at spot prices
  // minus withdrawn cost minus degradation. PV→УЗЕ enters the
  // basis at 0 UAH (sunlight is free), Grid→УЗЕ at the hour's
  // import price (real cash spent). All four fields are null when
  // there's no cost-basis pipeline running for the hour (missing
  // RDN price for the hour, missing yesterday seed and seed
  // tariff = 0 with no charges yet, etc).
  essCostBasisUahStart: number | null
  essAvgCostUahPerKwhStart: number | null
  essWithdrawnCostUah: number | null
  essRealizedProfitUah: number | null
  // End-of-hour cost-basis snapshot. `*End` is the state AFTER
  // this hour's charges/discharges, i.e. the start state for the
  // next hour. Stored on the row so `dailyTotals` can pick the
  // last priced hour's End values as the EOD snapshot without
  // re-rolling the whole day. null when the row didn't run
  // through `rollHour` (missing RDN price for the hour, etc).
  essCostBasisUahEnd: number | null
  essAvgCostUahPerKwhEnd: number | null
  essResidualKwhEnd: number | null
}
