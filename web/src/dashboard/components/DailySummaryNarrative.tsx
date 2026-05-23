import type { RegisterMeta } from '../../types'
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
  // kWh, or null when the forecast is unavailable (no n8n mapping
  // for the org or non-day preset). Drives the radial "% виконання"
  // ring above the segment bars.
  pvForecastTotal: number | null
  // loading tracks the period-flow allocator. The card stays
  // visible with last-known values during refresh so the operator
  // sees what's about to change; the title spinner is the
  // "fetching new numbers" cue.
  loading?: boolean
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
  if (!Number.isFinite(value) || value === 0) return '0,00 %'
  if (value >= 10) return `${Math.round(value)} %`
  return `${value.toFixed(2).replace('.', ',')} %`
}

function pctOf(part: number, total: number): number {
  if (!Number.isFinite(part) || !Number.isFinite(total) || total <= 0) return 0
  return Math.max(0, Math.min(100, (part / total) * 100))
}

function ForecastRing({
  actualKwh,
  forecastKwh,
  loading,
}: {
  actualKwh: number
  forecastKwh: number | null
  loading?: boolean
}) {
  // ratio is capped to 1.5 so a wildly-overshooting forecast (e.g.
  // backend reports 0 plan because of a stale n8n cache) doesn't
  // produce a multi-loop dasharray. The visible ring stays clamped
  // at 100% even when displayPct shows the true ratio.
  const ratio = loading
    ? null
    : forecastKwh && forecastKwh > 0
      ? Math.max(0, Math.min(1.5, actualKwh / forecastKwh))
      : null
  const displayPct = ratio === null ? null : Math.round(ratio * 100)
  const cappedRatio = ratio === null ? 0 : Math.min(ratio, 1)
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
        stroke="#e2e8f0"
        strokeWidth={6}
        fill="none"
      />
      {ratio !== null && (
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
        fill="#0f172a"
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
  loading,
}: {
  title: string
  totalKwh: number
  segments: Array<{ name: string; valueKwh: number; color: string }>
  loading?: boolean
}) {
  // While loading, segment bars collapse to zero width and per-row
  // values fall back to the dash placeholder so the operator can't
  // misread stale numbers as fresh ones. The list rows still render
  // (with their colour swatches and labels) so the card height
  // doesn't reflow once data lands.
  const safeSegments = segments.map((s) => ({
    ...s,
    pct: loading ? 0 : pctOf(s.valueKwh, totalKwh),
  }))
  return (
    <div className="summary-segbar">
      <div className="summary-segbar-head">
        <span>{title}</span>
        <strong>{loading ? PLACEHOLDER : formatEnergyUk(totalKwh)}</strong>
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
              {loading ? PLACEHOLDER : formatEnergyUk(s.valueKwh)}
              {!loading && (
                <span className="summary-segbar-pct"> · {formatPercent(s.pct)}</span>
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
  loading,
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
  // pvSelfConsumed sums the two non-export buckets so the bar
  // total matches the PV produced figure even when the algebra
  // drifts by a kWh of rounding (very rare but possible for the
  // synthetic flow allocator).
  const pvSelfConsumed = flows.pvToLoadKwh + flows.pvToEssKwh
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
  const forecastSummary = loading
    ? `прогноз ${PLACEHOLDER}`
    : pvForecastTotal !== null && pvForecastTotal > 0
      ? `прогноз ${formatEnergyUk(pvForecastTotal)}`
      : 'прогноз недоступний'

  return (
    <section
      className="metrics-group summary-narrative"
      aria-labelledby="daily-narrative-title"
      aria-busy={loading || undefined}
    >
      <header className="metrics-group-header">
        <h2 id="daily-narrative-title" className="metrics-group-title">
          {TITLES[preset]}
          <span className="metrics-group-subtitle"> · {periodLabel}</span>
        </h2>
        {loading && <LoadingSpinner label="Завантаження підсумку" />}
      </header>
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
            {loading ? PLACEHOLDER : formatEnergyUk(flows.pvProducedKwh)}
          </strong>
          <span className="summary-hero-sub">{forecastSummary}</span>
        </div>
        <ForecastRing
          actualKwh={flows.pvProducedKwh}
          forecastKwh={pvForecastTotal}
          loading={loading}
        />
      </div>
      <SegmentBar
        title="Куди пішла енергія від СЕС"
        totalKwh={Math.max(flows.pvProducedKwh, pvSelfConsumed + flows.pvToGridKwh)}
        segments={pvSegments}
        loading={loading}
      />
      <SegmentBar
        title="Споживання приладів"
        totalKwh={consumptionTotal}
        segments={consumptionSegments}
        loading={loading}
      />
    </section>
  )
}
