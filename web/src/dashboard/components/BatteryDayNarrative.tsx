import {
  BatteryFull,
  BatteryHigh,
  BatteryLow,
  BatteryMedium,
} from '@phosphor-icons/react'
import type { ReactElement } from 'react'
import type { CurrentResponse } from '../../types'
import type { EnergyFlows } from '../transforms/flows'
import { LoadingSpinner } from './LoadingSpinner'

type Props = {
  flows: EnergyFlows
  current: CurrentResponse | null
  loading?: boolean
}

// renderBatteryIcon returns the JSX element directly so the linter
// doesn't flag a "component created during render" — capturing
// imported phosphor components in a render-scoped const trips the
// react-hooks/static-components rule.
const ICON_PROPS = { size: 56, weight: 'duotone' as const, color: '#22c55e' }

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

// formatEnergyUk is an adaptive kWh/MWh formatter; the
// dashboard's shared format helpers are kWh-only, which would
// force any value above 1 MWh to render as a long four-digit kWh
// number on this card.
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

export function BatteryDayNarrative({ flows, current, loading }: Props) {
  const charged = flows.essChargedKwh
  const discharged = flows.essDischargedKwh
  const balance = charged - discharged
  // Both bars share the same max so a glance reads which side
  // dominated; the smaller side appears as a fraction.
  const max = Math.max(charged, discharged)
  const socPercent = readSoc(current)
  return (
    <section
      className="metrics-group battery-narrative"
      aria-labelledby="battery-day-title"
      aria-busy={loading || undefined}
    >
      <header className="metrics-group-header">
        <h2 id="battery-day-title" className="metrics-group-title">
          Батарея за день
        </h2>
        {loading && <LoadingSpinner label="Завантаження стану батареї" />}
      </header>
      <div className="battery-narrative-body">
        <div className="battery-narrative-soc">
          {renderBatteryIcon(socPercent)}
          <strong>
            {socPercent === null ? '—' : `${Math.round(socPercent)}%`}
          </strong>
          <span>SOC</span>
        </div>
        <div className="battery-narrative-stats">
          <div className="battery-narrative-row">
            <div className="battery-narrative-row-head">
              <span>Заряд</span>
              <strong>{formatEnergyUk(charged)}</strong>
            </div>
            <div className="battery-narrative-track" aria-hidden="true">
              <span
                className="battery-narrative-fill battery-narrative-fill--charge"
                style={{ width: `${clampPct(charged, max)}%` }}
              />
            </div>
          </div>
          <div className="battery-narrative-row">
            <div className="battery-narrative-row-head">
              <span>Розряд</span>
              <strong>{formatEnergyUk(discharged)}</strong>
            </div>
            <div className="battery-narrative-track" aria-hidden="true">
              <span
                className="battery-narrative-fill battery-narrative-fill--discharge"
                style={{ width: `${clampPct(discharged, max)}%` }}
              />
            </div>
          </div>
          <div className="battery-narrative-balance">
            <span>Баланс</span>
            <strong className={balance >= 0 ? 'is-positive' : 'is-negative'}>
              {balance >= 0 ? '+' : ''}
              {formatEnergyUk(balance)}
            </strong>
            <small>
              {balance >= 0
                ? 'більше заряду, ніж розряду'
                : 'більше розряду, ніж заряду'}
            </small>
          </div>
        </div>
      </div>
    </section>
  )
}
