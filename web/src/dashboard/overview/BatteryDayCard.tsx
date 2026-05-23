import { BatteryFull, BatteryHigh, BatteryLow, BatteryMedium } from '@phosphor-icons/react'
import type { ReactElement } from 'react'
import type { EnergyFlows } from '../transforms/flows'
import { formatEnergyUk } from './format'

type Props = {
  flows: EnergyFlows
  socPercent: number | null
  loading?: boolean
}

// renderBatteryIcon returns the JSX element directly (instead of
// returning a component constructor) so the linter doesn't flag a
// "component created during render" — react-hooks/static-components
// objects to capturing imported components in render-scoped consts.
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

export function BatteryDayCard({ flows, socPercent, loading }: Props) {
  const charged = flows.essChargedKwh
  const discharged = flows.essDischargedKwh
  const balance = charged - discharged
  // The two bars share a max so they can be visually compared at a
  // glance — taking the bigger of the two means whichever side
  // dominated lays at 100% and the other reads as a fraction.
  const max = Math.max(charged, discharged)
  return (
    <section
      className="overview-card overview-card--battery"
      aria-busy={loading || undefined}
    >
      <header className="overview-card-head">
        <h2 className="overview-card-title">Батарея за день</h2>
      </header>
      <div className="overview-battery-body">
        <div className="overview-battery-soc">
          {renderBatteryIcon(socPercent)}
          <strong>{socPercent === null ? '—' : `${Math.round(socPercent)}%`}</strong>
          <span>SOC</span>
        </div>
        <div className="overview-battery-stats">
          <div className="overview-battery-row">
            <div className="overview-battery-row-head">
              <span>Заряд</span>
              <strong>{formatEnergyUk(charged)}</strong>
            </div>
            <div className="overview-battery-track" aria-hidden="true">
              <span
                className="overview-battery-fill overview-battery-fill--charge"
                style={{ width: `${clampPct(charged, max)}%` }}
              />
            </div>
          </div>
          <div className="overview-battery-row">
            <div className="overview-battery-row-head">
              <span>Розряд</span>
              <strong>{formatEnergyUk(discharged)}</strong>
            </div>
            <div className="overview-battery-track" aria-hidden="true">
              <span
                className="overview-battery-fill overview-battery-fill--discharge"
                style={{ width: `${clampPct(discharged, max)}%` }}
              />
            </div>
          </div>
          <div className="overview-battery-balance">
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
