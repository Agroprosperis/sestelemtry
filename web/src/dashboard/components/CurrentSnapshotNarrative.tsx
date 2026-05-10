import {
  BatteryFull,
  Buildings,
  Gauge,
  Lightning,
  Sun,
} from '@phosphor-icons/react'
import type { CurrentResponse } from '../../types'
import { formatChartNumber } from '../format'

type Row = {
  key: string
  icon: React.ReactNode
  label: string
  unit: string
}

// ROWS hard-codes the five live metrics that make up the "Поточне
// енергоспоживання" snapshot. Labels are intentionally shorter than the
// backend DashboardMetric labels (which include English glosses) so each
// row fits comfortably on one line in the narrative layout.
//
// `load_power_kw` is derived (not read directly): the SmartLogger's
// 40503 register reflects only the inverter's "Backup load" branch and
// undercounts site-wide consumption during normal grid-tied operation.
// Instead we sum the bus inputs (PV + Grid + ESS, with our sign
// convention: PV ≥ 0, Grid > 0 = import, ESS > 0 = discharge) — same
// derivation as the day chart's load line — so the card matches what
// the chart shows.
const LOAD_KEY = 'load_power_kw'
const PV_KEY = 'active_pv_power_kw'
const GRID_KEY = 'grid_connected_active_power_kw'
const ESS_KEY = 'active_ess_power_kw'

const ICON_SIZE = 20

const ROWS: Row[] = [
  {
    key: PV_KEY,
    icon: <Sun size={ICON_SIZE} weight="duotone" color="#f59e0b" />,
    label: 'СЕС',
    unit: 'кВт',
  },
  {
    key: ESS_KEY,
    icon: <BatteryFull size={ICON_SIZE} weight="duotone" color="#22c55e" />,
    label: 'УЗЕ',
    unit: 'кВт',
  },
  {
    key: LOAD_KEY,
    icon: <Buildings size={ICON_SIZE} weight="duotone" color="#7c3aed" />,
    label: 'Навантаження',
    unit: 'кВт',
  },
  {
    key: GRID_KEY,
    icon: <Lightning size={ICON_SIZE} weight="duotone" color="#f59e0b" />,
    label: 'Точка приєднання',
    unit: 'кВт',
  },
  {
    key: 'soc_percent',
    icon: <Gauge size={ICON_SIZE} weight="duotone" color="#6366f1" />,
    label: 'SOC',
    unit: '%',
  },
]

type Props = {
  current: CurrentResponse | null
  loading: boolean
}

function formatValue(value: number | null | undefined, unit: string, loading: boolean): string {
  if (loading) return '...'
  if (value == null || !Number.isFinite(value)) return '--'
  return `${formatChartNumber(value)} ${unit}`
}

function readMetric(current: CurrentResponse | null, key: string): number | null {
  const v = current?.metrics?.[key]?.value
  return typeof v === 'number' && Number.isFinite(v) ? v : null
}

// derivedLoadKw mirrors the day chart's bus-balance derivation
// (see web/src/dashboard/transforms/power.ts). Returns null when any
// input is missing — a partial sum would mislead.
function derivedLoadKw(current: CurrentResponse | null): number | null {
  const pv = readMetric(current, PV_KEY)
  const grid = readMetric(current, GRID_KEY)
  const ess = readMetric(current, ESS_KEY)
  if (pv === null || grid === null || ess === null) return null
  return Math.abs(pv + grid + ess)
}

export function CurrentSnapshotNarrative({ current, loading }: Props) {
  return (
    <section
      className="metrics-group daily-narrative"
      aria-labelledby="current-snapshot-title"
      aria-busy={loading}
    >
      <h2 id="current-snapshot-title" className="metrics-group-title">
        Поточне енергоспоживання
      </h2>
      <ul className="daily-narrative-list">
        {ROWS.map((row) => {
          const value =
            row.key === LOAD_KEY
              ? derivedLoadKw(current)
              : (current?.metrics?.[row.key]?.value ?? null)
          return (
            <li key={row.key}>
              <span className="daily-narrative-icon" aria-hidden="true">
                {row.icon}
              </span>
              <span>
                {row.label}:{' '}
                <strong>{formatValue(value, row.unit, loading)}</strong>
              </span>
            </li>
          )
        })}
      </ul>
    </section>
  )
}
