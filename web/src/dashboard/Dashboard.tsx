import { useCallback, useState } from 'react'
import './dashboard.css'
import { DashboardHeader } from './components/DashboardHeader'
import { EnergyChart } from './components/EnergyChart'
import { MetricsPanel } from './components/MetricsPanel'
import { RevenueChart } from './components/RevenueChart'
import { useDashboardData } from './hooks/useDashboardData'
import { useOrganizationParam } from './hooks/useOrganizationParam'
import { startOfPeriod, type RangePreset } from './range'

type DashboardTab = 'metrics' | 'charts'

const TAB_LABELS: Record<DashboardTab, string> = {
  metrics: 'Поточні показники',
  charts: 'Графіки',
}

export function Dashboard() {
  const [preset, setPresetState] = useState<RangePreset>('day')
  const [anchor, setAnchor] = useState<Date>(() => startOfPeriod('day', new Date()))
  const [activeTab, setActiveTab] = useState<DashboardTab>('metrics')
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

      <div className="dashboard-tabs" role="tablist" aria-label="Dashboard sections">
        {(Object.keys(TAB_LABELS) as DashboardTab[]).map((tab) => (
          <button
            key={tab}
            type="button"
            role="tab"
            id={`dashboard-tab-${tab}`}
            aria-selected={activeTab === tab}
            aria-controls={`dashboard-panel-${tab}`}
            className={activeTab === tab ? 'active' : ''}
            onClick={() => setActiveTab(tab)}
          >
            {TAB_LABELS[tab]}
          </button>
        ))}
      </div>

      <section
        id="dashboard-panel-metrics"
        role="tabpanel"
        aria-labelledby="dashboard-tab-metrics"
        hidden={activeTab !== 'metrics'}
      >
        {activeTab === 'metrics' && (
          <MetricsPanel cards={config.cards} current={current} loading={loading} />
        )}
      </section>

      <section
        id="dashboard-panel-charts"
        role="tabpanel"
        aria-labelledby="dashboard-tab-charts"
        hidden={activeTab !== 'charts'}
      >
        {activeTab === 'charts' && (
          <div className="dashboard-charts">
            <EnergyChart
              metrics={config.energy_chart}
              series={energySeries}
              preset={preset}
              summary={energySummary}
              loading={loading}
              damSeries={damSeries}
            />
            <RevenueChart energySeries={energySeries} damSeries={damSeries} preset={preset} />
          </div>
        )}
      </section>
    </main>
  )
}
