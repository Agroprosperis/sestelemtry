import type { CurrentResponse, DashboardMetric } from '../types'

export type CardValueSources = {
  current: CurrentResponse | null
}

export function pickCardValue(card: DashboardMetric, sources: CardValueSources): number | null {
  const m = sources.current?.metrics?.[card.key]
  if (!m) return null
  return m.value
}

export type CardGroupId = 'current' | 'today' | 'accumulated'

export const CARD_GROUP_LABELS: Record<CardGroupId, string> = {
  current: 'Поточне енергоспоживання',
  today: 'Підсумки за сьогодні',
  accumulated: 'Накопичувальні показники',
}

// CARD_GROUP_ORDER controls which group is rendered first on the page.
// `current` first because it changes most often (live snapshot), then
// today's totals (daily-resetting), then lifetime counters that move
// slowest.
export const CARD_GROUP_ORDER: CardGroupId[] = ['current', 'today', 'accumulated']

// classifyCard buckets a metric into one of the three card groups using
// the metric_key naming convention:
//   - instantaneous power / state-of-charge units (`_kw`, `_percent`) are
//     "current" snapshot values;
//   - keys whose stem contains a `_day_` segment carry today's totals
//     (the device exposes daily-resetting counters via this convention);
//   - everything else is treated as a lifetime accumulator.
// Unknown metrics fall into `accumulated`, which is the most conservative
// default for a numeric counter coming from the backend.
export function classifyCard(card: DashboardMetric): CardGroupId {
  const key = card.key
  if (key.endsWith('_kw') || key.endsWith('_percent')) return 'current'
  if (key.includes('_day_')) return 'today'
  return 'accumulated'
}

export function groupCards(
  cards: DashboardMetric[],
): Record<CardGroupId, DashboardMetric[]> {
  const groups: Record<CardGroupId, DashboardMetric[]> = {
    current: [],
    today: [],
    accumulated: [],
  }
  for (const card of cards) {
    groups[classifyCard(card)].push(card)
  }
  return groups
}
