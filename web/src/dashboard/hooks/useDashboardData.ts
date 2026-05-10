import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import {
  fetchCurrent,
  fetchDAMPrices,
  fetchDashboardConfig,
  fetchEnergySummary,
  fetchTimeseries,
  recomputeEnergyFlow,
} from '../../api'
import type { CurrentResponse, DashboardConfig } from '../../types'
import {
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
// "spent / produced / from PV" breakdown) and the energy-flow period
// summary. Mirrors `EnergySummaryAccumulators` on the backend.
//
// The four `*_to_*_kwh` entries are synthetic counters emitted by
// the collector's energyflow aggregator — they live in
// telemetry_samples next to the SmartLogger accumulators, so the
// existing /api/v1/energy-summary endpoint serves them through the
// same `last(end) - last(seed)` lookup. Older deployments without
// the aggregator will return zero for these keys, which the
// flowsFromTotals transform handles gracefully.
const ENERGY_SUMMARY_METRIC_KEYS = [
  'accumulated_pv_energy_yield_kwh',
  'accumulated_electricity_sold_kwh',
  'accumulated_electricity_purchased_kwh',
  'accumulated_power_consumption_kwh',
  'total_energy_charged_kwh',
  'total_energy_discharged_kwh',
  'pv_to_ess_kwh',
  'grid_to_ess_kwh',
  'ess_to_load_kwh',
  'ess_to_grid_kwh',
]

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

  // Charts, summary, and DAM prices fetch on mount and whenever the user
  // changes preset or anchor; no setInterval. The user explicitly asked for
  // live updates only on the cards — charts are heavy and the numbers
  // don't drift visibly within a few minutes, so on-demand refresh is the
  // right tradeoff here.
  //
  // Both the timeseries fetches and the DAM fetch share a single effect /
  // AbortController so the chart-area `loading` flag flips false only once
  // every panel below has the data it needs to render. Splitting them
  // (as the previous version did) caused a two-step flicker where the
  // energy chart appeared first and the revenue chart filled in after.
  useEffect(() => {
    let cancelled = false
    let inflight: AbortController | null = null

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
        // We always fetch the summary, including for the day preset,
        // because the energy-flow period summary needs the four
        // synthetic counters (pv_to_ess_kwh, grid_to_ess_kwh,
        // ess_to_load_kwh, ess_to_grid_kwh) and those are only emitted
        // as cumulative samples — they have no per-bucket delta to
        // reconstruct from energySeries. The day preset still uses the
        // series-derived summary for its cards so the existing
        // bucket-clamp behaviour is preserved.
        const needsServerSummary = true

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
                  metricKeys: ENERGY_SUMMARY_METRIC_KEYS,
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
        // presets use the server-side cumulative summary. The period
        // flows always come from the server summary because the
        // synthetic counters are emitted as cumulative samples with
        // no per-bucket delta to reconstruct on the client.
        const summary =
          preset === 'day'
            ? energySummaryFromSeries(series)
            : summaryResp
              ? energySummaryFromTotals(summaryResp.totals)
              : energySummaryFromSeries(series)
        const flows = summaryResp ? flowsFromTotals(summaryResp.totals) : EMPTY_FLOWS
        setEnergySeries(series)
        setEnergySummary(summary)
        setEnergyFlows(flows)
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

    return () => {
      cancelled = true
      if (inflight) inflight.abort()
    }
  }, [organizationID, preset, anchorTime])

  // refreshFlows triggers a backend backfill for the current period
  // so missing historical data can be filled in on demand. The flow
  // is:
  //
  //  1. Ask the server to recompute the four synthetic counters from
  //     the raw source counters for [from, to). The server replaces
  //     any previously-emitted synthetic rows in that window with the
  //     freshly computed ones (idempotent on repeat clicks).
  //  2. Refetch /energy-summary so the dashboard immediately reflects
  //     the new cumulative values.
  //
  // The recompute step is clamped to `(now - 3 min)` because the
  // server refuses to recompute up to "now" — that would race the
  // live aggregator's in-flight bucket and corrupt the cumulative
  // timeline. For day-preset clicks that produce a recompute window
  // narrower than 60 s after clamping (i.e. the request fired right
  // after midnight against the same day), we skip the recompute and
  // only refetch — there is no historical data to backfill in that
  // sliver and the live aggregator's tick will pick it up shortly.
  //
  // A recompute failure does not block the refetch: the user clicked
  // "Оновити" expecting the panel to redraw, so we always end on a
  // summary fetch and surface the recompute error via setError().
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

      // Step 1: backend recompute. Clamp `to` against the server's
      // safety margin so a day-preset click against today still
      // backfills everything up to ~3 minutes ago instead of
      // bouncing with a 400. The 3 min figure leaves one minute of
      // headroom over the server's 2 min cutoff for clock skew /
      // request travel time.
      const SAFETY_MARGIN_MS = 3 * 60 * 1000
      const fromMs = new Date(baseRange.from).getTime()
      const toMs = Math.min(
        new Date(baseRange.to).getTime(),
        now.getTime() - SAFETY_MARGIN_MS,
      )
      let recomputeWarning: string | null = null
      if (toMs - fromMs > 60_000) {
        try {
          await recomputeEnergyFlow(
            {
              organizationID,
              from: new Date(fromMs).toISOString(),
              to: new Date(toMs).toISOString(),
            },
            controller.signal,
          )
        } catch (e) {
          if (controller.signal.aborted || isAbortError(e)) return
          recomputeWarning = e instanceof Error ? e.message : 'Recompute failed'
        }
      }
      if (controller.signal.aborted) return

      // Step 2: refetch the cumulative summary so the UI shows the
      // freshly recomputed values (and falls through to whatever
      // exists in the DB if the recompute call failed above).
      const summaryResp = await fetchEnergySummary(
        {
          organizationID,
          from: baseRange.from,
          to: baseRange.to,
          metricKeys: ENERGY_SUMMARY_METRIC_KEYS,
        },
        controller.signal,
      )
      if (controller.signal.aborted) return
      setEnergyFlows(flowsFromTotals(summaryResp.totals))
      if (preset !== 'day') {
        setEnergySummary(energySummaryFromTotals(summaryResp.totals))
      }
      if (recomputeWarning) {
        setError(recomputeWarning)
      } else {
        setError(null)
      }
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
    loading,
    cardsLoading,
    flowsRefreshing,
    refreshFlows,
    error,
  }
}
