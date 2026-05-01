import type { TimeseriesPoint } from '../../types'
import { APPLIANCE_CONSUMPTION_METRIC, ENERGY_TREND_METRIC_DIRECTIONS, type MetricKey } from '../metrics'
import type { RangePreset } from '../range'
import { DAY_BUCKET_MINUTES, timelineBuckets } from '../timeline'

export type EnergyRow = { time: string } & Partial<Record<MetricKey, number>> & Record<string, string | number>

export function applyApplianceConsumptionRule(rawDeltas: Record<string, number>): void {
  if (!(APPLIANCE_CONSUMPTION_METRIC in rawDeltas)) return
  const value =
    (rawDeltas.accumulated_electricity_purchased_kwh ?? 0) +
    (rawDeltas.pv_energy_yield_day_kwh ?? 0) +
    (rawDeltas.total_energy_discharged_kwh ?? 0) -
    (rawDeltas.total_energy_charged_kwh ?? 0)
  rawDeltas[APPLIANCE_CONSUMPTION_METRIC] = value < 0 ? 0 : value
}

// bucketKey returns a calendar-component key for the local timezone so that
// API rows bucketed in UTC by Postgres still land in the right slot of the
// locally-rendered timeline. For the day preset the key is resolved at
// DAY_BUCKET_MINUTES granularity so samples align with the 5-minute chart
// buckets.
function bucketKey(date: Date, preset: RangePreset): string {
  if (preset === 'year') {
    return `${date.getFullYear()}-${date.getMonth()}`
  }
  if (preset === 'month') {
    return `${date.getFullYear()}-${date.getMonth()}-${date.getDate()}`
  }
  const minute = Math.floor(date.getMinutes() / DAY_BUCKET_MINUTES) * DAY_BUCKET_MINUTES
  return `${date.getFullYear()}-${date.getMonth()}-${date.getDate()}-${date.getHours()}-${minute}`
}

function applyDirections(metricKeys: string[], values: Record<string, number>): EnergyRow {
  const out: EnergyRow = { time: '' }
  for (const key of metricKeys) {
    const direction = ENERGY_TREND_METRIC_DIRECTIONS[key as keyof typeof ENERGY_TREND_METRIC_DIRECTIONS] ?? 1
    out[key] = (values[key] ?? 0) * direction
  }
  return out
}

// indexBuckets groups bucket-contribution samples (server returns MAX - MIN per
// bucket) into a map keyed by the local-timezone calendar slot for the chart's
// preset. For year preset multiple daily samples sum into a single monthly key.
function indexBuckets(
  points: TimeseriesPoint[],
  metricKeys: string[],
  preset: RangePreset,
): Map<string, Record<string, number>> {
  const byKey = new Map<string, Record<string, number>>()
  for (const p of points) {
    if (!metricKeys.includes(p.metric_key)) continue
    const t = new Date(p.time)
    if (!Number.isFinite(t.getTime()) || !Number.isFinite(p.value)) continue
    const key = bucketKey(t, preset)
    const row = byKey.get(key) || {}
    row[p.metric_key] = (row[p.metric_key] ?? 0) + p.value
    byKey.set(key, row)
  }
  return byKey
}

// futureDayCutoff returns the local-time epoch (start of the 5-minute bucket
// containing `now`) when the day chart should stop drawing metric values, or
// null when the preset is not day or the anchor is not the current local day.
// Buckets with a start time strictly greater than the cutoff are treated as
// "future" slots that have not happened yet; their metric values are omitted
// from the row so the line ends at the latest known value rather than
// dropping to zero.
function futureDayCutoff(preset: RangePreset, anchor: Date, now: Date): number | null {
  if (preset !== 'day') return null
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

export function energyBucketDeltaRows(
  points: TimeseriesPoint[],
  metricKeys: string[],
  preset: RangePreset,
  anchor: Date,
  now: Date = new Date(),
): EnergyRow[] {
  const byKey = indexBuckets(points, metricKeys, preset)
  const timeline = timelineBuckets(preset, anchor)
  const cutoff = futureDayCutoff(preset, anchor, now)
  return timeline.map(({ t, label }) => {
    if (cutoff !== null && t > cutoff) {
      return { time: label }
    }
    const date = new Date(t)
    const values = byKey.get(bucketKey(date, preset)) || {}
    const cell: Record<string, number> = {}
    for (const key of metricKeys) {
      const v = values[key]
      cell[key] = Number.isFinite(v) ? Math.max(v as number, 0) : 0
    }
    applyApplianceConsumptionRule(cell)
    const row = applyDirections(metricKeys, cell)
    row.time = label
    return row
  })
}
