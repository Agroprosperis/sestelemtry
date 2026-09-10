import {
  ArrowDownLeft,
  ArrowUpRight,
  BatteryCharging,
  BatteryFull,
  Buildings,
  Plug,
  Sun,
} from '@phosphor-icons/react'
import type { CurrentResponse, RegisterMeta } from '../../types'
import { useCssVar } from '../../theme/useChartChrome'
import { formatEnergyCompactKWhUk } from '../format'
import { ModbusAddr } from './ModbusAddr'

type Row = {
  key: string
  icon: React.ReactNode
  label: string
}

const ICON_SIZE = 20

// Per-day counters reported directly by the inverter (registers
// 40438/40444/40468/40470/40509/40511/40513). They reset to zero at
// local midnight on the device, so we surface them verbatim instead of
// recomputing deltas client-side. Listed in source/sink order so the
// column reads top-to-bottom: produced -> consumed -> grid -> battery.
const ROWS: Row[] = [
  {
    key: 'pv_energy_yield_day_kwh',
    icon: <Sun size={ICON_SIZE} weight="duotone" color="#f59e0b" />,
    label: 'Виробіток СЕС за день',
  },
  {
    key: 'power_consumption_day_kwh',
    icon: <Buildings size={ICON_SIZE} weight="duotone" color="#7c3aed" />,
    label: 'Споживання елеватора за день',
  },
  {
    key: 'electricity_purchased_day_kwh',
    icon: <ArrowDownLeft size={ICON_SIZE} weight="bold" color="#3b82f6" />,
    label: 'Імпорт з мережі за день',
  },
  {
    key: 'electricity_sold_day_kwh',
    icon: <ArrowUpRight size={ICON_SIZE} weight="bold" color="#22c55e" />,
    label: 'Експорт в мережу за день',
  },
  {
    key: 'power_supply_from_grid_day_kwh',
    icon: null,
    label: 'Постачання з мережі за день',
  },
  {
    key: 'energy_charged_day_kwh',
    icon: <BatteryCharging size={ICON_SIZE} weight="duotone" color="#3b82f6" />,
    label: 'Заряд УЗЕ за день',
  },
  {
    key: 'energy_discharged_day_kwh',
    icon: <BatteryFull size={ICON_SIZE} weight="duotone" color="#22c55e" />,
    label: 'Розряд УЗЕ за день',
  },
]

type Props = {
  current: CurrentResponse | null
  loading: boolean
  debug: boolean
  registers: Record<string, RegisterMeta> | null
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

export function TodayCountersNarrative({ current, loading, debug, registers }: Props) {
  const muted = useCssVar('--text-muted', '#475569')
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
          const icon =
            row.key === 'power_supply_from_grid_day_kwh' ? (
              <Plug size={ICON_SIZE} weight="duotone" color={muted} />
            ) : (
              row.icon
            )
          return (
            <li key={row.key}>
              <span className="daily-narrative-icon" aria-hidden="true">
                {icon}
              </span>
              <span>
                {row.label}
                <ModbusAddr debug={debug} registers={registers} keys={row.key} />
                : <strong>{formatTotal(value, loading)}</strong>
              </span>
            </li>
          )
        })}
      </ul>
    </section>
  )
}
