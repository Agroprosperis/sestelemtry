import {
  ArrowDownLeft,
  ArrowsClockwise,
  ArrowUpRight,
  Lightning,
  Sun,
} from '@phosphor-icons/react'
import { formatEnergyCompactKWhUk, formatPeriodLabel } from '../format'
import type { RangePreset } from '../range'
import type { EnergyFlows } from '../transforms/flows'

// EnergyFlowPeriodSummary is the four-line narrative companion to
// the live power diagram. It reads off the directional flow totals
// the API server computed on the fly for the selected period and
// surfaces a plain-Ukrainian summary of "how the battery was used"
// without forcing the operator to read a Sankey. The card is
// rendered in the left metrics panel, sharing the `metrics-group`
// styling with the other narrative cards.
//
// Today the card is only rendered for the `day` preset (the parent
// `MetricsPanel` gates it on the global RangePreset). Month/year
// presets hide the card because the API restricts the on-the-fly
// allocator to day-sized windows for now. The `preset` prop is kept
// so the title still reads as part of the same "за день / за
// місяць / за рік" family with the other left-panel cards once we
// lift that restriction.

type Props = {
  flows: EnergyFlows
  preset: RangePreset
  anchor: Date
  onRefresh: () => void
  refreshing: boolean
}

const ICON_SIZE = 20

const TITLES: Record<RangePreset, string> = {
  day: 'Перетік за день',
  month: 'Перетік за місяць',
  year: 'Перетік за рік',
}

export function EnergyFlowPeriodSummary({
  flows,
  preset,
  anchor,
  onRefresh,
  refreshing,
}: Props) {
  const periodLabel = formatPeriodLabel(preset, anchor)
  return (
    <section
      className="metrics-group daily-narrative"
      aria-labelledby="energy-flow-period-title"
      aria-busy={refreshing}
    >
      <header className="metrics-group-header">
        <h2 id="energy-flow-period-title" className="metrics-group-title">
          {TITLES[preset]}
          <span className="metrics-group-subtitle"> · {periodLabel}</span>
        </h2>
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
      <ul className="daily-narrative-list">
        <li>
          <span className="daily-narrative-icon" aria-hidden="true">
            <Sun size={ICON_SIZE} weight="duotone" color="#f59e0b" />
          </span>
          <span>
            УЗЕ зарядилось від сонця:{' '}
            <strong>{formatEnergyCompactKWhUk(flows.pvToEssKwh)}</strong>
          </span>
        </li>
        <li>
          <span className="daily-narrative-icon" aria-hidden="true">
            <ArrowDownLeft size={ICON_SIZE} weight="bold" color="#3b82f6" />
          </span>
          <span>
            УЗЕ зарядилось від мережі:{' '}
            <strong>{formatEnergyCompactKWhUk(flows.gridToEssKwh)}</strong>
          </span>
        </li>
        <li>
          <span className="daily-narrative-icon" aria-hidden="true">
            <Lightning size={ICON_SIZE} weight="duotone" color="#7c3aed" />
          </span>
          <span>
            УЗЕ віддало на споживання:{' '}
            <strong>{formatEnergyCompactKWhUk(flows.essToLoadKwh)}</strong>
          </span>
        </li>
        <li>
          <span className="daily-narrative-icon" aria-hidden="true">
            <ArrowUpRight size={ICON_SIZE} weight="bold" color="#22c55e" />
          </span>
          <span>
            УЗЕ віддало в мережу:{' '}
            <strong>{formatEnergyCompactKWhUk(flows.essToGridKwh)}</strong>
          </span>
        </li>
      </ul>
    </section>
  )
}
