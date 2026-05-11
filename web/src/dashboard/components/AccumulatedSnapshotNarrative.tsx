import {
  ArrowDownLeft,
  ArrowUpRight,
  BatteryFull,
  Buildings,
  Plug,
  Sun,
} from '@phosphor-icons/react'
import type { CurrentResponse, RegisterMeta } from '../../types'
import { formatEnergyCompactKWhUk } from '../format'
import { ModbusAddr } from './ModbusAddr'

type Props = {
  current: CurrentResponse | null
  loading: boolean
  debug: boolean
  registers: Record<string, RegisterMeta> | null
}

const ICON_SIZE = 20

function reading(current: CurrentResponse | null, key: string): number | null {
  const v = current?.metrics?.[key]?.value
  return typeof v === 'number' && Number.isFinite(v) ? v : null
}

function formatTotal(value: number | null, loading: boolean): string {
  if (loading) return '...'
  if (value === null) return '--'
  return formatEnergyCompactKWhUk(value)
}

export function AccumulatedSnapshotNarrative({
  current,
  loading,
  debug,
  registers,
}: Props) {
  const pv = reading(current, 'accumulated_pv_energy_yield_kwh')
  const consumption = reading(current, 'accumulated_power_consumption_kwh')
  const purchased = reading(current, 'accumulated_electricity_purchased_kwh')
  const sold = reading(current, 'accumulated_electricity_sold_kwh')
  const charged = reading(current, 'total_energy_charged_kwh')
  const discharged = reading(current, 'total_energy_discharged_kwh')
  const gridSupply = reading(current, 'total_power_supply_from_grid_kwh')
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
        <li>
          <span className="daily-narrative-icon" aria-hidden="true">
            <Sun size={ICON_SIZE} weight="duotone" color="#f59e0b" />
          </span>
          <span>
            Виробіток СЕС
            <ModbusAddr
              debug={debug}
              registers={registers}
              keys="accumulated_pv_energy_yield_kwh"
            />
            : <strong>{formatTotal(pv, loading)}</strong>
          </span>
        </li>
        <li>
          <span className="daily-narrative-icon" aria-hidden="true">
            <Buildings size={ICON_SIZE} weight="duotone" color="#7c3aed" />
          </span>
          <span>
            Споживання приладами
            <ModbusAddr
              debug={debug}
              registers={registers}
              keys="accumulated_power_consumption_kwh"
            />
            : <strong>{formatTotal(consumption, loading)}</strong>
          </span>
        </li>
        <li>
          <span className="daily-narrative-icon" aria-hidden="true">
            <ArrowDownLeft size={ICON_SIZE} weight="bold" color="#3b82f6" />
          </span>
          <span>
            Куплено з мережі
            <ModbusAddr
              debug={debug}
              registers={registers}
              keys="accumulated_electricity_purchased_kwh"
            />
            : <strong>{formatTotal(purchased, loading)}</strong>
          </span>
        </li>
        <li>
          <span className="daily-narrative-icon" aria-hidden="true">
            <ArrowUpRight size={ICON_SIZE} weight="bold" color="#22c55e" />
          </span>
          <span>
            Відпущено в мережу
            <ModbusAddr
              debug={debug}
              registers={registers}
              keys="accumulated_electricity_sold_kwh"
            />
            : <strong>{formatTotal(sold, loading)}</strong>
          </span>
        </li>
        <li>
          <span className="daily-narrative-icon" aria-hidden="true">
            <BatteryFull size={ICON_SIZE} weight="duotone" color="#22c55e" />
          </span>
          <span>
            Батарея
            <ModbusAddr
              debug={debug}
              registers={registers}
              keys={['total_energy_charged_kwh', 'total_energy_discharged_kwh']}
            />
            : заряд <strong>{formatTotal(charged, loading)}</strong>, розряд{' '}
            <strong>{formatTotal(discharged, loading)}</strong>
          </span>
        </li>
        <li>
          <span className="daily-narrative-icon" aria-hidden="true">
            <Plug size={ICON_SIZE} weight="duotone" color="#475569" />
          </span>
          <span>
            Постачання з мережі (загальне)
            <ModbusAddr
              debug={debug}
              registers={registers}
              keys="total_power_supply_from_grid_kwh"
            />
            : <strong>{formatTotal(gridSupply, loading)}</strong>
          </span>
        </li>
      </ul>
    </section>
  )
}
