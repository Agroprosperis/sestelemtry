import { usePlotArea, useXAxisScale } from 'recharts'

export type OutageMark = { x1: string; x2: string; color: string }

// Two bars on the plot's top edge, the fact right under the plan.
const BAR_HEIGHT_PX = 4
const PLAN_TOP_PX = 0
const ACTUAL_TOP_PX = PLAN_TOP_PX + BAR_HEIGHT_PX + 1

// OutageMarkers draw inside the day chart, along the top of the plot: a
// bar over the hours of each planned outage and, right under it, a bar
// over each stretch the site actually ran without the grid. They take
// only the x scale and the plot box, so they show on a day with no
// reading to scale any y axis (tomorrow).
export function OutageMarkers({ marks, actual = [] }: { marks: OutageMark[]; actual?: OutageMark[] }) {
  const plot = usePlotArea()
  const x = useXAxisScale()
  if (!plot || !x || (marks.length === 0 && actual.length === 0)) return null
  const bar = (m: OutageMark, key: string, className: string, top: number) => {
    const x1 = x(m.x1, { position: 'start' })
    const x2 = x(m.x2, { position: 'end' })
    if (x1 == null || x2 == null || !(x2 > x1)) return null
    return (
      <rect
        key={key}
        className={className}
        x={x1}
        y={plot.y + top}
        width={x2 - x1}
        height={BAR_HEIGHT_PX}
        rx={2}
        fill={m.color}
      />
    )
  }
  return (
    <g className="outage-markers" pointerEvents="none">
      {marks.map((m, i) => bar(m, `plan-${i}`, 'outage-marker', PLAN_TOP_PX))}
      {actual.map((m, i) => bar(m, `actual-${i}`, 'outage-actual', ACTUAL_TOP_PX))}
    </g>
  )
}
