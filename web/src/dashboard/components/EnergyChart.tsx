import { useCallback, useMemo, useState } from 'react'
import {
  Area,
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
import type { UzePlanResponse } from '../../api'
import type { DashboardMetric } from '../../types'
import {
  AI_PLAN_COLOR,
  AI_PLAN_LOAD_COLOR,
  AI_PLAN_SOC_COLOR,
  dayPowerColor,
  energyColor,
  PV_FORECAST_COLOR,
} from '../colors'
import { formatChartNumber } from '../format'
import { DAY_POWER_METRIC_KEYS, DAY_POWER_METRIC_LABELS } from '../metrics'
import type { RangePreset } from '../range'
import {
  type AiPlanBucket,
  aiPlanBuckets,
  aiPlanHasDispatch,
  aiPlanHasLoad,
} from '../transforms/aiPlan'
import type { EnergyRow } from '../transforms/buckets'
import type { DAMChartRow } from '../transforms/dam'
import type { PowerChartRow } from '../transforms/power'
import type { PvForecastHourlyRow } from '../transforms/pvForecast'
import type { SOCChartRow } from '../transforms/soc'
import type { EnergySummary as Summary } from '../transforms/summary'
import { ChartSkeleton } from './ChartSkeleton'
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
  pvForecastSeries?: PvForecastHourlyRow[]
  aiPlan?: UzePlanResponse | null
  // planOverlay re-skins the AI plan series when the chart shows a
  // manifest plan instead of the retrospective optimum (control mode):
  // custom labels/title, plan lines visible from the start, and an
  // optional vertical annotation (e.g. «manifest applied»).
  planOverlay?: {
    title?: string
    essLabel?: string
    socLabel?: string
    defaultVisible?: boolean
    annotation?: { time: string; label: string }
  }
}

const DAM_PRICE_KEY = 'dam_price_uah_per_mwh'
const DAM_PRICE_COLOR = '#0ea5e9'
const DAM_PRICE_LABEL = 'Ціна РДН'
const SOC_KEY = 'soc_percent'
const SOC_COLOR = '#a855f7'
const SOC_LABEL = 'SOC'
// PV_FORECAST_KEY is the dataKey used to attach hourly forecast values onto
// the day-chart rows. We anchor one non-null sample per hour at the HH:30
// bucket and let recharts' `connectNulls` smooth the line through them.
const PV_FORECAST_KEY = 'planned_ac_kw'
const PV_FORECAST_LABEL = 'Прогноз СЕС'
// 5-min buckets, so HH:30 is index 6 within each 12-bucket hour.
const PV_FORECAST_BUCKET_OFFSET = 6
// The AI recommendation: the ESS dispatch an optimally-run battery would
// have followed on this day, signed like active_ess_power_kw so the plan
// and the realised ESS line can be read against each other directly.
const AI_ESS_KEY = 'ai_ess_power_kw'
const AI_ESS_LABEL = 'Рекомендація ШІ (УЗЕ)'
const AI_SOC_KEY = 'ai_soc_pct'
const AI_SOC_LABEL = 'SOC за планом ШІ'
const AI_REASON_KEY = 'ai_reason_text'
// The recommended elevator consumption, negated like load_power_kw so the
// planned load sits next to the actual load sink below zero.
const AI_LOAD_KEY = 'ai_load_kw'
const AI_LOAD_LABEL = 'Споживання за планом ШІ'

// Day preset uses 5-minute buckets (288 per day); show every 12th tick so
// labels land on the hour and the axis stays readable.
const DAY_TICKS_PER_HOUR = 12

// Debounce ResponsiveContainer's resize handler so layout thrash on window
// resize doesn't trigger a full recharts relayout per pixel.
const RESIZE_DEBOUNCE_MS = 150

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
  pvForecastSeries,
  aiPlan,
  planOverlay,
}: Props) {
  const aiEssLabel = planOverlay?.essLabel ?? AI_ESS_LABEL
  const aiSocLabel = planOverlay?.socLabel ?? AI_SOC_LABEL
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

  // Track which day-chart series the user has temporarily hidden by
  // clicking the corresponding legend item. State lives on the chart so
  // it survives re-renders triggered by data refetches but resets on
  // unmount (preset change). The battery-side AI series start hidden —
  // an opt-in overlay for comparing against the optimum — while the
  // recommended consumption schedule is on by default: it is the line
  // the operator acts on.
  const [hiddenSeries, setHiddenSeries] = useState<Set<string>>(() =>
    planOverlay?.defaultVisible ? new Set() : new Set([AI_ESS_KEY, AI_SOC_KEY]),
  )
  const toggleSeries = useCallback((id: string) => {
    setHiddenSeries((prev) => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }, [])

  // Day preset draws three instantaneous power lines (kW snapshots from
  // powerSeries) plus the DAM price hourly bands, the SOC band overlay, and
  // hourly PV forecast bars. We merge the price/SOC/forecast values onto
  // each power row by `time` so Recharts can align all four layers on the
  // same x-axis and surface them in the tooltip without separate lookups.
  // The forecast value is attached only at one bucket per hour (HH:30) —
  // recharts then renders one centered Bar per hour with its width fixed
  // by `barSize`, instead of 12 stacked thin bars per hour.
  const planBuckets = useMemo<Map<number, AiPlanBucket>>(
    () => (preset === 'day' ? aiPlanBuckets(aiPlan ?? null) : new Map()),
    [preset, aiPlan],
  )
  const dayData = useMemo<PowerChartRow[]>(() => {
    if (preset !== 'day') return []
    const rows = powerSeries ?? []
    const priceByTime = new Map<string, number | null>()
    for (const row of damSeries ?? []) priceByTime.set(String(row.time), row.price)
    const socByTime = new Map<string, number | null>()
    for (const row of socSeries ?? []) socByTime.set(String(row.time), row.soc)
    const forecastByHour = new Map<number, number>()
    for (const row of pvForecastSeries ?? []) {
      if (Number.isFinite(row.plannedKw)) forecastByHour.set(row.hour, row.plannedKw)
    }
    return rows.map((row, idx) => {
      const merged: PowerChartRow = { ...row }
      const price = priceByTime.get(String(row.time))
      if (price != null && Number.isFinite(price)) merged[DAM_PRICE_KEY] = price
      const soc = socByTime.get(String(row.time))
      if (soc != null && Number.isFinite(soc)) merged[SOC_KEY] = soc
      const hour = Math.floor(idx / 12)
      const inHour = idx % 12
      if (inHour === PV_FORECAST_BUCKET_OFFSET) {
        const planned = forecastByHour.get(hour)
        if (planned != null && Number.isFinite(planned)) {
          merged[PV_FORECAST_KEY] = planned
        }
      }
      // The recommendation is hourly: the kW value repeats across the
      // hour's 12 buckets (drawn as a step), the SOC only on the closing
      // bucket since it is the end-of-hour state.
      const bucket = planBuckets.get(idx)
      if (bucket) {
        merged[AI_ESS_KEY] = bucket.essKw
        if (bucket.socPct != null) merged[AI_SOC_KEY] = bucket.socPct
        // Negated like load_power_kw so the planned consumption sits next
        // to the actual load sink below zero.
        if (bucket.loadKw != null) merged[AI_LOAD_KEY] = -bucket.loadKw
        // Carried on the row (not as a series) so the tooltip can explain
        // why the optimizer chose this hour's action.
        merged[AI_REASON_KEY] = bucket.reasonText
      }
      return merged
    })
  }, [preset, powerSeries, damSeries, socSeries, pvForecastSeries, planBuckets])
  const hasSoc = (socSeries ?? []).some((r) => r.soc != null && Number.isFinite(r.soc))
  const hasPvForecast = (pvForecastSeries ?? []).length > 0
  const hasAiPlan = preset === 'day' && aiPlanHasDispatch(aiPlan ?? null)
  const hasAiLoad = preset === 'day' && aiPlanHasLoad(aiPlan ?? null)

  const dayLabels = useMemo(() => dayData.map((r) => String(r.time)), [dayData])
  const hourlyAreas = useMemo(() => hourlyDamAreas(damSeries, dayLabels), [damSeries, dayLabels])
  const priceDomain = useMemo(() => damPriceDomain(hourlyAreas), [hourlyAreas])

  const dayHasData = dayData.some((row) =>
    DAY_POWER_METRIC_KEYS.some((k) => {
      const v = row[k]
      return typeof v === 'number' && Number.isFinite(v)
    }),
  )

  // dayLegendItems pins the legend order explicitly: power lines first
  // (in their canonical PV/ESS/Grid/Load order), then the DAM price band,
  // then SOC at the very end. Recharts' auto-collected legend groups by
  // component type and y-axis, which puts SOC at the front and shuffles
  // power lines, so we render the legend ourselves via `Legend.content`.
  const dayLegendItems = useMemo(() => {
    type Item = { id: string; label: string; color: string }
    const items: Item[] = DAY_POWER_METRIC_KEYS.map((key) => ({
      id: key,
      label: DAY_POWER_METRIC_LABELS[key] ?? key,
      color: dayPowerColor(key),
    }))
    if (hasPvForecast) {
      items.push({ id: PV_FORECAST_KEY, label: PV_FORECAST_LABEL, color: PV_FORECAST_COLOR })
    }
    if (hasAiPlan) {
      items.push({ id: AI_ESS_KEY, label: aiEssLabel, color: AI_PLAN_COLOR })
    }
    if (hasAiLoad) {
      items.push({ id: AI_LOAD_KEY, label: AI_LOAD_LABEL, color: AI_PLAN_LOAD_COLOR })
    }
    items.push({ id: DAM_PRICE_KEY, label: DAM_PRICE_LABEL, color: DAM_PRICE_COLOR })
    if (hasSoc) {
      items.push({ id: SOC_KEY, label: SOC_LABEL, color: SOC_COLOR })
    }
    if (hasAiPlan) {
      items.push({ id: AI_SOC_KEY, label: aiSocLabel, color: AI_PLAN_SOC_COLOR })
    }
    return items
  }, [hasSoc, hasPvForecast, hasAiPlan, hasAiLoad, aiEssLabel, aiSocLabel])

  const renderDayLegend = useCallback(
    () => (
      <ul className="chart-legend">
        {dayLegendItems.map((item) => {
          const hidden = hiddenSeries.has(item.id)
          return (
            <li key={item.id}>
              <button
                type="button"
                className={`chart-legend-item${hidden ? ' chart-legend-item--hidden' : ''}`}
                aria-pressed={hidden}
                title={hidden ? 'Показати' : 'Сховати'}
                onClick={() => toggleSeries(item.id)}
              >
                <span className="chart-legend-swatch" style={{ background: item.color }} />
                <span className="chart-legend-label">{item.label}</span>
              </button>
            </li>
          )
        })}
      </ul>
    ),
    [dayLegendItems, hiddenSeries, toggleSeries],
  )

  const dayTickFormatter = useCallback((v: unknown): string => {
    const s = String(v)
    const idx = s.indexOf(':')
    return idx > 0 ? s.slice(0, idx) : s
  }, [])

  return (
    <div className="chart-card">
      <h2>{planOverlay?.title ?? 'Energy Trend'}</h2>
      <EnergySummary summary={summary} loading={loading} />
      <div className="chart-wrap">
        {loading ? (
          <ChartSkeleton preset={preset} />
        ) : preset === 'day' ? (
          !dayHasData ? (
            <p className="chart-placeholder">No data available for selected range.</p>
          ) : (
            <ResponsiveContainer width="100%" height="100%" debounce={RESIZE_DEBOUNCE_MS}>
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
                <Legend content={renderDayLegend} wrapperStyle={{ fontSize: 12 }} />
                {hasSoc && !hiddenSeries.has(SOC_KEY) && (
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
                {!hiddenSeries.has(DAM_PRICE_KEY) &&
                  hourlyAreas.map((a, i) => (
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
                {planOverlay?.annotation && dayLabels.includes(planOverlay.annotation.time) && (
                  <ReferenceLine
                    x={planOverlay.annotation.time}
                    yAxisId="power"
                    stroke="#475569"
                    strokeDasharray="4 3"
                    label={{
                      value: planOverlay.annotation.label,
                      position: 'top',
                      fill: '#475569',
                      fontSize: 11,
                    }}
                  />
                )}
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
                      hide={hiddenSeries.has(key)}
                      isAnimationActive={false}
                    />
                  )
                })}
                {hasPvForecast && !hiddenSeries.has(PV_FORECAST_KEY) && (
                  <Line
                    yAxisId="power"
                    type="monotone"
                    dataKey={PV_FORECAST_KEY}
                    name={PV_FORECAST_LABEL}
                    stroke={PV_FORECAST_COLOR}
                    strokeWidth={2}
                    strokeDasharray="5 4"
                    dot={{ r: 3, fill: PV_FORECAST_COLOR, stroke: PV_FORECAST_COLOR }}
                    activeDot={{ r: 4 }}
                    connectNulls
                    isAnimationActive={false}
                  />
                )}
                {/* The recommendation is a step: the optimizer's decision
                    is "hold N kW for this hour", not a smooth ramp, so
                    interpolating between hours would draw power the plan
                    never asked for. */}
                {hasAiPlan && !hiddenSeries.has(AI_ESS_KEY) && (
                  <Line
                    yAxisId="power"
                    type="stepAfter"
                    dataKey={AI_ESS_KEY}
                    name={aiEssLabel}
                    stroke={AI_PLAN_COLOR}
                    strokeWidth={2}
                    strokeDasharray="6 3"
                    dot={false}
                    activeDot={{ r: 4 }}
                    connectNulls
                    isAnimationActive={false}
                  />
                )}
                {hasAiPlan && !hiddenSeries.has(AI_SOC_KEY) && (
                  <Line
                    yAxisId="soc"
                    type="monotone"
                    dataKey={AI_SOC_KEY}
                    name={aiSocLabel}
                    stroke={AI_PLAN_SOC_COLOR}
                    strokeWidth={2}
                    strokeDasharray="4 4"
                    dot={false}
                    activeDot={{ r: 4 }}
                    connectNulls
                    isAnimationActive={false}
                  />
                )}
                {/* Recommended consumption: also a step (the schedule says
                    "run at N kW this hour") and negated like the actual
                    load line so plan and fact sit next to each other
                    below zero. */}
                {hasAiLoad && !hiddenSeries.has(AI_LOAD_KEY) && (
                  <Line
                    yAxisId="power"
                    type="stepAfter"
                    dataKey={AI_LOAD_KEY}
                    name={AI_LOAD_LABEL}
                    stroke={AI_PLAN_LOAD_COLOR}
                    strokeWidth={2}
                    strokeDasharray="6 3"
                    dot={false}
                    activeDot={{ r: 4 }}
                    connectNulls
                    isAnimationActive={false}
                  />
                )}
                {/* Invisible bar exists only so recharts can hit-test the
                    DAM price for the tooltip; it draws zero pixels because
                    ReferenceArea above already paints the hourly band. The
                    legend entry comes from `dayLegendPayload`, not this
                    bar's auto-discovered name. */}
                <Bar
                  yAxisId="price"
                  dataKey={DAM_PRICE_KEY}
                  name={DAM_PRICE_LABEL}
                  fill={DAM_PRICE_COLOR}
                  fillOpacity={0}
                  stroke="none"
                  hide={hiddenSeries.has(DAM_PRICE_KEY)}
                  isAnimationActive={false}
                />
              </ComposedChart>
            </ResponsiveContainer>
          )
        ) : series.length === 0 ? (
          <p className="chart-placeholder">No data available for selected range.</p>
        ) : (
          <ResponsiveContainer width="100%" height="100%" debounce={RESIZE_DEBOUNCE_MS}>
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
                  isAnimationActive={false}
                />
              ))}
            </BarChart>
          </ResponsiveContainer>
        )}
      </div>
    </div>
  )
}
