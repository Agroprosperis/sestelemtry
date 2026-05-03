import type { CurrentResponse, DashboardMetric } from '../../types'
import { CARD_GROUP_LABELS, CARD_GROUP_ORDER, groupCards, pickCardValue } from '../cards'
import { MetricCard } from './MetricCard'
import { MetricsAtPicker } from './MetricsAtPicker'

type Props = {
  cards: DashboardMetric[]
  current: CurrentResponse | null
  loading: boolean
  metricsAt: Date | null
  onMetricsAtChange: (next: Date | null) => void
}

function formatSnapshotLabel(at: Date): string {
  return at.toLocaleString(undefined, {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  })
}

export function MetricsPanel({ cards, current, loading, metricsAt, onMetricsAtChange }: Props) {
  const groups = groupCards(cards)
  return (
    <div className="metrics-panel-stack">
      <header className="metrics-at-bar">
        <MetricsAtPicker value={metricsAt} onChange={onMetricsAtChange} />
      </header>
      {metricsAt && (
        <p className="metrics-at-hint">
          Показники станом на <strong>{formatSnapshotLabel(metricsAt)}</strong>
        </p>
      )}
      {CARD_GROUP_ORDER.map((groupId) => {
        const groupCardsForId = groups[groupId]
        if (groupCardsForId.length === 0) return null
        return (
          <section key={groupId} className="metrics-group" aria-labelledby={`metrics-group-${groupId}`}>
            <h2 id={`metrics-group-${groupId}`} className="metrics-group-title">
              {CARD_GROUP_LABELS[groupId]}
            </h2>
            <div className="cards-grid">
              {groupCardsForId.map((card) => (
                <MetricCard
                  key={card.key}
                  card={card}
                  value={pickCardValue(card, { current })}
                  loading={loading}
                />
              ))}
            </div>
          </section>
        )
      })}
    </div>
  )
}
