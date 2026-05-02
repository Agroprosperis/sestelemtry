import type { TimeseriesPoint } from '../../types'
import { ENERGY_TREND_METRIC_DIRECTIONS } from '../metrics'
import type { RangePreset } from '../range'
import { timelineBuckets } from '../timeline'
import { applyApplianceConsumptionRule, type EnergyRow } from './buckets'

// CumulativeInput captures the two pieces needed to compute per-bucket
// deltas without summing 30+ small numbers:
//   * `bucketPoints` — server response with aggregation=last, one cumulative
//     value per (bucket, metric).
//   * `seed` — cumulative reading at-or-before the period start (returned by
//     /api/v1/current?at=startOfPeriod). Missing keys are treated as "no
//     historical data"; the first observed bucket value is used as the
//     implicit seed so the first delta starts at 0 instead of inflating to
//     the lifetime counter value.
export type CumulativeInput = {
  bucketPoints: TimeseriesPoint[]
  seed: Record<string, number>
}

// Calendar slot key in the user's local TZ. Mirrors the day/month grouping
// used by the existing energy bucket transform so the cumulative path lines
// up exactly with the chart's timeline buckets even if the browser TZ math
// drifts a millisecond from the server's time_bucket().
function bucketKey(date: Date, preset: RangePreset): string {
  if (preset === 'year') {
    return `${date.getFullYear()}-${date.getMonth()}`
  }
  return `${date.getFullYear()}-${date.getMonth()}-${date.getDate()}`
}

function indexBuckets(
  points: TimeseriesPoint[],
  metricKeys: string[],
  preset: RangePreset,
): Map<string, Record<string, number>> {
  const out = new Map<string, Record<string, number>>()
  for (const p of points) {
    if (!metricKeys.includes(p.metric_key)) continue
    const t = new Date(p.time)
    if (!Number.isFinite(t.getTime()) || !Number.isFinite(p.value)) continue
    const k = bucketKey(t, preset)
    const slot = out.get(k) ?? {}
    slot[p.metric_key] = p.value
    out.set(k, slot)
  }
  return out
}

// effectiveSeed prefers the explicit start-of-period reading; if absent,
// falls back to the first non-empty bucket's value. With this, fresh
// installations show 0 for the first bucket (instead of the device's
// lifetime counter) and continue with correct deltas afterwards.
function effectiveSeed(
  metricKey: string,
  seed: Record<string, number>,
  indexed: Map<string, Record<string, number>>,
  timelineKeys: string[],
): number {
  const s = seed[metricKey]
  if (Number.isFinite(s)) return s as number
  for (const k of timelineKeys) {
    const v = indexed.get(k)?.[metricKey]
    if (v != null && Number.isFinite(v)) return v
  }
  return 0
}

function applyDirections(metricKeys: string[], values: Record<string, number>): EnergyRow {
  const out: EnergyRow = { time: '' }
  for (const key of metricKeys) {
    const direction =
      ENERGY_TREND_METRIC_DIRECTIONS[key as keyof typeof ENERGY_TREND_METRIC_DIRECTIONS] ?? 1
    out[key] = (values[key] ?? 0) * direction
  }
  return out
}

// cumulativeBucketDeltaRows turns per-bucket cumulative readings + a seed
// into per-bucket positive deltas, applies the appliance-consumption
// recompute rule, and finally puts sink metrics on the negative side via
// ENERGY_TREND_METRIC_DIRECTIONS. The output shape (EnergyRow) is identical
// to energyBucketDeltaRows so the chart consumes both interchangeably.
export function cumulativeBucketDeltaRows(
  input: CumulativeInput,
  metricKeys: string[],
  preset: RangePreset,
  anchor: Date,
): EnergyRow[] {
  const { bucketPoints, seed } = input
  const indexed = indexBuckets(bucketPoints, metricKeys, preset)
  const timeline = timelineBuckets(preset, anchor)
  const tlKeys = timeline.map(({ t }) => bucketKey(new Date(t), preset))

  const prev: Record<string, number> = {}
  for (const key of metricKeys) {
    prev[key] = effectiveSeed(key, seed, indexed, tlKeys)
  }

  return timeline.map(({ label }, i) => {
    const slot = indexed.get(tlKeys[i])
    const cell: Record<string, number> = {}
    for (const key of metricKeys) {
      const cur = slot?.[key]
      if (cur != null && Number.isFinite(cur)) {
        const delta = cur - prev[key]
        cell[key] = delta < 0 ? 0 : delta
        prev[key] = cur
      } else {
        cell[key] = 0
        // prev unchanged: counter holds while the bucket is empty.
      }
    }
    applyApplianceConsumptionRule(cell)
    const row = applyDirections(metricKeys, cell)
    row.time = label
    return row
  })
}

// cumulativeTotals returns, per metric, `last(observed) - effective_seed`
// — exactly the period total without summing 30+ per-bucket deltas. Negative
// results (counter rollback / device restart) are clamped to zero.
export function cumulativeTotals(
  input: CumulativeInput,
  metricKeys: string[],
  preset: RangePreset,
  anchor: Date,
): Record<string, number> {
  const { bucketPoints, seed } = input
  const indexed = indexBuckets(bucketPoints, metricKeys, preset)
  const timeline = timelineBuckets(preset, anchor)
  const tlKeys = timeline.map(({ t }) => bucketKey(new Date(t), preset))

  const totals: Record<string, number> = {}
  for (const key of metricKeys) {
    const start = effectiveSeed(key, seed, indexed, tlKeys)
    let end = start
    for (let i = tlKeys.length - 1; i >= 0; i--) {
      const v = indexed.get(tlKeys[i])?.[key]
      if (v != null && Number.isFinite(v)) {
        end = v
        break
      }
    }
    const total = end - start
    totals[key] = total < 0 ? 0 : total
  }
  applyApplianceConsumptionRule(totals)
  return totals
}
