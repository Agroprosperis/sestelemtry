import { useMemo, type ReactElement, type SVGProps } from 'react'
import {
  ResponsiveContainer,
  Sankey,
  Tooltip,
  type SankeyLinkProps,
  type SankeyNodeProps,
} from 'recharts'
import type { EnergyFlows } from '../transforms/flows'
import { formatEnergyUk } from './format'

type Props = {
  flows: EnergyFlows
  date: Date
  loading?: boolean
}

// Node colours mirror the macro the user shared. Sources on the
// left + sinks on the right; the consumption hub in the middle is
// a deliberate neutral so the eye reads the flow directions, not
// the hub itself.
const NODE_COLORS: Record<string, string> = {
  pv: '#22c55e',
  gridImport: '#3b82f6',
  essDischarge: '#a855f7',
  load: '#0f172a',
  gridExport: '#16a34a',
  essCharge: '#f59e0b',
}

const LINK_COLORS: Record<string, string> = {
  'pv->load': 'rgba(34, 197, 94, 0.32)',
  'pv->gridExport': 'rgba(22, 163, 74, 0.32)',
  'pv->essCharge': 'rgba(245, 158, 11, 0.32)',
  'gridImport->load': 'rgba(59, 130, 246, 0.32)',
  'gridImport->essCharge': 'rgba(245, 158, 11, 0.32)',
  'essDischarge->load': 'rgba(168, 85, 247, 0.32)',
  'essDischarge->gridExport': 'rgba(22, 163, 74, 0.32)',
}

type SankeyNodeData = {
  id: string
  name: string
  value: number
  color: string
  side: 'source' | 'hub' | 'sink'
}

type SankeyLinkData = {
  source: number
  target: number
  value: number
  key: string
  color: string
}

function buildSankeyData(flows: EnergyFlows): {
  nodes: SankeyNodeData[]
  links: SankeyLinkData[]
} {
  // Aggregate node throughputs straight from the same numbers the
  // Daily Summary cards use, so the Sankey reconciles 1:1 with the
  // bar charts beside it. Sankey requires positive link values; we
  // floor every flow at a very small epsilon so a zero-flow edge
  // (e.g. УЗЕ → мережа on a calm day) collapses out of the
  // diagram instead of rendering as a hairline path.
  const eps = 1e-6
  const pvProduced = flows.pvProducedKwh
  const gridImport = flows.gridImportKwh
  const essDischarge = flows.essToLoadKwh + flows.essToGridKwh
  const load = flows.loadConsumedKwh
  const gridExport = flows.pvToGridKwh + flows.essToGridKwh
  const essCharge = flows.pvToEssKwh + flows.gridToEssKwh

  const nodes: SankeyNodeData[] = [
    { id: 'pv', name: 'СЕС', value: pvProduced, color: NODE_COLORS.pv, side: 'source' },
    {
      id: 'gridImport',
      name: 'Імпорт з мережі',
      value: gridImport,
      color: NODE_COLORS.gridImport,
      side: 'source',
    },
    {
      id: 'essDischarge',
      name: 'Розряд УЗЕ',
      value: essDischarge,
      color: NODE_COLORS.essDischarge,
      side: 'source',
    },
    {
      id: 'load',
      name: 'Споживання елеватора',
      value: load,
      color: NODE_COLORS.load,
      side: 'hub',
    },
    {
      id: 'gridExport',
      name: 'Експорт в мережу',
      value: gridExport,
      color: NODE_COLORS.gridExport,
      side: 'sink',
    },
    {
      id: 'essCharge',
      name: 'Заряд УЗЕ',
      value: essCharge,
      color: NODE_COLORS.essCharge,
      side: 'sink',
    },
  ]

  const idx: Record<string, number> = {}
  nodes.forEach((n, i) => {
    idx[n.id] = i
  })

  const linkSpecs: Array<[from: string, to: string, value: number, key: string]> = [
    ['pv', 'load', flows.pvToLoadKwh, 'pv->load'],
    ['pv', 'gridExport', flows.pvToGridKwh, 'pv->gridExport'],
    ['pv', 'essCharge', flows.pvToEssKwh, 'pv->essCharge'],
    ['gridImport', 'load', flows.gridToLoadKwh, 'gridImport->load'],
    ['gridImport', 'essCharge', flows.gridToEssKwh, 'gridImport->essCharge'],
    ['essDischarge', 'load', flows.essToLoadKwh, 'essDischarge->load'],
    ['essDischarge', 'gridExport', flows.essToGridKwh, 'essDischarge->gridExport'],
  ]

  const links: SankeyLinkData[] = []
  for (const [from, to, value, key] of linkSpecs) {
    if (!Number.isFinite(value) || value <= eps) continue
    links.push({
      source: idx[from],
      target: idx[to],
      value,
      key,
      color: LINK_COLORS[key],
    })
  }

  return { nodes, links }
}

// renderNode draws a coloured rectangle plus the node label and
// total kWh next to it, mirroring the static mock the user
// approved. Sankey passes us the laid-out geometry (x/y/width/
// height) plus the original payload, so we just render an SVG
// group at that position.
function renderNode(props: SankeyNodeProps): ReactElement {
  const { x, y, width, height, payload } = props
  const data = payload as unknown as SankeyNodeData
  const isLeftSide = data.side === 'source'
  const labelX = isLeftSide ? x - 6 : x + width + 6
  const valueY = y + height / 2
  return (
    <g className="overview-sankey-node">
      <rect x={x} y={y} width={width} height={height} fill={data.color} rx={2} />
      <text
        x={labelX}
        y={valueY - 6}
        textAnchor={isLeftSide ? 'end' : 'start'}
        fontSize={12}
        fontWeight={600}
        fill="#0f172a"
      >
        {data.name}
      </text>
      <text
        x={labelX}
        y={valueY + 8}
        textAnchor={isLeftSide ? 'end' : 'start'}
        fontSize={11}
        fill="#475569"
      >
        {formatEnergyUk(data.value)}
      </text>
    </g>
  )
}

// renderLink colours each path with the link key so the Sankey
// reads as the colour-coded mock — green for PV, blue for grid,
// purple for ESS discharge, etc. recharts gives us the bezier
// control points; we just hand them to a single <path>.
function renderLink(props: SankeyLinkProps): ReactElement<SVGProps<SVGPathElement>> {
  const {
    sourceX,
    targetX,
    sourceY,
    targetY,
    sourceControlX,
    targetControlX,
    linkWidth,
    payload,
  } = props
  const data = payload as unknown as SankeyLinkData
  const d = `M${sourceX},${sourceY}` +
    `C${sourceControlX},${sourceY} ${targetControlX},${targetY} ${targetX},${targetY}`
  return (
    <path
      d={d}
      stroke={data.color}
      strokeWidth={linkWidth}
      strokeOpacity={1}
      fill="none"
      data-key={data.key}
    />
  )
}

function formatDayHeading(date: Date): string {
  return new Intl.DateTimeFormat('uk-UA', {
    day: 'numeric',
    month: 'long',
    year: 'numeric',
  }).format(date)
}

export function EnergyBalanceSankey({ flows, date, loading = false }: Props) {
  const { nodes, links } = useMemo(() => buildSankeyData(flows), [flows])
  const hasFlow = links.length > 0
  return (
    <section
      className="overview-card overview-card--sankey"
      aria-busy={loading || undefined}
    >
      <header className="overview-card-head">
        <h2 className="overview-card-title">Сьогоднішній енергобаланс</h2>
        <span className="overview-card-date">{formatDayHeading(date)}</span>
      </header>
      <div className="overview-sankey-stage">
        {hasFlow ? (
          <ResponsiveContainer width="100%" height={320}>
            <Sankey
              data={{ nodes, links }}
              node={renderNode}
              link={renderLink}
              nodePadding={28}
              nodeWidth={10}
              margin={{ top: 16, right: 130, bottom: 16, left: 110 }}
            >
              <Tooltip
                formatter={(value: unknown) => {
                  if (typeof value === 'number') return formatEnergyUk(value)
                  return '—'
                }}
              />
            </Sankey>
          </ResponsiveContainer>
        ) : (
          <p className="overview-empty">Дані про потоки за обраний день недоступні.</p>
        )}
      </div>
      <ul className="overview-sankey-legend">
        <li>
          <span className="overview-swatch" style={{ background: NODE_COLORS.pv }} />
          від СЕС
        </li>
        <li>
          <span className="overview-swatch" style={{ background: NODE_COLORS.gridImport }} />
          з мережі
        </li>
        <li>
          <span className="overview-swatch" style={{ background: NODE_COLORS.essDischarge }} />
          від УЗЕ
        </li>
        <li>
          <span className="overview-swatch" style={{ background: NODE_COLORS.gridExport }} />
          до мережі
        </li>
        <li>
          <span className="overview-swatch" style={{ background: NODE_COLORS.essCharge }} />
          у батарею
        </li>
        <li>
          <span className="overview-swatch" style={{ background: NODE_COLORS.load }} />
          споживання
        </li>
      </ul>
    </section>
  )
}
