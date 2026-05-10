import {
  BatteryCharging,
  BatteryFull,
  Buildings,
  Gauge,
  Lightning,
  Sun,
  SunDim,
} from '@phosphor-icons/react'
import type { CurrentResponse } from '../../types'
import { formatChartNumber } from '../format'
import type { LiveAllocation } from '../transforms/liveAllocation'

// CurrentSnapshotNarrative renders the "Поточне енергоспоживання"
// card on the left panel. Each row shows the live reading of one
// node (PV / ESS / Load / Grid / SOC) plus a tiny animated flow
// rail on the right that mirrors the directional state of the live
// energy-flow diagram — so the card alone tells the story of where
// the kilowatts are going right now without operators needing to
// look at a separate chart.
//
// Sign and load conventions match liveAllocationFromCurrent:
//   * site_load = |pv + grid + ess|   (bus-balance derivation)
//   * idle threshold = LiveAllocation's IDLE_KW (we read pre-derived
//     state from liveAllocation, so thresholds stay in one place)
//
// The right-side rail uses the same marching-dashes animation as the
// diagram. Dot direction == direction of the kW relative to the
// system bus: outflow (PV gen, ESS discharge, grid import) marches
// rightward; inflow (load, ESS charge, grid export) marches leftward.

type RowKey =
  | 'pv'
  | 'ess'
  | 'load'
  | 'grid'
  | 'soc'

type FlowDir = 'out' | 'in' | 'idle'

type Row = {
  key: RowKey
  metricKey: string
  label: string
  unit: string
}

const PV_KEY = 'active_pv_power_kw'
const GRID_KEY = 'grid_connected_active_power_kw'
const ESS_KEY = 'active_ess_power_kw'
const LOAD_KEY = 'load_power_kw'
const SOC_KEY = 'soc_percent'

const ICON_SIZE = 20

const ROWS: Row[] = [
  { key: 'pv', metricKey: PV_KEY, label: 'СЕС', unit: 'кВт' },
  { key: 'ess', metricKey: ESS_KEY, label: 'УЗЕ', unit: 'кВт' },
  { key: 'load', metricKey: LOAD_KEY, label: 'Навантаження', unit: 'кВт' },
  { key: 'grid', metricKey: GRID_KEY, label: 'Точка приєднання', unit: 'кВт' },
  { key: 'soc', metricKey: SOC_KEY, label: 'SOC', unit: '%' },
]

// Colour palette mirrors EnergyFlowLive's COLORS so the in-card
// rail and the (now optional) standalone diagram stay visually
// consistent.
const COLORS = {
  pv: '#3b82f6',
  load: '#7c3aed',
  essCharge: '#3b82f6',
  essDischarge: '#22c55e',
  gridImport: '#f59e0b',
  gridExport: '#22c55e',
  socAccent: '#6366f1',
  idle: '#cbd5e1',
}

type Props = {
  current: CurrentResponse | null
  liveAllocation: LiveAllocation
  loading: boolean
}

function formatValue(
  value: number | null | undefined,
  unit: string,
  loading: boolean,
): string {
  if (loading) return '...'
  if (value == null || !Number.isFinite(value)) return '--'
  return `${formatChartNumber(value)} ${unit}`
}

function readMetric(current: CurrentResponse | null, key: string): number | null {
  const v = current?.metrics?.[key]?.value
  return typeof v === 'number' && Number.isFinite(v) ? v : null
}

// rowSignal maps a row to its (icon, colour, flow direction, kW)
// based on the live allocation. We use liveAllocation rather than
// reading metrics directly so the per-row visuals match the central
// diagram exactly (including the bus-balance-derived load and the
// IDLE_KW threshold for "no flow").
function rowSignal(
  key: RowKey,
  allocation: LiveAllocation,
): {
  icon: React.ReactNode
  color: string
  direction: FlowDir
  kw: number
  value: number | null
} {
  switch (key) {
    case 'pv': {
      const generating = allocation.pvState === 'generating'
      return {
        icon: generating ? (
          <Sun size={ICON_SIZE} weight="duotone" color={COLORS.pv} />
        ) : (
          <SunDim size={ICON_SIZE} weight="duotone" color={COLORS.idle} />
        ),
        color: generating ? COLORS.pv : COLORS.idle,
        direction: generating ? 'out' : 'idle',
        kw: allocation.pvKw,
        value: allocation.pvKw,
      }
    }
    case 'ess': {
      const charging = allocation.essState === 'charging'
      const discharging = allocation.essState === 'discharging'
      const color = charging
        ? COLORS.essCharge
        : discharging
          ? COLORS.essDischarge
          : COLORS.idle
      return {
        icon: charging ? (
          <BatteryCharging size={ICON_SIZE} weight="duotone" color={color} />
        ) : (
          <BatteryFull size={ICON_SIZE} weight="duotone" color={color} />
        ),
        color,
        direction: charging ? 'in' : discharging ? 'out' : 'idle',
        kw: allocation.essKw,
        value: allocation.essKw,
      }
    }
    case 'load': {
      const consuming = allocation.loadState === 'consuming'
      return {
        icon: (
          <Buildings
            size={ICON_SIZE}
            weight="duotone"
            color={consuming ? COLORS.load : COLORS.idle}
          />
        ),
        color: consuming ? COLORS.load : COLORS.idle,
        direction: consuming ? 'in' : 'idle',
        kw: allocation.loadKw,
        value: allocation.loadKw,
      }
    }
    case 'grid': {
      const importing = allocation.gridState === 'importing'
      const exporting = allocation.gridState === 'exporting'
      const color = importing
        ? COLORS.gridImport
        : exporting
          ? COLORS.gridExport
          : COLORS.idle
      return {
        icon: <Lightning size={ICON_SIZE} weight="duotone" color={color} />,
        color,
        direction: importing ? 'out' : exporting ? 'in' : 'idle',
        kw: allocation.gridKw,
        value: allocation.gridKw,
      }
    }
    case 'soc': {
      return {
        icon: (
          <Gauge size={ICON_SIZE} weight="duotone" color={COLORS.socAccent} />
        ),
        color: COLORS.socAccent,
        direction: 'idle',
        kw: 0,
        value: allocation.socPercent,
      }
    }
  }
}

// clampSpeed maps |kW| to an animation-duration in seconds. The
// 2.6s ↔ 0.8s envelope matches the standalone EnergyFlowLive bounds
// proportionally so the card and the diagram march in unison.
function clampSpeed(kw: number): number {
  const abs = Math.abs(kw)
  if (!Number.isFinite(abs) || abs <= 0) return 2.6
  const ratio = Math.min(abs / 50, 1)
  return 2.6 - 1.8 * ratio
}

function balanceLine(allocation: LiveAllocation): {
  text: string
  tone: 'idle' | 'export' | 'import' | 'stale'
} {
  if (allocation.status === 'no_data') {
    return { text: 'Очікуємо опитування', tone: 'stale' }
  }
  if (allocation.netExportKw > 0.05) {
    return {
      text: `+${formatChartNumber(allocation.netExportKw)} кВт експорт`,
      tone: 'export',
    }
  }
  if (allocation.netExportKw < -0.05) {
    return {
      text: `−${formatChartNumber(Math.abs(allocation.netExportKw))} кВт імпорт`,
      tone: 'import',
    }
  }
  return { text: 'Без обміну з мережею', tone: 'idle' }
}

export function CurrentSnapshotNarrative({
  current,
  liveAllocation,
  loading,
}: Props) {
  const balance = balanceLine(liveAllocation)
  return (
    <section
      className="metrics-group daily-narrative daily-narrative--with-flow"
      aria-labelledby="current-snapshot-title"
      aria-busy={loading}
    >
      <h2 id="current-snapshot-title" className="metrics-group-title">
        Поточне енергоспоживання
      </h2>
      <ul className="daily-narrative-list">
        {ROWS.map((row) => {
          const sig = rowSignal(row.key, liveAllocation)
          // For SOC we trust /current directly (the live allocation
          // mirrors it but rounding could disagree); falling back
          // here keeps the value visible even before allocation
          // settles on the first poll.
          const displayValue =
            row.key === 'soc' ? readMetric(current, SOC_KEY) : sig.value
          return (
            <li key={row.key}>
              <span className="daily-narrative-icon" aria-hidden="true">
                {sig.icon}
              </span>
              <span className="daily-narrative-label">
                {row.label}:{' '}
                <strong>{formatValue(displayValue, row.unit, loading)}</strong>
              </span>
              {row.key !== 'soc' && (
                <RowFlow
                  direction={sig.direction}
                  color={sig.color}
                  kw={sig.kw}
                />
              )}
            </li>
          )
        })}
      </ul>
      <p
        className={`current-snapshot-balance current-snapshot-balance--${balance.tone}`}
      >
        <span className="current-snapshot-balance-dot" aria-hidden="true" />
        {balance.text}
      </p>
    </section>
  )
}

function RowFlow({
  direction,
  color,
  kw,
}: {
  direction: FlowDir
  color: string
  kw: number
}) {
  const active = direction !== 'idle'
  // We swap the path endpoints rather than CSS animation-direction
  // so a single `live-flow-march` keyframe handles both directions
  // consistently with the standalone diagram.
  const d = direction === 'in' ? 'M 36 4 L 0 4' : 'M 0 4 L 36 4'
  return (
    <svg
      className={`live-row-flow${active ? '' : ' is-idle'}`}
      viewBox="0 0 36 8"
      preserveAspectRatio="none"
      aria-hidden="true"
    >
      <path
        d={d}
        stroke={active ? color : COLORS.idle}
        strokeWidth={3}
        strokeLinecap="round"
        fill="none"
        className="live-row-flow-path"
        style={{ animationDuration: `${clampSpeed(kw)}s` }}
        vectorEffect="non-scaling-stroke"
      />
    </svg>
  )
}
