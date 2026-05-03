import type { CurrentResponse, DashboardMetric } from '../../types'
import { CARD_GROUP_LABELS, groupCards, pickCardValue } from '../cards'
import type { RangePreset } from '../range'
import type { EnergySummary } from '../transforms/summary'
import { CurrentSnapshotNarrative } from './CurrentSnapshotNarrative'
import { DailySummaryNarrative } from './DailySummaryNarrative'
import { MetricCard } from './MetricCard'
import { MetricsAtPicker } from './MetricsAtPicker'

type Props = {
  cards: DashboardMetric[]
  current: CurrentResponse | null
  loading: boolean
  metricsAt: Date | null
  onMetricsAtChange: (next: Date | null) => void
  summary: EnergySummary
  preset: RangePreset
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

export function MetricsPanel({
  cards,
  current,
  loading,
  metricsAt,
  onMetricsAtChange,
  summary,
  preset,
}: Props) {
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
      <CurrentSnapshotNarrative current={current} loading={loading} />
      <DailySummaryNarrative summary={summary} preset={preset} />
      {groups.accumulated.length > 0 && (
        <section
          className="metrics-group"
          aria-labelledby="metrics-group-accumulated"
        >
          <h2 id="metrics-group-accumulated" className="metrics-group-title">
            {CARD_GROUP_LABELS.accumulated}
          </h2>
          <div className="cards-grid">
            {groups.accumulated.map((card) => (
              <MetricCard
                key={card.key}
                card={card}
                value={pickCardValue(card, { current })}
                loading={loading}
              />
            ))}
          </div>
        </section>
      )}
    </div>
  )
}
