import { useEffect, useState } from 'react'
import {
  BatteryCharging,
  BatteryFull,
  Buildings,
  Lightning,
  Sun,
  SunDim,
} from '@phosphor-icons/react'
import { formatChartNumber } from '../format'
import type { LiveAllocation } from '../transforms/liveAllocation'

// EnergyFlowLive renders a real-time site-wide power flow diagram.
// Four corner nodes (PV / Load / Battery / Grid) connect to a
// central hub via bezier paths whose flowing-dot animation marches
// in the direction the energy is currently moving. The diagram
// reads at a glance: which sources are active, where they're
// pushing power, and whether the site is net-importing or
// net-exporting from the grid.
//
// Implementation note: we use a hybrid HTML+SVG layout — corner
// cards and the hub are HTML divs (so Phosphor icons, fonts and
// progress bars stay native) absolutely positioned over an SVG
// connection layer that hosts the cubic bezier paths. The
// container locks a 2:1 aspect ratio so SVG coordinates and CSS
// percentages stay in sync when the dashboard resizes. The SVG
// uses viewBox 0 0 1000 500 with preserveAspectRatio="xMidYMid
// meet" so paths never distort, while corner cards use the same
// percentage breakdown so they sit exactly on the SVG anchor
// points.
//
// Animation: each active path uses stroke-dasharray + animated
// stroke-dashoffset (CSS `live-flow-march` keyframe) to produce
// the marching-dots effect. The animation-direction flips for
// reversed flows (charging battery, importing grid). Speed
// scales with the absolute kW magnitude — busier flows march
// faster — bounded so a 0.1 kW trickle still moves visibly and
// a 50 kW surge doesn't blur into a solid line.

type EdgeId = 'pv' | 'load' | 'ess' | 'grid'

type EdgeShape = {
  // d is the cubic bezier from the corner-card-side anchor toward
  // the hub. dReverse swaps endpoints (used when the energy flows
  // hub→corner; the visual stroke is identical, only the marching
  // direction flips).
  d: string
  dReverse: string
  // labelPos is the rough midpoint where the kW value sits.
  labelX: number
  labelY: number
  // textAnchor for the label so it doesn't overlap the curve.
  textAnchor: 'start' | 'middle' | 'end'
}

// Coordinates assume viewBox 0 0 1000 500. Card edges are at:
//   PV   right=300,  Y=140
//   Load left =700,  Y=140
//   ESS  right=300,  Y=360
//   Grid left =700,  Y=360
// Hub anchors at left=440 / right=560, top=230 / bottom=270.
const EDGE_SHAPES: Record<EdgeId, EdgeShape> = {
  pv: {
    d: 'M 300 140 C 360 140, 380 230, 440 230',
    dReverse: 'M 440 230 C 380 230, 360 140, 300 140',
    labelX: 370,
    labelY: 168,
    textAnchor: 'middle',
  },
  load: {
    d: 'M 700 140 C 640 140, 620 230, 560 230',
    dReverse: 'M 560 230 C 620 230, 640 140, 700 140',
    labelX: 630,
    labelY: 168,
    textAnchor: 'middle',
  },
  ess: {
    d: 'M 300 360 C 360 360, 380 270, 440 270',
    dReverse: 'M 440 270 C 380 270, 360 360, 300 360',
    labelX: 370,
    labelY: 332,
    textAnchor: 'middle',
  },
  grid: {
    d: 'M 700 360 C 640 360, 620 270, 560 270',
    dReverse: 'M 560 270 C 620 270, 640 360, 700 360',
    labelX: 630,
    labelY: 332,
    textAnchor: 'middle',
  },
}

const COLORS = {
  pv: '#3b82f6',
  pvIdle: '#cbd5e1',
  load: '#7c3aed',
  loadIdle: '#cbd5e1',
  essCharge: '#3b82f6',
  essDischarge: '#22c55e',
  essIdle: '#cbd5e1',
  gridImport: '#f59e0b',
  gridExport: '#22c55e',
  gridIdle: '#cbd5e1',
  hub: '#0f172a',
}

// IDLE_OPACITY and the marching-speed bounds keep the diagram
// readable across the full dynamic range. 0.18 leaves the path
// ghosted but visible, so the topology always reads even when
// every source is offline.
const IDLE_OPACITY = 0.18

function clampSpeed(kw: number): number {
  // Map kW magnitude → animation-duration in seconds. Lower
  // duration = faster march. Bounds picked empirically so a
  // 0.1 kW idle leak still moves perceptibly (3 s/period) and
  // 50 kW peaks don't smear past the eye (0.6 s/period).
  const abs = Math.abs(kw)
  if (!Number.isFinite(abs) || abs <= 0) return 3
  const maxKw = 50
  const ratio = Math.min(abs / maxKw, 1)
  return 3 - 2.4 * ratio
}

function formatKw(value: number): string {
  return `${formatChartNumber(value)} kW`
}

function formatSeconds(seconds: number): string {
  if (seconds < 60) return `${seconds} с`
  const m = Math.floor(seconds / 60)
  const s = seconds % 60
  return s === 0 ? `${m} хв` : `${m} хв ${s} с`
}

type EdgeRender = {
  id: EdgeId
  d: string
  color: string
  active: boolean
  speedSec: number
}

function buildEdges(allocation: LiveAllocation): EdgeRender[] {
  const pvActive = allocation.pvState !== 'idle'
  const loadActive = allocation.loadState !== 'idle'
  const essActive = allocation.essState !== 'idle'
  const gridActive = allocation.gridState !== 'idle'
  return [
    {
      id: 'pv',
      // PV always feeds inward when generating; the corner→hub
      // direction matches the sun-as-source convention.
      d: pvActive ? EDGE_SHAPES.pv.d : EDGE_SHAPES.pv.d,
      color: pvActive ? COLORS.pv : COLORS.pvIdle,
      active: pvActive,
      speedSec: clampSpeed(allocation.pvKw),
    },
    {
      id: 'load',
      // hub→load — energy always exits the hub toward the load.
      d: EDGE_SHAPES.load.dReverse,
      color: loadActive ? COLORS.load : COLORS.loadIdle,
      active: loadActive,
      speedSec: clampSpeed(allocation.loadKw),
    },
    {
      id: 'ess',
      // ESS direction depends on state: discharging → ESS→hub
      // (forward), charging → hub→ESS (reverse). When idle the
      // path is rendered ghosted in the forward orientation so
      // the dashed pattern doesn't visibly snap when state flips.
      d:
        allocation.essState === 'charging'
          ? EDGE_SHAPES.ess.dReverse
          : EDGE_SHAPES.ess.d,
      color:
        allocation.essState === 'charging'
          ? COLORS.essCharge
          : allocation.essState === 'discharging'
            ? COLORS.essDischarge
            : COLORS.essIdle,
      active: essActive,
      speedSec: clampSpeed(allocation.essKw),
    },
    {
      id: 'grid',
      // Grid: exporting → hub→grid (reverse), importing →
      // grid→hub (forward). Same idle handling as ESS.
      d:
        allocation.gridState === 'exporting'
          ? EDGE_SHAPES.grid.dReverse
          : EDGE_SHAPES.grid.d,
      color:
        allocation.gridState === 'exporting'
          ? COLORS.gridExport
          : allocation.gridState === 'importing'
            ? COLORS.gridImport
            : COLORS.gridIdle,
      active: gridActive,
      speedSec: clampSpeed(allocation.gridKw),
    },
  ]
}

type Props = {
  allocation: LiveAllocation
}

export function EnergyFlowLive({ allocation }: Props) {
  // ageSeconds drives the "Updated N seconds ago" pill in the
  // header. We re-tick every second instead of recomputing on
  // each render so the value is correct even when /current
  // happens to return the same payload.
  const [now, setNow] = useState<number>(() => Date.now())
  useEffect(() => {
    const id = window.setInterval(() => setNow(Date.now()), 1000)
    return () => window.clearInterval(id)
  }, [])

  const ageSeconds =
    allocation.observedAt === null
      ? null
      : Math.max(0, Math.floor((now - allocation.observedAt.getTime()) / 1000))

  const edges = buildEdges(allocation)

  const balanceLabel = describeBalance(allocation)

  return (
    <section
      className="chart-card energy-flow-live-card"
      aria-label="Перетік потужності в реальному часі"
    >
      <header className="energy-flow-live-header">
        <h2>Перетік потужності (зараз)</h2>
        <span
          className={`energy-flow-live-status${
            allocation.status === 'no_data' ? ' is-stale' : ''
          }`}
          aria-live="polite"
        >
          <span className="energy-flow-live-dot" aria-hidden="true" />
          {allocation.status === 'no_data'
            ? 'Дані відсутні'
            : ageSeconds === null
              ? 'Оновлення…'
              : `Оновлено ${formatSeconds(ageSeconds)} тому`}
        </span>
      </header>
      <div className="energy-flow-live-stage">
        <svg
          viewBox="0 0 1000 500"
          className="energy-flow-live-svg"
          preserveAspectRatio="xMidYMid meet"
          role="img"
          aria-label="Діаграма потужностей"
        >
          {edges.map((edge) => (
            <g key={edge.id}>
              <path
                d={edge.d}
                stroke={edge.color}
                strokeWidth={6}
                fill="none"
                opacity={edge.active ? 0.25 : IDLE_OPACITY}
              />
              <path
                d={edge.d}
                stroke={edge.color}
                strokeWidth={6}
                fill="none"
                strokeLinecap="round"
                className={`energy-flow-live-path${
                  edge.active ? '' : ' is-idle'
                }`}
                style={{ animationDuration: `${edge.speedSec}s` }}
                data-edge={edge.id}
                data-active={edge.active ? '1' : '0'}
              />
              <text
                x={EDGE_SHAPES[edge.id].labelX}
                y={EDGE_SHAPES[edge.id].labelY}
                className="energy-flow-live-label"
                textAnchor={EDGE_SHAPES[edge.id].textAnchor}
                opacity={edge.active ? 1 : IDLE_OPACITY * 2}
              >
                {formatKw(kwForEdge(edge.id, allocation))}
              </text>
            </g>
          ))}
        </svg>

        <NodeCard
          variant="pv"
          icon={
            allocation.pvState === 'generating' ? (
              <Sun size={28} weight="duotone" color={COLORS.pv} />
            ) : (
              <SunDim size={28} weight="duotone" color={COLORS.pvIdle} />
            )
          }
          title="СЕС (PV)"
          kw={allocation.pvKw}
          status={allocation.pvState === 'generating' ? 'Генерує' : 'Очікування'}
          active={allocation.pvState === 'generating'}
        />
        <NodeCard
          variant="load"
          icon={<Buildings size={28} weight="duotone" color={COLORS.load} />}
          title="Споживання"
          kw={allocation.loadKw}
          status={allocation.loadState === 'consuming' ? 'Споживає' : 'Очікування'}
          active={allocation.loadState === 'consuming'}
        />
        <NodeCard
          variant="ess"
          icon={
            allocation.essState === 'charging' ? (
              <BatteryCharging size={28} weight="duotone" color={COLORS.essCharge} />
            ) : (
              <BatteryFull
                size={28}
                weight="duotone"
                color={
                  allocation.essState === 'discharging'
                    ? COLORS.essDischarge
                    : COLORS.essIdle
                }
              />
            )
          }
          title="Батарея (УЗЕ)"
          kw={allocation.essKw}
          status={
            allocation.essState === 'charging'
              ? 'Заряджається'
              : allocation.essState === 'discharging'
                ? 'Розряджається'
                : 'Очікування'
          }
          active={allocation.essState !== 'idle'}
          soc={allocation.socPercent}
          socColor={
            allocation.essState === 'charging'
              ? COLORS.essCharge
              : COLORS.essDischarge
          }
        />
        <NodeCard
          variant="grid"
          icon={
            <Lightning
              size={28}
              weight="duotone"
              color={
                allocation.gridState === 'importing'
                  ? COLORS.gridImport
                  : allocation.gridState === 'exporting'
                    ? COLORS.gridExport
                    : COLORS.gridIdle
              }
            />
          }
          title="Мережа"
          kw={allocation.gridKw}
          status={
            allocation.gridState === 'importing'
              ? 'Імпорт'
              : allocation.gridState === 'exporting'
                ? 'Експорт'
                : 'Очікування'
          }
          active={allocation.gridState !== 'idle'}
        />

        <div
          className={`energy-flow-live-hub${
            allocation.status === 'no_data' ? ' is-stale' : ''
          }`}
          aria-label="Стан системи"
        >
          <div className="energy-flow-live-hub-status">
            {allocation.status === 'no_data' ? 'Немає даних' : 'У нормі'}
          </div>
          <div className="energy-flow-live-hub-balance">
            {balanceLabel.kw}
          </div>
          <div className="energy-flow-live-hub-balance-text">
            {balanceLabel.text}
          </div>
        </div>
      </div>
    </section>
  )
}

function describeBalance(a: LiveAllocation): { kw: string; text: string } {
  if (a.status === 'no_data') return { kw: '--', text: 'очікуємо опитування' }
  if (a.netExportKw > 0.05) {
    return {
      kw: `+${formatChartNumber(a.netExportKw)} kW`,
      text: 'експорт у мережу',
    }
  }
  if (a.netExportKw < -0.05) {
    return {
      kw: `−${formatChartNumber(Math.abs(a.netExportKw))} kW`,
      text: 'імпорт з мережі',
    }
  }
  return { kw: '0 kW', text: 'обмін з мережею відсутній' }
}

function kwForEdge(id: EdgeId, a: LiveAllocation): number {
  switch (id) {
    case 'pv':
      return a.pvKw
    case 'load':
      return a.loadKw
    case 'ess':
      return a.essKw
    case 'grid':
      return a.gridKw
  }
}

type NodeCardProps = {
  variant: EdgeId
  icon: React.ReactNode
  title: string
  kw: number
  status: string
  active: boolean
  soc?: number | null
  socColor?: string
}

function NodeCard({
  variant,
  icon,
  title,
  kw,
  status,
  active,
  soc,
  socColor,
}: NodeCardProps) {
  return (
    <div
      className={`energy-flow-live-node energy-flow-live-node--${variant}${
        active ? '' : ' is-idle'
      }`}
      role="group"
      aria-label={`${title}: ${formatKw(kw)} (${status})`}
    >
      <div className="energy-flow-live-node-head">
        <span className="energy-flow-live-node-icon">{icon}</span>
        <h3>{title}</h3>
      </div>
      <strong className="energy-flow-live-node-kw">{formatKw(kw)}</strong>
      <span className="energy-flow-live-node-status">{status}</span>
      {typeof soc === 'number' && Number.isFinite(soc) && (
        <div className="energy-flow-live-soc" aria-label={`SOC ${soc.toFixed(0)}%`}>
          <div className="energy-flow-live-soc-track">
            <span
              className="energy-flow-live-soc-fill"
              style={{
                width: `${Math.max(0, Math.min(100, soc))}%`,
                background: socColor ?? COLORS.essDischarge,
              }}
            />
          </div>
          <span className="energy-flow-live-soc-label">SOC {soc.toFixed(0)}%</span>
        </div>
      )}
    </div>
  )
}
