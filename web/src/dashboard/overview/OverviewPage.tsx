import '../dashboard.css'
import './overview.css'
import { DashboardControls } from '../components/DashboardControls'
import { DashboardHeader } from '../components/DashboardHeader'
import { useDebugMode } from '../hooks/useDebugMode'
import { useOrganizationParam } from '../hooks/useOrganizationParam'
import { useRangeParams } from '../hooks/useRangeParams'
import { BatteryDayCard } from './BatteryDayCard'
import { BatteryFlowsCard } from './BatteryFlowsCard'
import { CumulativeMetricsCard } from './CumulativeMetricsCard'
import { DailySummaryCard } from './DailySummaryCard'
import { EnergyBalanceSankey } from './EnergyBalanceSankey'
import { useOverviewData } from './useOverviewData'

// OverviewPage is the high-level "today's energy" view. It packs
// six panels (Sankey balance, daily summary with forecast ring,
// battery card, battery flows, cumulative metrics) onto one
// screen, all driven by the same anchor day picked in the shared
// DashboardControls strip.
//
// Routing-wise this page is reached via `?view=overview`; the
// detailed dashboard remains the default and the two share the
// same organization / anchor URL params so navigating between
// them preserves context.
export function OverviewPage() {
  const { preset, anchor, setPreset, setAnchor } = useRangeParams()
  const { organizationID, options, change: onOrganizationChange } = useOrganizationParam()
  const { debug, toggleDebug } = useDebugMode()

  const { flows, socPercent, cumulative, pvForecastKwh, loading, error } =
    useOverviewData({ organizationID, anchor })

  return (
    <main className="dashboard-page overview-page">
      <DashboardHeader organizationID={organizationID} />

      <DashboardControls
        organizationID={organizationID}
        organizationOptions={options}
        onOrganizationChange={onOrganizationChange}
        preset={preset}
        onPresetChange={setPreset}
        anchor={anchor}
        onAnchorChange={setAnchor}
        debug={debug}
        onDebugToggle={toggleDebug}
        view="overview"
      />

      {error && (
        <section className="error-banner" role="alert" aria-live="polite">
          Не вдалось завантажити дані: {error}
        </section>
      )}

      <section className="overview-grid overview-grid--top">
        <EnergyBalanceSankey flows={flows} date={anchor} loading={loading} />
        <DailySummaryCard
          flows={flows}
          date={anchor}
          pvForecastKwh={pvForecastKwh}
          loading={loading}
        />
      </section>

      <section className="overview-grid overview-grid--bottom">
        <BatteryDayCard
          flows={flows}
          socPercent={socPercent}
          loading={loading}
        />
        <BatteryFlowsCard flows={flows} loading={loading} />
        <CumulativeMetricsCard cumulative={cumulative} loading={loading} />
      </section>
    </main>
  )
}
