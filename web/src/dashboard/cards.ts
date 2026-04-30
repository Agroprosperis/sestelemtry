import type { CurrentResponse, DashboardMetric } from '../types'
import { DAY_ENERGY_METRIC_KEYS, PERIOD_ENERGY_METRIC_KEYS } from './metrics'

export type CardValueSources = {
  current: CurrentResponse | null
  dayEnergyValues: Record<string, number>
  periodEnergyValues: Record<string, number>
}

export function pickCardValue(card: DashboardMetric, sources: CardValueSources): number | null {
  if (DAY_ENERGY_METRIC_KEYS.has(card.key as never)) {
    return sources.dayEnergyValues[card.key] ?? 0
  }
  if (PERIOD_ENERGY_METRIC_KEYS.has(card.key as never)) {
    return sources.periodEnergyValues[card.key] ?? 0
  }
  const m = sources.current?.metrics?.[card.key]
  if (!m) return null
  return m.value
}
