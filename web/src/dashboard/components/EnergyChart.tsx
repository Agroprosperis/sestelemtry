import { useCallback, useMemo } from 'react'
import {
  Bar,
  BarChart,
  CartesianGrid,
  ComposedChart,
  Legend,
  Line,
  ReferenceLine,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts'
import type { DashboardMetric } from '../../types'
import { energyColor } from '../colors'
import { formatChartNumber } from '../format'
import type { RangePreset } from '../range'
import type { EnergyRow } from '../transforms/buckets'
import type { DAMChartRow } from '../transforms/dam'
import type { EnergySummary as Summary } from '../transforms/summary'
import { EnergySummary } from './EnergySummary'
import { EnergyTooltip } from './EnergyTooltip'

type Props = {
  metrics: DashboardMetric[]
  series: EnergyRow[]
  preset: RangePreset
  summary: Summary
  loading: boolean
  damSeries?: DAMChartRow[]
}

const DAM_PRICE_KEY = 'dam_price_uah_per_mwh'
const DAM_PRICE_COLOR = '#0ea5e9'
const DAM_PRICE_LABEL = 'Ціна РДН'

// Day preset uses 5-minute buckets (288 per day); show every 12th tick so
// labels land on the hour and the axis stays readable.
const DAY_TICKS_PER_HOUR = 12

function xAxisInterval(preset: RangePreset): number {
  if (preset === 'day') return DAY_TICKS_PER_HOUR - 1
  if (preset === 'month') return 2
  return 0
}

export function EnergyChart({ metrics, series, preset, summary, loading, damSeries }: Props) {
  const tooltipContent = useCallback(
    (props: Omit<React.ComponentProps<typeof EnergyTooltip>, 'preset'>) => (
      <EnergyTooltip {...props} preset={preset} />
    ),
    [preset],
  )
  const tickInterval = xAxisInterval(preset)

  const dayData = useMemo(() => {
    if (preset !== 'day') return series
    const priceByTime = new Map<string, number | null>()
    for (const row of damSeries ?? []) {
      priceByTime.set(String(row.time), row.price)
    }
    return series.map((row) => {
      const price = priceByTime.get(String(row.time))
      return price != null && Number.isFinite(price)
        ? { ...row, [DAM_PRICE_KEY]: price }
        : row
    })
  }, [preset, series, damSeries])

  const dayTooltipFormatter = useCallback(
    (value: unknown, name: unknown): [string, string] => {
      const n = Number(value)
      const label = typeof name === 'string' ? name : String(name ?? '')
      if (label === DAM_PRICE_LABEL) {
        return [`${formatChartNumber(n)} грн/МВт·год`, DAM_PRICE_LABEL]
      }
      return [`${formatChartNumber(n)} kWh`, label]
    },
    [],
  )

  return (
    <div className="chart-card">
      <h2>Energy Trend</h2>
      <EnergySummary summary={summary} />
      <div className="chart-wrap">
        {loading ? (
          <p className="chart-placeholder">Loading...</p>
        ) : series.length === 0 ? (
          <p className="chart-placeholder">No data available for selected range.</p>
        ) : preset === 'day' ? (
          <ResponsiveContainer width="100%" height="100%">
            <ComposedChart data={dayData} barCategoryGap={0} barGap={0}>
              <CartesianGrid strokeDasharray="3 3" />
              <XAxis dataKey="time" interval={tickInterval} />
              <YAxis
                yAxisId="energy"
                tickFormatter={(v) => formatChartNumber(Number(v))}
              />
              <YAxis
                yAxisId="price"
                orientation="right"
                tickFormatter={(v) => formatChartNumber(Number(v))}
                tick={{ fill: DAM_PRICE_COLOR, fontSize: 11 }}
                axisLine={{ stroke: DAM_PRICE_COLOR, opacity: 0.4 }}
                tickLine={{ stroke: DAM_PRICE_COLOR, opacity: 0.4 }}
                width={48}
              />
              <Tooltip formatter={dayTooltipFormatter} />
              <Legend />
              <ReferenceLine y={0} yAxisId="energy" stroke="#64748b" />
              <Bar
                yAxisId="price"
                dataKey={DAM_PRICE_KEY}
                name={DAM_PRICE_LABEL}
                fill={DAM_PRICE_COLOR}
                fillOpacity={0.18}
                stroke="none"
                isAnimationActive={false}
              />
              {metrics.map((m) => (
                <Line
                  key={m.key}
                  yAxisId="energy"
                  type="monotone"
                  dataKey={m.key}
                  name={m.label}
                  dot={false}
                  stroke={energyColor(m.key, preset)}
                />
              ))}
            </ComposedChart>
          </ResponsiveContainer>
        ) : (
          <ResponsiveContainer width="100%" height="100%">
            <BarChart data={series} stackOffset="sign">
              <CartesianGrid strokeDasharray="3 3" />
              <XAxis dataKey="time" interval={tickInterval} />
              <YAxis tickFormatter={(v) => formatChartNumber(Number(v))} />
              <Tooltip content={tooltipContent} />
              <Legend />
              <ReferenceLine y={0} stroke="#64748b" />
              {metrics.map((m) => (
                <Bar
                  key={m.key}
                  dataKey={m.key}
                  name={m.label}
                  stackId="energy"
                  fill={energyColor(m.key, preset)}
                />
              ))}
            </BarChart>
          </ResponsiveContainer>
        )}
      </div>
    </div>
  )
}
