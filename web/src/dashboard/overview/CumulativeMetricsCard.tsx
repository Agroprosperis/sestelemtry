import {
  ArrowDownLeft,
  ArrowUpRight,
  BatteryFull,
  Buildings,
  Plug,
  Sun,
  type Icon,
} from '@phosphor-icons/react'
import type { CumulativeTotals } from './useOverviewData'
import { formatEnergyUkCompactMWh } from './format'

type Props = {
  cumulative: CumulativeTotals
  loading?: boolean
}

type Row = {
  label: string
  valueKwh: number
  Icon: Icon
  color: string
}

function formatReferenceLabel(at: Date | null): string {
  if (!at) return 'з початку періоду'
  return `з ${new Intl.DateTimeFormat('uk-UA', {
    day: 'numeric',
    month: 'long',
    year: 'numeric',
  }).format(at)}`
}

export function CumulativeMetricsCard({ cumulative, loading }: Props) {
  const rows: Row[] = [
    {
      label: 'Виробіток СЕС',
      valueKwh: cumulative.pvProducedKwh,
      Icon: Sun,
      color: '#f59e0b',
    },
    {
      label: 'Споживання приладів',
      valueKwh: cumulative.consumptionKwh,
      Icon: Buildings,
      color: '#7c3aed',
    },
    {
      label: 'Куплено з мережі',
      valueKwh: cumulative.gridImportKwh,
      Icon: ArrowDownLeft,
      color: '#3b82f6',
    },
    {
      label: 'Відпущено в мережу',
      valueKwh: cumulative.gridExportKwh,
      Icon: ArrowUpRight,
      color: '#22c55e',
    },
    {
      label: 'Батарея: заряд',
      valueKwh: cumulative.essChargedKwh,
      Icon: BatteryFull,
      color: '#16a34a',
    },
    {
      label: 'Батарея: розряд',
      valueKwh: cumulative.essDischargedKwh,
      Icon: BatteryFull,
      color: '#94a3b8',
    },
    {
      label: 'Постачання з мережі (загальне)',
      valueKwh: cumulative.gridSupplyKwh,
      Icon: Plug,
      color: '#475569',
    },
  ]
  // Bar widths normalize against the largest value in the set so
  // every row has a non-trivial fill — picking a bigger anchor
  // (e.g. the load total) would crush every other row to a
  // hairline, defeating the at-a-glance comparison. The card is a
  // relative, not absolute, snapshot.
  const max = rows.reduce((m, r) => (r.valueKwh > m ? r.valueKwh : m), 0)

  return (
    <section
      className="overview-card overview-card--cumulative"
      aria-busy={loading || undefined}
    >
      <header className="overview-card-head">
        <h2 className="overview-card-title">Накопичувальні показники</h2>
        <span className="overview-card-date">
          {formatReferenceLabel(cumulative.referenceAt)}
        </span>
      </header>
      <ul className="overview-flowlist overview-flowlist--cumulative">
        {rows.map((r) => {
          const pct = max > 0 ? (r.valueKwh / max) * 100 : 0
          return (
            <li key={r.label} className="overview-flowlist-item">
              <span className="overview-flowlist-icon" aria-hidden="true">
                <r.Icon size={20} weight="duotone" color={r.color} />
              </span>
              <span className="overview-flowlist-label">{r.label}</span>
              <strong className="overview-flowlist-value">
                {formatEnergyUkCompactMWh(r.valueKwh)}
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
            </li>
          )
        })}
      </ul>
    </section>
  )
}
