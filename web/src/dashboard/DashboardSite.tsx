import { useMemo, type ReactNode } from 'react'
import { useEconomicsAnnualData } from '../economics/useEconomicsAnnualData'
import { useEconomicsMonthlyData } from '../economics/useEconomicsMonthlyData'
import { EdgeStatusChips } from '../shell/EdgeStatusChips'
import { useEdgeSiteStatus } from '../shell/useEdgeSiteStatus'
import type { RegisterMeta } from '../types'
import { EnergyChart } from './components/EnergyChart'
import { EnergyTrendCard } from './components/EnergyTrendCard'
import { MetricsPanel } from './components/MetricsPanel'
import { WeatherCard } from './components/WeatherCard'
import { formatOrganizationLabel } from './config'
import { useDashboardData } from './hooks/useDashboardData'
import { useGridVoltage } from './hooks/useGridVoltage'
import { useUzeDayPlan } from './hooks/useUzeDayPlan'
import type { RangePreset } from './range'
import { monthTrendRows, yearTrendRows } from './transforms/energyTrend'

type Props = {
  organizationID: string
  preset: RangePreset
  anchor: Date
  metricsAt: Date | null
  onMetricsAtChange: (next: Date | null) => void
  debug: boolean
  registers: Record<string, RegisterMeta> | null
  // weather shows the forecast card above the chart. The sites of one
  // elevator share a village, so only the first of them shows it.
  weather: boolean
  // titled heads the block with the site's name and edge status, on a
  // page that stacks several sites.
  titled?: boolean
}

// DashboardSite is one site's «Система» block: the live and period cards
// beside the weather and the energy chart.
export function DashboardSite({
  organizationID,
  preset,
  anchor,
  metricsAt,
  onMetricsAtChange,
  debug,
  registers,
  weather,
  titled = false,
}: Props) {
  const edgeStatus = useEdgeSiteStatus(organizationID, titled)
  const {
    current,
    liveAllocation,
    energySummary,
    energyFlows,
    damSeries,
    socSeries,
    powerSeries,
    pvForecastSeries,
    pvForecastTotal,
    pvForecastLoading,
    pvForecastCoverage,
    pvForecastWholeElevator,
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

  // The voltage is a live reading only; a historical snapshot leaves it out.
  const gridVoltage = useGridVoltage(organizationID, metricsAt === null)

  // Fetched on its own pipeline: the server solves a dynamic program for
  // the day, which must never gate the chart's own data from painting.
  const { data: aiPlan } = useUzeDayPlan({
    organizationID,
    anchor,
    enabled: preset === 'day',
  })

  // The month / year trend reads the economics rollups, which carry the
  // per-day and per-month flow split (who covered the load, what went
  // out). Each hook idles while its preset is not on screen.
  const monthKey = `${anchor.getFullYear()}-${String(anchor.getMonth() + 1).padStart(2, '0')}`
  const monthly = useEconomicsMonthlyData({
    organizationID: preset === 'month' ? organizationID : '',
    month: monthKey,
  })
  const annual = useEconomicsAnnualData({
    organizationID: preset === 'year' ? organizationID : '',
    period: String(anchor.getFullYear()),
  })
  const trend = useMemo(() => {
    if (preset === 'month') {
      return {
        rows: monthTrendRows(monthly.month?.days ?? [], anchor),
        loading: monthly.loading,
        error: monthly.error,
      }
    }
    if (preset === 'year') {
      return {
        rows: yearTrendRows(annual.year?.months ?? [], anchor.getFullYear()),
        loading: annual.loading,
        error: annual.error,
      }
    }
    return null
  }, [preset, anchor, monthly, annual])

  const body: ReactNode = (
    <>
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
          pvForecastWholeElevator={pvForecastWholeElevator}
          gridVoltage={gridVoltage}
        />

        <div className="dashboard-charts">
          {weather && <WeatherCard organizationID={organizationID} anchor={anchor} preset={preset} />}
          {preset === 'day' || !trend ? (
            <EnergyChart
              summary={energySummary}
              loading={loading}
              damSeries={damSeries}
              socSeries={socSeries}
              powerSeries={powerSeries}
              pvForecastSeries={pvForecastSeries}
              pvForecastWholeElevator={pvForecastWholeElevator}
              aiPlan={aiPlan}
            />
          ) : (
            <EnergyTrendCard
              preset={preset}
              rows={trend.rows}
              loading={trend.loading}
              error={trend.error}
              summary={energySummary}
              summaryLoading={loading}
            />
          )}
        </div>
      </div>
    </>
  )

  if (!titled) return body
  const name = formatOrganizationLabel(organizationID)
  return (
    <section className="dashboard-site" aria-label={name}>
      <div className="dashboard-site-head">
        <h2>{name}</h2>
        <EdgeStatusChips status={edgeStatus} mode="analytics" />
      </div>
      {body}
    </section>
  )
}
