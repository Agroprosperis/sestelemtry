import type { TimeseriesPoint } from '../../types'
import { APPLIANCE_CONSUMPTION_METRIC, ENERGY_TREND_METRIC_DIRECTIONS, type MetricKey } from '../metrics'
import type { RangePreset } from '../range'
import { timelineBuckets } from '../timeline'

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
// locally-rendered timeline (e.g. "1 day" buckets returned at UTC midnight
// are still attributed to the correct local day).
function bucketKey(date: Date, preset: RangePreset): string {
  if (preset === 'year') {
    return `${date.getFullYear()}-${date.getMonth()}`
  }
  if (preset === 'month') {
    return `${date.getFullYear()}-${date.getMonth()}-${date.getDate()}`
  }
  return `${date.getFullYear()}-${date.getMonth()}-${date.getDate()}-${date.getHours()}`
}

export function energyBucketDeltaRows(
  points: TimeseriesPoint[],
  metricKeys: string[],
  preset: RangePreset,
  anchor: Date,
): EnergyRow[] {
  const byKey = new Map<string, Record<string, number>>()
  for (const p of points) {
    if (!metricKeys.includes(p.metric_key)) continue
    const t = new Date(p.time)
    if (!Number.isFinite(t.getTime()) || !Number.isFinite(p.value)) continue
    const key = bucketKey(t, preset)
    const row = byKey.get(key) || {}
    row[p.metric_key] = p.value
    byKey.set(key, row)
  }

  const timeline = timelineBuckets(preset, anchor)
  const prev = new Map<string, number>()

  return timeline.map(({ t, label }) => {
    const values = byKey.get(bucketKey(new Date(t), preset)) || {}
    const out: EnergyRow = { time: label }
    const rawDeltas: Record<string, number> = {}
    for (const key of metricKeys) {
      const current = values[key]
      if (!Number.isFinite(current)) {
        rawDeltas[key] = 0
        continue
      }
      const previous = prev.get(key)
      let delta = 0
      if (Number.isFinite(previous)) {
        delta = current - (previous as number)
      }
      if (delta < 0) delta = 0
      prev.set(key, current)
      rawDeltas[key] = delta
    }

    applyApplianceConsumptionRule(rawDeltas)

    for (const key of metricKeys) {
      const direction = ENERGY_TREND_METRIC_DIRECTIONS[key as keyof typeof ENERGY_TREND_METRIC_DIRECTIONS] ?? 1
      out[key] = (rawDeltas[key] ?? 0) * direction
    }
    return out
  })
}
