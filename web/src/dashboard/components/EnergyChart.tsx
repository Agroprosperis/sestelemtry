import { useCallback, useMemo } from 'react'
import {
  Bar,
  BarChart,
  CartesianGrid,
  ComposedChart,
  Legend,
  Line,
  ReferenceArea,
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

type HourlyDamArea = { x1: string; x2: string; price: number }

// hourlyDamAreas turns per-5-minute damSeries into one block per hour for
// rendering as ReferenceArea overlays. x1 is the hour-start label ("HH:00"),
// x2 is the next hour-start label (or the last timeline label for hour 23).
function hourlyDamAreas(
  damSeries: DAMChartRow[] | undefined,
  timelineLabels: string[],
): HourlyDamArea[] {
  if (!damSeries || damSeries.length === 0 || timelineLabels.length === 0) return []
  const priceAtLabel = new Map<string, number>()
  for (const r of damSeries) {
    if (r.price == null || !Number.isFinite(r.price)) continue
    priceAtLabel.set(String(r.time), r.price)
  }
  const lastLabel = timelineLabels[timelineLabels.length - 1]
  const out: HourlyDamArea[] = []
  for (let hour = 0; hour < 24; hour++) {
    const label = `${String(hour).padStart(2, '0')}:00`
    const price = priceAtLabel.get(label)
    if (price == null || !Number.isFinite(price)) continue
    const nextLabel =
      hour < 23 ? `${String(hour + 1).padStart(2, '0')}:00` : lastLabel
    out.push({ x1: label, x2: nextLabel, price })
  }
  return out
}

function damPriceDomain(areas: HourlyDamArea[]): [number, number] {
  if (areas.length === 0) return [0, 0]
  let max = 0
  for (const a of areas) if (a.price > max) max = a.price
  return [0, Math.ceil(max * 1.1)]
}

export function EnergyChart({ metrics, series, preset, summary, loading, damSeries }: Props) {
  const tooltipContent = useCallback(
    (props: Omit<React.ComponentProps<typeof EnergyTooltip>, 'preset'>) => (
      <EnergyTooltip {...props} preset={preset} />
    ),
    [preset],
  )
  const tickInterval = xAxisInterval(preset)

  // Day preset carries the current hourly DAM price on every 5-minute row so
  // the tooltip surfaces the containing hour's price at any hover position.
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

  const hourlyAreas = useMemo(
    () => hourlyDamAreas(damSeries, series.map((r) => String(r.time))),
    [damSeries, series],
  )
  const priceDomain = useMemo(() => damPriceDomain(hourlyAreas), [hourlyAreas])
  const hasDam = hourlyAreas.length > 0

  const dayTickFormatter = useCallback((v: unknown): string => {
    const s = String(v)
    const idx = s.indexOf(':')
    return idx > 0 ? s.slice(0, idx) : s
  }, [])


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
            <ComposedChart data={dayData}>
              <CartesianGrid strokeDasharray="3 3" />
              <XAxis
                dataKey="time"
                interval={tickInterval}
                tickFormatter={dayTickFormatter}
              />
              <YAxis
                yAxisId="energy"
                tickFormatter={(v) => formatChartNumber(Number(v))}
              />
              <YAxis
                yAxisId="price"
                orientation="right"
                domain={priceDomain}
                tickFormatter={(v) => formatChartNumber(Number(v))}
                tick={{ fill: DAM_PRICE_COLOR, fontSize: 11 }}
                axisLine={{ stroke: DAM_PRICE_COLOR, opacity: 0.4 }}
                tickLine={{ stroke: DAM_PRICE_COLOR, opacity: 0.4 }}
                width={48}
                hide={!hasDam}
              />
              <Tooltip content={tooltipContent} />
              <Legend />
              {hourlyAreas.map((a, i) => (
                <ReferenceArea
                  key={`dam-${i}`}
                  yAxisId="price"
                  x1={a.x1}
                  x2={a.x2}
                  y1={0}
                  y2={a.price}
                  fill={DAM_PRICE_COLOR}
                  fillOpacity={0.18}
                  stroke="none"
                  ifOverflow="visible"
                />
              ))}
              <ReferenceLine y={0} yAxisId="energy" stroke="#64748b" />
              {/* Invisible bar carries "Ціна РДН" into the legend + tooltip
                  without drawing any pixels (ReferenceArea handles the
                  hourly visual). */}
              <Bar
                yAxisId="price"
                dataKey={DAM_PRICE_KEY}
                name={DAM_PRICE_LABEL}
                fill={DAM_PRICE_COLOR}
                fillOpacity={0}
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
