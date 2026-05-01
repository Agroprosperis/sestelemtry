import {
  Area,
  AreaChart,
  CartesianGrid,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts'
import { formatChartNumber } from '../format'
import type { RangePreset } from '../range'
import type { EnergyRow } from '../transforms/buckets'
import type { DAMChartRow } from '../transforms/dam'
import { revenueChartRows, totalRevenue } from '../transforms/revenue'

type Props = {
  energySeries: EnergyRow[]
  damSeries: DAMChartRow[]
  preset: RangePreset
}

const REVENUE_LINE_COLOR = '#16a34a'
const REVENUE_FILL_COLOR = '#86efac'

const PRESET_LABEL: Record<RangePreset, string> = {
  day: 'погодинно',
  month: 'за день',
  year: 'за місяць',
}

function xAxisInterval(preset: RangePreset): number {
  if (preset === 'day') return 1
  if (preset === 'month') return 2
  return 0
}

export function RevenueChart({ energySeries, damSeries, preset }: Props) {
  const series = revenueChartRows(energySeries, damSeries)
  const hasAnyValue = series.some((r) => r.revenue != null)
  const total = totalRevenue(series)
  const tickInterval = xAxisInterval(preset)
  return (
    <div className="chart-card">
      <div className="dam-chart-head">
        <h2>Дохід від ФЕ (оцінка за РДН)</h2>
        <span className="dam-chart-meta">
          {PRESET_LABEL[preset]}
          {hasAnyValue && (
            <>
              {' · '}
              <strong>разом {formatChartNumber(total)} грн</strong>
            </>
          )}
        </span>
      </div>
      <div className="chart-wrap">
        {!hasAnyValue ? (
          <p className="chart-placeholder">Немає даних для розрахунку доходу.</p>
        ) : (
          <ResponsiveContainer width="100%" height="100%">
            <AreaChart data={series}>
              <defs>
                <linearGradient id="revenue-fill" x1="0" y1="0" x2="0" y2="1">
                  <stop offset="0%" stopColor={REVENUE_FILL_COLOR} stopOpacity={0.7} />
                  <stop offset="100%" stopColor={REVENUE_FILL_COLOR} stopOpacity={0.05} />
                </linearGradient>
              </defs>
              <CartesianGrid strokeDasharray="3 3" />
              <XAxis dataKey="time" interval={tickInterval} />
              <YAxis tickFormatter={(v) => formatChartNumber(Number(v))} />
              <Tooltip
                formatter={(v) => [`${formatChartNumber(Number(v))} грн`, 'Дохід від ФЕ']}
                labelFormatter={(label) => String(label)}
              />
              <Area
                type="monotone"
                dataKey="revenue"
                name="Дохід від ФЕ"
                stroke={REVENUE_LINE_COLOR}
                strokeWidth={2}
                fill="url(#revenue-fill)"
                dot={false}
                connectNulls
              />
            </AreaChart>
          </ResponsiveContainer>
        )}
      </div>
    </div>
  )
}
