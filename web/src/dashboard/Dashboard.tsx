import { lazy, Suspense, useState } from 'react'
import { useAccess } from '../auth/authContext'
import './dashboard.css'
import { ModeTopBar, type TopBarMenuItem } from '../shell/ModeTopBar'
import { DashboardControls } from './components/DashboardControls'
import { EnergyChart } from './components/EnergyChart'
import { MetricsPanel } from './components/MetricsPanel'
import { WeatherCard } from './components/WeatherCard'
import { useDashboardData } from './hooks/useDashboardData'
import { useDebugMode } from './hooks/useDebugMode'
import { useOrganizationParam } from './hooks/useOrganizationParam'
import { useRangeParams } from './hooks/useRangeParams'
import { useRegistersWhenDebug } from './hooks/useRegistersWhenDebug'
import { useUzeDayPlan } from './hooks/useUzeDayPlan'

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

// goToView rewrites ?view= (preserving organization_id etc) for the
// service pages reachable from the «Сервіс» menu.
function goToView(view: 'station' | 'alerts' | 'import') {
  const url = new URL(window.location.href)
  url.searchParams.set('view', view)
  window.history.pushState({}, '', url.toString())
  window.dispatchEvent(new PopStateEvent('popstate'))
}

export function Dashboard() {
  const { organizationID, options, change: onOrganizationChange } = useOrganizationParam('analytics.day')
  const access = useAccess()
  // Engineers get the day chart only; money stays with the roles that
  // may read economics, and the service tools with administrators.
  const fullRange = access.can('analytics.full', organizationID)
  const showRevenue = access.can('economics.read', organizationID)
  const canService = access.can('service', organizationID)
  const { preset, anchor, setPreset, setAnchor } = useRangeParams({ dayOnly: !fullRange })
  const [metricsAt, setMetricsAt] = useState<Date | null>(null)
  const [exportOpen, setExportOpen] = useState(false)
  const { debug: debugWanted, toggleDebug } = useDebugMode()
  const debug = canService && debugWanted
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
    pvForecastLoading,
    pvForecastCoverage,
    loading,
    cardsLoading,
    flowsRefreshing,
    flowsLoaded,
    flowsGap,
    refreshFlows,
    error,
  } = useDashboardData({
    organizationID,
    preset,
    anchor,
    metricsAt,
  })

  // Fetched on its own pipeline: the server solves a dynamic program for
  // the day, which must never gate the chart's own data from painting.
  const { data: aiPlan } = useUzeDayPlan({
    organizationID,
    anchor,
    enabled: preset === 'day',
  })

  const serviceMenu: TopBarMenuItem[] = canService
    ? [
        { id: 'station', label: 'Паспорт станції', onSelect: () => goToView('station') },
        { id: 'alerts', label: 'Сповіщення', onSelect: () => goToView('alerts') },
        { id: 'import', label: 'Імпорт архіву', onSelect: () => goToView('import') },
        { id: 'export', label: 'Експорт даних', onSelect: () => setExportOpen(true) },
      ]
    : []

  return (
    <main className="dashboard-page">
      <ModeTopBar
        mode="analytics"
        organizationID={organizationID}
        options={options}
        onOrganizationChange={onOrganizationChange}
        menu={serviceMenu}
      />

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
          flows={energyFlows}
          preset={preset}
          anchor={anchor}
          flowsRefreshing={flowsRefreshing}
          flowsLoaded={flowsLoaded}
          flowsGap={flowsGap}
          onRefreshFlows={() => void refreshFlows()}
          debug={debug}
          registers={registers}
          pvForecastTotal={pvForecastTotal}
          pvForecastLoading={pvForecastLoading}
          pvForecastCoverage={pvForecastCoverage}
        />

        <div className="dashboard-charts">
          <DashboardControls
            preset={preset}
            onPresetChange={setPreset}
            presetLocked={!fullRange}
            anchor={anchor}
            onAnchorChange={setAnchor}
            debug={debug}
            onDebugToggle={canService ? toggleDebug : undefined}
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
            aiPlan={aiPlan}
          />
          {showRevenue && (
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
          )}
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
