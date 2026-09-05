import { useMemo } from 'react'
import type { RegisterMeta } from '../../types'
import { cssVar } from '../../theme/cssVar'
import { useTheme } from '../../theme/theme'
import { formatPeriodLabel } from '../format'
import type { RangePreset } from '../range'
import type { EnergyFlows } from '../transforms/flows'
import { LoadingSpinner } from './LoadingSpinner'
import { ModbusAddr } from './ModbusAddr'

type Props = {
  flows: EnergyFlows
  preset: RangePreset
  anchor: Date
  debug: boolean
  registers: Record<string, RegisterMeta> | null
  // pvForecastTotal is the planned generation for the period in
  // kWh, or null when there is no plan to compare against (the org
  // isn't in the forecast flow, or no day in range carries one).
  // Drives the radial "% виконання" ring above the segment bars.
  pvForecastTotal: number | null
  // pvForecastLoading keeps the placeholder up while the period plan
  // is still in flight, so the line doesn't flash "прогноз
  // недоступний" between the flows landing and the plan landing.
  pvForecastLoading?: boolean
  // pvForecastCoverage is set when the plan covers fewer days than the
  // period holds (the forecast flow's history stops short of it), so
  // the ring's percentage is measured against a partial plan.
  pvForecastCoverage?: { covered: number; expected: number } | null
  // loading tracks the period-flow allocator. Drives only the
  // header spinner (and aria-busy); the actual numbers are gated
  // by `flowsLoaded` so background refreshes don't wipe stale-
  // but-correct values from the screen.
  loading?: boolean
  // flowsLoaded is true after the first successful /energy-summary
  // fetch. Before that, segment bars / hero / forecast ring render
  // placeholders so the operator doesn't read all-zero rows as
  // "the day produced nothing" during the initial allocator call
  // (it can take 5–15 s on a busy day).
  flowsLoaded?: boolean
  // flowsGap is set when a month/year total covers fewer days than the
  // period holds, because the economics daemon hasn't computed the
  // rest. Without saying so the card would present a short total as
  // the full period.
  flowsGap?: { covered: number; expected: number } | null
}

const TITLES: Record<RangePreset, string> = {
  day: 'Підсумок за день',
  month: 'Підсумок за місяць',
  year: 'Підсумок за рік',
}

const RING_RADIUS = 30
const RING_CIRCUMFERENCE = 2 * Math.PI * RING_RADIUS

// formatEnergyUk is an adaptive kWh/MWh formatter; the dashboard's
// shared format helpers are kWh-only, which would force every
// value above 1 MWh to render as a long four-digit kWh number on
// this card.
function formatEnergyUk(valueKWh: number): string {
  if (!Number.isFinite(valueKWh)) return '—'
  const abs = Math.abs(valueKWh)
  if (abs >= 1000) {
    return `${(valueKWh / 1000)
      .toFixed(abs >= 10_000 ? 1 : 2)
      .replace('.', ',')
      .replace(/,0+$/, '')} МВт·год`
  }
  if (abs >= 100) return `${Math.round(valueKWh)} кВт·год`
  if (abs >= 10) return `${valueKWh.toFixed(1).replace('.', ',')} кВт·год`
  return `${valueKWh.toFixed(2).replace('.', ',')} кВт·год`
}

// PLACEHOLDER is the dash glyph used in place of numbers while
// data is in flight. Em dash with the same trailing kWh suffix
// would lie about units, so we show only the dash to make it
// obvious that nothing has loaded yet.
const PLACEHOLDER = '—'

function formatPercent(value: number): string {
  if (!Number.isFinite(value) || value === 0) return '0 %'
  return `${Math.round(value)} %`
}

function pctOf(part: number, total: number): number {
  if (!Number.isFinite(part) || !Number.isFinite(total) || total <= 0) return 0
  return Math.max(0, Math.min(100, (part / total) * 100))
}

// integerHamilton converts an array of raw percentages into integers
// that sum to exactly the rounded raw total. We floor each value
// then hand out the leftover slots one-by-one to the entries with
// the largest fractional parts (Hamilton / largest-remainder
// method). Without this step naive Math.round on each pct lets the
// row sum drift to 99 or 101 — the operator reads that as a bug
// even though the underlying ratios are fine.
function integerHamilton(rawPcts: number[]): number[] {
  if (rawPcts.length === 0) return []
  const floors = rawPcts.map((v) => Math.max(0, Math.floor(v)))
  const target = Math.round(rawPcts.reduce((a, b) => a + b, 0))
  let leftover = target - floors.reduce((a, b) => a + b, 0)
  if (leftover <= 0) return floors
  const order = rawPcts
    .map((v, i) => ({ i, frac: v - Math.floor(v) }))
    .sort((a, b) => b.frac - a.frac)
  const out = floors.slice()
  for (let k = 0; k < order.length && leftover > 0; k++) {
    out[order[k].i] += 1
    leftover--
  }
  return out
}

function ForecastRing({
  actualKwh,
  forecastKwh,
  hasData,
}: {
  actualKwh: number
  forecastKwh: number | null
  hasData: boolean
}) {
  const { resolved } = useTheme()
  const grid = useMemo(() => cssVar('--chart-grid', '#e2e8f0'), [resolved])
  const text = useMemo(() => cssVar('--text', '#0f172a'), [resolved])
  // The visible arc is clamped to a single full revolution so a
  // big overshoot (e.g. forecast underestimated by 2x) doesn't
  // produce a multi-loop dasharray; the printed percentage stays
  // truthful so 200 %, 250 %, etc. are still legible.
  const rawRatio =
    hasData && forecastKwh !== null && forecastKwh > 0
      ? actualKwh / forecastKwh
      : null
  const displayPct =
    rawRatio === null ? null : Math.round(Math.max(0, rawRatio) * 100)
  const cappedRatio = rawRatio === null ? 0 : Math.max(0, Math.min(rawRatio, 1))
  const dashOffset = RING_CIRCUMFERENCE * (1 - cappedRatio)
  return (
    <svg
      className="summary-ring"
      width={72}
      height={72}
      viewBox="0 0 72 72"
      role="img"
      aria-label={
        displayPct !== null
          ? `Виконання прогнозу: ${displayPct}%`
          : 'Прогноз недоступний'
      }
    >
      <circle
        cx={36}
        cy={36}
        r={RING_RADIUS}
        stroke={grid}
        strokeWidth={6}
        fill="none"
      />
      {rawRatio !== null && (
        <circle
          cx={36}
          cy={36}
          r={RING_RADIUS}
          stroke="#22c55e"
          strokeWidth={6}
          fill="none"
          strokeDasharray={RING_CIRCUMFERENCE}
          strokeDashoffset={dashOffset}
          strokeLinecap="round"
          transform="rotate(-90 36 36)"
        />
      )}
      <text
        x={36}
        y={40}
        textAnchor="middle"
        fontSize={14}
        fontWeight={700}
        fill={text}
      >
        {displayPct !== null ? `${displayPct}%` : '—'}
      </text>
    </svg>
  )
}

function SegmentBar({
  title,
  totalKwh,
  segments,
  hasData,
}: {
  title: string
  totalKwh: number
  segments: Array<{ name: string; valueKwh: number; color: string }>
  // hasData drives blank-vs-render: false on the very first load
  // (before /energy-summary returns), true thereafter. Background
  // refreshes keep showing the previous values; the header spinner
  // signals that fresh data is on its way.
  hasData: boolean
}) {
  const rawPcts = segments.map((s) =>
    hasData ? pctOf(s.valueKwh, totalKwh) : 0,
  )
  const intPcts = integerHamilton(rawPcts)
  const safeSegments = segments.map((s, i) => ({
    ...s,
    pct: rawPcts[i],
    intPct: intPcts[i],
  }))
  return (
    <div className="summary-segbar">
      <div className="summary-segbar-head">
        <span>{title}</span>
        <strong>{hasData ? formatEnergyUk(totalKwh) : PLACEHOLDER}</strong>
      </div>
      <div className="summary-segbar-track" aria-hidden="true">
        {safeSegments.map((s) => (
          <span
            key={s.name}
            className="summary-segbar-fill"
            style={{ width: `${s.pct}%`, background: s.color }}
          />
        ))}
      </div>
      <ul className="summary-segbar-list">
        {safeSegments.map((s) => (
          <li key={s.name}>
            <span className="summary-segbar-row">
              <span
                className="summary-swatch"
                style={{ background: s.color }}
                aria-hidden="true"
              />
              <span className="summary-segbar-name">{s.name}</span>
            </span>
            <span className="summary-segbar-value">
              {hasData ? formatEnergyUk(s.valueKwh) : PLACEHOLDER}
              {hasData && (
                <span className="summary-segbar-pct">
                  {' '}
                  · {formatPercent(s.intPct)}
                </span>
              )}
            </span>
          </li>
        ))}
      </ul>
    </div>
  )
}

export function DailySummaryNarrative({
  flows,
  preset,
  anchor,
  debug,
  registers,
  pvForecastTotal,
  pvForecastLoading = false,
  pvForecastCoverage = null,
  loading,
  flowsLoaded = false,
  flowsGap = null,
}: Props) {
  const periodLabel = formatPeriodLabel(preset, anchor)
  const pvSegments = [
    {
      name: 'Експорт в мережу',
      valueKwh: flows.pvToGridKwh,
      color: '#22c55e',
    },
    {
      name: 'Споживання приладів',
      valueKwh: flows.pvToLoadKwh,
      color: '#a855f7',
    },
    { name: 'Заряд УЗЕ', valueKwh: flows.pvToEssKwh, color: '#f59e0b' },
  ]
  // Drive the bar total off the component sum so segment
  // percentages always add to exactly 100 %. Using the meter
  // (`flows.pvProducedKwh`) here would let the row sum dip below
  // 100 % whenever the on-the-fly allocator under-attributes a
  // few kWh, which the operator reads as a bug. The hero block
  // above still prints `pvProducedKwh` separately so the meter
  // truth is preserved.
  const pvBreakdownTotal = pvSegments.reduce(
    (acc, s) => acc + s.valueKwh,
    0,
  )
  const consumptionSegments = [
    {
      name: 'Від СЕС та УЗЕ',
      valueKwh: flows.pvToLoadKwh + flows.essToLoadKwh,
      color: '#7c3aed',
    },
    {
      name: 'Імпорт з мережі',
      valueKwh: flows.gridToLoadKwh,
      color: '#3b82f6',
    },
  ]
  const consumptionTotal =
    consumptionSegments[0].valueKwh + consumptionSegments[1].valueKwh
  const forecastSummary =
    !flowsLoaded || pvForecastLoading
      ? `прогноз ${PLACEHOLDER}`
      : pvForecastTotal !== null && pvForecastTotal > 0
        ? `прогноз ${formatEnergyUk(pvForecastTotal)}`
        : 'прогноз недоступний'

  // The in-header spinner only fires before the first successful
  // /energy-summary lands — subsequent background refreshes run
  // silently so the operator isn't watching a loader animate every
  // few seconds while perfectly valid numbers sit underneath.
  const showFirstLoadSpinner = loading && !flowsLoaded
  return (
    <section
      className="metrics-group summary-narrative"
      aria-labelledby="daily-narrative-title"
      aria-busy={showFirstLoadSpinner || undefined}
    >
      <header className="metrics-group-header">
        <h2 id="daily-narrative-title" className="metrics-group-title">
          {TITLES[preset]}
          <span className="metrics-group-subtitle"> · {periodLabel}</span>
        </h2>
        {showFirstLoadSpinner && (
          <LoadingSpinner label="Завантаження підсумку" />
        )}
      </header>
      {flowsLoaded && flowsGap && (
        <p className="summary-coverage-note">
          Розподіл енергії порахований за {flowsGap.covered} з{' '}
          {flowsGap.expected} днів періоду — решта днів ще не оброблена.
        </p>
      )}
      {flowsLoaded && !pvForecastLoading && pvForecastCoverage && (
        <p className="summary-coverage-note">
          Прогноз відомий за {pvForecastCoverage.covered} з{' '}
          {pvForecastCoverage.expected} днів періоду — порівняння з планом
          неповне.
        </p>
      )}
      <div className="summary-hero">
        <div className="summary-hero-text">
          <span className="summary-hero-label">
            СЕС згенерувала
            <ModbusAddr
              debug={debug}
              registers={registers}
              keys="accumulated_pv_energy_yield_kwh"
            />
          </span>
          <strong className="summary-hero-value">
            {flowsLoaded ? formatEnergyUk(flows.pvProducedKwh) : PLACEHOLDER}
          </strong>
          <span className="summary-hero-sub">{forecastSummary}</span>
        </div>
        <ForecastRing
          actualKwh={flows.pvProducedKwh}
          forecastKwh={pvForecastTotal}
          hasData={flowsLoaded}
        />
      </div>
      <SegmentBar
        title="Куди пішла енергія від СЕС"
        totalKwh={pvBreakdownTotal}
        segments={pvSegments}
        hasData={flowsLoaded}
      />
      <SegmentBar
        title="Споживання приладів"
        totalKwh={consumptionTotal}
        segments={consumptionSegments}
        hasData={flowsLoaded}
      />
    </section>
  )
}
