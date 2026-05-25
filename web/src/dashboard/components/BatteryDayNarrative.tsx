import {
  ArrowsClockwise,
  BatteryFull,
  BatteryHigh,
  BatteryLow,
  BatteryMedium,
} from '@phosphor-icons/react'
import type { ReactElement } from 'react'
import type { CurrentResponse } from '../../types'
import { formatPeriodLabel } from '../format'
import type { RangePreset } from '../range'
import type { EnergyFlows } from '../transforms/flows'
import { LoadingSpinner } from './LoadingSpinner'

type Props = {
  flows: EnergyFlows
  current: CurrentResponse | null
  preset: RangePreset
  anchor: Date
  refreshing: boolean
  onRefresh: () => void
  loading?: boolean
}

const TITLES: Record<RangePreset, string> = {
  day: 'Батарея за день',
  month: 'Батарея за місяць',
  year: 'Батарея за рік',
}

const RING_RADIUS = 30
const RING_CIRCUMFERENCE = 2 * Math.PI * RING_RADIUS

// PLACEHOLDER replaces stale numbers in the card while a refresh
// is in flight so the operator can't misread the previous load's
// values as the new ones.
const PLACEHOLDER = '—'

// formatEnergyUk is an adaptive kWh/MWh formatter; the dashboard's
// shared format helpers are kWh-only, which would force any value
// above 1 MWh to render as a long four-digit kWh number on this
// card.
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

function formatPercent(value: number): string {
  if (!Number.isFinite(value) || value === 0) return '0 %'
  return `${Math.round(value)} %`
}

function pctOf(part: number, total: number): number {
  if (!Number.isFinite(part) || !Number.isFinite(total) || total <= 0) return 0
  return Math.max(0, Math.min(100, (part / total) * 100))
}

function readSoc(current: CurrentResponse | null): number | null {
  const v = current?.metrics?.soc_percent?.value
  return typeof v === 'number' && Number.isFinite(v) ? v : null
}

const RING_ICON_PROPS = {
  size: 28,
  weight: 'duotone' as const,
  color: '#15803d',
}

// renderRingBatteryIcon picks the Phosphor glyph that visually
// matches the current SOC band, so the centre of the ring
// reinforces what the arc length already says without printing
// another "52 %" string.
function renderRingBatteryIcon(soc: number | null): ReactElement {
  if (soc === null) return <BatteryMedium {...RING_ICON_PROPS} />
  if (soc >= 80) return <BatteryFull {...RING_ICON_PROPS} />
  if (soc >= 50) return <BatteryHigh {...RING_ICON_PROPS} />
  if (soc >= 25) return <BatteryMedium {...RING_ICON_PROPS} />
  return <BatteryLow {...RING_ICON_PROPS} />
}

// SocRing mirrors DailySummaryNarrative.ForecastRing so the two
// hero blocks read as a pair: same radius, stroke width and label
// typography. The arc length encodes SOC directly (0–100 %) since
// state-of-charge is already a percentage.
function SocRing({
  socPercent,
  loading,
}: {
  socPercent: number | null
  loading?: boolean
}) {
  const visible = loading ? null : socPercent
  const ratio = visible === null ? 0 : Math.max(0, Math.min(1, visible / 100))
  const dashOffset = RING_CIRCUMFERENCE * (1 - ratio)
  return (
    <span
      className="summary-ring summary-ring--battery"
      role="img"
      aria-label={
        visible === null ? 'SOC недоступний' : `SOC ${Math.round(visible)} відсотків`
      }
    >
      <svg width={72} height={72} viewBox="0 0 72 72" aria-hidden="true">
        <circle
          cx={36}
          cy={36}
          r={RING_RADIUS}
          stroke="#e2e8f0"
          strokeWidth={6}
          fill="none"
        />
        {visible !== null && (
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
      </svg>
      <span className="summary-ring-icon" aria-hidden="true">
        {renderRingBatteryIcon(visible)}
      </span>
    </span>
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
  // While loading, segment widths collapse to zero and per-row
  // numbers fall back to the dash placeholder so the operator
  // can't misread stale values as fresh ones. Rows still render
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

export function BatteryDayNarrative({
  flows,
  current,
  preset,
  anchor,
  refreshing,
  onRefresh,
  loading,
}: Props) {
  const charged = flows.essChargedKwh
  const discharged = flows.essDischargedKwh
  const balance = charged - discharged
  const socPercent = readSoc(current)
  const periodLabel = formatPeriodLabel(preset, anchor)
  const isBusy = loading || refreshing

  const chargeSegments = [
    { name: 'Від сонця → УЗЕ', valueKwh: flows.pvToEssKwh, color: '#f59e0b' },
    { name: 'З мережі → УЗЕ', valueKwh: flows.gridToEssKwh, color: '#3b82f6' },
  ]
  const dischargeSegments = [
    {
      name: 'УЗЕ → споживання',
      valueKwh: flows.essToLoadKwh,
      color: '#7c3aed',
    },
    { name: 'УЗЕ → мережа', valueKwh: flows.essToGridKwh, color: '#22c55e' },
  ]

  return (
    <section
      className="metrics-group summary-narrative"
      aria-labelledby="battery-day-title"
      aria-busy={isBusy || undefined}
    >
      <header className="metrics-group-header">
        <h2 id="battery-day-title" className="metrics-group-title">
          {TITLES[preset]}
          <span className="metrics-group-subtitle"> · {periodLabel}</span>
        </h2>
        {isBusy && <LoadingSpinner label="Завантаження стану батареї" />}
        <button
          type="button"
          className={`metrics-group-refresh${refreshing ? ' is-spinning' : ''}`}
          onClick={onRefresh}
          disabled={refreshing}
          title="Оновити перетік"
          aria-label="Оновити перетік"
        >
          <ArrowsClockwise size={16} weight="bold" />
        </button>
      </header>
      <div className="summary-hero">
        <div className="summary-hero-text">
          <span className="summary-hero-label">Стан заряду</span>
          <strong className="summary-hero-value">
            {isBusy || socPercent === null
              ? PLACEHOLDER
              : `${Math.round(socPercent)}%`}
          </strong>
        </div>
        <SocRing socPercent={socPercent} loading={isBusy} />
      </div>
      <SegmentBar
        title="Заряд"
        totalKwh={charged}
        segments={chargeSegments}
        loading={isBusy}
      />
      <SegmentBar
        title="Розряд"
        totalKwh={discharged}
        segments={dischargeSegments}
        loading={isBusy}
      />
      <p className="battery-narrative-balance-foot">
        Баланс батареї:{' '}
        {isBusy ? (
          <strong>{PLACEHOLDER}</strong>
        ) : (
          <strong className={balance >= 0 ? 'is-positive' : 'is-negative'}>
            {balance >= 0 ? '+' : ''}
            {formatEnergyUk(balance)}
          </strong>
        )}
        {!isBusy && (
          <small>
            {' '}
            ·{' '}
            {balance >= 0
              ? 'більше заряду, ніж розряду'
              : 'більше розряду, ніж заряду'}
          </small>
        )}
      </p>
    </section>
  )
}
