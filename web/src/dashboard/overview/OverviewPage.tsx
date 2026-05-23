import '../dashboard.css'
import './overview.css'
import { DashboardControls } from '../components/DashboardControls'
import { DashboardHeader } from '../components/DashboardHeader'
import { useDebugMode } from '../hooks/useDebugMode'
import { useOrganizationParam } from '../hooks/useOrganizationParam'
import { useRangeParams } from '../hooks/useRangeParams'
import type { EnergyFlows } from '../transforms/flows'
import { BatteryDayCard } from './BatteryDayCard'
import { BatteryFlowsCard } from './BatteryFlowsCard'
import { CumulativeMetricsCard } from './CumulativeMetricsCard'
import { DailySummaryCard } from './DailySummaryCard'
import { EnergyBalanceSankey } from './EnergyBalanceSankey'
import { useOverviewData } from './useOverviewData'

// hasDemoFlag is a dev-only escape hatch: with `?demo=1` the page
// short-circuits the data hook and renders a deterministic snapshot
// that mirrors the design mock. Useful for visual review when the
// local DB has no real data, but never enabled in production builds.
function hasDemoFlag(): boolean {
  if (!import.meta.env.DEV) return false
  if (typeof window === 'undefined') return false
  return new URLSearchParams(window.location.search).get('demo') === '1'
}

const DEMO_FLOWS: EnergyFlows = {
  pvProducedKwh: 2880,
  loadConsumedKwh: 311.7,
  gridImportKwh: 310,
  gridExportKwh: 2570,
  essChargedKwh: 118.91,
  essDischargedKwh: 0.13,
  pvToLoadKwh: 191.2,
  gridToLoadKwh: 120.4,
  essToLoadKwh: 0.1,
  pvToGridKwh: 2569.97,
  pvToEssKwh: 118.83,
  gridToEssKwh: 0.08,
  essToGridKwh: 0.03,
}

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

  const data = useOverviewData({ organizationID, anchor })
  const demo = hasDemoFlag()
  const flows = demo ? DEMO_FLOWS : data.flows
  const socPercent = demo ? 64 : data.socPercent
  const cumulative = data.cumulative
  const pvForecastKwh = demo ? 3120 : data.pvForecastKwh
  const loading = demo ? false : data.loading
  const error = demo ? undefined : data.error

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
