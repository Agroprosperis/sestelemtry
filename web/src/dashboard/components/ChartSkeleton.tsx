import type { RangePreset } from '../range'

type Props = {
  preset: RangePreset
  // shape overrides the bar/line decision; useful when a chart is always
  // line-shaped (e.g. RevenueChart) regardless of the active preset.
  shape?: 'bars' | 'line'
}

// barCountFor approximates the number of x-axis slots the real chart will
// render so the skeleton occupies roughly the same horizontal density and
// the layout doesn't visibly shift when data arrives.
function barCountFor(preset: RangePreset): number {
  if (preset === 'year') return 12
  if (preset === 'month') return 30
  return 24
}

export function ChartSkeleton({ preset, shape }: Props) {
  const resolved = shape ?? (preset === 'day' ? 'line' : 'bars')
  if (resolved === 'line') {
    return (
      <div className="chart-skeleton" aria-hidden="true" data-testid="chart-skeleton">
        <div className="chart-skeleton-line" />
      </div>
    )
  }
  const count = barCountFor(preset)
  const bars = Array.from({ length: count }, (_, i) => {
    // Heights drift a bit so the skeleton doesn't look like a flat block;
    // the values are deterministic to avoid hydration mismatches.
    const h = 40 + ((i * 17) % 55)
    return <span key={i} className="chart-skeleton-bar" style={{ height: `${h}%` }} />
  })
  return (
    <div className="chart-skeleton" aria-hidden="true" data-testid="chart-skeleton">
      <div className="chart-skeleton-bars">{bars}</div>
    </div>
  )
}
