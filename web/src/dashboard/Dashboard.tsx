import { lazy, Suspense, useState } from 'react'
import './dashboard.css'
import { DashboardControls } from './components/DashboardControls'
import { DashboardHeader } from './components/DashboardHeader'
import { EnergyChart } from './components/EnergyChart'
import { MetricsPanel } from './components/MetricsPanel'
import { WeatherCard } from './components/WeatherCard'
import { useDashboardData } from './hooks/useDashboardData'
import { useDebugMode } from './hooks/useDebugMode'
import { useOrganizationParam } from './hooks/useOrganizationParam'
import { useRangeParams } from './hooks/useRangeParams'
import { useRegistersWhenDebug } from './hooks/useRegistersWhenDebug'

// RevenueChart sits below the fold for most users and pulls a sizable
// recharts subgraph (AreaChart + gradients) that the energy chart
// already reuses. Lazy-loading it keeps the initial paint small without
// affecting the energy chart, which renders synchronously.
const RevenueChart = lazy(() =>
  import('./components/RevenueChart').then((mod) => ({ default: mod.RevenueChart })),
)

// ExportDialog is also below-the-fold (it only renders when the user
// clicks "Експорт даних"); lazy import keeps its form + custom-export
// fetch helpers out of the initial bundle.
const ExportDialog = lazy(() =>
  import('./components/ExportDialog').then((mod) => ({ default: mod.ExportDialog })),
)

export function Dashboard() {
  const { preset, anchor, setPreset, setAnchor } = useRangeParams()
  const [metricsAt, setMetricsAt] = useState<Date | null>(null)
  const [exportOpen, setExportOpen] = useState(false)
  const { organizationID, options, change: onOrganizationChange } = useOrganizationParam()
  const { debug, toggleDebug } = useDebugMode()
  const registers = useRegistersWhenDebug(debug)

  const {
    config,
    current,
    liveAllocation,
    energySeries,
    energySummary,
    energyFlows,
    damSeries,
    socSeries,
    powerSeries,
    pvForecastSeries,
    pvForecastTotal,
    loading,
    cardsLoading,
    flowsRefreshing,
    refreshFlows,
    error,
  } = useDashboardData({
    organizationID,
    preset,
    anchor,
    metricsAt,
  })

  return (
    <main className="dashboard-page">
      <DashboardHeader organizationID={organizationID} />

      {error && (
        <section className="error-banner" role="alert" aria-live="polite">
          Failed to load data: {error}
        </section>
      )}

      <div className="dashboard-content">
        <MetricsPanel
          current={current}
          liveAllocation={liveAllocation}
          loading={cardsLoading}
          metricsAt={metricsAt}
          onMetricsAtChange={setMetricsAt}
          summary={energySummary}
          flows={energyFlows}
          preset={preset}
          anchor={anchor}
          flowsRefreshing={flowsRefreshing}
          onRefreshFlows={() => void refreshFlows()}
          debug={debug}
          registers={registers}
          pvForecastTotal={pvForecastTotal}
        />

        <div className="dashboard-charts">
          <DashboardControls
            organizationID={organizationID}
            organizationOptions={options}
            onOrganizationChange={onOrganizationChange}
            preset={preset}
            onPresetChange={setPreset}
            anchor={anchor}
            onAnchorChange={setAnchor}
            onExportClick={() => setExportOpen(true)}
            debug={debug}
            onDebugToggle={toggleDebug}
            view="dashboard"
          />
          <WeatherCard
            organizationID={organizationID}
            anchor={anchor}
            preset={preset}
          />
          <EnergyChart
            metrics={config.energy_chart}
            series={energySeries}
            preset={preset}
            summary={energySummary}
            loading={loading}
            damSeries={damSeries}
            socSeries={socSeries}
            powerSeries={powerSeries}
            pvForecastSeries={pvForecastSeries}
          />
          <Suspense
            fallback={
              <div className="chart-card">
                <div className="chart-wrap">
                  <p className="chart-placeholder">Loading…</p>
                </div>
              </div>
            }
          >
            <RevenueChart
              energySeries={energySeries}
              damSeries={damSeries}
              preset={preset}
              loading={loading}
            />
          </Suspense>
        </div>
      </div>

      {exportOpen && (
        <Suspense fallback={null}>
          <ExportDialog
            organizationID={organizationID}
            initialAnchor={anchor}
            onClose={() => setExportOpen(false)}
          />
        </Suspense>
      )}
    </main>
  )
}
