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
  pvConsumedPct: number
  pvExportPct: number
  loadFromPVPct: number
  loadFromBatteryPct: number
  loadFromGridPct: number
  selfSufficiencyPct: number
}

export function energySummaryFromSeries(series: EnergyRow[]): EnergySummary {
  const pvProduced = sumSeriesValue(series, 'pv_energy_yield_day_kwh', 'positive')
  const gridExport = sumSeriesValue(series, 'accumulated_electricity_sold_kwh', 'absolute')
  const pvConsumed = Math.max(pvProduced - gridExport, 0)
  const consumption = sumSeriesValue(series, 'accumulated_power_consumption_kwh', 'absolute')
  const fromGrid = sumSeriesValue(series, 'accumulated_electricity_purchased_kwh', 'positive')

  const charge = sumSeriesValue(series, 'total_energy_charged_kwh', 'absolute')
  const discharge = sumSeriesValue(series, 'total_energy_discharged_kwh', 'absolute')
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
    pvConsumedPct,
    pvExportPct,
    loadFromPVPct,
    loadFromBatteryPct,
    loadFromGridPct,
    selfSufficiencyPct,
  }
}
