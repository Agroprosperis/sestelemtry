import { useMemo, useState } from 'react'
import { formatEnergyCompactKWh } from '../format'
import type { EnergyFlows } from '../transforms/flows'

// EnergyFlowSankey renders the four-node site energy flow (PV /
// Grid / ESS / Load) as a Sankey diagram in pure SVG. We avoid
// d3-sankey on purpose — the layout is fixed (always four nodes in
// the same positions), so a hand-laid-out cubic Bezier per edge is
// shorter than the dependency, has no runtime, and produces a
// consistent layout across data points.
//
// The component is self-contained: input is the seven flows from
// `flowsFromTotals`, output is one <svg> with hover tooltips and a
// legend on the side. When all flows are zero (period before the
// energy-flow aggregator started) the diagram renders translucent
// node placeholders and a hint instead of an empty card.

const NODE_WIDTH = 18
const NODE_PAD_Y = 24
const SVG_PADDING_X = 120
const SVG_PADDING_Y = 32
const SVG_HEIGHT = 360
const SVG_WIDTH = 720
const MIN_STREAM_W = 1.5
const MAX_STREAM_W = 56

type NodeID = 'pv' | 'grid' | 'ess' | 'load'

type NodeDef = {
  id: NodeID
  label: string
  side: 'left' | 'right'
  // y-axis ordinal (0 = top, 1 = bottom) used to lay out nodes
  // vertically within the SVG. PV/ESS occupy the left column, Grid
  // and Load occupy the right column.
  row: 0 | 1
  color: string
}

const NODES: Record<NodeID, NodeDef> = {
  pv: { id: 'pv', label: 'СЕС', side: 'left', row: 0, color: '#f59e0b' },
  ess: { id: 'ess', label: 'УЗЕ', side: 'left', row: 1, color: '#22c55e' },
  grid: { id: 'grid', label: 'Мережа', side: 'right', row: 1, color: '#3b82f6' },
  load: { id: 'load', label: 'Споживання', side: 'right', row: 0, color: '#7c3aed' },
}

type FlowDef = {
  id: string
  from: NodeID
  to: NodeID
  label: string
  // Color: each edge picks the source node's hue so the diagram
  // reads "where energy came from"; this matches the Sankey
  // convention used by Open Energy Monitor and similar tools.
  color: string
}

const FLOWS: readonly FlowDef[] = [
  { id: 'pv_to_load', from: 'pv', to: 'load', label: 'СЕС → Споживання', color: '#fbbf24' },
  { id: 'pv_to_ess', from: 'pv', to: 'ess', label: 'СЕС → УЗЕ', color: '#facc15' },
  { id: 'pv_to_grid', from: 'pv', to: 'grid', label: 'СЕС → Мережа', color: '#fde68a' },
  { id: 'grid_to_load', from: 'grid', to: 'load', label: 'Мережа → Споживання', color: '#60a5fa' },
  { id: 'grid_to_ess', from: 'grid', to: 'ess', label: 'Мережа → УЗЕ', color: '#93c5fd' },
  { id: 'ess_to_load', from: 'ess', to: 'load', label: 'УЗЕ → Споживання', color: '#34d399' },
  { id: 'ess_to_grid', from: 'ess', to: 'grid', label: 'УЗЕ → Мережа', color: '#86efac' },
] as const

type Layout = {
  rect: { x: number; y: number; w: number; h: number }
  // Vertical "ports" on the inner edge of the node rectangle. Each
  // outgoing flow gets one, sorted top-to-bottom by destination row
  // so streams don't cross at the node face.
  ports: Map<string, { x: number; y: number }>
}

// nodeHeight scales the rectangle of a node to the kWh that flow
// through it. The longer side ratio is clamped to avoid a single
// dominant flow squashing the others to 0 px.
function nodeHeight(throughput: number, maxThroughput: number, maxHeight: number): number {
  if (maxThroughput <= 0) return 24
  const ratio = throughput / maxThroughput
  const clamped = Math.max(0.06, Math.min(ratio, 1))
  return Math.max(24, clamped * maxHeight)
}

// streamWidth maps a flow's kWh to a SVG stream thickness. It
// shares the diagram's max throughput so a 100 kWh flow looks
// proportionally larger than a 10 kWh one across all node pairs.
function streamWidth(value: number, maxValue: number): number {
  if (maxValue <= 0 || value <= 0) return 0
  const ratio = Math.min(value / maxValue, 1)
  return Math.max(MIN_STREAM_W, ratio * MAX_STREAM_W)
}

type Props = {
  flows: EnergyFlows
  loading?: boolean
}

export function EnergyFlowSankey({ flows, loading = false }: Props) {
  const [hovered, setHovered] = useState<string | null>(null)

  // throughput per node = sum of incoming + outgoing flow values;
  // we use the larger of the two so node height reflects the busier
  // side.
  const nodeThroughput = useMemo<Record<NodeID, number>>(() => {
    const inSum: Record<NodeID, number> = { pv: 0, ess: 0, grid: 0, load: 0 }
    const outSum: Record<NodeID, number> = { pv: 0, ess: 0, grid: 0, load: 0 }
    for (const f of FLOWS) {
      const v = valueFor(f.id, flows)
      outSum[f.from] += v
      inSum[f.to] += v
    }
    return {
      pv: Math.max(inSum.pv, outSum.pv),
      ess: Math.max(inSum.ess, outSum.ess),
      grid: Math.max(inSum.grid, outSum.grid),
      load: Math.max(inSum.load, outSum.load),
    }
  }, [flows])

  const maxThroughput = useMemo(
    () => Math.max(nodeThroughput.pv, nodeThroughput.ess, nodeThroughput.grid, nodeThroughput.load, 0),
    [nodeThroughput],
  )

  const layout = useMemo<Map<NodeID, Layout>>(() => {
    const usable = SVG_HEIGHT - 2 * SVG_PADDING_Y - NODE_PAD_Y
    const halfH = usable / 2
    const out = new Map<NodeID, Layout>()
    for (const n of Object.values(NODES)) {
      const x = n.side === 'left' ? SVG_PADDING_X : SVG_WIDTH - SVG_PADDING_X - NODE_WIDTH
      const h = nodeHeight(nodeThroughput[n.id], maxThroughput, halfH)
      const slotTop = SVG_PADDING_Y + n.row * (halfH + NODE_PAD_Y)
      const y = slotTop + (halfH - h) / 2
      const ports = new Map<string, { x: number; y: number }>()
      const outFlows = FLOWS.filter((f) => f.from === n.id)
      const inFlows = FLOWS.filter((f) => f.to === n.id)
      // Outgoing ports sit on the inner edge (right for left-side
      // nodes, left for right-side nodes); incoming ports on the
      // same edge but offset inward. Within each side we stack the
      // ports proportional to flow value so the dominant flow keeps
      // a stable y as the period changes.
      const outPortX = n.side === 'left' ? x + NODE_WIDTH : x
      const inPortX = outPortX
      const outTotal = outFlows.reduce((s, f) => s + valueFor(f.id, flows), 0)
      const inTotal = inFlows.reduce((s, f) => s + valueFor(f.id, flows), 0)
      let cursor = y
      const denom = Math.max(outTotal, 1)
      for (const f of outFlows) {
        const v = valueFor(f.id, flows)
        const portH = (v / denom) * h
        ports.set(`out:${f.id}`, { x: outPortX, y: cursor + portH / 2 })
        cursor += portH
      }
      cursor = y
      const denomIn = Math.max(inTotal, 1)
      for (const f of inFlows) {
        const v = valueFor(f.id, flows)
        const portH = (v / denomIn) * h
        ports.set(`in:${f.id}`, { x: inPortX, y: cursor + portH / 2 })
        cursor += portH
      }
      out.set(n.id, { rect: { x, y, w: NODE_WIDTH, h }, ports })
    }
    return out
  }, [flows, maxThroughput, nodeThroughput])

  const totalEnergy = nodeThroughput.pv + nodeThroughput.grid

  return (
    <section
      className="chart-card energy-flow-sankey-card"
      aria-label="Перетік енергії"
      aria-busy={loading}
    >
      <div className="energy-flow-sankey-header">
        <h2>Перетік енергії</h2>
        {!flows.hasEnergyFlowSamples && totalEnergy > 0 && (
          <span className="energy-flow-sankey-hint" role="note">
            Дані з лічильників УЗЕ ще не зібрані — стрічки до батареї будуть нульові
          </span>
        )}
      </div>
      <svg
        viewBox={`0 0 ${SVG_WIDTH} ${SVG_HEIGHT}`}
        role="img"
        aria-labelledby="energy-flow-sankey-title"
        className="energy-flow-sankey-svg"
        preserveAspectRatio="xMidYMid meet"
      >
        <title id="energy-flow-sankey-title">Перетік енергії за період між СЕС, УЗЕ, мережею та споживанням</title>
        <g>
          {FLOWS.map((f) => {
            const v = valueFor(f.id, flows)
            const w = streamWidth(v, Math.max(maxThroughput, 1))
            const fromLayout = layout.get(f.from)
            const toLayout = layout.get(f.to)
            if (!fromLayout || !toLayout || w === 0) return null
            const start = fromLayout.ports.get(`out:${f.id}`)
            const end = toLayout.ports.get(`in:${f.id}`)
            if (!start || !end) return null
            const dx = (end.x - start.x) * 0.5
            const c1 = `${start.x + dx},${start.y}`
            const c2 = `${end.x - dx},${end.y}`
            const path = `M${start.x},${start.y} C${c1} ${c2} ${end.x},${end.y}`
            const isHover = hovered === f.id
            return (
              <path
                key={f.id}
                d={path}
                stroke={f.color}
                strokeWidth={w}
                fill="none"
                opacity={hovered === null ? 0.55 : isHover ? 0.95 : 0.18}
                strokeLinecap="round"
                onMouseEnter={() => setHovered(f.id)}
                onMouseLeave={() => setHovered(null)}
              >
                <title>{`${f.label}: ${formatEnergyCompactKWh(v)}`}</title>
              </path>
            )
          })}
        </g>
        <g>
          {Object.values(NODES).map((n) => {
            const l = layout.get(n.id)
            if (!l) return null
            const labelX = n.side === 'left' ? l.rect.x - 8 : l.rect.x + l.rect.w + 8
            const labelAnchor = n.side === 'left' ? 'end' : 'start'
            return (
              <g key={n.id}>
                <rect
                  x={l.rect.x}
                  y={l.rect.y}
                  width={l.rect.w}
                  height={l.rect.h}
                  fill={n.color}
                  rx={3}
                />
                <text
                  x={labelX}
                  y={l.rect.y + l.rect.h / 2}
                  textAnchor={labelAnchor}
                  dominantBaseline="middle"
                  fontSize={13}
                  fill="#0f172a"
                >
                  {n.label}
                </text>
                <text
                  x={labelX}
                  y={l.rect.y + l.rect.h / 2 + 14}
                  textAnchor={labelAnchor}
                  dominantBaseline="middle"
                  fontSize={11}
                  fill="#64748b"
                >
                  {formatEnergyCompactKWh(nodeThroughput[n.id])}
                </text>
              </g>
            )
          })}
        </g>
      </svg>
      <ul className="energy-flow-sankey-legend">
        {FLOWS.map((f) => {
          const v = valueFor(f.id, flows)
          return (
            <li
              key={f.id}
              className={`energy-flow-sankey-legend-row${hovered === f.id ? ' is-hovered' : ''}`}
              onMouseEnter={() => setHovered(f.id)}
              onMouseLeave={() => setHovered(null)}
            >
              <span className="energy-flow-sankey-swatch" style={{ background: f.color }} />
              <span className="energy-flow-sankey-legend-label">{f.label}</span>
              <strong>{formatEnergyCompactKWh(v)}</strong>
            </li>
          )
        })}
      </ul>
    </section>
  )
}

function valueFor(id: string, f: EnergyFlows): number {
  switch (id) {
    case 'pv_to_load':
      return f.pvToLoadKwh
    case 'pv_to_ess':
      return f.pvToEssKwh
    case 'pv_to_grid':
      return f.pvToGridKwh
    case 'grid_to_load':
      return f.gridToLoadKwh
    case 'grid_to_ess':
      return f.gridToEssKwh
    case 'ess_to_load':
      return f.essToLoadKwh
    case 'ess_to_grid':
      return f.essToGridKwh
    default:
      return 0
  }
}
