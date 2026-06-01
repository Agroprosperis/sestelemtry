import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import {
  fetchCurrent,
  fetchDAMPrices,
  fetchDashboardConfig,
  fetchEnergySummary,
  fetchTimeseries,
} from '../../api'
import type { CurrentResponse, DashboardConfig } from '../../types'
import {
  DASHBOARD_CHART_REFRESH_MS,
  DASHBOARD_REFRESH_MS,
  FALLBACK_DASHBOARD_CONFIG,
  MIN_RELIABLE_DATA_AT,
} from '../config'
import { DAY_POWER_FETCH_METRIC_KEYS, DAY_POWER_METRIC_KEYS } from '../metrics'
import { endOfPeriod, rangeParams, startOfPeriod, type RangePreset } from '../range'
import { energyBucketDeltaRows, type EnergyRow } from '../transforms/buckets'
import { damChartRows, type DAMChartRow } from '../transforms/dam'
import { powerChartRows, type PowerChartRow } from '../transforms/power'
import {
  aggregatePvForecastHourly,
  type PvForecastHourlyRow,
} from '../transforms/pvForecast'
import { socChartRows, type SOCChartRow } from '../transforms/soc'
import { flowsFromTotals, EMPTY_FLOWS, type EnergyFlows } from '../transforms/flows'
import {
  liveAllocationFromCurrent,
  type LiveAllocation,
} from '../transforms/liveAllocation'
import {
  energySummaryFromSeries,
  energySummaryFromTotals,
  type EnergySummary,
} from '../transforms/summary'
import { usePvForecast } from './usePvForecast'

// Metrics whose period totals back the dashboard summary cards (the
// "spent / produced / from PV" breakdown). Mirrors
// `EnergySummaryAccumulators` on the backend.
//
// The four directional flow counters (pv_to_ess_kwh / grid_to_ess_kwh
// / ess_to_load_kwh / ess_to_grid_kwh) are NOT listed here — they
// arrive on `response.flows` instead, populated by the API's
// on-the-fly allocator. We opt into the compute by appending the
// synthetic keys to `metric_keys` only on the `day` preset; the
// dashboard hides the period-flow card on month/year so requesting
// the compute for wider windows would just burn CPU.
const BASE_ENERGY_SUMMARY_METRIC_KEYS = [
  'accumulated_pv_energy_yield_kwh',
  'accumulated_electricity_sold_kwh',
  'accumulated_electricity_purchased_kwh',
  'accumulated_power_consumption_kwh',
  'total_energy_charged_kwh',
  'total_energy_discharged_kwh',
]

// Mirrors energyflow.SyntheticMetricKeys on the backend. Included in
// the request only when the user is on the day preset so the API
// runs the allocator and returns response.flows.
const FLOW_METRIC_KEYS = [
  'pv_to_ess_kwh',
  'grid_to_ess_kwh',
  'ess_to_load_kwh',
  'ess_to_grid_kwh',
]

function energySummaryMetricKeys(preset: RangePreset): string[] {
  if (preset === 'day') {
    return [...BASE_ENERGY_SUMMARY_METRIC_KEYS, ...FLOW_METRIC_KEYS]
  }
  return BASE_ENERGY_SUMMARY_METRIC_KEYS
}

export type DashboardData = {
  config: DashboardConfig
  current: CurrentResponse | null
  liveAllocation: LiveAllocation
  energySeries: EnergyRow[]
  energySummary: EnergySummary
  energyFlows: EnergyFlows
  damSeries: DAMChartRow[]
  socSeries: SOCChartRow[]
  powerSeries: PowerChartRow[]
  pvForecastSeries: PvForecastHourlyRow[]
  // pvForecastTotal is the sum of hourly forecast values (kWh; each
  // hour's `plannedKw` already equals planned kWh because the interval
  // is exactly 1 hour). Populated only on the day preset where the
  // n8n flow returns the full forecast for the anchor day; null
  // otherwise so the UI can hide the "vs forecast" comparison instead
  // of showing 0 against actual production.
  pvForecastTotal: number | null
  // loading reflects the charts/summary fetch state and is what `EnergyChart`
  // shows the "Loading..." placeholder for. Cards have their own
  // `cardsLoading` flag so they don't go blank between live ticks.
  loading: boolean
  cardsLoading: boolean
  // flowsRefreshing flips while the user-triggered `refreshFlows`
  // call is in flight. The period-flow card uses it to disable
  // its "Оновити" button and spin the icon — distinct from the
  // initial `loading` so explicit refresh actions don't blank
  // the surrounding charts.
  flowsRefreshing: boolean
  // flowsLoaded becomes true after the first successful
  // /energy-summary fetch and stays true for the lifetime of the
  // hook instance. Cards that read from `energyFlows` use it to
  // distinguish "still loading for the first time" (show placeholders)
  // from "background refresh in flight" (keep the previous values on
  // screen — stale-while-revalidate). A failed refresh does not flip
  // it back to false: the operator keeps seeing the last known good
  // numbers instead of an alarming row of dashes after a transient
  // backend hiccup.
  flowsLoaded: boolean
  // refreshFlows re-fetches /api/v1/energy-summary for the
  // currently selected preset/anchor and refreshes the period
  // flow numbers (plus the cumulative-summary cards on month/
  // year presets). Returns once the request settles so callers
  // can await it; never throws — errors surface through the
  // existing `error` channel.
  refreshFlows: () => Promise<void>
  error: string | null
}

const DAM_DEFAULT_ZONE = 2

function pad(n: number): string {
  return n < 10 ? `0${n}` : String(n)
}

function toDateOnly(d: Date): string {
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
}

function isAbortError(e: unknown): boolean {
  return e instanceof DOMException && e.name === 'AbortError'
}

export function useDashboardData(input: {
  organizationID: string
  preset: RangePreset
  anchor: Date
  metricsAt?: Date | null
}): DashboardData {
  const { organizationID, preset, anchor, metricsAt } = input
  const anchorTime = anchor.getTime()
  const metricsAtTime = metricsAt ? metricsAt.getTime() : null
  const [config, setConfig] = useState<DashboardConfig>(FALLBACK_DASHBOARD_CONFIG)
  const [current, setCurrent] = useState<CurrentResponse | null>(null)
  const [energySeries, setEnergySeries] = useState<EnergyRow[]>([])
  const [energySummary, setEnergySummary] = useState<EnergySummary>(() =>
    energySummaryFromTotals({}),
  )
  const [energyFlows, setEnergyFlows] = useState<EnergyFlows>(EMPTY_FLOWS)
  const [damSeries, setDamSeries] = useState<DAMChartRow[]>([])
  const [socSeries, setSocSeries] = useState<SOCChartRow[]>([])
  const [powerSeries, setPowerSeries] = useState<PowerChartRow[]>([])
  const [loading, setLoading] = useState(true)
  const [cardsLoading, setCardsLoading] = useState(true)
  const [flowsRefreshing, setFlowsRefreshing] = useState(false)
  const [flowsLoaded, setFlowsLoaded] = useState(false)
  const [error, setError] = useState<string | null>(null)
  // flowsRefreshController keeps the AbortController for the
  // in-flight user-triggered refresh so a rapid second click (or
  // a preset/anchor change mid-flight) cancels the stale request
  // before its result clobbers fresh state.
  const flowsRefreshController = useRef<AbortController | null>(null)

  // liveAllocation is the per-poll fan-out of /current into the seven
  // directional kW edges that drive `EnergyFlowLive`. Recomputed on
  // every `current` tick — the transform itself is a few math ops,
  // cheaper than memoizing more aggressively.
  const liveAllocation = useMemo<LiveAllocation>(
    () => liveAllocationFromCurrent(current /* essDischargeSign: hard-coded 1 for now */),
    [current],
  )

  // Forecast lives outside the main /timeseries effect so a slow n8n call
  // doesn't gate the rest of the day chart. For organizations without a
  // forecast mapping (`demo-org`) the hook is a no-op and returns [].
  const pvForecast = usePvForecast({ organizationID, anchor })
  const pvForecastSeries = useMemo<PvForecastHourlyRow[]>(() => {
    if (preset !== 'day') return []
    return aggregatePvForecastHourly(pvForecast.data)
  }, [preset, pvForecast.data])
  const pvForecastTotal = useMemo<number | null>(() => {
    if (preset !== 'day') return null
    if (pvForecastSeries.length === 0) return null
    let sum = 0
    for (const row of pvForecastSeries) {
      if (Number.isFinite(row.plannedKw)) sum += row.plannedKw
    }
    return sum
  }, [preset, pvForecastSeries])

  const configRef = useRef(config)
  useEffect(() => {
    configRef.current = config
  }, [config])

  useEffect(() => {
    const controller = new AbortController()
    let cancelled = false

    async function load() {
      try {
        const cfg = await fetchDashboardConfig(controller.signal)
        if (cancelled) return
        setConfig(cfg)
      } catch (e) {
        if (cancelled || isAbortError(e)) return
        setError(e instanceof Error ? e.message : 'Failed to load dashboard config')
      }
    }
    void load()

    return () => {
      cancelled = true
      controller.abort()
    }
  }, [])

  // Cards live tick: a single /current request, polled at
  // DASHBOARD_REFRESH_MS. The cards represent the system's current state and
  // are independent of which day/preset the user is viewing in the chart
  // tab. A historical snapshot (metricsAt != null) is fetched once and not
  // polled, since the past is immutable.
  useEffect(() => {
    let cancelled = false
    let inflight: AbortController | null = null
    let timer: number | null = null
    const isHistoricalSnapshot = metricsAtTime !== null

    async function tickCards(showLoading: boolean) {
      if (cancelled) return
      if (document.visibilityState === 'hidden') return
      if (inflight) inflight.abort()
      const controller = new AbortController()
      inflight = controller
      if (showLoading) setCardsLoading(true)
      try {
        const cur = await fetchCurrent(
          {
            organizationID,
            at: metricsAtTime ? new Date(metricsAtTime).toISOString() : undefined,
          },
          controller.signal,
        )
        if (cancelled || controller.signal.aborted) return
        setCurrent(cur)
      } catch (e) {
        if (cancelled || isAbortError(e)) return
        setError(e instanceof Error ? e.message : 'Failed to load current metrics')
      } finally {
        if (!cancelled && showLoading) setCardsLoading(false)
      }
    }

    void tickCards(true)
    if (!isHistoricalSnapshot) {
      timer = window.setInterval(() => void tickCards(false), DASHBOARD_REFRESH_MS)
    }

    function onVisibilityChange() {
      if (document.visibilityState === 'visible') void tickCards(false)
    }
    document.addEventListener('visibilitychange', onVisibilityChange)

    return () => {
      cancelled = true
      if (inflight) inflight.abort()
      if (timer !== null) window.clearInterval(timer)
      document.removeEventListener('visibilitychange', onVisibilityChange)
    }
  }, [organizationID, metricsAtTime])

  // Charts, summary, and DAM prices fetch on mount, whenever the user
  // changes preset or anchor, AND every DASHBOARD_CHART_REFRESH_MS so a
  // tab left open doesn't drift away from the live numbers. The
  // background refresh runs with `showLoading=false` so it never
  // blanks the panels — they just re-render when fresh data arrives.
  //
  // Both the timeseries fetches and the DAM fetch share a single effect /
  // AbortController so the chart-area `loading` flag flips false only once
  // every panel below has the data it needs to render. Splitting them
  // (as the previous version did) caused a two-step flicker where the
  // energy chart appeared first and the revenue chart filled in after.
  useEffect(() => {
    let cancelled = false
    let inflight: AbortController | null = null
    let timer: number | null = null

    async function tickCharts(showLoading: boolean) {
      if (cancelled) return
      if (inflight) inflight.abort()
      const controller = new AbortController()
      inflight = controller
      if (showLoading) setLoading(true)
      try {
        const cfg = configRef.current
        const energyKeys = cfg.energy_chart.map((m) => m.key)
        const anchorDate = new Date(anchorTime)
        const now = new Date()
        // SOC is an instantaneous metric, so we fetch it with an `avg`
        // aggregation instead of the default accumulator-delta. We only
        // need it for the day preset (the energy chart overlays it as a
        // background band).
        const needsSOC = preset === 'day'
        // Day preset additionally fetches three instantaneous power metrics
        // (kW snapshots) with `last` aggregation. They drive the redesigned
        // day-chart lines (ESS/Grid/Load) instead of the energy delta areas.
        const needsPower = preset === 'day'
        // Energy series uses the server's per-bucket `delta` aggregation
        // for every preset (5min for day, 1day for month, 1month for
        // year). The server applies `last(value, time) - lag(...)` per
        // bucket and clamps each delta to >= 0 individually, so a single
        // bogus pre-deployment sample at the period boundary can poison
        // at most one bucket — not the whole period. The day summary is
        // derived from the same series client-side via
        // `energySummaryFromSeries`; the month/year summary is computed
        // server-side with the same SUM-of-clamped-bucket-deltas formula,
        // so chart and summary stay byte-identical regardless of preset.
        //
        // The `from` edge is clamped to MIN_RELIABLE_DATA_AT to avoid
        // pulling in lifetime-counter readings from before the deployment
        // was healthy; periods that sit entirely before the floor return
        // empty bars and a zero summary.
        const minReliable = MIN_RELIABLE_DATA_AT.getTime()
        const rawRange = rangeParams(preset, anchorDate, now)
        const energyFrom = new Date(
          Math.max(new Date(rawRange.from).getTime(), minReliable),
        )
        const baseRange = { ...rawRange, from: energyFrom.toISOString() }
        const damFromDate = startOfPeriod(preset, anchorDate)
        const damToExclusive = endOfPeriod(preset, anchorDate)
        const damToDate = new Date(damToExclusive)
        damToDate.setDate(damToDate.getDate() - 1)
        const damFrom = toDateOnly(damFromDate)
        const damTo = toDateOnly(damToDate)

        // For month/year presets we ask the server for the summary totals
        // directly. The server computes `last(end) - last(seed)` per
        // metric — three indexed lookups per metric, no per-bucket
        // aggregation — so monthly/yearly cards never block on summing
        // 30+ deltas on the client. A counter that rolls back
        // mid-period clamps to zero, which the dashboard intentionally
        // surfaces as "no usable data" instead of inventing a number
        // from corrupted samples.
        //
        // Day preset does NOT include the summary in the critical
        // Promise.all because the daily counter cards are derived from
        // `energySeries` (via `energySummaryFromSeries`) and don't
        // depend on summaryResp at all. The synthetic flow counters
        // are the only consumer of summaryResp on the day preset, and
        // their on-the-fly compute can take several seconds — we
        // refuse to block the rest of the dashboard on it. The flow
        // request fires below as an independent async pipeline that
        // updates `energyFlows` whenever it resolves; the period-flow
        // card surfaces its own `flowsRefreshing` spinner so the user
        // can tell the numbers are catching up.
        const needsServerSummary = preset !== 'day'

        const [energy, soc, power, dam, summaryResp] = await Promise.all([
          fetchTimeseries(
            {
              organizationID,
              metricKeys: energyKeys,
              ...baseRange,
            },
            controller.signal,
          ),
          needsSOC
            ? fetchTimeseries(
                {
                  organizationID,
                  metricKeys: ['soc_percent'],
                  ...rangeParams('day', anchorDate, now),
                  aggregation: 'avg',
                },
                controller.signal,
              )
            : Promise.resolve(null),
          needsPower
            ? fetchTimeseries(
                {
                  organizationID,
                  metricKeys: DAY_POWER_FETCH_METRIC_KEYS,
                  ...rangeParams('day', anchorDate, now),
                  aggregation: 'last',
                },
                controller.signal,
              )
            : Promise.resolve(null),
          fetchDAMPrices(
            { zone: DAM_DEFAULT_ZONE, from: damFrom, to: damTo },
            controller.signal,
          ).catch((e) => {
            if (isAbortError(e)) throw e
            // DAM is best-effort: a missing day's prices shouldn't block the
            // energy chart. Surface the failure as an empty price set and
            // let the revenue panel show its own "no data" placeholder.
            return null
          }),
          needsServerSummary
            ? fetchEnergySummary(
                {
                  organizationID,
                  from: baseRange.from,
                  to: baseRange.to,
                  metricKeys: energySummaryMetricKeys(preset),
                },
                controller.signal,
              ).catch((e) => {
                if (isAbortError(e)) throw e
                // Backend may not have the new endpoint deployed yet (rolling
                // upgrade); fall back to the legacy series-derived summary.
                return null
              })
            : Promise.resolve(null),
        ])
        if (cancelled || controller.signal.aborted) return
        const series: EnergyRow[] = energyBucketDeltaRows(
          energy.points,
          energyKeys,
          preset,
          anchorDate,
          now,
        )
        // Day preset keeps its series-derived summary so the cards
        // share the same clamp semantics as the chart bars; month/year
        // presets use the server-side cumulative summary.
        const summary =
          preset === 'day'
            ? energySummaryFromSeries(series)
            : summaryResp
              ? energySummaryFromTotals(summaryResp.totals)
              : energySummaryFromSeries(series)
        setEnergySeries(series)
        setEnergySummary(summary)
        // Month/year never show the period-flow card, so flows
        // collapse to EMPTY_FLOWS here regardless of summaryResp.
        // Day preset's flows arrive via the independent flows pipeline
        // below.
        if (preset !== 'day') {
          setEnergyFlows(EMPTY_FLOWS)
        }
        setSocSeries(soc ? socChartRows(soc.points, 'day', anchorDate) : [])
        setPowerSeries(
          power ? powerChartRows(power.points, DAY_POWER_METRIC_KEYS, anchorDate, now) : [],
        )
        setDamSeries(dam ? damChartRows(dam.prices, preset, anchorDate) : [])
        setError(null)
      } catch (e) {
        if (cancelled || isAbortError(e)) return
        setError(e instanceof Error ? e.message : 'Failed to load dashboard data')
      } finally {
        if (!cancelled && showLoading) setLoading(false)
      }
    }

    void tickCharts(true)
    // Historical snapshots (metricsAt != null) are immutable — no
    // reason to poll. For live dashboards, refresh charts/summary in
    // the background so the period flow numbers stay fresh even on
    // a tab the operator left open since midnight.
    if (metricsAtTime === null) {
      timer = window.setInterval(
        () => void tickCharts(false),
        DASHBOARD_CHART_REFRESH_MS,
      )
    }

    function onVisibilityChange() {
      if (document.visibilityState === 'visible') void tickCharts(false)
    }
    document.addEventListener('visibilitychange', onVisibilityChange)

    return () => {
      cancelled = true
      if (inflight) inflight.abort()
      if (timer !== null) window.clearInterval(timer)
      document.removeEventListener('visibilitychange', onVisibilityChange)
    }
  }, [organizationID, preset, anchorTime, metricsAtTime])

  // Period flows are NOT loaded automatically. The on-the-fly
  // allocator inside /energy-summary scans every telemetry_samples
  // row in the window and pins a Postgres backend for several
  // seconds, which slows down the concurrent /current and /timeseries
  // requests that drive the live diagram and the daily counters.
  // Running it on every page mount made the rest of the dashboard
  // feel "frozen" for 10–20 s, so we now require the operator to
  // ask for it explicitly via the refresh button on the period-flow
  // card (see `refreshFlows` below). Until the button is clicked
  // `energyFlows` stays at `EMPTY_FLOWS` and the card renders its
  // own empty state.

  // refreshFlows refetches /energy-summary for the currently selected
  // preset / anchor and rebuilds the period-flow numbers from the
  // returned totals. The API computes the synthetic
  // `pv_to_ess_kwh` / `grid_to_ess_kwh` / `ess_to_load_kwh` /
  // `ess_to_grid_kwh` counters on the fly from raw Modbus
  // accumulators, so there is no shared cumulative state to drift —
  // a refresh is literally "re-run the allocator on the current
  // raw data" and always produces the same numbers for the same
  // window.
  //
  // The on-the-fly allocator is gated to the `day` preset to keep
  // a refresh cheap; month/year requests refresh the cumulative
  // `energySummary` totals but leave `energyFlows` as EMPTY_FLOWS
  // and the period-flow card hidden.
  const refreshFlows = useCallback(async () => {
    if (flowsRefreshController.current) {
      flowsRefreshController.current.abort()
    }
    const controller = new AbortController()
    flowsRefreshController.current = controller
    setFlowsRefreshing(true)
    try {
      const anchorDate = new Date(anchorTime)
      const now = new Date()
      const minReliable = MIN_RELIABLE_DATA_AT.getTime()
      const rawRange = rangeParams(preset, anchorDate, now)
      const energyFrom = new Date(
        Math.max(new Date(rawRange.from).getTime(), minReliable),
      )
      const baseRange = { ...rawRange, from: energyFrom.toISOString() }

      const summaryResp = await fetchEnergySummary(
        {
          organizationID,
          from: baseRange.from,
          to: baseRange.to,
          metricKeys: energySummaryMetricKeys(preset),
        },
        controller.signal,
      )
      if (controller.signal.aborted) return
      if (preset === 'day') {
        setEnergyFlows(flowsFromTotals(summaryResp.totals, summaryResp.flows ?? null))
      } else {
        setEnergyFlows(EMPTY_FLOWS)
        setEnergySummary(energySummaryFromTotals(summaryResp.totals))
      }
      setFlowsLoaded(true)
      setError(null)
    } catch (e) {
      if (controller.signal.aborted || isAbortError(e)) return
      setError(e instanceof Error ? e.message : 'Failed to refresh period flows')
    } finally {
      if (flowsRefreshController.current === controller) {
        flowsRefreshController.current = null
      }
      setFlowsRefreshing(false)
    }
  }, [organizationID, preset, anchorTime])

  // Auto-trigger the flow allocator on `day` preset so the
  // BatteryDayNarrative / DailySummaryNarrative cards (which read
  // pvToLoad / pvToEss / pvToGrid / essCharged / essDischarged out
  // of `energyFlows`) have data on first paint instead of zeros.
  // The allocator is still gated to `day` because the on-the-fly
  // allocator is meaningless for month/year aggregates. A user
  // switching organization or date kicks this off again; the
  // refresh button on the period-flow card stays useful for forced
  // re-runs without reloading the page.
  //
  // Reset `flowsLoaded` + `energyFlows` to placeholder state on
  // scope change so the period-flow card briefly shows dashes
  // instead of the previous scope's numbers labeled with the new
  // header (e.g. yesterday's flows under today's date). Manual
  // refresh button clicks do NOT pass through here, so they keep
  // the current values on screen during the in-flight fetch.
  useEffect(() => {
    if (preset !== 'day') return
    setFlowsLoaded(false)
    setEnergyFlows(EMPTY_FLOWS)
    refreshFlows()
  }, [organizationID, anchorTime, preset, refreshFlows])

  return {
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
    loading,
    cardsLoading,
    flowsRefreshing,
    flowsLoaded,
    refreshFlows,
    error,
  }
}
