import type { EconomicsAnnualMonthRollup, EconomicsMonthlyDay } from '../../api'

// TrendRow is one point of the month / year energy trend, in MWh: the
// consumption, the three sources that covered it (PV, УЗЕ and grid sum
// to load), and the two export flows. key is the day (YYYY-MM-DD) or
// month (YYYY-MM), unique on the axis — recharts resolves a hovered
// category to the first row carrying it. Values are null where the
// period has no data (future days, telemetry gaps), so the lines break
// instead of diving to zero.
export type TrendRow = {
  key: string
  load: number | null
  pv: number | null
  ess: number | null
  grid: number | null
  pvExport: number | null
  essExport: number | null
}

export type TrendSeriesKey = Exclude<keyof TrendRow, 'key'>

type Flows = Pick<
  EconomicsMonthlyDay,
  | 'load_kwh'
  | 'pv_to_load_kwh'
  | 'ess_to_load_kwh'
  | 'grid_to_load_kwh'
  | 'pv_to_grid_kwh'
  | 'ess_to_grid_kwh'
  | 'hours_with_data'
>

function pad(n: number): string {
  return n < 10 ? `0${n}` : String(n)
}

function toRow(key: string, f: Flows | undefined): TrendRow {
  if (!f || !(f.hours_with_data > 0)) {
    return { key, load: null, pv: null, ess: null, grid: null, pvExport: null, essExport: null }
  }
  return {
    key,
    load: f.load_kwh / 1000,
    pv: f.pv_to_load_kwh / 1000,
    ess: f.ess_to_load_kwh / 1000,
    grid: f.grid_to_load_kwh / 1000,
    pvExport: f.pv_to_grid_kwh / 1000,
    essExport: f.ess_to_grid_kwh / 1000,
  }
}

// monthTrendRows lays the economics month rollup over every calendar
// day of the anchor's month.
export function monthTrendRows(days: EconomicsMonthlyDay[], anchor: Date): TrendRow[] {
  const year = anchor.getFullYear()
  const month = anchor.getMonth()
  const daysInMonth = new Date(year, month + 1, 0).getDate()
  const byDate = new Map(days.map((d) => [d.date, d]))
  return Array.from({ length: daysInMonth }, (_, i) => {
    const key = `${year}-${pad(month + 1)}-${pad(i + 1)}`
    return toRow(key, byDate.get(key))
  })
}

// yearTrendRows lays the economics year rollup over the twelve months.
export function yearTrendRows(months: EconomicsAnnualMonthRollup[], year: number): TrendRow[] {
  const byMonth = new Map(months.map((m) => [m.month, m.totals]))
  return Array.from({ length: 12 }, (_, i) => {
    const key = `${year}-${pad(i + 1)}`
    return toRow(key, byMonth.get(key))
  })
}
