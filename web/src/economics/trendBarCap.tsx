import { Rectangle } from 'recharts'

// The energy trend is a diverging stacked bar: sources stack up (`pos`),
// sinks stack down (`neg`). We want only the *outermost* segment of each
// half rounded (the end away from the zero axis), so the whole column reads
// as one capped bar. recharts' static `radius` prop can't do this because
// the designated series (grid import at the top, grid export at the bottom)
// is frequently zero — on an import-only day the real bottom segment is
// `load`/`essCharge`, which then renders square. This shape picks, per row,
// whichever series is actually the last non-zero one in the stack draw order
// and rounds that segment's outer corners; every other segment stays square.
// It delegates to recharts' own <Rectangle> so the geometry (including
// negative bars) is identical to the default bar rendering.

const CAP_RADIUS = 3

type Corners = [number, number, number, number]

// Draw order per stack: index 0 sits nearest the zero axis, the last entry is
// the outermost segment. Keep these in sync with the <Bar> order in the chart.
export const TREND_POS_ORDER = ['pv', 'essDischarge', 'gridImport'] as const
export const TREND_NEG_ORDER = ['load', 'essCharge', 'gridExport'] as const

function outermostNonZero(payload: Record<string, number> | undefined, order: readonly string[]): string | null {
  if (!payload) return null
  let outer: string | null = null
  for (const key of order) {
    if (Math.abs(payload[key] ?? 0) > 1e-9) outer = key
  }
  return outer
}

// makeTrendCap returns a recharts bar `shape` for one series. `side` is which
// end of the diverging bar this stack lives on, which decides which pair of
// corners gets rounded when this series is the outermost non-zero one.
export function makeTrendCap(side: 'top' | 'bottom', ownKey: string, order: readonly string[]) {
  const roundedCorners: Corners = side === 'top' ? [CAP_RADIUS, CAP_RADIUS, 0, 0] : [0, 0, CAP_RADIUS, CAP_RADIUS]
  // recharts types the shape callback with its internal BarShapeProps, which
  // carries the row under `payload`. We only read `payload` and forward the
  // rest to <Rectangle>, so accept the loose prop bag recharts hands us.
  return function TrendCap(props: { payload?: Record<string, number> }) {
    const outer = outermostNonZero(props.payload, order)
    const radius: Corners = ownKey === outer ? roundedCorners : [0, 0, 0, 0]
    return <Rectangle {...props} radius={radius} />
  }
}
