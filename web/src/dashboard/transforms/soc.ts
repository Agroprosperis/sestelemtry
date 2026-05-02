import type { TimeseriesPoint } from '../../types'
import type { RangePreset } from '../range'
import { DAY_BUCKET_MINUTES, timelineBuckets } from '../timeline'

export type SOCChartRow = { time: string; soc: number | null }

// socChartRows aligns SOC samples to the chart timeline. Day preset expects
// 5-minute buckets; we bucket the server-returned points locally and keep
// `null` where no sample landed in the bucket so the Area chart does not
// draw a phantom 0%.
export function socChartRows(
  points: TimeseriesPoint[],
  preset: RangePreset,
  anchor: Date,
): SOCChartRow[] {
  if (preset !== 'day') return []
  const byKey = new Map<string, number[]>()
  for (const p of points) {
    if (p.metric_key !== 'soc_percent') continue
    const t = new Date(p.time)
    if (!Number.isFinite(t.getTime()) || !Number.isFinite(p.value)) continue
    const key = bucketKey(t)
    const arr = byKey.get(key) ?? []
    arr.push(p.value)
    byKey.set(key, arr)
  }
  const timeline = timelineBuckets(preset, anchor)
  return timeline.map(({ t, label }) => {
    const arr = byKey.get(bucketKey(new Date(t)))
    if (!arr || arr.length === 0) return { time: label, soc: null }
    const avg = arr.reduce((s, v) => s + v, 0) / arr.length
    return { time: label, soc: avg }
  })
}

function bucketKey(date: Date): string {
  const minute = Math.floor(date.getMinutes() / DAY_BUCKET_MINUTES) * DAY_BUCKET_MINUTES
  return `${date.getFullYear()}-${date.getMonth()}-${date.getDate()}-${date.getHours()}-${minute}`
}
