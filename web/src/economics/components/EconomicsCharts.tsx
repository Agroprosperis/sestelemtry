import { useMemo } from 'react'
import {
  Bar,
  BarChart,
  CartesianGrid,
  Legend,
  Line,
  LineChart,
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

type PriceChartRow = {
  hour: number
  hourLabel: string
  rdn: number | null
  importPrice: number | null
  exportPrice: number | null
}

type CostChartRow = {
  hour: number
  hourLabel: string
  baseline: number | null
  actual: number | null
  effect: number | null
  essNet: number | null
}

const formatHourLabel = (hour: number) => `${String(hour).padStart(2, '0')}:00`

// uahFormatter / priceFormatter are reused by the Tooltip render
// callbacks. Defined at module level so each chart re-render doesn't
// allocate a new Intl instance per cell.
const uahFormatter = new Intl.NumberFormat('uk-UA', {
  style: 'currency',
  currency: 'UAH',
  minimumFractionDigits: 0,
  maximumFractionDigits: 0,
})

const priceFormatter = new Intl.NumberFormat('uk-UA', {
  style: 'currency',
  currency: 'UAH',
  minimumFractionDigits: 2,
  maximumFractionDigits: 2,
})

function priceTickFormatter(v: number): string {
  return priceFormatter.format(v).replace('UAH', '').trim()
}

function uahTickFormatter(v: number): string {
  return uahFormatter.format(v).replace('UAH', '').trim()
}

export function EconomicsCharts({ rows }: Props) {
  const priceData = useMemo<PriceChartRow[]>(
    () =>
      rows.map((row, idx) => ({
        hour: idx,
        hourLabel: formatHourLabel(idx),
        rdn: row?.rdnUahPerKwh ?? null,
        importPrice: row && row.rdnUahPerKwh !== null ? row.economics.importPriceUahPerKwh : null,
        exportPrice: row && row.rdnUahPerKwh !== null ? row.economics.exportPriceUahPerKwh : null,
      })),
    [rows],
  )

  const costData = useMemo<CostChartRow[]>(
    () =>
      rows.map((row, idx) => ({
        hour: idx,
        hourLabel: formatHourLabel(idx),
        baseline: row && row.rdnUahPerKwh !== null ? row.economics.baselineCost : null,
        actual: row && row.rdnUahPerKwh !== null ? row.economics.actualCost : null,
        effect: row && row.rdnUahPerKwh !== null ? row.economics.effect : null,
        essNet: row && row.rdnUahPerKwh !== null ? row.economics.essNet : null,
      })),
    [rows],
  )

  const tooltipPriceFormatter = (value: number) =>
    Number.isFinite(value) ? `${priceFormatter.format(value)}/кВт·год` : '—'
  const tooltipUahFormatter = (value: number) =>
    Number.isFinite(value) ? uahFormatter.format(value) : '—'

  return (
    <section className="economics-charts">
      <div className="chart-card">
        <h3>Ціни (РДН + повні тарифи)</h3>
        <ResponsiveContainer width="100%" height={260}>
          <LineChart data={priceData} margin={{ top: 16, right: 24, bottom: 8, left: 8 }}>
            <CartesianGrid stroke="#e2e8f0" strokeDasharray="3 3" />
            <XAxis dataKey="hourLabel" tick={{ fontSize: 12 }} />
            <YAxis tickFormatter={priceTickFormatter} tick={{ fontSize: 12 }} width={70} />
            <Tooltip formatter={(value) => tooltipPriceFormatter(Number(value))} />
            <Legend wrapperStyle={{ fontSize: 12 }} />
            <Line
              type="monotone"
              dataKey="rdn"
              name="РДН (sport)"
              stroke="#2563eb"
              strokeWidth={2}
              dot={false}
              connectNulls
            />
            <Line
              type="monotone"
              dataKey="importPrice"
              name="Імпорт (з тарифами)"
              stroke="#dc2626"
              strokeWidth={2}
              dot={false}
              connectNulls
            />
            <Line
              type="monotone"
              dataKey="exportPrice"
              name="Експорт"
              stroke="#16a34a"
              strokeWidth={2}
              dot={false}
              connectNulls
            />
          </LineChart>
        </ResponsiveContainer>
      </div>

      <div className="chart-card">
        <h3>Базова vs фактична вартість (по годинах)</h3>
        <ResponsiveContainer width="100%" height={260}>
          <BarChart data={costData} margin={{ top: 16, right: 24, bottom: 8, left: 8 }}>
            <CartesianGrid stroke="#e2e8f0" strokeDasharray="3 3" />
            <XAxis dataKey="hourLabel" tick={{ fontSize: 12 }} />
            <YAxis tickFormatter={uahTickFormatter} tick={{ fontSize: 12 }} width={70} />
            <Tooltip formatter={(value) => tooltipUahFormatter(Number(value))} />
            <Legend wrapperStyle={{ fontSize: 12 }} />
            <Bar dataKey="baseline" name="Базова" fill="#94a3b8" />
            <Bar dataKey="actual" name="Фактична" fill="#0ea5e9" />
          </BarChart>
        </ResponsiveContainer>
      </div>

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
