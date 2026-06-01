import {
  ArrowDownLeft,
  ArrowUpRight,
  BatteryFull,
  Buildings,
  Plug,
  Sun,
  type Icon,
} from '@phosphor-icons/react'
import type { CurrentResponse, RegisterMeta } from '../../types'
import { formatEnergyCompactKWhUk } from '../format'
import { LoadingSpinner } from './LoadingSpinner'
import { ModbusAddr } from './ModbusAddr'

type Props = {
  current: CurrentResponse | null
  loading: boolean
  debug: boolean
  registers: Record<string, RegisterMeta> | null
}

const ICON_SIZE = 20

type RowSpec = {
  label: string
  metricKey: string | string[]
  Icon: Icon
  color: string
  value: number | null
}

function reading(current: CurrentResponse | null, key: string): number | null {
  const v = current?.metrics?.[key]?.value
  return typeof v === 'number' && Number.isFinite(v) ? v : null
}

// PLACEHOLDER is shown only when we have no data at all (the very
// first load before /current resolves). On subsequent background
// refreshes we keep the previous numbers on screen — the spinner in
// the card header signals "refresh in flight" without leaving the
// operator staring at dashes for the duration of the request. Stale-
// while-revalidate matches what the rest of the dashboard already
// does for charts.
const PLACEHOLDER = '—'

function formatTotal(value: number | null): string {
  if (value === null) return PLACEHOLDER
  return formatEnergyCompactKWhUk(value)
}

export function AccumulatedSnapshotNarrative({
  current,
  loading,
  debug,
  registers,
}: Props) {
  const rows: RowSpec[] = [
    {
      label: 'Виробіток СЕС',
      metricKey: 'accumulated_pv_energy_yield_kwh',
      Icon: Sun,
      color: '#f59e0b',
      value: reading(current, 'accumulated_pv_energy_yield_kwh'),
    },
    {
      label: 'Споживання приладів',
      metricKey: 'accumulated_power_consumption_kwh',
      Icon: Buildings,
      color: '#7c3aed',
      value: reading(current, 'accumulated_power_consumption_kwh'),
    },
    {
      label: 'Куплено з мережі',
      metricKey: 'accumulated_electricity_purchased_kwh',
      Icon: ArrowDownLeft,
      color: '#3b82f6',
      value: reading(current, 'accumulated_electricity_purchased_kwh'),
    },
    {
      label: 'Відпущено в мережу',
      metricKey: 'accumulated_electricity_sold_kwh',
      Icon: ArrowUpRight,
      color: '#22c55e',
      value: reading(current, 'accumulated_electricity_sold_kwh'),
    },
    {
      label: 'Батарея: заряд',
      metricKey: 'total_energy_charged_kwh',
      Icon: BatteryFull,
      color: '#16a34a',
      value: reading(current, 'total_energy_charged_kwh'),
    },
    {
      label: 'Батарея: розряд',
      metricKey: 'total_energy_discharged_kwh',
      Icon: BatteryFull,
      color: '#94a3b8',
      value: reading(current, 'total_energy_discharged_kwh'),
    },
    {
      label: 'Постачання з мережі (загальне)',
      metricKey: 'total_power_supply_from_grid_kwh',
      Icon: Plug,
      color: '#475569',
      value: reading(current, 'total_power_supply_from_grid_kwh'),
    },
  ]

  // Bar widths normalize against the largest reading so every row
  // has a non-trivial fill — picking an absolute scale would crush
  // small values to a hairline. The card is a relative comparison,
  // not an absolute one. Bars stay rendered during background
  // refreshes (stale-while-revalidate) so the operator doesn't see
  // them collapse to zero between live ticks.
  const max = rows.reduce(
    (m, r) => (r.value !== null && r.value > m ? r.value : m),
    0,
  )

  // The header spinner is only rendered before the very first
  // /current sample arrives. After that, background refreshes run
  // silently — the dashboard polls /current once a second, so a
  // visible spinner on every tick would feel like the card is
  // constantly broken even though it isn't.
  const showFirstLoadSpinner = loading && current === null
  return (
    <section
      className="metrics-group accum-narrative"
      aria-labelledby="accumulated-snapshot-title"
      aria-busy={showFirstLoadSpinner}
    >
      <header className="metrics-group-header">
        <h2 id="accumulated-snapshot-title" className="metrics-group-title">
          Накопичувальні показники
        </h2>
        {showFirstLoadSpinner && (
          <LoadingSpinner label="Завантаження лічильників" />
        )}
      </header>
      <ul className="accum-narrative-list">
        {rows.map((r) => {
          const pct = max > 0 && r.value !== null ? (r.value / max) * 100 : 0
          return (
            <li key={r.label} className="accum-narrative-item">
              <span className="accum-narrative-icon" aria-hidden="true">
                <r.Icon size={ICON_SIZE} weight="duotone" color={r.color} />
              </span>
              <span className="accum-narrative-label">
                {r.label}
                <ModbusAddr
                  debug={debug}
                  registers={registers}
                  keys={r.metricKey}
                />
              </span>
              <strong className="accum-narrative-value">
                {formatTotal(r.value)}
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
            </li>
          )
        })}
      </ul>
    </section>
  )
}
