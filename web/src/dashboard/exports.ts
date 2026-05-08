import type { RangePreset } from './range'
import type { EnergyRow } from './transforms/buckets'
import type { DAMChartRow } from './transforms/dam'
import type { PowerChartRow } from './transforms/power'
import type { PvForecastHourlyRow } from './transforms/pvForecast'
import type { RevenueChartRow } from './transforms/revenue'
import type { SOCChartRow } from './transforms/soc'

export type ExportTable = {
  headers: string[]
  rows: Array<Record<string, unknown>>
}

const DAY_ENERGY_HEADERS = [
  'time',
  'active_pv_power_kw',
  'active_ess_power_kw',
  'grid_connected_active_power_kw',
  'load_power_kw',
  'soc_percent',
  'dam_price_uah_per_mwh',
  'planned_ac_kw_forecast',
] as const

function numOrNull(v: unknown): number | null {
  return typeof v === 'number' && Number.isFinite(v) ? v : null
}

function hourFromLabel(label: string): number | null {
  // Day-preset labels look like "HH:MM"; pull HH out and validate it.
  const m = /^(\d{2}):/.exec(label)
  if (!m) return null
  const h = parseInt(m[1], 10)
  return Number.isFinite(h) && h >= 0 && h <= 23 ? h : null
}

// buildEnergyExport collapses the union of series feeding the day chart
// (or the energy bucket-delta series for month/year) into a flat CSV-ready
// table. Day rows are 5-minute buckets with each layer joined back onto
// the matching `time` label, plus the hourly forecast value broadcast across
// every bucket of its hour so the row is independently meaningful.
export function buildEnergyExport(input: {
  preset: RangePreset
  energySeries: EnergyRow[]
  powerSeries?: PowerChartRow[]
  damSeries?: DAMChartRow[]
  socSeries?: SOCChartRow[]
  pvForecastSeries?: PvForecastHourlyRow[]
}): ExportTable {
  if (input.preset === 'day') {
    const damByHour = new Map<number, number>()
    for (const r of input.damSeries ?? []) {
      if (r.price == null || !Number.isFinite(r.price)) continue
      const h = hourFromLabel(String(r.time))
      if (h !== null && !damByHour.has(h)) damByHour.set(h, r.price)
    }
    const forecastByHour = new Map<number, number>()
    for (const r of input.pvForecastSeries ?? []) {
      if (Number.isFinite(r.plannedKw)) forecastByHour.set(r.hour, r.plannedKw)
    }
    const socByTime = new Map<string, number>()
    for (const r of input.socSeries ?? []) {
      if (r.soc != null && Number.isFinite(r.soc)) socByTime.set(String(r.time), r.soc)
    }

    const rows = (input.powerSeries ?? []).map((r) => {
      const time = String(r.time)
      const hour = hourFromLabel(time)
      return {
        time,
        active_pv_power_kw: numOrNull(r.active_pv_power_kw),
        active_ess_power_kw: numOrNull(r.active_ess_power_kw),
        grid_connected_active_power_kw: numOrNull(r.grid_connected_active_power_kw),
        load_power_kw: numOrNull(r.load_power_kw),
        soc_percent: numOrNull(socByTime.get(time)),
        dam_price_uah_per_mwh: hour !== null ? (damByHour.get(hour) ?? null) : null,
        planned_ac_kw_forecast: hour !== null ? (forecastByHour.get(hour) ?? null) : null,
      }
    })
    return { headers: [...DAY_ENERGY_HEADERS], rows }
  }

  // Month / year preset: each EnergyRow is a single bucket-delta record
  // already keyed by the metric column names. Project the union of keys
  // (sorted) so the column order is deterministic across exports.
  const keys = new Set<string>()
  for (const row of input.energySeries) {
    for (const k of Object.keys(row)) {
      if (k !== 'time') keys.add(k)
    }
  }
  const sortedKeys = Array.from(keys).sort()
  const rows = input.energySeries.map((r) => {
    const out: Record<string, unknown> = { time: String(r.time) }
    for (const k of sortedKeys) {
      const v = (r as Record<string, unknown>)[k]
      out[k] = numOrNull(v)
    }
    return out
  })
  return { headers: ['time', ...sortedKeys], rows }
}

// buildRevenueExport mirrors the Revenue chart: one row per timeline
// bucket with the time label and the per-bucket revenue estimate in UAH.
export function buildRevenueExport(input: {
  series: RevenueChartRow[]
}): ExportTable {
  const rows = input.series.map((r) => ({
    time: r.time,
    revenue_uah: numOrNull(r.revenue),
  }))
  return { headers: ['time', 'revenue_uah'], rows }
}

// csvFilename returns a safe, deterministic filename for an export. The
// suffix combines preset and anchor day so multiple downloads from the
// same browser session don't overwrite each other.
export function csvFilename(input: {
  chart: 'energy' | 'revenue'
  organizationID: string
  preset: RangePreset
  anchor: Date
}): string {
  const y = input.anchor.getFullYear()
  const m = String(input.anchor.getMonth() + 1).padStart(2, '0')
  const d = String(input.anchor.getDate()).padStart(2, '0')
  const safeOrg = input.organizationID.replace(/[^A-Za-z0-9_-]+/g, '_')
  return `${input.chart}_${safeOrg}_${input.preset}_${y}-${m}-${d}.csv`
}
