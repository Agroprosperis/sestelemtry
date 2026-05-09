import { lazy, Suspense, useState } from 'react'
import './dashboard.css'
import { DashboardHeader } from './components/DashboardHeader'
import { EnergyChart } from './components/EnergyChart'
import { MetricsPanel } from './components/MetricsPanel'
import { useDashboardData } from './hooks/useDashboardData'
import { useOrganizationParam } from './hooks/useOrganizationParam'
import { useRangeParams } from './hooks/useRangeParams'

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

  const {
    config,
    current,
    energySeries,
    energySummary,
    damSeries,
    socSeries,
    powerSeries,
    pvForecastSeries,
    loading,
    cardsLoading,
    error,
  } = useDashboardData({
    organizationID,
    preset,
    anchor,
    metricsAt,
  })

  return (
    <main className="dashboard-page">
      <DashboardHeader
        organizationID={organizationID}
        organizationOptions={options}
        onOrganizationChange={onOrganizationChange}
        preset={preset}
        onPresetChange={setPreset}
        anchor={anchor}
        onAnchorChange={setAnchor}
        onExportClick={() => setExportOpen(true)}
      />

      {error && (
        <section className="error-banner" role="alert" aria-live="polite">
          Failed to load data: {error}
        </section>
      )}

      <div className="dashboard-content">
        <MetricsPanel
          current={current}
          loading={cardsLoading}
          metricsAt={metricsAt}
          onMetricsAtChange={setMetricsAt}
          summary={energySummary}
          preset={preset}
        />

        <div className="dashboard-charts">
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
            organizationID={organizationID}
            anchor={anchor}
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
              organizationID={organizationID}
              anchor={anchor}
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
