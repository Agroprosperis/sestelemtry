import type { CurrentResponse } from '../../types'
import { formatEnergyCompactKWhUk } from '../format'

type Props = {
  current: CurrentResponse | null
  loading: boolean
}

function reading(current: CurrentResponse | null, key: string): number | null {
  const v = current?.metrics?.[key]?.value
  return typeof v === 'number' && Number.isFinite(v) ? v : null
}

function formatTotal(value: number | null, loading: boolean): string {
  if (loading) return '...'
  if (value === null) return '--'
  return formatEnergyCompactKWhUk(value)
}

type Row = {
  icon: string
  label: string
  value: number | null
}

export function AccumulatedSnapshotNarrative({ current, loading }: Props) {
  const rows: Row[] = [
    {
      icon: '☀',
      label: 'Виробіток СЕС',
      value: reading(current, 'accumulated_pv_energy_yield_kwh'),
    },
    {
      icon: '⚡',
      label: 'Споживання приладами',
      value: reading(current, 'accumulated_power_consumption_kwh'),
    },
    {
      icon: '🔌',
      label: 'Куплено з мережі',
      value: reading(current, 'accumulated_electricity_purchased_kwh'),
    },
    {
      icon: '🌐',
      label: 'Відпущено в мережу',
      value: reading(current, 'accumulated_electricity_sold_kwh'),
    },
    {
      icon: '🔋',
      label: 'Заряд батареї',
      value: reading(current, 'total_energy_charged_kwh'),
    },
    {
      icon: '🔋',
      label: 'Розряд батареї',
      value: reading(current, 'total_energy_discharged_kwh'),
    },
    {
      icon: '⚙',
      label: 'Постачання з мережі',
      value: reading(current, 'total_power_supply_from_grid_kwh'),
    },
  ]
  return (
    <section
      className="metrics-group daily-narrative"
      aria-labelledby="accumulated-snapshot-title"
      aria-busy={loading}
    >
      <h2 id="accumulated-snapshot-title" className="metrics-group-title">
        Накопичувальні показники
      </h2>
      <ul className="daily-narrative-list">
        {rows.map((row) => (
          <li key={row.label}>
            <span className="daily-narrative-icon" aria-hidden="true">
              {row.icon}
            </span>
            <span className="daily-narrative-label">{row.label}</span>
            <strong className="daily-narrative-value">
              {formatTotal(row.value, loading)}
            </strong>
          </li>
        ))}
      </ul>
    </section>
  )
}
