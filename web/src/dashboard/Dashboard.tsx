import { useCallback, useState } from 'react'
import './dashboard.css'
import { DAMPriceChart } from './components/DAMPriceChart'
import { DashboardHeader } from './components/DashboardHeader'
import { EnergyChart } from './components/EnergyChart'
import { MetricsPanel } from './components/MetricsPanel'
import { useDashboardData } from './hooks/useDashboardData'
import { useOrganizationParam } from './hooks/useOrganizationParam'
import { startOfPeriod, type RangePreset } from './range'

export function Dashboard() {
  const [preset, setPresetState] = useState<RangePreset>('day')
  const [anchor, setAnchor] = useState<Date>(() => startOfPeriod('day', new Date()))
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

  const { config, current, energySeries, energySummary, damSeries, loading, error } = useDashboardData({
    organizationID,
    preset,
    anchor,
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

      <section className="dashboard-main">
        <MetricsPanel cards={config.cards} current={current} loading={loading} />
        <div className="dashboard-charts">
          <EnergyChart
            metrics={config.energy_chart}
            series={energySeries}
            preset={preset}
            summary={energySummary}
            loading={loading}
          />
          <DAMPriceChart series={damSeries} preset={preset} />
        </div>
      </section>
    </main>
  )
}
