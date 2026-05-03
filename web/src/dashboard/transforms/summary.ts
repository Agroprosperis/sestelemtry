import type { EnergyRow } from './buckets'

export type SumMode = 'positive' | 'absolute'

export function sumSeriesValue(series: EnergyRow[], metricKey: string, mode: SumMode): number {
  return series.reduce((sum, row) => {
    const raw = Number(row[metricKey] ?? 0)
    if (!Number.isFinite(raw)) return sum
    if (mode === 'positive') return sum + Math.max(raw, 0)
    return sum + Math.abs(raw)
  }, 0)
}

export type EnergySummary = {
  pvProduced: number
  gridExport: number
  pvConsumed: number
  consumption: number
  fromGrid: number
  fromPV: number
  fromBattery: number
  // batteryCharged / batteryDischarged are the raw absolute totals fed
  // through the storage in the period. fromBattery (above) is what the
  // load actually consumed from the battery (net discharge minus what
  // went back to charge); the two raw totals stay separately exposed
  // because the narrative summary needs to show charge / discharge
  // independently.
  batteryCharged: number
  batteryDischarged: number
  pvConsumedPct: number
  pvExportPct: number
  loadFromPVPct: number
  loadFromBatteryPct: number
  loadFromGridPct: number
  selfSufficiencyPct: number
}

// energySummaryFromTotals derives the dashboard summary from a precomputed
// {metric_key: total_for_period} map. Used by the cumulative path
// (month/year) where totals come straight from `last(end) - last(start)`
// and don't need to be reconstructed by summing 30+ per-bucket deltas. Each
// total is treated as positive energy in the metric's natural direction
// (sources stay positive, sinks are absolute) so the allocation math below
// works regardless of sign conventions in upstream callers.
export function energySummaryFromTotals(totals: Record<string, number>): EnergySummary {
  const pvProduced = Math.max(totals.accumulated_pv_energy_yield_kwh ?? 0, 0)
  const gridExport = Math.abs(totals.accumulated_electricity_sold_kwh ?? 0)
  const pvConsumed = Math.max(pvProduced - gridExport, 0)
  const consumption = Math.abs(totals.accumulated_power_consumption_kwh ?? 0)
  const fromGrid = Math.max(totals.accumulated_electricity_purchased_kwh ?? 0, 0)

  const charge = Math.abs(totals.total_energy_charged_kwh ?? 0)
  const discharge = Math.abs(totals.total_energy_discharged_kwh ?? 0)
  const batteryNet = Math.max(discharge - charge, 0)

  // Allocate consumption among sources: trust measured grid purchase first (it
  // matches the chart's grid bar), then battery net discharge (matches the
  // discharge bar minus charge bar), and treat the remainder as PV-to-load.
  // Sum of the three rows equals consumption by construction.
  const fromGridUsed = Math.min(fromGrid, consumption)
  const remainingAfterGrid = Math.max(consumption - fromGridUsed, 0)
  const fromBattery = Math.min(batteryNet, remainingAfterGrid)
  const fromPV = Math.max(remainingAfterGrid - fromBattery, 0)

  const pvConsumedPct = pvProduced > 0 ? (pvConsumed / pvProduced) * 100 : 0
  const pvExportPct = pvProduced > 0 ? (gridExport / pvProduced) * 100 : 0
  const loadFromPVPct = consumption > 0 ? (fromPV / consumption) * 100 : 0
  const loadFromBatteryPct = consumption > 0 ? (fromBattery / consumption) * 100 : 0
  const loadFromGridPct = consumption > 0 ? (fromGridUsed / consumption) * 100 : 0
  const selfSufficiencyPct = loadFromPVPct + loadFromBatteryPct

  return {
    pvProduced,
    gridExport,
    pvConsumed,
    consumption,
    fromGrid: fromGridUsed,
    fromPV,
    fromBattery,
    batteryCharged: charge,
    batteryDischarged: discharge,
    pvConsumedPct,
    pvExportPct,
    loadFromPVPct,
    loadFromBatteryPct,
    loadFromGridPct,
    selfSufficiencyPct,
  }
}

export function energySummaryFromSeries(series: EnergyRow[]): EnergySummary {
  return energySummaryFromTotals({
    accumulated_pv_energy_yield_kwh: sumSeriesValue(series, 'accumulated_pv_energy_yield_kwh', 'positive'),
    accumulated_electricity_sold_kwh: sumSeriesValue(series, 'accumulated_electricity_sold_kwh', 'absolute'),
    accumulated_power_consumption_kwh: sumSeriesValue(series, 'accumulated_power_consumption_kwh', 'absolute'),
    accumulated_electricity_purchased_kwh: sumSeriesValue(series, 'accumulated_electricity_purchased_kwh', 'positive'),
    total_energy_charged_kwh: sumSeriesValue(series, 'total_energy_charged_kwh', 'absolute'),
    total_energy_discharged_kwh: sumSeriesValue(series, 'total_energy_discharged_kwh', 'absolute'),
  })
}
