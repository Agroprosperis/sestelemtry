import type { CurrentResponse, RegisterMeta } from '../../types'
import type { RangePreset } from '../range'
import type { EnergyFlows } from '../transforms/flows'
import type { LiveAllocation } from '../transforms/liveAllocation'
import { AccumulatedSnapshotNarrative } from './AccumulatedSnapshotNarrative'
import { BatteryDayNarrative } from './BatteryDayNarrative'
import { CurrentSnapshotNarrative } from './CurrentSnapshotNarrative'
import { DailySummaryNarrative } from './DailySummaryNarrative'
import { EnergyFlowPeriodSummary } from './EnergyFlowPeriodSummary'
import { MetricsAtPicker } from './MetricsAtPicker'

type Props = {
  current: CurrentResponse | null
  liveAllocation: LiveAllocation
  loading: boolean
  metricsAt: Date | null
  onMetricsAtChange: (next: Date | null) => void
  flows: EnergyFlows
  preset: RangePreset
  anchor: Date
  flowsRefreshing: boolean
  onRefreshFlows: () => void
  debug: boolean
  registers: Record<string, RegisterMeta> | null
  // pvForecastTotal is the planned generation for the period in kWh,
  // or null when the forecast is unavailable for the current preset
  // (only `day` is wired up; month/year would need N daily fetches).
  // Passed straight through to DailySummaryNarrative for the
  // "plan vs fact" line.
  pvForecastTotal: number | null
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
  flows,
  preset,
  anchor,
  flowsRefreshing,
  onRefreshFlows,
  debug,
  registers,
  pvForecastTotal,
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
      <DailySummaryNarrative
        flows={flows}
        preset={preset}
        anchor={anchor}
        debug={debug}
        registers={registers}
        pvForecastTotal={pvForecastTotal}
        loading={flowsRefreshing}
      />
      {preset === 'day' && (
        <BatteryDayNarrative
          flows={flows}
          current={current}
          loading={flowsRefreshing}
        />
      )}
      {preset === 'day' && (
        <EnergyFlowPeriodSummary
          flows={flows}
          preset={preset}
          anchor={anchor}
          refreshing={flowsRefreshing}
          onRefresh={onRefreshFlows}
        />
      )}
      <AccumulatedSnapshotNarrative
        current={current}
        loading={loading}
        debug={debug}
        registers={registers}
      />
    </div>
  )
}
