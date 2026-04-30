import type { CurrentResponse, DashboardMetric } from '../../types'
import { pickCardValue } from '../cards'
import { MetricCard } from './MetricCard'

type Props = {
  cards: DashboardMetric[]
  current: CurrentResponse | null
  dayEnergyValues: Record<string, number>
  periodEnergyValues: Record<string, number>
  loading: boolean
}

export function MetricsPanel({ cards, current, dayEnergyValues, periodEnergyValues, loading }: Props) {
  return (
    <aside className="metrics-panel">
      <h2>Поточні показники</h2>
      <section className="cards-grid">
        {cards.map((card) => (
          <MetricCard
            key={card.key}
            card={card}
            value={pickCardValue(card, { current, dayEnergyValues, periodEnergyValues })}
            loading={loading}
          />
        ))}
      </section>
    </aside>
  )
}
