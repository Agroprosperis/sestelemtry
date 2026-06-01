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
  // flowsLoaded marks whether `flows` has been populated at least
  // once from a successful /energy-summary fetch. Used to keep the
  // segment bars and balance footer rendered with stale-but-correct
  // values during background refreshes — without it the card would
  // collapse to dashes every time the slow on-the-fly allocator
  // re-runs (5–15 s for a busy day). Initial mounts still show
  // dashes because there is genuinely nothing to show yet.
  flowsLoaded?: boolean
}

const TITLES: Record<RangePreset, string> = {
  day: 'Батарея за день',
  month: 'Батарея за місяць',
  year: 'Батарея за рік',
}

const RING_RADIUS = 30
const RING_CIRCUMFERENCE = 2 * Math.PI * RING_RADIUS

// PLACEHOLDER stands in for "no data yet" — used only on the first
// load before /current or /energy-summary have returned. Background
// refreshes keep the previous numbers on screen and rely on the
// header spinner to signal "fresh data is on its way"; stale-while-
// revalidate matches the rest of the dashboard.
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

// integerHamilton converts an array of raw percentages (0..100, must
// sum to <=100 within float noise) into integers that sum to exactly
// the same total but with no leftover. We floor each value, then
// hand out the remaining integer slots one-by-one to the entries
// with the largest fractional parts (Hamilton / largest-remainder
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
// state-of-charge is already a percentage. Rendering is gated by
// the SOC value alone — once /current has returned a sample we keep
// the ring on screen even while a background refresh is in flight,
// because the operator should never lose sight of the battery state.
function SocRing({ socPercent }: { socPercent: number | null }) {
  const visible = socPercent
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
  hasData,
}: {
  title: string
  totalKwh: number
  segments: Array<{ name: string; valueKwh: number; color: string }>
  // hasData distinguishes "first load — nothing fetched yet" from
  // "loaded, but the day happens to have a zero" (e.g. early morning
  // before any charge cycle). Without it we'd either render "0,00
  // кВт·год · 0 %" too eagerly or blank the bars on every refresh.
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

export function BatteryDayNarrative({
  flows,
  current,
  preset,
  anchor,
  refreshing,
  onRefresh,
  loading,
  flowsLoaded = false,
}: Props) {
  const socPercent = readSoc(current)
  const periodLabel = formatPeriodLabel(preset, anchor)
  // isBusy now controls only the visual "refreshing" affordances
  // (spinner + aria-busy). The numbers below are gated by whether
  // their underlying data has arrived at least once, so a background
  // refresh no longer wipes the card to dashes.
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
  // Drive the bar total + per-segment percentages off the same
  // component sum so the row labels always add to 100 %. The
  // alternative — using the raw `total_energy_charged_kwh` /
  // `total_energy_discharged_kwh` accumulators — drifts a few
  // percent from the on-the-fly allocator's PV→ESS / Grid→ESS
  // (and ESS→Load / ESS→Grid) outputs because the allocator
  // attributes each minute heuristically while the meter is a
  // straight integral. The drift is typically <5 % and shows up
  // as 101 % / 99 % rounding mismatches that the operator reads
  // as a bug. Using the component sum keeps the breakdown self-
  // consistent; the `Баланс батареї` line below uses the same
  // sums so charge-minus-discharge stays equal to the difference
  // of what's printed in each bar header.
  const charged = chargeSegments.reduce((acc, s) => acc + s.valueKwh, 0)
  const discharged = dischargeSegments.reduce(
    (acc, s) => acc + s.valueKwh,
    0,
  )
  const balance = charged - discharged

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
            {socPercent === null ? PLACEHOLDER : `${Math.round(socPercent)}%`}
          </strong>
        </div>
        <SocRing socPercent={socPercent} />
      </div>
      <SegmentBar
        title="Заряд"
        totalKwh={charged}
        segments={chargeSegments}
        hasData={flowsLoaded}
      />
      <SegmentBar
        title="Розряд"
        totalKwh={discharged}
        segments={dischargeSegments}
        hasData={flowsLoaded}
      />
      <p className="battery-narrative-balance-foot">
        Баланс батареї:{' '}
        {flowsLoaded ? (
          <strong className={balance >= 0 ? 'is-positive' : 'is-negative'}>
            {balance >= 0 ? '+' : ''}
            {formatEnergyUk(balance)}
          </strong>
        ) : (
          <strong>{PLACEHOLDER}</strong>
        )}
        {flowsLoaded && (
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
