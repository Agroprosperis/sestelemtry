import { useState } from 'react'
import './dashboard.css'
import { DashboardHeader } from './components/DashboardHeader'
import { EnergyChart } from './components/EnergyChart'
import { MetricsPanel } from './components/MetricsPanel'
import { useDashboardData } from './hooks/useDashboardData'
import { useOrganizationParam } from './hooks/useOrganizationParam'
import type { RangePreset } from './range'

export function Dashboard() {
  const [preset, setPreset] = useState<RangePreset>('day')
  const { organizationID, options, change: onOrganizationChange } = useOrganizationParam()

  const {
    config,
    current,
    energySeries,
    periodEnergyValues,
    dayEnergyValues,
    energySummary,
    loading,
    error,
  } = useDashboardData({ organizationID, preset })

  return (
    <main className="dashboard-page">
      <DashboardHeader
        organizationID={organizationID}
        organizationOptions={options}
        onOrganizationChange={onOrganizationChange}
        preset={preset}
        onPresetChange={setPreset}
      />

      {error && (
        <section className="error-banner" role="alert" aria-live="polite">
          Failed to load data: {error}
        </section>
      )}

      <section className="dashboard-main">
        <MetricsPanel
          cards={config.cards}
          current={current}
          dayEnergyValues={dayEnergyValues}
          periodEnergyValues={periodEnergyValues}
          loading={loading}
        />
        <EnergyChart
          metrics={config.energy_chart}
          series={energySeries}
          preset={preset}
          summary={energySummary}
          loading={loading}
        />
      </section>
    </main>
  )
}
