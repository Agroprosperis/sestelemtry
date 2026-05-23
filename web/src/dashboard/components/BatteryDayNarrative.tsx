import {
  ArrowDownLeft,
  ArrowsClockwise,
  ArrowUpRight,
  BatteryFull,
  BatteryHigh,
  BatteryLow,
  BatteryMedium,
  Lightning,
  Sun,
  type Icon,
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

const ICON_PROPS = { size: 56, weight: 'duotone' as const, color: '#22c55e' }
const ROW_ICON_SIZE = 18

// PLACEHOLDER replaces stale numbers in the card while a refresh
// is in flight so the operator can't misread the previous load's
// values as the new ones.
const PLACEHOLDER = '—'

function renderBatteryIcon(soc: number | null): ReactElement {
  if (soc === null) return <BatteryMedium {...ICON_PROPS} />
  if (soc >= 80) return <BatteryFull {...ICON_PROPS} />
  if (soc >= 50) return <BatteryHigh {...ICON_PROPS} />
  if (soc >= 25) return <BatteryMedium {...ICON_PROPS} />
  return <BatteryLow {...ICON_PROPS} />
}

function clampPct(value: number, max: number): number {
  if (!Number.isFinite(value) || !Number.isFinite(max) || max <= 0) return 0
  return Math.max(0, Math.min(100, (value / max) * 100))
}

function readSoc(current: CurrentResponse | null): number | null {
  const v = current?.metrics?.soc_percent?.value
  return typeof v === 'number' && Number.isFinite(v) ? v : null
}

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
  if (!Number.isFinite(value) || value === 0) return '0,00 %'
  if (value >= 10) return `${Math.round(value)} %`
  return `${value.toFixed(2).replace('.', ',')} %`
}

const TITLES: Record<RangePreset, string> = {
  day: 'Батарея за день',
  month: 'Батарея за місяць',
  year: 'Батарея за рік',
}

type FlowRow = {
  label: string
  valueKwh: number
  Icon: Icon
  color: string
  // bucket=in groups inflows (PV→ESS, Grid→ESS) and bucket=out
  // groups outflows (ESS→Load, ESS→Grid) so each row's percentage
  // is normalized against its own direction's total instead of a
  // mixed sum that would skew small flows into invisibility.
  bucket: 'in' | 'out'
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
  // Both summary bars share the same max so a glance reads which
  // side dominated; the smaller side appears as a fraction.
  const maxTotal = Math.max(charged, discharged)
  const socPercent = readSoc(current)
  const periodLabel = formatPeriodLabel(preset, anchor)

  const flowRows: FlowRow[] = [
    {
      label: 'Від сонця → УЗЕ',
      valueKwh: flows.pvToEssKwh,
      Icon: Sun,
      color: '#f59e0b',
      bucket: 'in',
    },
    {
      label: 'З мережі → УЗЕ',
      valueKwh: flows.gridToEssKwh,
      Icon: ArrowDownLeft,
      color: '#3b82f6',
      bucket: 'in',
    },
    {
      label: 'УЗЕ → споживання',
      valueKwh: flows.essToLoadKwh,
      Icon: Lightning,
      color: '#7c3aed',
      bucket: 'out',
    },
    {
      label: 'УЗЕ → мережа',
      valueKwh: flows.essToGridKwh,
      Icon: ArrowUpRight,
      color: '#22c55e',
      bucket: 'out',
    },
  ]
  const totalIn = flowRows
    .filter((r) => r.bucket === 'in')
    .reduce((s, r) => s + r.valueKwh, 0)
  const totalOut = flowRows
    .filter((r) => r.bucket === 'out')
    .reduce((s, r) => s + r.valueKwh, 0)
  const denomFor = (b: 'in' | 'out') => (b === 'in' ? totalIn : totalOut)

  const isBusy = loading || refreshing

  return (
    <section
      className="metrics-group battery-narrative"
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
      <div className="battery-narrative-body">
        <div className="battery-narrative-soc">
          {renderBatteryIcon(isBusy ? null : socPercent)}
          <strong>
            {isBusy || socPercent === null
              ? PLACEHOLDER
              : `${Math.round(socPercent)}%`}
          </strong>
          <span>SOC</span>
        </div>
        <div className="battery-narrative-stats">
          <div className="battery-narrative-row">
            <div className="battery-narrative-row-head">
              <span>Заряд</span>
              <strong>{isBusy ? PLACEHOLDER : formatEnergyUk(charged)}</strong>
            </div>
            <div className="battery-narrative-track" aria-hidden="true">
              <span
                className="battery-narrative-fill battery-narrative-fill--charge"
                style={{ width: `${isBusy ? 0 : clampPct(charged, maxTotal)}%` }}
              />
            </div>
          </div>
          <div className="battery-narrative-row">
            <div className="battery-narrative-row-head">
              <span>Розряд</span>
              <strong>
                {isBusy ? PLACEHOLDER : formatEnergyUk(discharged)}
              </strong>
            </div>
            <div className="battery-narrative-track" aria-hidden="true">
              <span
                className="battery-narrative-fill battery-narrative-fill--discharge"
                style={{
                  width: `${isBusy ? 0 : clampPct(discharged, maxTotal)}%`,
                }}
              />
            </div>
          </div>
        </div>
      </div>
      <div className="battery-narrative-divider" aria-hidden="true" />
      <ul className="accum-narrative-list">
        {flowRows.map((r) => {
          const denom = denomFor(r.bucket)
          // While refreshing, bars and percentages collapse so the
          // previous load's flow story doesn't linger as fresh data.
          const pct = isBusy || denom <= 0 ? 0 : (r.valueKwh / denom) * 100
          return (
            <li key={r.label} className="accum-narrative-item">
              <span className="accum-narrative-icon" aria-hidden="true">
                <r.Icon size={ROW_ICON_SIZE} weight="duotone" color={r.color} />
              </span>
              <span className="accum-narrative-label">{r.label}</span>
              <strong className="accum-narrative-value">
                {isBusy ? PLACEHOLDER : formatEnergyUk(r.valueKwh)}
              </strong>
              <span className="accum-narrative-bar" aria-hidden="true">
                <span
                  className="accum-narrative-bar-fill"
                  style={{
                    width: `${Math.max(0, Math.min(100, pct))}%`,
                    background: r.color,
                  }}
                />
              </span>
              <span className="accum-narrative-pct">
                {isBusy ? PLACEHOLDER : formatPercent(pct)}
              </span>
            </li>
          )
        })}
      </ul>
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
