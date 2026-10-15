import { useCallback, useId, useMemo, useState } from 'react'
import { Area, CartesianGrid, ComposedChart, Line, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'
import { formatMonthShort, formatMonthTitle } from '../../economics/monthly/format'
import { useChartChrome, useCssVar } from '../../theme/useChartChrome'
import { formatPeriodLabel } from '../format'
import type { TrendRow, TrendSeriesKey } from '../transforms/energyTrend'
import type { EnergySummary as Summary } from '../transforms/summary'
import { ChartSkeleton } from './ChartSkeleton'
import { EnergySummary } from './EnergySummary'

type Preset = 'month' | 'year'

type Props = {
  preset: Preset
  rows: TrendRow[]
  loading: boolean
  error: string | null
  summary: Summary
  summaryLoading: boolean
}

const COPY: Record<Preset, { title: string; subtitle: string; unit: string }> = {
  month: {
    title: 'Енерготренд за місяць',
    subtitle: 'Споживання, покриття та експорт',
    unit: 'МВт·год/день',
  },
  year: {
    title: 'Тренд за рік',
    subtitle: 'Місячна динаміка споживання, покриття та експорту',
    unit: 'МВт·год/місяць',
  },
}

// Consumption has no fixed color: it takes the theme's text color, so
// the headline line reads light on the dark theme and dark on the light
// one.
const SERIES: { key: TrendSeriesKey; label: string; color?: string; dashed?: boolean }[] = [
  { key: 'load', label: 'Споживання' },
  { key: 'pv', label: 'СЕС', color: '#22c55e' },
  { key: 'ess', label: 'УЗЕ', color: '#06b6d4' },
  { key: 'grid', label: 'Імпорт з мережі', color: '#fb923c' },
  { key: 'pvExport', label: 'Експорт з СЕС', color: '#3b82f6', dashed: true },
  { key: 'essExport', label: 'Експорт з УЗЕ', color: '#a78bfa', dashed: true },
]

const RESIZE_DEBOUNCE_MS = 150

const axisFmt = new Intl.NumberFormat('uk-UA', { maximumFractionDigits: 2 })
const mwhSmallFmt = new Intl.NumberFormat('uk-UA', { minimumFractionDigits: 2, maximumFractionDigits: 2 })
const mwhLargeFmt = new Intl.NumberFormat('uk-UA', { minimumFractionDigits: 1, maximumFractionDigits: 1 })

function formatMwh(v: number | null): string {
  if (v === null || !Number.isFinite(v)) return '—'
  return `${(Math.abs(v) < 10 ? mwhSmallFmt : mwhLargeFmt).format(v)} МВт·год`
}

function tickLabel(preset: Preset, key: string): string {
  return preset === 'month' ? key.slice(8, 10) : formatMonthShort(key)
}

function rowTitle(preset: Preset, key: string): string {
  if (preset === 'year') return formatMonthTitle(key)
  const [y, m, d] = key.split('-').map(Number)
  return formatPeriodLabel('day', new Date(y, m - 1, d))
}

type TooltipProps = {
  active?: boolean
  payload?: readonly { payload?: unknown }[]
}

// EnergyTrendCard is the month / year Energy Trend of the Система page:
// consumption, the three sources that covered it and the two export
// flows, one point per day (month) or per month (year), with the
// period's totals underneath.
export function EnergyTrendCard({ preset, rows, loading, error, summary, summaryLoading }: Props) {
  const chrome = useChartChrome()
  const loadColor = useCssVar('--text', '#0f172a')
  const dotRing = useCssVar('--bg-elevated', '#ffffff')
  const gradientId = `energy-trend-${useId().replace(/[^a-zA-Z0-9_-]/g, '')}`
  const [hidden, setHidden] = useState<ReadonlySet<TrendSeriesKey>>(() => new Set())
  const copy = COPY[preset]

  const colorOf = useCallback(
    (s: (typeof SERIES)[number]) => s.color ?? loadColor,
    [loadColor],
  )

  const toggle = useCallback((key: TrendSeriesKey) => {
    setHidden((prev) => {
      const next = new Set(prev)
      if (next.has(key)) next.delete(key)
      else next.add(key)
      return next
    })
  }, [])

  const hasData = useMemo(() => rows.some((r) => r.load !== null), [rows])

  const renderTooltip = useCallback(
    ({ active, payload }: TooltipProps) => {
      const row = active ? (payload?.[0]?.payload as TrendRow | undefined) : undefined
      if (!row) return null
      return (
        <div className="energy-trend-tip">
          <div className="energy-tooltip-label">{rowTitle(preset, row.key)}</div>
          {SERIES.filter((s) => !hidden.has(s.key)).map((s) => (
            <div key={s.key} className="energy-tooltip-row">
              <span className="energy-tooltip-dot" style={{ backgroundColor: colorOf(s) }} />
              <span className="energy-tooltip-name">{s.label}</span>
              <span className="energy-tooltip-value">{formatMwh(row[s.key])}</span>
            </div>
          ))}
        </div>
      )
    },
    [preset, hidden, colorOf],
  )

  let body: React.ReactNode
  if (loading) {
    body = <ChartSkeleton preset={preset} shape="line" />
  } else if (error) {
    body = <p className="chart-placeholder">Не вдалося завантажити енерготренд: {error}</p>
  } else if (!hasData) {
    body = <p className="chart-placeholder">Немає даних за обраний період.</p>
  } else {
    body = (
      <ResponsiveContainer width="100%" height="100%" debounce={RESIZE_DEBOUNCE_MS}>
        <ComposedChart data={rows} margin={{ top: 8, right: 12, bottom: 0, left: 0 }}>
          <defs>
            <linearGradient id={gradientId} x1="0" y1="0" x2="0" y2="1">
              <stop offset="0%" stopColor={loadColor} stopOpacity={0.22} />
              <stop offset="100%" stopColor={loadColor} stopOpacity={0.02} />
            </linearGradient>
          </defs>
          <CartesianGrid strokeDasharray="3 3" stroke={chrome.grid} vertical={false} />
          <XAxis
            dataKey="key"
            tickFormatter={(key: string) => tickLabel(preset, key)}
            interval={preset === 'year' ? 0 : 'preserveStartEnd'}
            minTickGap={4}
            tick={{ fontSize: 11, fill: chrome.tick }}
            tickLine={false}
            axisLine={{ stroke: chrome.grid }}
          />
          <YAxis
            width={44}
            tickFormatter={(v: number) => axisFmt.format(v)}
            tick={{ fontSize: 11, fill: chrome.tick }}
            tickLine={false}
            axisLine={false}
          />
          <Tooltip
            content={renderTooltip}
            cursor={{ stroke: chrome.cursorStroke, strokeDasharray: '3 3' }}
            isAnimationActive={false}
          />
          {!hidden.has('load') && (
            <Area
              type="monotone"
              dataKey="load"
              stroke="none"
              fill={`url(#${gradientId})`}
              activeDot={false}
              isAnimationActive={false}
            />
          )}
          {SERIES.filter((s) => !hidden.has(s.key)).map((s) => (
            <Line
              key={s.key}
              type="monotone"
              dataKey={s.key}
              name={s.label}
              stroke={colorOf(s)}
              strokeWidth={s.key === 'load' ? 2.4 : 2}
              strokeDasharray={s.dashed ? '6 4' : undefined}
              dot={s.dashed ? false : { r: 3, fill: colorOf(s), stroke: dotRing, strokeWidth: 1.5 }}
              activeDot={{ r: 4.5 }}
              isAnimationActive={false}
            />
          ))}
        </ComposedChart>
      </ResponsiveContainer>
    )
  }

  return (
    <div className="chart-card energy-trend-card">
      <div className="energy-trend-head">
        <div>
          <h2>{copy.title}</h2>
          <p className="energy-trend-subtitle">{copy.subtitle}</p>
        </div>
        <ul className="chart-legend energy-trend-legend">
          {SERIES.map((s) => {
            const off = hidden.has(s.key)
            return (
              <li key={s.key}>
                <button
                  type="button"
                  className={`chart-legend-item${off ? ' chart-legend-item--hidden' : ''}`}
                  aria-pressed={off}
                  title={off ? 'Показати' : 'Сховати'}
                  onClick={() => toggle(s.key)}
                >
                  <span
                    className={`energy-trend-swatch${s.dashed ? ' energy-trend-swatch--dashed' : ''}`}
                    style={{ borderColor: colorOf(s) }}
                  />
                  <span className="chart-legend-label">{s.label}</span>
                </button>
              </li>
            )
          })}
        </ul>
      </div>
      <div className="energy-trend-unit">{copy.unit}</div>
      <div className="chart-wrap">{body}</div>
      <EnergySummary summary={summary} loading={summaryLoading} />
    </div>
  )
}
