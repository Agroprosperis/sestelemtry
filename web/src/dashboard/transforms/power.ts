import type { TimeseriesPoint } from '../../types'
import { DAY_BUCKET_MINUTES, timelineBuckets } from '../timeline'

export type PowerChartRow = { time: string } & Record<string, string | number | null>

// powerChartRows aligns instantaneous power samples (kW) to the day-preset
// 5-minute timeline. For each (bucket, metric) it keeps the sample with the
// latest `time` (semantics matches the server `aggregation=last`). Empty
// buckets stay `null` so the Recharts <Line> draws a gap instead of dropping
// to zero. Buckets with a start time strictly greater than the current
// 5-minute bucket on the anchor day are also returned with `null` values so
// the chart's lines do not extend into the future.
export function powerChartRows(
  points: TimeseriesPoint[],
  keys: string[],
  anchor: Date,
  now: Date = new Date(),
): PowerChartRow[] {
  const lastByKey = new Map<string, { value: number; time: number }>()
  for (const p of points) {
    if (!keys.includes(p.metric_key)) continue
    const t = new Date(p.time)
    const ts = t.getTime()
    if (!Number.isFinite(ts) || !Number.isFinite(p.value)) continue
    const slot = bucketKey(t)
    const composite = `${p.metric_key}@${slot}`
    const existing = lastByKey.get(composite)
    if (!existing || ts > existing.time) {
      lastByKey.set(composite, { value: p.value, time: ts })
    }
  }
  const cutoff = futureDayCutoff(anchor, now)
  const timeline = timelineBuckets('day', anchor)
  return timeline.map(({ t, label }) => {
    const row: PowerChartRow = { time: label }
    const slot = bucketKey(new Date(t))
    const isFuture = cutoff !== null && t > cutoff
    for (const key of keys) {
      if (isFuture) {
        row[key] = null
        continue
      }
      const hit = lastByKey.get(`${key}@${slot}`)
      row[key] = hit ? hit.value : null
    }
    return row
  })
}

function bucketKey(date: Date): string {
  const minute = Math.floor(date.getMinutes() / DAY_BUCKET_MINUTES) * DAY_BUCKET_MINUTES
  return `${date.getFullYear()}-${date.getMonth()}-${date.getDate()}-${date.getHours()}-${minute}`
}

// futureDayCutoff mirrors the energy-bucket transform: when the anchor is the
// local current day, return the start time of the bucket containing `now`.
// Buckets with a start strictly after this are considered "future" and get
// null values so the lines end at the latest known sample.
function futureDayCutoff(anchor: Date, now: Date): number | null {
  const anchorDay = new Date(anchor)
  anchorDay.setHours(0, 0, 0, 0)
  const today = new Date(now)
  today.setHours(0, 0, 0, 0)
  if (anchorDay.getTime() !== today.getTime()) return null
  const currentBucketStart = new Date(now)
  const minute = Math.floor(currentBucketStart.getMinutes() / DAY_BUCKET_MINUTES) * DAY_BUCKET_MINUTES
  currentBucketStart.setMinutes(minute, 0, 0)
  return currentBucketStart.getTime()
}
