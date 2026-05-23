import type { EnergyFlows } from '../transforms/flows'
import { formatEnergyUk, formatPercent } from './format'

type Props = {
  flows: EnergyFlows
  date: Date
  pvForecastKwh: number | null
  loading?: boolean
}

const RING_RADIUS = 36
const RING_CIRCUMFERENCE = 2 * Math.PI * RING_RADIUS

function formatDayHeading(date: Date): string {
  return new Intl.DateTimeFormat('uk-UA', {
    day: 'numeric',
    month: 'long',
    year: 'numeric',
  }).format(date)
}

function pctOf(part: number, total: number): number {
  if (!Number.isFinite(part) || !Number.isFinite(total) || total <= 0) return 0
  return Math.max(0, Math.min(100, (part / total) * 100))
}

// ForecastRing renders the "PV vs forecast" radial progress shown
// next to the daily PV total. We hand-roll it instead of pulling
// recharts' RadialBarChart because the geometry is trivial — one
// SVG circle with stroke-dasharray — and a hand-rolled component
// gives us full control over the centred percentage label.
function ForecastRing({
  actualKwh,
  forecastKwh,
}: {
  actualKwh: number
  forecastKwh: number | null
}) {
  const ratio =
    forecastKwh && forecastKwh > 0
      ? Math.max(0, Math.min(1.5, actualKwh / forecastKwh))
      : null
  const displayPct = ratio === null ? null : Math.round(ratio * 100)
  const cappedRatio = ratio === null ? 0 : Math.min(ratio, 1)
  const dashOffset = RING_CIRCUMFERENCE * (1 - cappedRatio)
  return (
    <svg
      className="overview-ring"
      width={88}
      height={88}
      viewBox="0 0 88 88"
      role="img"
      aria-label={
        displayPct !== null
          ? `Виконання прогнозу: ${displayPct}%`
          : 'Прогноз недоступний'
      }
    >
      <circle
        cx={44}
        cy={44}
        r={RING_RADIUS}
        stroke="#e2e8f0"
        strokeWidth={8}
        fill="none"
      />
      {ratio !== null && (
        <circle
          cx={44}
          cy={44}
          r={RING_RADIUS}
          stroke="#22c55e"
          strokeWidth={8}
          fill="none"
          strokeDasharray={RING_CIRCUMFERENCE}
          strokeDashoffset={dashOffset}
          strokeLinecap="round"
          transform="rotate(-90 44 44)"
        />
      )}
      <text
        x={44}
        y={49}
        textAnchor="middle"
        fontSize={18}
        fontWeight={700}
        fill="#0f172a"
      >
        {displayPct !== null ? `${displayPct}%` : '—'}
      </text>
    </svg>
  )
}

// SegmentBar renders a stacked horizontal bar with an optional
// caption row above (label + total) and a per-segment list below.
// It's the building block for both the "Куди пішла енергія від
// СЕС" and "Споживання приладів" sub-panels of the Daily Summary.
function SegmentBar({
  title,
  totalKwh,
  segments,
}: {
  title: string
  totalKwh: number
  segments: Array<{
    name: string
    valueKwh: number
    color: string
  }>
}) {
  const safeSegments = segments.map((s) => ({
    ...s,
    pct: pctOf(s.valueKwh, totalKwh),
  }))
  return (
    <div className="overview-segbar">
      <div className="overview-segbar-head">
        <span>{title}</span>
        <strong>{formatEnergyUk(totalKwh)}</strong>
      </div>
      <div className="overview-segbar-track" aria-hidden="true">
        {safeSegments.map((s) => (
          <span
            key={s.name}
            className="overview-segbar-fill"
            style={{ width: `${s.pct}%`, background: s.color }}
          />
        ))}
      </div>
      <ul className="overview-segbar-list">
        {safeSegments.map((s) => (
          <li key={s.name}>
            <span className="overview-segbar-row">
              <span
                className="overview-swatch"
                style={{ background: s.color }}
                aria-hidden="true"
              />
              <span className="overview-segbar-name">{s.name}</span>
            </span>
            <span className="overview-segbar-value">
              {formatEnergyUk(s.valueKwh)}
              <span className="overview-segbar-pct"> · {formatPercent(s.pct)}</span>
            </span>
          </li>
        ))}
      </ul>
    </div>
  )
}

export function DailySummaryCard({ flows, date, pvForecastKwh, loading }: Props) {
  const pvSelfConsumed = flows.pvToLoadKwh + flows.pvToEssKwh
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
    pvForecastKwh && pvForecastKwh > 0
      ? `прогноз ${formatEnergyUk(pvForecastKwh)}`
      : 'прогноз недоступний'

  return (
    <section
      className="overview-card overview-card--summary"
      aria-busy={loading || undefined}
    >
      <header className="overview-card-head">
        <h2 className="overview-card-title">Підсумок за день</h2>
        <span className="overview-card-date">{formatDayHeading(date)}</span>
      </header>
      <div className="overview-summary-hero">
        <div className="overview-summary-hero-text">
          <span className="overview-summary-label">СЕС згенерувала</span>
          <strong className="overview-summary-value">
            {formatEnergyUk(flows.pvProducedKwh)}
          </strong>
          <span className="overview-summary-sub">{forecastSummary}</span>
        </div>
        <ForecastRing
          actualKwh={flows.pvProducedKwh}
          forecastKwh={pvForecastKwh}
        />
      </div>
      <SegmentBar
        title={`Куди пішла енергія від СЕС (${formatEnergyUk(flows.pvProducedKwh)})`}
        totalKwh={Math.max(flows.pvProducedKwh, pvSelfConsumed + flows.pvToGridKwh)}
        segments={pvSegments}
      />
      <SegmentBar
        title="Споживання приладів"
        totalKwh={consumptionTotal}
        segments={consumptionSegments}
      />
    </section>
  )
}
