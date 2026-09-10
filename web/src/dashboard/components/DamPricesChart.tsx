import { useMemo } from 'react'
import {
  Bar,
  BarChart,
  CartesianGrid,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts'
import type { DAMPrice } from '../../types'
import { useChartChrome } from '../../theme/useChartChrome'
import type { RangePreset } from '../range'

type Props = {
  prices: DAMPrice[]
  preset: RangePreset
  loading: boolean
  error: string | null
}

type ChartRow = {
  label: string
  price: number | null
  count: number
}

function uahPerMwh(value: number): string {
  return new Intl.NumberFormat(undefined, {
    minimumFractionDigits: 0,
    maximumFractionDigits: 2,
  }).format(value)
}

function buildRowsForDay(prices: DAMPrice[]): ChartRow[] {
  const rows: ChartRow[] = []
  for (let h = 1; h <= 24; h++) {
    const row = prices.find((p) => p.hour === h)
    rows.push({
      label: `${String(h).padStart(2, '0')}:00`,
      price: row && typeof row.price_uah_per_mwh === 'number' ? row.price_uah_per_mwh : null,
      count: row ? 1 : 0,
    })
  }
  return rows
}

function buildDailyAverages(prices: DAMPrice[], preset: RangePreset): ChartRow[] {
  const byDate = new Map<string, { sum: number; n: number }>()
  for (const p of prices) {
    if (typeof p.price_uah_per_mwh !== 'number') continue
    const key = p.delivery_date.slice(0, 10)
    const acc = byDate.get(key) ?? { sum: 0, n: 0 }
    acc.sum += p.price_uah_per_mwh
    acc.n += 1
    byDate.set(key, acc)
  }
  const sorted = Array.from(byDate.entries()).sort(([a], [b]) => (a < b ? -1 : 1))
  return sorted.map(([key, acc]) => {
    const date = new Date(`${key}T00:00:00Z`)
    const label =
      preset === 'year'
        ? date.toLocaleDateString(undefined, { month: 'short', day: '2-digit', timeZone: 'UTC' })
        : date.toLocaleDateString(undefined, { day: '2-digit', month: 'short', timeZone: 'UTC' })
    return { label, price: acc.n > 0 ? acc.sum / acc.n : null, count: acc.n }
  })
}

function summarize(prices: DAMPrice[]) {
  let min = Infinity
  let max = -Infinity
  let sum = 0
  let n = 0
  for (const p of prices) {
    if (typeof p.price_uah_per_mwh !== 'number') continue
    min = Math.min(min, p.price_uah_per_mwh)
    max = Math.max(max, p.price_uah_per_mwh)
    sum += p.price_uah_per_mwh
    n += 1
  }
  if (n === 0) return null
  return { min, max, avg: sum / n, n }
}

export function DamPricesChart({ prices, preset, loading, error }: Props) {
  const chrome = useChartChrome()
  const rows = useMemo(
    () => (preset === 'day' ? buildRowsForDay(prices) : buildDailyAverages(prices, preset)),
    [prices, preset],
  )
  const summary = useMemo(() => summarize(prices), [prices])
  const title = preset === 'day' ? 'РДН: ціна по годинах (грн/МВт·год)' : 'РДН: середня ціна за добу (грн/МВт·год)'

  return (
    <div className="chart-card dam-card">
      <h2>{title}</h2>
      {summary && (
        <div className="dam-summary">
          <div>
            <span className="dam-summary-label">мін</span>
            <strong>{uahPerMwh(summary.min)}</strong>
          </div>
          <div>
            <span className="dam-summary-label">сер</span>
            <strong>{uahPerMwh(summary.avg)}</strong>
          </div>
          <div>
            <span className="dam-summary-label">макс</span>
            <strong>{uahPerMwh(summary.max)}</strong>
          </div>
        </div>
      )}
      <div className="chart-wrap dam-chart-wrap">
        {error ? (
          <p className="chart-placeholder">DAM: {error}</p>
        ) : loading && prices.length === 0 ? (
          <p className="chart-placeholder">Loading...</p>
        ) : prices.length === 0 ? (
          <p className="chart-placeholder">No DAM data for selected period.</p>
        ) : (
          <ResponsiveContainer width="100%" height="100%">
            <BarChart data={rows}>
              <CartesianGrid strokeDasharray="3 3" stroke={chrome.grid} />
              <XAxis dataKey="label" interval="preserveStartEnd" />
              <YAxis tickFormatter={(v) => uahPerMwh(Number(v))} width={70} />
              <Tooltip
                formatter={(v) => [`${uahPerMwh(Number(v))} грн/МВт·год`, 'Ціна']}
                labelFormatter={(label) => `${label}`}
              />
              <Bar dataKey="price" name="Ціна" fill="#0ea5e9" />
            </BarChart>
          </ResponsiveContainer>
        )}
      </div>
    </div>
  )
}
