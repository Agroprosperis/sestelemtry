import { formatTimeLabel } from './format'
import { startOfPeriod, type RangePreset } from './range'

export type TimelineBucket = {
  t: number
  label: string
}

// Width of a single bucket on the day chart, in minutes. Drives both the
// server-side time_bucket() interval and the client-side timeline / row
// alignment, so this is the single source of truth.
export const DAY_BUCKET_MINUTES = 5

export function timelineBuckets(preset: RangePreset, anchor: Date): TimelineBucket[] {
  const start = startOfPeriod(preset, anchor)
  const out: TimelineBucket[] = []
  if (preset === 'day') {
    for (let m = 0; m < 24 * 60; m += DAY_BUCKET_MINUTES) {
      const d = new Date(start)
      d.setHours(0, m, 0, 0)
      out.push({ t: d.getTime(), label: formatTimeLabel(d, 'day') })
    }
    return out
  }
  if (preset === 'month') {
    const month = start.getMonth()
    const d = new Date(start)
    while (d.getMonth() === month) {
      out.push({ t: d.getTime(), label: formatTimeLabel(d, 'month') })
      d.setDate(d.getDate() + 1)
    }
    return out
  }
  const year = start.getFullYear()
  for (let m = 0; m < 12; m++) {
    const d = new Date(year, m, 1)
    out.push({ t: d.getTime(), label: formatTimeLabel(d, 'year') })
  }
  return out
}
