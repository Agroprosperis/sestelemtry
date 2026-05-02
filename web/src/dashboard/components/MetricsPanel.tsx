import type { CurrentResponse, DashboardMetric } from '../../types'
import { pickCardValue } from '../cards'
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
  return (
    <aside className="metrics-panel">
      <header className="metrics-panel-head">
        <h2>Поточні показники</h2>
        <MetricsAtPicker value={metricsAt} onChange={onMetricsAtChange} />
      </header>
      {metricsAt && (
        <p className="metrics-at-hint">
          Показники станом на <strong>{formatSnapshotLabel(metricsAt)}</strong>
        </p>
      )}
      <section className="cards-grid">
        {cards.map((card) => (
          <MetricCard
            key={card.key}
            card={card}
            value={pickCardValue(card, { current })}
            loading={loading}
          />
        ))}
      </section>
    </aside>
  )
}
