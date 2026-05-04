import { applyApplianceConsumptionRule } from './buckets'

// CumulativeReadings holds two cumulative-counter snapshots — the value at
// the moment just before the period started (seed) and at the period's
// right edge (end = min(endOfPeriod, now)). Each map is keyed by metric_key
// with the cumulative kWh value at that timestamp; missing keys are treated
// as zero, which yields the documented behaviour of using the lifetime
// counter as the total for fresh deployments without pre-period samples.
export type CumulativeReadings = {
  seed: Record<string, number>
  end: Record<string, number>
}

// summaryTotalsFromReadings derives the period total per metric as
// `end - seed`, clamped at zero (counter rollback / device restart), then
// runs the appliance-consumption fallback so a silent
// `accumulated_power_consumption_kwh` counter (some Huawei inverters never
// increment it) gets backfilled from the energy-balance identity instead
// of leaving the summary at zero.
export function summaryTotalsFromReadings(
  readings: CumulativeReadings,
  metricKeys: string[],
): Record<string, number> {
  const { seed, end } = readings
  const totals: Record<string, number> = {}
  for (const key of metricKeys) {
    const s = Number.isFinite(seed[key]) ? seed[key] : 0
    const e = Number.isFinite(end[key]) ? end[key] : s
    const diff = e - s
    totals[key] = diff < 0 ? 0 : diff
  }
  applyApplianceConsumptionRule(totals)
  return totals
}
