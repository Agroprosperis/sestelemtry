import { usePlotArea, useXAxisScale } from 'recharts'

// The crossed-out bolt (Lucide «zap-off», ISC) marks a planned outage on
// the day chart and in its legend.
function ZapOffPaths() {
  return (
    <>
      <path d="M10.513 4.856 13.12 2.17a.5.5 0 0 1 .86.46l-1.377 4.317" />
      <path d="M15.656 10H20a1 1 0 0 1 .78 1.63l-1.72 1.773" />
      <path d="M16.273 16.273 10.88 21.83a.5.5 0 0 1-.86-.46l1.92-6.02A1 1 0 0 0 11 14H4a1 1 0 0 1-.78-1.63l4.507-4.643" />
      <path d="m2 2 20 20" />
    </>
  )
}

const ICON_PX = 16

export function OutageIcon({ color, size = ICON_PX }: { color: string; size?: number }) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke={color}
      strokeWidth={2.2}
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden
    >
      <ZapOffPaths />
    </svg>
  )
}

export type OutageMark = { x1: string; x2: string; color: string }

// The fact track sits just under the plan's icons.
const ACTUAL_TOP_PX = 28
const ACTUAL_HEIGHT_PX = 4

// OutageMarkers draw inside the day chart, along the top of the plot:
// per planned outage a dashed line over its hours and the icon above
// their middle; per stretch the site actually ran without the grid a
// solid bar just under the icons. They take only the x scale and the
// plot box, so they show on a day with no reading to scale any y axis
// (tomorrow).
export function OutageMarkers({ marks, actual = [] }: { marks: OutageMark[]; actual?: OutageMark[] }) {
  const plot = usePlotArea()
  const x = useXAxisScale()
  if (!plot || !x || (marks.length === 0 && actual.length === 0)) return null
  const span = (m: OutageMark) => {
    const x1 = x(m.x1, { position: 'start' })
    const x2 = x(m.x2, { position: 'end' })
    return x1 == null || x2 == null || !(x2 > x1) ? null : { x: x1, width: x2 - x1 }
  }
  return (
    <g className="outage-markers" pointerEvents="none">
      {actual.map((m, i) => {
        const s = span(m)
        if (!s) return null
        return (
          <rect
            key={`actual-${i}`}
            className="outage-actual"
            x={s.x}
            y={plot.y + ACTUAL_TOP_PX}
            width={s.width}
            height={ACTUAL_HEIGHT_PX}
            rx={2}
            fill={m.color}
          />
        )
      })}
      {marks.map((m, i) => {
        const s = span(m)
        return s ? <OutageMarker key={i} x={s.x} width={s.width} top={plot.y} color={m.color} /> : null
      })}
    </g>
  )
}

// OutageMarker sits its icon on a disc of the card's background, so a
// line passing under it does not cut through.
function OutageMarker({ x, width, top, color }: { x: number; width: number; top: number; color: string }) {
  const cx = x + width / 2
  const cy = top + 14
  return (
    <g className="outage-marker">
      <line
        x1={x}
        x2={x + width}
        y1={top + 1}
        y2={top + 1}
        stroke={color}
        strokeWidth={2}
        strokeDasharray="4 3"
        strokeLinecap="round"
      />
      <circle cx={cx} cy={cy} r={11} stroke={color} strokeOpacity={0.4} style={{ fill: 'var(--bg-elevated)' }} />
      <svg
        x={cx - ICON_PX / 2}
        y={cy - ICON_PX / 2}
        width={ICON_PX}
        height={ICON_PX}
        viewBox="0 0 24 24"
        fill="none"
        stroke={color}
        strokeWidth={2.2}
        strokeLinecap="round"
        strokeLinejoin="round"
      >
        <ZapOffPaths />
      </svg>
    </g>
  )
}
