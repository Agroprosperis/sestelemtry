import { lazy, Suspense, useId, useState } from 'react'
import { useAccess } from '../auth/authContext'
import type { RegisterMeta } from '../types'
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
import { usePeriodAnchor } from './hooks/usePeriodAnchor'
import { useRegistersWhenDebug } from './hooks/useRegistersWhenDebug'
import { useUzeDayPlan } from './hooks/useUzeDayPlan'
import type { RangePreset } from './range'

// ExportDialog is also below-the-fold (it only renders when the user
// clicks "Експорт даних"); lazy import keeps its form + custom-export
// fetch helpers out of the initial bundle.
const ExportDialog = lazy(() =>
  import('./components/ExportDialog').then((mod) => ({ default: mod.ExportDialog })),
)

type PeriodBlockProps = {
  organizationID: string
  preset: RangePreset
  title: string
  anchor: Date
  onAnchorChange: (next: Date) => void
  metricsAt: Date | null
  onMetricsAtChange: (next: Date | null) => void
  debug: boolean
  registers: Record<string, RegisterMeta> | null
  onDebugToggle?: () => void
}

// PeriodBlock is one period of the Система page with its own picker and
// data pipeline. Only the day block carries the live cards, so only it
// polls /current and honours the «На момент» snapshot.
function PeriodBlock({
  organizationID,
  preset,
  title,
  anchor,
  onAnchorChange,
  metricsAt,
  onMetricsAtChange,
  debug,
  registers,
  onDebugToggle,
}: PeriodBlockProps) {
  const titleId = useId()
  const isDay = preset === 'day'
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
    metricsAt: isDay ? metricsAt : null,
    live: isDay,
  })

  // Fetched on its own pipeline: the server solves a dynamic program for
  // the day, which must never gate the chart's own data from painting.
  const { data: aiPlan } = useUzeDayPlan({
    organizationID,
    anchor,
    enabled: isDay,
  })

  return (
    <section className="dashboard-period" aria-labelledby={titleId}>
      <DashboardControls
        title={title}
        titleId={titleId}
        preset={preset}
        anchor={anchor}
        onAnchorChange={onAnchorChange}
        debug={debug}
        onDebugToggle={onDebugToggle}
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
          onMetricsAtChange={onMetricsAtChange}
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
    </section>
  )
}

export function Dashboard() {
  const { organizationID, options, change: onOrganizationChange } = useOrganizationParam('analytics.day')
  const access = useAccess()
  // Engineers get the day block only; the service tools stay with
  // administrators.
  const fullRange = access.can('analytics.full', organizationID)
  const canService = access.can('service', organizationID)
  const day = usePeriodAnchor('day')
  const month = usePeriodAnchor('month')
  const year = usePeriodAnchor('year')
  const [metricsAt, setMetricsAt] = useState<Date | null>(null)
  const [exportOpen, setExportOpen] = useState(false)
  const { debug: debugWanted, toggleDebug } = useDebugMode()
  const debug = canService && debugWanted
  const registers = useRegistersWhenDebug(debug)

  const serviceMenu: TopBarMenuItem[] = canService
    ? [
        { id: 'station', label: 'Паспорт станції', onSelect: () => navigateView('station') },
        { id: 'alerts', label: 'Сповіщення', onSelect: () => navigateView('alerts') },
        { id: 'import', label: 'Імпорт архіву', onSelect: () => navigateView('import') },
        { id: 'export', label: 'Експорт даних', onSelect: () => setExportOpen(true) },
      ]
    : []

  const shared = {
    organizationID,
    metricsAt,
    onMetricsAtChange: setMetricsAt,
    debug,
    registers,
  }

  return (
    <main className="dashboard-page">
      <ModeTopBar
        mode="analytics"
        organizationID={organizationID}
        options={options}
        onOrganizationChange={onOrganizationChange}
        menu={serviceMenu}
      />

      <PeriodBlock
        {...shared}
        preset="day"
        title="День"
        anchor={day.anchor}
        onAnchorChange={day.setAnchor}
        onDebugToggle={canService ? toggleDebug : undefined}
      />
      {fullRange && (
        <>
          <PeriodBlock
            {...shared}
            preset="month"
            title="Місяць"
            anchor={month.anchor}
            onAnchorChange={month.setAnchor}
          />
          <PeriodBlock
            {...shared}
            preset="year"
            title="Рік"
            anchor={year.anchor}
            onAnchorChange={year.setAnchor}
          />
        </>
      )}

      {exportOpen && (
        <Suspense fallback={null}>
          <ExportDialog
            organizationID={organizationID}
            initialAnchor={day.anchor}
            onClose={() => setExportOpen(false)}
          />
        </Suspense>
      )}
    </main>
  )
}
