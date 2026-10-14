import type { CurrentResponse, RegisterMeta } from '../../types'
import type { RangePreset } from '../range'
import type { EnergyFlows } from '../transforms/flows'
import type { LiveAllocation } from '../transforms/liveAllocation'
import { AccumulatedSnapshotNarrative } from './AccumulatedSnapshotNarrative'
import { BatteryDayNarrative } from './BatteryDayNarrative'
import { CurrentSnapshotNarrative } from './CurrentSnapshotNarrative'
import { DailySummaryNarrative } from './DailySummaryNarrative'

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
  // flowsLoaded marks the first successful /energy-summary completion;
  // the period-flow card uses it to keep stale-but-valid numbers
  // on screen during background refreshes instead of blanking to
  // dashes for the full duration of the on-the-fly allocator.
  flowsLoaded: boolean
  // flowsGap marks a month/year total summed from an incomplete set of
  // days; the period-flow card prints the shortfall.
  flowsGap: { covered: number; expected: number } | null
  onRefreshFlows: () => void
  debug: boolean
  registers: Record<string, RegisterMeta> | null
  // pvForecastTotal / pvForecastLoading / pvForecastCoverage are the
  // period plan and its provenance, passed straight through to
  // DailySummaryNarrative for the "plan vs fact" line. The day preset
  // sums the hourly forecast it already plots; month and year read the
  // per-day plan rollup from the API.
  pvForecastTotal: number | null
  pvForecastLoading: boolean
  pvForecastCoverage: { covered: number; expected: number } | null
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
  flowsLoaded,
  flowsGap,
  onRefreshFlows,
  debug,
  registers,
  pvForecastTotal,
  pvForecastLoading,
  pvForecastCoverage,
}: Props) {
  return (
    <div className="metrics-panel-stack">
      <CurrentSnapshotNarrative
        liveAllocation={liveAllocation}
        loading={loading}
        debug={debug}
        registers={registers}
        metricsAt={metricsAt}
        onMetricsAtChange={onMetricsAtChange}
      />
      <DailySummaryNarrative
        flows={flows}
        preset={preset}
        anchor={anchor}
        debug={debug}
        registers={registers}
        pvForecastTotal={pvForecastTotal}
        pvForecastLoading={pvForecastLoading}
        pvForecastCoverage={pvForecastCoverage}
        loading={flowsRefreshing}
        flowsLoaded={flowsLoaded}
        flowsGap={flowsGap}
      />
      {preset === 'day' && (
        <BatteryDayNarrative
          flows={flows}
          current={current}
          preset={preset}
          anchor={anchor}
          refreshing={flowsRefreshing}
          onRefresh={onRefreshFlows}
          loading={loading}
          flowsLoaded={flowsLoaded}
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
