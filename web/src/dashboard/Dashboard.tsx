import { useCallback, useState } from 'react'
import './dashboard.css'
import { DashboardHeader } from './components/DashboardHeader'
import { EnergyChart } from './components/EnergyChart'
import { MetricsPanel } from './components/MetricsPanel'
import { RevenueChart } from './components/RevenueChart'
import { useDashboardData } from './hooks/useDashboardData'
import { useOrganizationParam } from './hooks/useOrganizationParam'
import { startOfPeriod, type RangePreset } from './range'

export function Dashboard() {
  const [preset, setPresetState] = useState<RangePreset>('day')
  const [anchor, setAnchor] = useState<Date>(() => startOfPeriod('day', new Date()))
  const [metricsAt, setMetricsAt] = useState<Date | null>(null)
  const { organizationID, options, change: onOrganizationChange } = useOrganizationParam()

  const onPresetChange = useCallback((next: RangePreset) => {
    setPresetState(next)
    setAnchor(startOfPeriod(next, new Date()))
  }, [])

  const onAnchorChange = useCallback(
    (next: Date) => {
      setAnchor(startOfPeriod(preset, next))
    },
    [preset],
  )

  const {
    config,
    current,
    energySeries,
    energySummary,
    damSeries,
    socSeries,
    powerSeries,
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
        onPresetChange={onPresetChange}
        anchor={anchor}
        onAnchorChange={onAnchorChange}
      />

      {error && (
        <section className="error-banner" role="alert" aria-live="polite">
          Failed to load data: {error}
        </section>
      )}

      <div className="dashboard-content">
        <MetricsPanel
          cards={config.cards}
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
          />
          <RevenueChart energySeries={energySeries} damSeries={damSeries} preset={preset} />
        </div>
      </div>
    </main>
  )
}
