import {
  ArrowDownLeft,
  ArrowUpRight,
  Lightning,
  Sun,
} from '@phosphor-icons/react'
import { formatEnergyCompactKWhUk } from '../format'
import type { EnergyFlows } from '../transforms/flows'

// EnergyFlowPeriodSummary is the four-line narrative companion to the
// live power diagram. It reads off the cumulative `*_to_*_kwh`
// counters that `flowsFromTotals` already computes for the selected
// period, so the dashboard surfaces a plain-Ukrainian summary of
// "how the battery was used" without forcing the operator to read
// a Sankey. Rendered in the left metrics panel, sharing the
// `metrics-group` / `daily-narrative-list` styling with the other
// narrative cards (DailySummaryNarrative, AccumulatedSnapshotNarrative,
// etc.) so all left-panel groups read as a single column.

type Props = {
  flows: EnergyFlows
}

const ICON_SIZE = 20

export function EnergyFlowPeriodSummary({ flows }: Props) {
  return (
    <section
      className="metrics-group daily-narrative"
      aria-labelledby="energy-flow-period-title"
    >
      <h2 id="energy-flow-period-title" className="metrics-group-title">
        Перетік за період
      </h2>
      {!flows.hasEnergyFlowSamples && (
        <p className="daily-narrative-note" role="note">
          Дані з лічильників УЗЕ ще не зібрані за вибраний період.
        </p>
      )}
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
