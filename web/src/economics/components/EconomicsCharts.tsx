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

const uahFormatter = new Intl.NumberFormat('uk-UA', {
  style: 'currency',
  currency: 'UAH',
  minimumFractionDigits: 0,
  maximumFractionDigits: 0,
})

function uahTickFormatter(v: number): string {
  return uahFormatter.format(v).replace('UAH', '').trim()
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
