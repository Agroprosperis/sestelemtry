import type { CurrentResponse } from '../../types'
import { formatEnergyCompactKWhUk } from '../format'

type Row = {
  key: string
  icon: string
  label: string
}

// Per-day counters reported directly by the inverter (registers
// 40438/40444/40468/40470/40509/40511/40513). They reset to zero at
// local midnight on the device, so we surface them verbatim instead of
// recomputing deltas client-side. Listed in source/sink order so the
// column reads top-to-bottom: produced -> consumed -> grid -> battery.
const ROWS: Row[] = [
  { key: 'pv_energy_yield_day_kwh', icon: '☀', label: 'СЕС згенерувала' },
  { key: 'power_consumption_day_kwh', icon: '⚡', label: 'Спожито приладами' },
  { key: 'electricity_purchased_day_kwh', icon: '🔌', label: 'Імпорт з мережі' },
  { key: 'electricity_sold_day_kwh', icon: '🌐', label: 'Експорт у мережу' },
  { key: 'power_supply_from_grid_day_kwh', icon: '⚙', label: 'Постачання з мережі' },
  { key: 'energy_charged_day_kwh', icon: '🔋', label: 'Заряд УЗЕ' },
  { key: 'energy_discharged_day_kwh', icon: '🪫', label: 'Розряд УЗЕ' },
]

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

export function TodayCountersNarrative({ current, loading }: Props) {
  return (
    <section
      className="metrics-group daily-narrative"
      aria-labelledby="today-counters-title"
      aria-busy={loading}
    >
      <h2 id="today-counters-title" className="metrics-group-title">
        Сьогоднішні лічильники
      </h2>
      <ul className="daily-narrative-list">
        {ROWS.map((row) => {
          const value = reading(current, row.key)
          return (
            <li key={row.key}>
              <span className="daily-narrative-icon" aria-hidden="true">
                {row.icon}
              </span>
              <span>
                {row.label}: <strong>{formatTotal(value, loading)}</strong>
              </span>
            </li>
          )
        })}
      </ul>
    </section>
  )
}
