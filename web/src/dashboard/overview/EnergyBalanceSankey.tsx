import {
  ArrowUpRight,
  BatteryFull,
  Buildings,
  Lightning,
  Sun,
  type Icon,
} from '@phosphor-icons/react'
import type { EnergyFlows } from '../transforms/flows'
import { formatEnergyUk } from './format'

type Props = {
  flows: EnergyFlows
  date: Date
  loading?: boolean
}

// Sankey layout is hand-rolled instead of using recharts because the
// macro is asymmetric: a hub card centred between three left-column
// sources (СЕС / Імпорт / Розряд УЗЕ) and two right-column sinks
// (Експорт / Заряд УЗЕ). recharts' Sankey assigns columns from the
// link topology, which lands all three depth-1 nodes (load,
// gridExport, essCharge) in one column — no way to coax `load` into
// its own middle column without faking intermediate nodes. A small
// CSS-grid + SVG ribbons combo gives us pixel-level control over
// the card positions without re-implementing card layouts in SVG
// primitives.

// CardSlot positions a card via percent of the stage. The SVG uses
// the same percent grid (viewBox="0 0 100 100", non-uniform scale)
// so ribbon endpoints can be computed in the same coordinate system.
type CardSlot = {
  left: number
  top: number
  width: number
  height: number
}

const CARDS: Record<string, CardSlot> = {
  pv: { left: 0, top: 2, width: 22, height: 22 },
  gridImport: { left: 0, top: 38, width: 22, height: 22 },
  essDischarge: { left: 0, top: 74, width: 22, height: 22 },
  load: { left: 38, top: 30, width: 24, height: 38 },
  gridExport: { left: 78, top: 2, width: 22, height: 22 },
  essCharge: { left: 78, top: 56, width: 22, height: 22 },
}

const NODE_ICONS: Record<string, Icon> = {
  pv: Sun,
  gridImport: Lightning,
  essDischarge: BatteryFull,
  load: Buildings,
  gridExport: ArrowUpRight,
  essCharge: BatteryFull,
}

const NODE_TINTS: Record<string, { bg: string; ring: string; icon: string }> = {
  pv: { bg: '#fef3c7', ring: '#facc15', icon: '#d97706' },
  gridImport: { bg: '#dbeafe', ring: '#60a5fa', icon: '#2563eb' },
  essDischarge: { bg: '#dcfce7', ring: '#86efac', icon: '#16a34a' },
  load: { bg: '#ede9fe', ring: '#c4b5fd', icon: '#7c3aed' },
  gridExport: { bg: '#dcfce7', ring: '#86efac', icon: '#16a34a' },
  essCharge: { bg: '#ffedd5', ring: '#fdba74', icon: '#ea580c' },
}

const NODE_TITLES: Record<string, string> = {
  pv: 'Виробіток СЕС',
  gridImport: 'Імпорт з мережі',
  essDischarge: 'Розряд УЗЕ',
  load: 'Споживання елеватора',
  gridExport: 'Експорт в мережу',
  essCharge: 'Заряд УЗЕ',
}

// Ribbons are coloured by destination type — matches the macro's
// legend (зелений = до мережі / від СЕС, фіолетовий = споживання,
// помаранчевий = у батарею, синій = з мережі). The destination
// drives the colour because the eye reads "where it ends up"
// faster than "where it came from" on a Sankey.
const RIBBON_COLOR_BY_TARGET: Record<string, string> = {
  load: 'rgba(167, 139, 250, 0.55)',
  gridExport: 'rgba(74, 222, 128, 0.55)',
  essCharge: 'rgba(251, 146, 60, 0.55)',
}

// SOURCE_OUTPUT_ORDER and TARGET_INPUT_ORDER fix the lane stacking
// order on each card edge. Top-of-card lanes go to top-of-stage
// targets so the ribbons read as "PV at the top streams to the
// export at the top" without crossing more than necessary.
const SOURCE_OUTPUT_ORDER: Record<string, string[]> = {
  pv: ['pv->gridExport', 'pv->load', 'pv->essCharge'],
  gridImport: ['gridImport->load', 'gridImport->essCharge'],
  essDischarge: ['essDischarge->gridExport', 'essDischarge->load'],
}

const TARGET_INPUT_ORDER: Record<string, string[]> = {
  load: ['pv->load', 'gridImport->load', 'essDischarge->load'],
  gridExport: ['pv->gridExport', 'essDischarge->gridExport'],
  essCharge: ['pv->essCharge', 'gridImport->essCharge'],
}

type FlowSpec = {
  key: string
  source: string
  target: string
  value: number
}

function buildFlows(flows: EnergyFlows): FlowSpec[] {
  const all: FlowSpec[] = [
    { key: 'pv->load', source: 'pv', target: 'load', value: flows.pvToLoadKwh },
    {
      key: 'pv->gridExport',
      source: 'pv',
      target: 'gridExport',
      value: flows.pvToGridKwh,
    },
    {
      key: 'pv->essCharge',
      source: 'pv',
      target: 'essCharge',
      value: flows.pvToEssKwh,
    },
    {
      key: 'gridImport->load',
      source: 'gridImport',
      target: 'load',
      value: flows.gridToLoadKwh,
    },
    {
      key: 'gridImport->essCharge',
      source: 'gridImport',
      target: 'essCharge',
      value: flows.gridToEssKwh,
    },
    {
      key: 'essDischarge->load',
      source: 'essDischarge',
      target: 'load',
      value: flows.essToLoadKwh,
    },
    {
      key: 'essDischarge->gridExport',
      source: 'essDischarge',
      target: 'gridExport',
      value: flows.essToGridKwh,
    },
  ]
  return all.filter(
    (f) => Number.isFinite(f.value) && f.value > 0,
  )
}

type LaneEdge = { y0: number; y1: number }

// computeLanes splits a card edge into N stacked lanes, each
// proportional in height to the corresponding flow value. Returns
// a map keyed by ribbon id so both endpoints of a ribbon can pick
// up its lane independently.
function computeLanes(
  card: CardSlot,
  ribbons: FlowSpec[],
  order: string[],
): Map<string, LaneEdge> {
  const lanes = new Map<string, LaneEdge>()
  if (ribbons.length === 0) return lanes
  const orderedByValue = new Map(ribbons.map((r) => [r.key, r.value]))
  const total = ribbons.reduce((s, r) => s + r.value, 0)
  if (total <= 0) return lanes
  // Reserve a tiny vertical inset so the topmost / bottommost
  // ribbons don't touch the rounded card corners.
  const inset = card.height * 0.05
  const usable = card.height - inset * 2
  let acc = 0
  for (const key of order) {
    const value = orderedByValue.get(key)
    if (!value) continue
    const frac = value / total
    const y0 = card.top + inset + acc * usable
    const y1 = y0 + frac * usable
    lanes.set(key, { y0, y1 })
    acc += frac
  }
  return lanes
}

function ribbonPath(srcX: number, src: LaneEdge, tgtX: number, tgt: LaneEdge): string {
  const midX = (srcX + tgtX) / 2
  return (
    `M ${srcX} ${src.y0}` +
    ` C ${midX} ${src.y0} ${midX} ${tgt.y0} ${tgtX} ${tgt.y0}` +
    ` L ${tgtX} ${tgt.y1}` +
    ` C ${midX} ${tgt.y1} ${midX} ${src.y1} ${srcX} ${src.y1}` +
    ` Z`
  )
}

function formatDayHeading(date: Date): string {
  return new Intl.DateTimeFormat('uk-UA', {
    day: 'numeric',
    month: 'long',
    year: 'numeric',
  }).format(date)
}

function nodeValue(id: string, flows: EnergyFlows): number {
  switch (id) {
    case 'pv':
      return flows.pvProducedKwh
    case 'gridImport':
      return flows.gridImportKwh
    case 'essDischarge':
      return flows.essToLoadKwh + flows.essToGridKwh
    case 'load':
      return flows.loadConsumedKwh
    case 'gridExport':
      return flows.pvToGridKwh + flows.essToGridKwh
    case 'essCharge':
      return flows.pvToEssKwh + flows.gridToEssKwh
    default:
      return 0
  }
}

function NodeCard({ id, value, isHub }: { id: string; value: number; isHub?: boolean }) {
  const slot = CARDS[id]
  const tint = NODE_TINTS[id]
  const Icon = NODE_ICONS[id]
  return (
    <div
      className={`overview-balance-card${isHub ? ' overview-balance-card--hub' : ''}`}
      style={{
        left: `${slot.left}%`,
        top: `${slot.top}%`,
        width: `${slot.width}%`,
        height: `${slot.height}%`,
      }}
    >
      <span
        className="overview-balance-card-icon"
        style={{ background: tint.bg, color: tint.icon, borderColor: tint.ring }}
        aria-hidden="true"
      >
        <Icon size={18} weight="duotone" />
      </span>
      <div className="overview-balance-card-text">
        <span className="overview-balance-card-label">{NODE_TITLES[id]}</span>
        <strong className="overview-balance-card-value">{formatEnergyUk(value)}</strong>
      </div>
    </div>
  )
}

export function EnergyBalanceSankey({ flows, date, loading = false }: Props) {
  const ribbons = buildFlows(flows)

  // Per-card output / input lanes share the same ribbon keys, so
  // a single ribbon can read its src lane from the source card and
  // its tgt lane from the target card independently.
  const sourceLanes = new Map<string, Map<string, LaneEdge>>()
  for (const id of Object.keys(SOURCE_OUTPUT_ORDER)) {
    const own = ribbons.filter((r) => r.source === id)
    sourceLanes.set(id, computeLanes(CARDS[id], own, SOURCE_OUTPUT_ORDER[id]))
  }
  const targetLanes = new Map<string, Map<string, LaneEdge>>()
  for (const id of Object.keys(TARGET_INPUT_ORDER)) {
    const own = ribbons.filter((r) => r.target === id)
    targetLanes.set(id, computeLanes(CARDS[id], own, TARGET_INPUT_ORDER[id]))
  }

  const visibleNodeIds = new Set<string>()
  for (const r of ribbons) {
    visibleNodeIds.add(r.source)
    visibleNodeIds.add(r.target)
  }
  // Always render the hub card so the diagram has a visual centre
  // even on days with no consumption (e.g. fresh deployment).
  visibleNodeIds.add('load')

  return (
    <section
      className="overview-card overview-card--sankey"
      aria-busy={loading || undefined}
    >
      <header className="overview-card-head">
        <h2 className="overview-card-title">Сьогоднішній енергобаланс</h2>
        <span className="overview-card-date">{formatDayHeading(date)}</span>
      </header>
      <div className="overview-balance-stage">
        <svg
          className="overview-balance-svg"
          viewBox="0 0 100 100"
          preserveAspectRatio="none"
          aria-hidden="true"
        >
          {ribbons.map((r) => {
            const src = sourceLanes.get(r.source)?.get(r.key)
            const tgt = targetLanes.get(r.target)?.get(r.key)
            if (!src || !tgt) return null
            const srcCard = CARDS[r.source]
            const tgtCard = CARDS[r.target]
            const srcX = srcCard.left + srcCard.width
            const tgtX = tgtCard.left
            return (
              <path
                key={r.key}
                d={ribbonPath(srcX, src, tgtX, tgt)}
                fill={RIBBON_COLOR_BY_TARGET[r.target] ?? 'rgba(148, 163, 184, 0.4)'}
                data-key={r.key}
              />
            )
          })}
        </svg>
        {Array.from(visibleNodeIds).map((id) => (
          <NodeCard
            key={id}
            id={id}
            value={nodeValue(id, flows)}
            isHub={id === 'load'}
          />
        ))}
        {ribbons.length === 0 && (
          <p className="overview-balance-empty">
            Дані про потоки за обраний день недоступні.
          </p>
        )}
      </div>
      <ul className="overview-sankey-legend">
        <li>
          <span
            className="overview-swatch"
            style={{ background: NODE_TINTS.pv.icon }}
          />
          від СЕС
        </li>
        <li>
          <span
            className="overview-swatch"
            style={{ background: NODE_TINTS.gridImport.icon }}
          />
          з мережі
        </li>
        <li>
          <span
            className="overview-swatch"
            style={{ background: NODE_TINTS.essDischarge.icon }}
          />
          від УЗЕ
        </li>
        <li>
          <span
            className="overview-swatch"
            style={{ background: NODE_TINTS.gridExport.icon }}
          />
          до мережі
        </li>
        <li>
          <span
            className="overview-swatch"
            style={{ background: NODE_TINTS.essCharge.icon }}
          />
          у батарею
        </li>
        <li>
          <span
            className="overview-swatch"
            style={{ background: NODE_TINTS.load.icon }}
          />
          споживання
        </li>
      </ul>
      <p className="overview-balance-foot">Всі значення за день</p>
    </section>
  )
}
