import {
  ArrowDownLeft,
  ArrowUpRight,
  Lightning,
  SunDim,
} from '@phosphor-icons/react'
import { formatEnergyCompactKWhUk } from '../format'
import type { EnergyFlows } from '../transforms/flows'

// EnergyFlowPeriodSummary is the four-line narrative companion to the
// live power diagram. It reads off the cumulative `*_to_*_kwh`
// counters that `flowsFromTotals` already computes for the selected
// period, so the dashboard surfaces a plain-Ukrainian summary of
// "how the battery was used" without forcing the operator to read
// a Sankey. When the synthetic counters are missing (collector
// hasn't emitted any energyflow samples yet) the card falls back
// to a single hint row instead of four zero values.

type Props = {
  flows: EnergyFlows
}

type Row = {
  id: 'pv_to_ess' | 'grid_to_ess' | 'ess_to_load' | 'ess_to_grid'
  label: string
  value: number
  icon: React.ReactNode
}

const ICON_SIZE = 18

export function EnergyFlowPeriodSummary({ flows }: Props) {
  const rows: Row[] = [
    {
      id: 'pv_to_ess',
      label: 'УЗЕ зарядилось від сонця',
      value: flows.pvToEssKwh,
      icon: <SunDim size={ICON_SIZE} weight="duotone" color="#f59e0b" />,
    },
    {
      id: 'grid_to_ess',
      label: 'УЗЕ зарядилось від мережі',
      value: flows.gridToEssKwh,
      icon: <ArrowDownLeft size={ICON_SIZE} weight="bold" color="#3b82f6" />,
    },
    {
      id: 'ess_to_load',
      label: 'УЗЕ віддало на споживання',
      value: flows.essToLoadKwh,
      icon: <Lightning size={ICON_SIZE} weight="duotone" color="#7c3aed" />,
    },
    {
      id: 'ess_to_grid',
      label: 'УЗЕ віддало в мережу',
      value: flows.essToGridKwh,
      icon: <ArrowUpRight size={ICON_SIZE} weight="bold" color="#22c55e" />,
    },
  ]

  return (
    <section
      className="chart-card energy-flow-period-card"
      aria-label="Перетік енергії за період"
    >
      <h2>Перетік за період</h2>
      {!flows.hasEnergyFlowSamples && (
        <p className="energy-flow-period-hint" role="note">
          Дані з лічильників УЗЕ ще не зібрані за вибраний період.
        </p>
      )}
      <ul className="energy-flow-period-list">
        {rows.map((row) => (
          <li key={row.id} className="energy-flow-period-row">
            <span className="energy-flow-period-icon" aria-hidden="true">
              {row.icon}
            </span>
            <span>{row.label}</span>
            <strong>{formatEnergyCompactKWhUk(row.value)}</strong>
          </li>
        ))}
      </ul>
    </section>
  )
}
