import type { TimeseriesPoint } from '../../types'
import { formatTimeLabel } from '../format'
import { APPLIANCE_CONSUMPTION_METRIC, ENERGY_TREND_METRIC_DIRECTIONS, type MetricKey } from '../metrics'
import type { RangePreset } from '../range'

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

export function energyBucketDeltaRows(
  points: TimeseriesPoint[],
  metricKeys: string[],
  preset: RangePreset,
): EnergyRow[] {
  const keyed = new Map<string, { t: number; values: Record<string, number> }>()
  for (const p of points) {
    if (!metricKeys.includes(p.metric_key)) continue
    const t = new Date(p.time).getTime()
    if (!Number.isFinite(t) || !Number.isFinite(p.value)) continue
    const k = new Date(p.time).toISOString()
    const row = keyed.get(k) || { t, values: {} }
    row.values[p.metric_key] = p.value
    keyed.set(k, row)
  }

  const sorted = Array.from(keyed.values()).sort((a, b) => a.t - b.t)
  const prev = new Map<string, number>()

  return sorted.map((row) => {
    const dt = new Date(row.t)
    const timeLabel = formatTimeLabel(dt, preset)
    const out: EnergyRow = { time: timeLabel }
    const rawDeltas: Record<string, number> = {}
    for (const key of metricKeys) {
      const current = row.values[key]
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
