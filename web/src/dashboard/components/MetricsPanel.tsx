import type { CurrentResponse, RegisterMeta } from '../../types'
import type { RangePreset } from '../range'
import type { EnergyFlows } from '../transforms/flows'
import type { LiveAllocation } from '../transforms/liveAllocation'
import type { EnergySummary } from '../transforms/summary'
import { AccumulatedSnapshotNarrative } from './AccumulatedSnapshotNarrative'
import { CurrentSnapshotNarrative } from './CurrentSnapshotNarrative'
import { DailySummaryNarrative } from './DailySummaryNarrative'
import { EnergyFlowPeriodSummary } from './EnergyFlowPeriodSummary'
import { MetricsAtPicker } from './MetricsAtPicker'
import { TodayCountersNarrative } from './TodayCountersNarrative'

type Props = {
  current: CurrentResponse | null
  liveAllocation: LiveAllocation
  loading: boolean
  metricsAt: Date | null
  onMetricsAtChange: (next: Date | null) => void
  summary: EnergySummary
  flows: EnergyFlows
  preset: RangePreset
  anchor: Date
  flowsRefreshing: boolean
  onRefreshFlows: () => void
  debug: boolean
  registers: Record<string, RegisterMeta> | null
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
  liveAllocation,
  loading,
  metricsAt,
  onMetricsAtChange,
  summary,
  flows,
  preset,
  anchor,
  flowsRefreshing,
  onRefreshFlows,
  debug,
  registers,
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
      <CurrentSnapshotNarrative
        liveAllocation={liveAllocation}
        loading={loading}
        debug={debug}
        registers={registers}
      />
      <TodayCountersNarrative
        current={current}
        loading={loading}
        debug={debug}
        registers={registers}
      />
      <DailySummaryNarrative
        summary={summary}
        preset={preset}
        anchor={anchor}
        debug={debug}
        registers={registers}
      />
      <EnergyFlowPeriodSummary
        flows={flows}
        preset={preset}
        anchor={anchor}
        refreshing={flowsRefreshing}
        onRefresh={onRefreshFlows}
      />
      <AccumulatedSnapshotNarrative
        current={current}
        loading={loading}
        debug={debug}
        registers={registers}
      />
    </div>
  )
}
