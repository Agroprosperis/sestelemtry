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
  pvConsumedPct: number
  pvExportPct: number
  loadFromPVPct: number
  loadFromGridPct: number
}

export function energySummaryFromSeries(series: EnergyRow[]): EnergySummary {
  const pvProduced = sumSeriesValue(series, 'pv_energy_yield_day_kwh', 'positive')
  const gridExport = sumSeriesValue(series, 'accumulated_electricity_sold_kwh', 'absolute')
  const pvConsumed = Math.max(pvProduced - gridExport, 0)
  const consumption = sumSeriesValue(series, 'accumulated_power_consumption_kwh', 'absolute')
  const fromGrid = sumSeriesValue(series, 'accumulated_electricity_purchased_kwh', 'positive')
  const fromPV = Math.max(consumption - fromGrid, 0)

  const pvConsumedPct = pvProduced > 0 ? (pvConsumed / pvProduced) * 100 : 0
  const pvExportPct = pvProduced > 0 ? (gridExport / pvProduced) * 100 : 0
  const loadFromPVPct = consumption > 0 ? (fromPV / consumption) * 100 : 0
  const loadFromGridPct = consumption > 0 ? (fromGrid / consumption) * 100 : 0

  return {
    pvProduced,
    gridExport,
    pvConsumed,
    consumption,
    fromGrid,
    fromPV,
    pvConsumedPct,
    pvExportPct,
    loadFromPVPct,
    loadFromGridPct,
  }
}
