import { useMemo } from 'react'
import {
  Bar,
  BarChart,
  CartesianGrid,
  Legend,
  ReferenceLine,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts'
import type { HourEconomicsRow } from '../compute'

type Props = {
  rows: Array<HourEconomicsRow | null>
}

type CostChartRow = {
  hour: number
  hourLabel: string
  essNet: number | null
}

const formatHourLabel = (hour: number) => `${String(hour).padStart(2, '0')}:00`

// pickFractionDigits adapts UAH precision to the chart's value
// range. Without this, an off-peak day where the УЗЕ contributes a
// few hryvnias gets rounded to all-zeros — the bars stay visible
// but every Y-axis tick and tooltip prints "0 ₴", making the chart
// look broken when it isn't. Two decimals for sub-1 ₴ values, one
// decimal for 1..9 ₴, and integers from 10 ₴ upward keeps the
// labels readable across day-types without retuning per fixture.
function pickFractionDigits(maxAbs: number): number {
  if (maxAbs < 1) return 2
  if (maxAbs < 10) return 1
  return 0
}

export function EconomicsCharts({ rows }: Props) {
  const costData = useMemo<CostChartRow[]>(
    () =>
      rows.map((row, idx) => ({
        hour: idx,
        hourLabel: formatHourLabel(idx),
        essNet: row && row.rdnUahPerKwh !== null ? row.economics.essNet : null,
      })),
    [rows],
  )

  // Drive Y-axis + tooltip precision from the largest hour magnitude
  // shown on the chart so every series gets the same number of
  // decimals (avoids "0 ₴" labels next to non-zero bars).
  const maxAbs = useMemo(() => {
    let m = 0
    for (const row of costData) {
      if (row.essNet === null || !Number.isFinite(row.essNet)) continue
      const a = Math.abs(row.essNet)
      if (a > m) m = a
    }
    return m
  }, [costData])

  const uahFormatter = useMemo(() => {
    const fractionDigits = pickFractionDigits(maxAbs)
    return new Intl.NumberFormat('uk-UA', {
      style: 'currency',
      currency: 'UAH',
      minimumFractionDigits: fractionDigits,
      maximumFractionDigits: fractionDigits,
    })
  }, [maxAbs])

  const uahTickFormatter = (v: number): string =>
    uahFormatter.format(v).replace('UAH', '').trim()

  const tooltipUahFormatter = (value: number) =>
    Number.isFinite(value) ? uahFormatter.format(value) : '—'

  return (
    <section className="economics-charts">
      <div className="chart-card">
        <h3>Чистий ефект УЗЕ (по годинах)</h3>
        <ResponsiveContainer width="100%" height={260}>
          <BarChart data={costData} margin={{ top: 16, right: 24, bottom: 8, left: 8 }}>
            <CartesianGrid stroke="#e2e8f0" strokeDasharray="3 3" />
            <XAxis dataKey="hourLabel" tick={{ fontSize: 12 }} />
            <YAxis tickFormatter={uahTickFormatter} tick={{ fontSize: 12 }} width={70} />
            <Tooltip formatter={(value) => tooltipUahFormatter(Number(value))} />
            <ReferenceLine y={0} stroke="#475569" strokeWidth={1} />
            <Legend wrapperStyle={{ fontSize: 12 }} />
            <Bar dataKey="essNet" name="ess_net_effect" fill="#7c3aed" />
          </BarChart>
        </ResponsiveContainer>
      </div>
    </section>
  )
}
