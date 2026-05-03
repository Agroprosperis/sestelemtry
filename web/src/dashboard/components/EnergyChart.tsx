import { useCallback, useMemo } from 'react'
import {
  Area,
  Bar,
  BarChart,
  CartesianGrid,
  ComposedChart,
  Legend,
  ReferenceArea,
  ReferenceLine,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts'
import type { DashboardMetric } from '../../types'
import { dayPowerColor, energyColor } from '../colors'
import { formatChartNumber } from '../format'
import { DAY_POWER_METRIC_KEYS, DAY_POWER_METRIC_LABELS } from '../metrics'
import type { RangePreset } from '../range'
import type { EnergyRow } from '../transforms/buckets'
import type { DAMChartRow } from '../transforms/dam'
import type { PowerChartRow } from '../transforms/power'
import type { SOCChartRow } from '../transforms/soc'
import type { EnergySummary as Summary } from '../transforms/summary'
import { EnergySummary } from './EnergySummary'
import { EnergyTooltip } from './EnergyTooltip'
import { PowerTooltip } from './PowerTooltip'

type Props = {
  metrics: DashboardMetric[]
  series: EnergyRow[]
  preset: RangePreset
  summary: Summary
  loading: boolean
  damSeries?: DAMChartRow[]
  socSeries?: SOCChartRow[]
  powerSeries?: PowerChartRow[]
}

const DAM_PRICE_KEY = 'dam_price_uah_per_mwh'
const DAM_PRICE_COLOR = '#0ea5e9'
const DAM_PRICE_LABEL = 'Ціна РДН'
const SOC_KEY = 'soc_percent'
const SOC_COLOR = '#a855f7'
const SOC_LABEL = 'SOC'

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

export function EnergyChart({
  metrics,
  series,
  preset,
  summary,
  loading,
  damSeries,
  socSeries,
  powerSeries,
}: Props) {
  const energyTooltip = useCallback(
    (props: Omit<React.ComponentProps<typeof EnergyTooltip>, 'preset'>) => (
      <EnergyTooltip {...props} preset={preset} />
    ),
    [preset],
  )
  const powerTooltip = useCallback(
    (props: React.ComponentProps<typeof PowerTooltip>) => <PowerTooltip {...props} />,
    [],
  )
  const tickInterval = xAxisInterval(preset)

  // Day preset draws three instantaneous power lines (kW snapshots from
  // powerSeries) plus the DAM price hourly bands and the SOC band overlay.
  // We merge the price/SOC values onto each power row by `time` so Recharts
  // can align all three layers on the same x-axis and surface them in the
  // tooltip without separate lookups.
  const dayData = useMemo<PowerChartRow[]>(() => {
    if (preset !== 'day') return []
    const rows = powerSeries ?? []
    const priceByTime = new Map<string, number | null>()
    for (const row of damSeries ?? []) priceByTime.set(String(row.time), row.price)
    const socByTime = new Map<string, number | null>()
    for (const row of socSeries ?? []) socByTime.set(String(row.time), row.soc)
    return rows.map((row) => {
      const merged: PowerChartRow = { ...row }
      const price = priceByTime.get(String(row.time))
      if (price != null && Number.isFinite(price)) merged[DAM_PRICE_KEY] = price
      const soc = socByTime.get(String(row.time))
      if (soc != null && Number.isFinite(soc)) merged[SOC_KEY] = soc
      return merged
    })
  }, [preset, powerSeries, damSeries, socSeries])
  const hasSoc = (socSeries ?? []).some((r) => r.soc != null && Number.isFinite(r.soc))

  const dayLabels = useMemo(() => dayData.map((r) => String(r.time)), [dayData])
  const hourlyAreas = useMemo(() => hourlyDamAreas(damSeries, dayLabels), [damSeries, dayLabels])
  const priceDomain = useMemo(() => damPriceDomain(hourlyAreas), [hourlyAreas])

  const dayHasData = dayData.some((row) =>
    DAY_POWER_METRIC_KEYS.some((k) => {
      const v = row[k]
      return typeof v === 'number' && Number.isFinite(v)
    }),
  )

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
        ) : preset === 'day' ? (
          !dayHasData ? (
            <p className="chart-placeholder">No data available for selected range.</p>
          ) : (
            <ResponsiveContainer width="100%" height="100%">
              <ComposedChart data={dayData}>
                <CartesianGrid strokeDasharray="3 3" />
                <XAxis
                  dataKey="time"
                  interval={tickInterval}
                  tickFormatter={dayTickFormatter}
                />
                <YAxis
                  yAxisId="power"
                  tickFormatter={(v) => formatChartNumber(Number(v))}
                />
                <YAxis
                  yAxisId="soc"
                  orientation="right"
                  domain={[0, 100]}
                  tickFormatter={(v) => `${v}%`}
                  tick={{ fill: SOC_COLOR, fontSize: 11 }}
                  axisLine={{ stroke: SOC_COLOR, opacity: 0.4 }}
                  tickLine={{ stroke: SOC_COLOR, opacity: 0.4 }}
                  width={48}
                />
                <YAxis
                  yAxisId="price"
                  orientation="right"
                  domain={priceDomain}
                  hide
                  width={0}
                />
                <Tooltip
                  content={powerTooltip}
                  offset={12}
                  allowEscapeViewBox={{ x: false, y: true }}
                  wrapperStyle={{ pointerEvents: 'none', zIndex: 5 }}
                  isAnimationActive={false}
                  cursor={{ stroke: '#94a3b8', strokeDasharray: '3 3' }}
                />
                <Legend wrapperStyle={{ fontSize: 12 }} />
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
                    label={{
                      value: (a.price / 1000).toFixed(1),
                      position: 'insideTop',
                      fill: DAM_PRICE_COLOR,
                      fontSize: 11,
                      fontWeight: 600,
                    }}
                  />
                ))}
                <ReferenceLine y={0} yAxisId="power" stroke="#64748b" />
                {DAY_POWER_METRIC_KEYS.map((key) => {
                  const color = dayPowerColor(key)
                  return (
                    <Area
                      key={key}
                      yAxisId="power"
                      type="monotone"
                      dataKey={key}
                      name={DAY_POWER_METRIC_LABELS[key] ?? key}
                      stroke={color}
                      strokeWidth={2}
                      fill={color}
                      fillOpacity={0.18}
                      baseValue={0}
                      dot={false}
                      connectNulls={false}
                      isAnimationActive={false}
                    />
                  )
                })}
                {/* Invisible bar carries "Ціна РДН" into the legend + tooltip
                    without drawing any pixels (ReferenceArea handles the
                    hourly visual). Placed after power Areas so the legend
                    lists DAM after the power lines. */}
                <Bar
                  yAxisId="price"
                  dataKey={DAM_PRICE_KEY}
                  name={DAM_PRICE_LABEL}
                  fill={DAM_PRICE_COLOR}
                  fillOpacity={0}
                  stroke="none"
                  isAnimationActive={false}
                />
                {/* SOC Area is rendered last so it appears at the end of the
                    legend; its fill is kept very translucent (0.12) so it
                    reads as a background tint over the power areas instead
                    of obscuring them. */}
                {hasSoc && (
                  <Area
                    yAxisId="soc"
                    type="monotone"
                    dataKey={SOC_KEY}
                    name={SOC_LABEL}
                    stroke="none"
                    fill={SOC_COLOR}
                    fillOpacity={0.12}
                    isAnimationActive={false}
                    connectNulls
                  />
                )}
              </ComposedChart>
            </ResponsiveContainer>
          )
        ) : series.length === 0 ? (
          <p className="chart-placeholder">No data available for selected range.</p>
        ) : (
          <ResponsiveContainer width="100%" height="100%">
            <BarChart data={series} stackOffset="sign">
              <CartesianGrid strokeDasharray="3 3" />
              <XAxis dataKey="time" interval={tickInterval} />
              <YAxis tickFormatter={(v) => formatChartNumber(Number(v))} />
              <Tooltip
                content={energyTooltip}
                offset={12}
                allowEscapeViewBox={{ x: false, y: true }}
                wrapperStyle={{ pointerEvents: 'none', zIndex: 5 }}
                isAnimationActive={false}
                cursor={{ fill: 'rgba(148, 163, 184, 0.15)' }}
              />
              <Legend wrapperStyle={{ fontSize: 12 }} />
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
