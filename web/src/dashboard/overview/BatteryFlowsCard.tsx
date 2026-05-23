import {
  ArrowDownLeft,
  ArrowUpRight,
  Lightning,
  Sun,
  type Icon,
} from '@phosphor-icons/react'
import type { EnergyFlows } from '../transforms/flows'
import { formatEnergyUk, formatPercent } from './format'

type Props = {
  flows: EnergyFlows
  loading?: boolean
}

type FlowRow = {
  label: string
  valueKwh: number
  Icon: Icon
  color: string
  // bucket=in groups inflows (PV→ESS, Grid→ESS) and bucket=out
  // groups outflows (ESS→Load, ESS→Grid) so the percentages on
  // each row are computed against the right denominator instead of
  // a single total that mixes directions.
  bucket: 'in' | 'out'
}

export function BatteryFlowsCard({ flows, loading }: Props) {
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
  const totalIn = rows.filter((r) => r.bucket === 'in').reduce((s, r) => s + r.valueKwh, 0)
  const totalOut = rows.filter((r) => r.bucket === 'out').reduce((s, r) => s + r.valueKwh, 0)
  const denomFor = (bucket: 'in' | 'out') => (bucket === 'in' ? totalIn : totalOut)
  const balance = totalIn - totalOut

  return (
    <section
      className="overview-card overview-card--flows"
      aria-busy={loading || undefined}
    >
      <header className="overview-card-head">
        <h2 className="overview-card-title">Перетік за день (батарея)</h2>
      </header>
      <ul className="overview-flowlist">
        {rows.map((r) => {
          const denom = denomFor(r.bucket)
          const pct = denom > 0 ? (r.valueKwh / denom) * 100 : 0
          return (
            <li key={r.label} className="overview-flowlist-item">
              <span className="overview-flowlist-icon" aria-hidden="true">
                <r.Icon size={20} weight="duotone" color={r.color} />
              </span>
              <span className="overview-flowlist-label">{r.label}</span>
              <strong className="overview-flowlist-value">
                {formatEnergyUk(r.valueKwh)}
              </strong>
              <span className="overview-flowlist-bar" aria-hidden="true">
                <span
                  className="overview-flowlist-bar-fill"
                  style={{
                    width: `${Math.max(0, Math.min(100, pct))}%`,
                    background: r.color,
                  }}
                />
              </span>
              <span className="overview-flowlist-pct">{formatPercent(pct)}</span>
            </li>
          )
        })}
      </ul>
      <p className="overview-flowlist-foot">
        Баланс батареї: {' '}
        <strong className={balance >= 0 ? 'is-positive' : 'is-negative'}>
          {balance >= 0 ? '+' : ''}
          {formatEnergyUk(balance)}
        </strong>
      </p>
    </section>
  )
}
