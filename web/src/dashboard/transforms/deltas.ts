import type { TimeseriesPoint } from '../../types'
import { DAY_ENERGY_METRIC_KEYS, PERIOD_ENERGY_METRIC_KEYS } from '../metrics'

type Span = {
  firstTime: number
  firstValue: number
  lastTime: number
  lastValue: number
}

export function metricDeltas(points: TimeseriesPoint[], keys: Set<string>): Record<string, number> {
  const span = new Map<string, Span>()
  for (const p of points) {
    if (!keys.has(p.metric_key)) continue
    const t = new Date(p.time).getTime()
    if (!Number.isFinite(t) || !Number.isFinite(p.value)) continue
    const current = span.get(p.metric_key)
    if (!current) {
      span.set(p.metric_key, { firstTime: t, firstValue: p.value, lastTime: t, lastValue: p.value })
      continue
    }
    if (t < current.firstTime) {
      current.firstTime = t
      current.firstValue = p.value
    }
    if (t > current.lastTime) {
      current.lastTime = t
      current.lastValue = p.value
    }
  }
  const out: Record<string, number> = {}
  for (const [metricKey, v] of span.entries()) {
    out[metricKey] = v.lastValue - v.firstValue
  }
  return out
}

export function periodEnergyDeltas(points: TimeseriesPoint[]): Record<string, number> {
  return metricDeltas(points, PERIOD_ENERGY_METRIC_KEYS as Set<string>)
}

export function dayEnergyDeltas(points: TimeseriesPoint[]): Record<string, number> {
  return metricDeltas(points, DAY_ENERGY_METRIC_KEYS as Set<string>)
}
