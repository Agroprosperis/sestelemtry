// costBasis tracks the running "how much money is sitting inside
// the УЗЕ" so each discharge can be matched against the price at
// which the energy was originally charged, instead of only the
// spot price of the discharge hour.
//
// The model is **Weighted Average Cost** (WAC): the battery state
// is two scalars — `kwh` and `uah` — and the average cost per kWh
// is `uah / kwh` whenever `kwh > 0`. Charges add to both scalars,
// discharges remove from `kwh` and from `uah` proportionally
// (`avgCost · discharged`). Energy is fungible inside the battery,
// so this is the most defensible model without queue bookkeeping.
//
// Pricing of charges (per ТЗ daily_economic_model_mvp.md and
// operator decision):
//   - PV → УЗЕ costs **0 UAH/kWh**. Sunlight is free; no cash
//     leaves the operator's pocket. PV inflow dilutes the existing
//     average cost as more kWh share the same UAH bucket.
//   - Mains → УЗЕ costs `importPrice` of the charge hour. Full
//     retail (RDN + distribution + transmission + … + VAT). This
//     is the "real cash spent" line.
//
// Round-trip losses are absorbed naturally: if the on-the-fly
// allocator reports `pvToEss + gridToEss > essToLoad + essToGrid`
// over a window, `kwh` rises by the smaller charged amount that
// actually entered the battery (the per-hour caller only feeds us
// the four directional flows, not the SmartLogger accumulator
// delta), so the average cost stays calibrated to what's
// physically left.
//
// State is intentionally caller-owned: `rollHour` returns the new
// state next to per-hour observables so a sequence of calls can
// compose into the day's hourly cost-basis curve without `this`
// or class wiring. See `useEconomicsData` for the consumer.

import type { HourFlows } from './compute'

// EssState is the running cost-basis pair tracked through every
// hour the battery is active. `kwh` is the residual energy in the
// battery as known to the cost-basis bookkeeper (it can drift
// from the SOC-driven `essRemainingKwhStart` when the on-the-fly
// allocator reports flows that don't line up exactly with the
// inverter accumulator; the difference is treated as round-trip
// loss). `uah` is the total UAH currently stored — divide by
// `kwh` for the per-kWh average.
export type EssState = {
  kwh: number
  uah: number
}

export const ZERO_ESS_STATE: EssState = Object.freeze({ kwh: 0, uah: 0 })

// HourCostBasis is the per-hour observable layer of the WAC roll.
// `avgCostStart` is the average cost in `prev` (i.e. before any
// charge or discharge in this hour); `avgCostEnd` is the average
// after `next`. `withdrawnCostUah` is the UAH removed from the
// state to back the discharges (ESS→Load + ESS→Grid) at
// `avgCostStart`. `realizedProfitUah` puts the discharge revenue
// (essToLoad·importPrice + essToGrid·exportPrice), the withdrawn
// cost, and degradation together into the project-effect line we
// surface in the KPI / table.
export type HourCostBasis = {
  prev: EssState
  next: EssState
  avgCostStartUahPerKwh: number
  avgCostEndUahPerKwh: number
  withdrawnCostUah: number
  realizedProfitUah: number
}

// rollHour applies one hour of charges and discharges to `prev`,
// using charge-then-discharge ordering: the discharge withdraws
// at the average cost that already accounts for the same hour's
// charges. This matches the typical inverter behaviour where
// charge and discharge legs interleave at sub-minute granularity
// and the reported aggregates over an hour represent net activity.
//
// `importPrice` and `exportPrice` are the FULL stack prices for
// the hour (already scaled by VAT / discounts upstream by
// `hourEconomics`). `degradationUahPerKwh` is the per-kWh wear
// proxy already used by `hourEconomics.essNet`; we keep it on the
// realized profit line so the two metrics can be compared
// apples-to-apples.
//
// Numerical guards:
//   - When `prev.kwh <= 0` the hour starts with no inventory; the
//     start-average is reported as 0 to avoid divide-by-zero, and
//     a discharge that occurs anyway (drift from the SOC track)
//     withdraws 0 UAH (the energy is treated as free from a
//     cost-basis perspective). That ensures the realized profit
//     never goes infinite.
//   - When `next.kwh <= 0` after the discharge (drained battery)
//     we re-anchor to `ZERO_ESS_STATE` so floating-point drift
//     can't accumulate into negative inventory.
export function rollHour(
  prev: EssState,
  flow: HourFlows,
  importPriceUahPerKwh: number,
  exportPriceUahPerKwh: number,
  degradationUahPerKwh: number,
): HourCostBasis {
  const avgCostStart = prev.kwh > 0 ? prev.uah / prev.kwh : 0

  // Charge leg: PV at zero cost (operator decision — sunshine is
  // not a paid input); grid at the import price stack.
  const chargedKwh = Math.max(flow.pvToEss, 0) + Math.max(flow.gridToEss, 0)
  const chargedUah = Math.max(flow.gridToEss, 0) * importPriceUahPerKwh
  const afterChargeKwh = prev.kwh + chargedKwh
  const afterChargeUah = prev.uah + chargedUah

  // Discharge leg: pull at the post-charge average so a busy hour
  // with simultaneous charge + discharge withdraws against the
  // freshly diluted price.
  const dischargedKwh = Math.max(flow.essToLoad, 0) + Math.max(flow.essToGrid, 0)
  const avgCostMid = afterChargeKwh > 0 ? afterChargeUah / afterChargeKwh : 0
  const withdrawnCostUah = avgCostMid * dischargedKwh

  let nextKwh = afterChargeKwh - dischargedKwh
  let nextUah = afterChargeUah - withdrawnCostUah
  if (nextKwh <= 0) {
    nextKwh = 0
    nextUah = 0
  } else if (nextUah < 0) {
    // Float noise can push us slightly below zero on the UAH side
    // even with non-zero kWh. Clamp without wiping kWh so the next
    // charge re-seeds a clean basis.
    nextUah = 0
  }
  const next: EssState = { kwh: nextKwh, uah: nextUah }
  const avgCostEnd = next.kwh > 0 ? next.uah / next.kwh : 0

  // Realized profit framing: discharge revenue (ESS→Load avoids an
  // import, ESS→Grid earns an export) minus the cost basis we just
  // withdrew, minus the wear charge. When prices haven't moved
  // since the charge, this collapses to ~0 as expected.
  const dischargeRevenueUah =
    Math.max(flow.essToLoad, 0) * importPriceUahPerKwh +
    Math.max(flow.essToGrid, 0) * exportPriceUahPerKwh
  const degradationUah = Math.max(flow.essDischarged, 0) * degradationUahPerKwh
  const realizedProfitUah = dischargeRevenueUah - withdrawnCostUah - degradationUah

  return {
    prev,
    next,
    avgCostStartUahPerKwh: avgCostStart,
    avgCostEndUahPerKwh: avgCostEnd,
    withdrawnCostUah,
    realizedProfitUah,
  }
}

