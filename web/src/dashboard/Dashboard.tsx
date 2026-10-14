import { lazy, Suspense, useState } from 'react'
import { useAccess } from '../auth/authContext'
import './dashboard.css'
import { ModeTopBar, type TopBarMenuItem } from '../shell/ModeTopBar'
import { navigateView } from '../shell/navigation'
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

// ExportDialog is also below-the-fold (it only renders when the user
// clicks "Експорт даних"); lazy import keeps its form + custom-export
// fetch helpers out of the initial bundle.
const ExportDialog = lazy(() =>
  import('./components/ExportDialog').then((mod) => ({ default: mod.ExportDialog })),
)

export function Dashboard() {
  const { organizationID, options, change: onOrganizationChange } = useOrganizationParam('analytics.day')
  const access = useAccess()
  // Engineers get the day chart only; the service tools stay with
  // administrators.
  const fullRange = access.can('analytics.full', organizationID)
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
        { id: 'station', label: 'Паспорт станції', onSelect: () => navigateView('station') },
        { id: 'alerts', label: 'Сповіщення', onSelect: () => navigateView('alerts') },
        { id: 'import', label: 'Імпорт архіву', onSelect: () => navigateView('import') },
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

      <DashboardControls
        preset={preset}
        onPresetChange={setPreset}
        presetLocked={!fullRange}
        anchor={anchor}
        onAnchorChange={setAnchor}
        debug={debug}
        onDebugToggle={canService ? toggleDebug : undefined}
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
