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

// EnergyFlowPeriodSummary is the four-line narrative companion to the
// live power diagram. It reads off the cumulative `*_to_*_kwh`
// counters that `flowsFromTotals` already computes for the selected
// period, so the dashboard surfaces a plain-Ukrainian summary of
// "how the battery was used" without forcing the operator to read
// a Sankey. Rendered in the left metrics panel, sharing the
// `metrics-group` / `daily-narrative-list` styling with the other
// narrative cards (DailySummaryNarrative, AccumulatedSnapshotNarrative,
// etc.) so all left-panel groups read as a single column.
//
// The title mirrors the global RangePreset (`Перетік за
// день/місяць/рік`) so it scans next to "Підсумок за …" without
// requiring an operator to remember which period the dashboard is
// on. The "Оновити" button re-runs the period flow fetch on
// demand — handy after the operator notices a fresh sample
// landed but the chart's auto-refetch is still on cooldown.

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
