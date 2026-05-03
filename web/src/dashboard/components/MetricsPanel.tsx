import type { CurrentResponse } from '../../types'
import type { RangePreset } from '../range'
import type { EnergySummary } from '../transforms/summary'
import { AccumulatedSnapshotNarrative } from './AccumulatedSnapshotNarrative'
import { CurrentSnapshotNarrative } from './CurrentSnapshotNarrative'
import { DailySummaryNarrative } from './DailySummaryNarrative'
import { MetricsAtPicker } from './MetricsAtPicker'

type Props = {
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
  current,
  loading,
  metricsAt,
  onMetricsAtChange,
  summary,
  preset,
}: Props) {
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
      <AccumulatedSnapshotNarrative current={current} loading={loading} />
    </div>
  )
}
