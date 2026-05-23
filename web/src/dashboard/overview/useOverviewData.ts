import { useEffect, useMemo, useRef, useState } from 'react'
import {
  fetchCurrent,
  fetchEnergyFlowHourly,
  fetchEnergySummary,
  type EnergySummaryResponse,
} from '../../api'
import type {
  CurrentResponse,
  EnergyFlowHourlyResponse,
  EnergyFlowHourlyRow,
} from '../../types'
import { DASHBOARD_CHART_REFRESH_MS, MIN_RELIABLE_DATA_AT } from '../config'
import { dayRangeParams } from '../range'
import { flowsFromTotals, type EnergyFlows } from '../transforms/flows'
import { aggregatePvForecastHourly } from '../transforms/pvForecast'
import { usePvForecast } from '../hooks/usePvForecast'

// Reference instant the cumulative-counter card uses as the "since
// the period started" anchor. We pin it to MIN_RELIABLE_DATA_AT
// because everything before that point is the pre-deployment seed
// (lifetime counter from a backfilled / faulty sample) — averaging
// it into a "since-period" view would inflate the totals to numbers
// the operator can't reconcile against the dashboard. Keeping the
// value here (vs hard-coding a calendar day) means the cumulative
// card automatically tracks any future bumps to the floor.
const CUMULATIVE_REFERENCE_AT = MIN_RELIABLE_DATA_AT

const DAY_FLOW_METRIC_KEYS = [
  'pv_to_ess_kwh',
  'grid_to_ess_kwh',
  'ess_to_load_kwh',
  'ess_to_grid_kwh',
]

const DAY_TOTAL_METRIC_KEYS = [
  'accumulated_pv_energy_yield_kwh',
  'accumulated_electricity_sold_kwh',
  'accumulated_electricity_purchased_kwh',
  'accumulated_power_consumption_kwh',
  'total_energy_charged_kwh',
  'total_energy_discharged_kwh',
]

const DAY_SUMMARY_METRIC_KEYS = [
  ...DAY_TOTAL_METRIC_KEYS,
  ...DAY_FLOW_METRIC_KEYS,
]

const CUMULATIVE_METRIC_KEYS = [
  'accumulated_pv_energy_yield_kwh',
  'accumulated_power_consumption_kwh',
  'accumulated_electricity_purchased_kwh',
  'accumulated_electricity_sold_kwh',
  'total_energy_charged_kwh',
  'total_energy_discharged_kwh',
  'total_power_supply_from_grid_kwh',
]

function isAbortError(e: unknown): boolean {
  return e instanceof DOMException && e.name === 'AbortError'
}

function pad(n: number): string {
  return n < 10 ? `0${n}` : String(n)
}

function toLocalDateString(d: Date): string {
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
}

function nonNegative(value: number | undefined | null): number {
  return typeof value === 'number' && Number.isFinite(value) && value > 0
    ? value
    : 0
}

// Sum the four directional flows + ESS counter deltas across the
// hourly response so the SOC/perhour callers can fall back on the
// same daily numbers when the day-summary fetch is still in flight
// or the API truncates flows. Used only as a tiebreaker — the
// daily energy-summary `flows` field is always preferred because
// the backend's allocator runs over the full day in one pass.
function sumHourlyFlows(rows: EnergyFlowHourlyRow[]): {
  pvToEss: number
  gridToEss: number
  essToLoad: number
  essToGrid: number
  essCharged: number
  essDischarged: number
} {
  let pvToEss = 0
  let gridToEss = 0
  let essToLoad = 0
  let essToGrid = 0
  let essCharged = 0
  let essDischarged = 0
  for (const row of rows) {
    pvToEss += nonNegative(row.pv_to_ess_kwh)
    gridToEss += nonNegative(row.grid_to_ess_kwh)
    essToLoad += nonNegative(row.ess_to_load_kwh)
    essToGrid += nonNegative(row.ess_to_grid_kwh)
    essCharged += nonNegative(row.ess_charged_kwh)
    essDischarged += nonNegative(row.ess_discharged_kwh)
  }
  return { pvToEss, gridToEss, essToLoad, essToGrid, essCharged, essDischarged }
}

export type CumulativeTotals = {
  pvProducedKwh: number
  consumptionKwh: number
  gridImportKwh: number
  gridExportKwh: number
  essChargedKwh: number
  essDischargedKwh: number
  // gridSupplyKwh is the SmartLogger's "загальне постачання з мережі"
  // counter (`total_power_supply_from_grid_kwh`). Distinct from
  // `gridImportKwh` (which mirrors the salable-direction counter):
  // gridSupply also includes back-feed corrections.
  gridSupplyKwh: number
  // referenceAt is the lower-bound timestamp the totals were
  // computed against; surfaced so the card can render "since
  // <date>" without the parent re-deriving the floor.
  referenceAt: Date | null
}

const EMPTY_CUMULATIVE: CumulativeTotals = {
  pvProducedKwh: 0,
  consumptionKwh: 0,
  gridImportKwh: 0,
  gridExportKwh: 0,
  essChargedKwh: 0,
  essDischargedKwh: 0,
  gridSupplyKwh: 0,
  referenceAt: null,
}

export type OverviewData = {
  organizationID: string
  // anchor is the active calendar day for which all the day-level
  // numbers (Sankey, Daily Summary, Battery cards) are computed.
  // The page picker writes it through useRangeParams.
  anchor: Date
  flows: EnergyFlows
  // dayTotals breaks out the numbers driving the Daily Summary card
  // (forecast comparison + segment bars). It mirrors selected fields
  // from `flows` for backwards compatibility with future callers that
  // only need totals without the directional split.
  dayTotals: {
    pvProducedKwh: number
    gridImportKwh: number
    gridExportKwh: number
    essChargedKwh: number
    essDischargedKwh: number
    consumptionKwh: number
    pvSelfConsumedKwh: number
  }
  // hourly is the raw per-hour breakdown returned by
  // /api/v1/energy-flow-hourly. The Sankey / Daily Summary cards
  // don't currently use the breakdown, but smaller widgets (e.g.
  // SOC by hour) can sit on the same fetch without re-issuing the
  // request.
  hourly: EnergyFlowHourlyRow[]
  // socPercent is the live state-of-charge for the BatteryDayCard.
  // null when the metric is unavailable on the current org or the
  // /current call has not yet resolved.
  socPercent: number | null
  cumulative: CumulativeTotals
  // pvForecastKwh is the n8n forecast total for the active day in
  // kWh, or null when the org has no forecast mapping.
  pvForecastKwh: number | null
  loading: boolean
  error: string | null
}

export function useOverviewData(input: {
  organizationID: string
  anchor: Date
}): OverviewData {
  const { organizationID } = input
  const anchorTime = input.anchor.getTime()

  const [flows, setFlows] = useState<EnergyFlows>(() =>
    flowsFromTotals({}, null),
  )
  const [hourly, setHourly] = useState<EnergyFlowHourlyRow[]>([])
  const [current, setCurrent] = useState<CurrentResponse | null>(null)
  const [cumulative, setCumulative] = useState<CumulativeTotals>(EMPTY_CUMULATIVE)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  // PV forecast lives outside the main effect so a slow n8n call
  // doesn't gate the rest of the cards. For organizations without
  // a forecast mapping (e.g. demo-org) the hook is a no-op and
  // returns []; the caller silently hides the comparison.
  const pvForecast = usePvForecast({
    organizationID,
    anchor: input.anchor,
  })
  const pvForecastTotal = useMemo<number | null>(() => {
    if (pvForecast.data.length === 0) return null
    // Reuse the same hourly aggregation the detailed dashboard uses
    // for its day chart so the two views render exactly the same
    // forecast number for the same anchor day.
    const hourly = aggregatePvForecastHourly(pvForecast.data)
    if (hourly.length === 0) return null
    let sum = 0
    for (const row of hourly) {
      if (Number.isFinite(row.plannedKw)) sum += row.plannedKw
    }
    return sum
  }, [pvForecast.data])

  // The four data fetches are independent but kept in one effect so
  // the parent only flips off `loading` once everything has landed.
  // A separate timer below polls in the background so the Sankey
  // doesn't go stale on a tab the operator left open all day.
  const inflightRef = useRef<AbortController | null>(null)

  useEffect(() => {
    let cancelled = false

    async function load(showLoading: boolean) {
      if (cancelled) return
      if (inflightRef.current) inflightRef.current.abort()
      const controller = new AbortController()
      inflightRef.current = controller
      if (showLoading) setLoading(true)

      try {
        const anchorDate = new Date(anchorTime)
        const dateStr = toLocalDateString(anchorDate)
        const tz = Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'
        const dayRange = dayRangeParams(anchorDate, new Date())
        const cumulativeFrom = CUMULATIVE_REFERENCE_AT.toISOString()
        const cumulativeTo = new Date().toISOString()

        const hourlyP: Promise<EnergyFlowHourlyResponse> = fetchEnergyFlowHourly(
          { organizationID, date: dateStr, tz },
          controller.signal,
        )
        const daySummaryP: Promise<EnergySummaryResponse | null> = fetchEnergySummary(
          {
            organizationID,
            from: dayRange.from,
            to: dayRange.to,
            metricKeys: DAY_SUMMARY_METRIC_KEYS,
          },
          controller.signal,
        ).catch((e: unknown) => {
          if (isAbortError(e)) throw e
          return null
        })
        const cumulativeP: Promise<EnergySummaryResponse | null> = fetchEnergySummary(
          {
            organizationID,
            from: cumulativeFrom,
            to: cumulativeTo,
            metricKeys: CUMULATIVE_METRIC_KEYS,
          },
          controller.signal,
        ).catch((e: unknown) => {
          if (isAbortError(e)) throw e
          return null
        })
        const currentP: Promise<CurrentResponse | null> = fetchCurrent(
          { organizationID },
          controller.signal,
        ).catch((e: unknown) => {
          if (isAbortError(e)) throw e
          return null
        })

        const [hourlyResp, daySummary, cumulativeResp, currentResp] =
          await Promise.all([hourlyP, daySummaryP, cumulativeP, currentP])

        if (cancelled || controller.signal.aborted) return

        // Prefer the day-summary `flows` field (single full-day
        // allocator pass on the backend) so the Sankey reconciles
        // exactly with the same numbers the Перетік card on the
        // detailed dashboard shows. Fall back to summing the
        // hourly response when the daily call failed; that path
        // can drift by float-rounding if the backend re-bucketed
        // intervals across hour boundaries, but it keeps the page
        // useful when the backend is partially degraded.
        let dayFlows: EnergyFlows
        if (daySummary) {
          dayFlows = flowsFromTotals(daySummary.totals, daySummary.flows ?? null)
        } else {
          const hSum = sumHourlyFlows(hourlyResp.hours ?? [])
          dayFlows = flowsFromTotals(
            {
              total_energy_charged_kwh: hSum.essCharged,
              total_energy_discharged_kwh: hSum.essDischarged,
            },
            {
              pv_to_ess_kwh: hSum.pvToEss,
              grid_to_ess_kwh: hSum.gridToEss,
              ess_to_load_kwh: hSum.essToLoad,
              ess_to_grid_kwh: hSum.essToGrid,
            },
          )
        }

        setFlows(dayFlows)
        setHourly(hourlyResp.hours ?? [])
        setCurrent(currentResp)

        if (cumulativeResp) {
          const t = cumulativeResp.totals
          setCumulative({
            pvProducedKwh: nonNegative(t.accumulated_pv_energy_yield_kwh),
            consumptionKwh: nonNegative(t.accumulated_power_consumption_kwh),
            gridImportKwh: nonNegative(t.accumulated_electricity_purchased_kwh),
            gridExportKwh: nonNegative(t.accumulated_electricity_sold_kwh),
            essChargedKwh: nonNegative(t.total_energy_charged_kwh),
            essDischargedKwh: nonNegative(t.total_energy_discharged_kwh),
            gridSupplyKwh: nonNegative(t.total_power_supply_from_grid_kwh),
            referenceAt: CUMULATIVE_REFERENCE_AT,
          })
        } else {
          setCumulative({ ...EMPTY_CUMULATIVE, referenceAt: CUMULATIVE_REFERENCE_AT })
        }

        setError(null)
      } catch (e) {
        if (cancelled || isAbortError(e)) return
        setError(e instanceof Error ? e.message : 'Failed to load overview data')
      } finally {
        if (!cancelled && showLoading) setLoading(false)
      }
    }

    void load(true)
    const timer = window.setInterval(
      () => void load(false),
      DASHBOARD_CHART_REFRESH_MS,
    )

    function onVisibilityChange() {
      if (document.visibilityState === 'visible') void load(false)
    }
    document.addEventListener('visibilitychange', onVisibilityChange)

    return () => {
      cancelled = true
      if (inflightRef.current) inflightRef.current.abort()
      window.clearInterval(timer)
      document.removeEventListener('visibilitychange', onVisibilityChange)
    }
  }, [organizationID, anchorTime])

  const socPercent = useMemo<number | null>(() => {
    const v = current?.metrics?.soc_percent?.value
    return typeof v === 'number' && Number.isFinite(v) ? v : null
  }, [current])

  const dayTotals = useMemo(
    () => ({
      pvProducedKwh: flows.pvProducedKwh,
      gridImportKwh: flows.gridImportKwh,
      gridExportKwh: flows.gridExportKwh,
      essChargedKwh: flows.essChargedKwh,
      essDischargedKwh: flows.essDischargedKwh,
      consumptionKwh: flows.loadConsumedKwh,
      // pvSelfConsumedKwh is what stayed on-site: PV → load + PV → ESS.
      // Used by the Daily Summary segment bar (Куди пішла енергія від
      // СЕС) so the percentages add up to 100% with the export slice.
      pvSelfConsumedKwh: flows.pvToLoadKwh + flows.pvToEssKwh,
    }),
    [flows],
  )

  return {
    organizationID,
    anchor: input.anchor,
    flows,
    dayTotals,
    hourly,
    socPercent,
    cumulative,
    pvForecastKwh: pvForecastTotal,
    loading: loading || pvForecast.loading,
    error,
  }
}

export const __testing__ = {
  CUMULATIVE_REFERENCE_AT,
  DAY_SUMMARY_METRIC_KEYS,
  CUMULATIVE_METRIC_KEYS,
}
