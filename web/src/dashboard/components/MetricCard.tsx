import type { DashboardMetric } from '../../types'
import { formatNumber } from '../format'

type Props = {
  card: DashboardMetric
  value: number | null
  loading: boolean
}

export function MetricCard({ card, value, loading }: Props) {
  const display = loading ? '...' : value === null ? '--' : formatNumber(value, card.unit)
  return (
    <article className="card" aria-busy={loading}>
      <p className="card-label">{card.label}</p>
      <p className="card-value">
        {display} <span>{card.unit}</span>
      </p>
    </article>
  )
}
