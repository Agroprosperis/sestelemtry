import {
  ArrowDownLeft,
  ArrowsClockwise,
  ArrowUpRight,
  Lightning,
  Sun,
  type Icon,
} from '@phosphor-icons/react'
import { formatEnergyCompactKWhUk, formatPeriodLabel } from '../format'
import type { RangePreset } from '../range'
import type { EnergyFlows } from '../transforms/flows'

// EnergyFlowPeriodSummary is the four-line battery flow companion
// to the live power diagram. It reads off the directional flow
// totals the API server computed on the fly for the selected
// period and surfaces them with an icon · label · value · bar · %
// layout, normalising in/out percentages against their respective
// direction totals so a tiny inflow stays visible against a much
// larger outflow.
//
// Today the card is only rendered for the `day` preset (the parent
// `MetricsPanel` gates it on the global RangePreset). Month/year
// presets hide the card because the API restricts the on-the-fly
// allocator to day-sized windows for now.

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

function formatPercent(value: number): string {
  if (!Number.isFinite(value) || value === 0) return '0,00 %'
  if (value >= 10) return `${Math.round(value)} %`
  return `${value.toFixed(2).replace('.', ',')} %`
}

export function EnergyFlowPeriodSummary({
  flows,
  preset,
  anchor,
  onRefresh,
  refreshing,
}: Props) {
  const periodLabel = formatPeriodLabel(preset, anchor)
  const rows: FlowRow[] = [
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
  const totalIn = rows
    .filter((r) => r.bucket === 'in')
    .reduce((s, r) => s + r.valueKwh, 0)
  const totalOut = rows
    .filter((r) => r.bucket === 'out')
    .reduce((s, r) => s + r.valueKwh, 0)
  const denomFor = (b: 'in' | 'out') => (b === 'in' ? totalIn : totalOut)
  const balance = totalIn - totalOut

  return (
    <section
      className="metrics-group accum-narrative flow-period-narrative"
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
      <ul className="accum-narrative-list">
        {rows.map((r) => {
          const denom = denomFor(r.bucket)
          const pct = denom > 0 ? (r.valueKwh / denom) * 100 : 0
          return (
            <li key={r.label} className="accum-narrative-item">
              <span className="accum-narrative-icon" aria-hidden="true">
                <r.Icon size={ICON_SIZE} weight="duotone" color={r.color} />
              </span>
              <span className="accum-narrative-label">{r.label}</span>
              <strong className="accum-narrative-value">
                {formatEnergyCompactKWhUk(r.valueKwh)}
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
              <span className="accum-narrative-pct">{formatPercent(pct)}</span>
            </li>
          )
        })}
      </ul>
      <p className="flow-period-balance">
        Баланс батареї:{' '}
        <strong className={balance >= 0 ? 'is-positive' : 'is-negative'}>
          {balance >= 0 ? '+' : ''}
          {formatEnergyCompactKWhUk(balance)}
        </strong>
      </p>
    </section>
  )
}
