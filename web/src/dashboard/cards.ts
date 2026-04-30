import type { CurrentResponse, DashboardMetric } from '../types'

export type CardValueSources = {
  current: CurrentResponse | null
}

export function pickCardValue(card: DashboardMetric, sources: CardValueSources): number | null {
  const m = sources.current?.metrics?.[card.key]
  if (!m) return null
  return m.value
}
