import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import {
  fetchCurrent,
  fetchDAMPrices,
  fetchDashboardConfig,
  fetchEnergySummary,
  fetchPvPlanSummary,
  fetchTimeseries,
  type EnergyFlowMeta,
} from '../../api'
import type { CurrentResponse, DashboardConfig } from '../../types'
import {
  DASHBOARD_CHART_REFRESH_MS,
  DASHBOARD_REFRESH_MS,
  energyFloorFor,
  FALLBACK_DASHBOARD_CONFIG,
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
// arrive on `response.flows` instead. Asking for them is what opts
// into the flow pipeline: the live allocator for a day-sized window,
// the persisted per-day rollup for month and year.
const BASE_ENERGY_SUMMARY_METRIC_KEYS = [
  'accumulated_pv_energy_yield_kwh',
  'accumulated_electricity_sold_kwh',
  'accumulated_electricity_purchased_kwh',
  'accumulated_power_consumption_kwh',
  'total_energy_charged_kwh',
  'total_energy_discharged_kwh',
]

// Mirrors energyflow.SyntheticMetricKeys on the backend. Asking for
// any of them is what makes the API attach `flows` to the response.
const FLOW_METRIC_KEYS = [
  'pv_to_ess_kwh',
  'grid_to_ess_kwh',
  'ess_to_load_kwh',
  'ess_to_grid_kwh',
]

// clampEnergyFromIso raises the `from` edge to this site's floor (its
// commissioning day, or MIN_RELIABLE_DATA_AT for a site with no known
// one) so the energy-summary seed sample isn't taken from
// pre-deployment garbage — EXCEPT when the whole requested
// period sits before that floor (e.g. the user is viewing an imported
// archive day/month). In that case clamping would push `from` past
// `to` and invert the range, which the backend rejects (the dashboard
// then showed "energy-summary request failed"). For wholly-historical
// periods the archive data IS the reliable source, so we keep the real
// range unclamped.
function clampEnergyFromIso(fromIso: string, toIso: string, floorMs: number): string {
  const fromMs = new Date(fromIso).getTime()
  const toMs = new Date(toIso).getTime()
  const clamped = Math.max(fromMs, floorMs)
  if (clamped >= toMs) return fromIso
  return new Date(clamped).toISOString()
}

// The flow pipeline asks for the synthetic keys on top of the raw
// accumulators; the chart pipeline asks for the accumulators alone, so
// its own /energy-summary call doesn't pay for flow work twice.
const FLOW_SUMMARY_METRIC_KEYS = [
  ...BASE_ENERGY_SUMMARY_METRIC_KEYS,
  ...FLOW_METRIC_KEYS,
]

// FlowsGap reports that a month/year total was summed from an
// incomplete set of days, so the card can say so instead of presenting
// a short total as the whole period.
export type FlowsGap = { covered: number; expected: number }

// PvPlanRange is the month/year plan-vs-actual denominator: the period
// plan in kWh plus how many of its days actually carry a forecast.
// `plannedKwh: null` means there is no plan to compare against (the
// organization isn't in the forecast flow, or no day in range has one).
type PvPlanRange = { plannedKwh: number | null; coverage: FlowsGap | null }

const EMPTY_PV_PLAN_RANGE: PvPlanRange = { plannedKwh: null, coverage: null }

// pvPlanRangeFrom reads the plan endpoint's coverage fields with the
// same "ignore a one-day shortfall" rule the flow gap uses: the missing
// day is usually today, still being published upstream, and a banner
// every morning trains the operator to ignore banners.
function pvPlanRangeFrom(resp: {
  supported: boolean
  planned_kwh: number
  days_covered: number
  days_expected: number
}): PvPlanRange {
  if (!resp.supported || resp.days_covered <= 0 || !(resp.planned_kwh > 0)) {
    return EMPTY_PV_PLAN_RANGE
  }
  const missing = resp.days_expected - resp.days_covered
  return {
    plannedKwh: resp.planned_kwh,
    coverage:
      missing >= FLOWS_GAP_MIN_MISSING_DAYS
        ? { covered: resp.days_covered, expected: resp.days_expected }
        : null,
  }
}

// flowsGapFrom reads the API's provenance field. Only the cached
// per-day rollup can be incomplete: the live allocator either covers
// the window it was given or reports nothing at all.
//
// A single missing day is ignored. In the current period that day is
// almost always today, which the economics daemon has not reached yet
// on its hourly pass — a banner every morning about the few kWh
// produced since midnight would train the operator to ignore the
// banner. Two or more missing days mean real absent history.
const FLOWS_GAP_MIN_MISSING_DAYS = 2

function flowsGapFrom(meta: EnergyFlowMeta | null | undefined): FlowsGap | null {
  if (!meta || meta.source !== 'daily_cache') return null
  const covered = meta.days_covered ?? 0
  const expected = meta.days_expected ?? 0
  if (covered <= 0 || expected <= 0) return null
  if (expected - covered < FLOWS_GAP_MIN_MISSING_DAYS) return null
  return { covered, expected }
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
  // pvForecastTotal is the planned generation for the period on
  // screen, in kWh. The day preset sums the hourly forecast it already
  // plots; month and year read /api/v1/pv-plan-summary, which sums the
  // same per-day forecasts server-side (see fetchPvPlanSummary). null
  // means there is no plan to compare against, so the UI hides the
  // comparison instead of showing 0 against actual production.
  pvForecastTotal: number | null
  // pvForecastLoading is true while the period plan is in flight, so
  // the card shows a placeholder instead of flashing "прогноз
  // недоступний" between the flows landing and the plan landing.
  pvForecastLoading: boolean
  // pvForecastCoverage is non-null when the period plan covers fewer
  // days than the period holds — the forecast flow's history doesn't
  // reach back that far, so the plan understates the period.
  pvForecastCoverage: FlowsGap | null
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
  // flowsGap is non-null only when a month/year total was summed from
  // fewer days than the period holds, i.e. the economics daemon has
  // not computed every day in range. The card prints the shortfall so
  // an incomplete total isn't read as a complete one.
  flowsGap: FlowsGap | null
  // refreshFlows re-fetches /api/v1/energy-summary for the
  // currently selected preset/anchor and refreshes the period
  // flow numbers. Returns once the request settles so callers
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
  const [flowsGap, setFlowsGap] = useState<FlowsGap | null>(null)
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

  // How far back this site's charts may reach. Carried as epoch ms so
  // the effects below depend on a primitive rather than a fresh Date
  // identity every render.
  const energyFloorMs = useMemo(
    () => energyFloorFor(organizationID).getTime(),
    [organizationID],
  )

  // Forecast lives outside the main /timeseries effect so a slow n8n call
  // doesn't gate the rest of the day chart. For organizations without a
  // forecast mapping (`demo-org`) the hook is a no-op and returns [].
  const pvForecast = usePvForecast({ organizationID, anchor })
  const pvForecastSeries = useMemo<PvForecastHourlyRow[]>(() => {
    if (preset !== 'day') return []
    return aggregatePvForecastHourly(pvForecast.data)
  }, [preset, pvForecast.data])
  const pvDayForecastTotal = useMemo<number | null>(() => {
    if (preset !== 'day') return null
    if (pvForecastSeries.length === 0) return null
    let sum = 0
    for (const row of pvForecastSeries) {
      if (Number.isFinite(row.plannedKw)) sum += row.plannedKw
    }
    return sum
  }, [preset, pvForecastSeries])

  // Month/year plan comes from the server, which sums the per-day
  // forecasts (30 to 365 upstream calls, cached per day) rather than
  // the browser firing one request per day of the period.
  //
  // The scope it was fetched for travels with it, so a plan is only
  // ever read under the header it belongs to: a period change makes the
  // stored key mismatch and the card falls back to placeholders without
  // an effect having to blank anything.
  const planScope = `${organizationID}|${preset}|${anchorTime}`
  const [pvPlan, setPvPlan] = useState<{ scope: string; range: PvPlanRange } | null>(null)
  const pvPlanRange = pvPlan?.scope === planScope ? pvPlan.range : EMPTY_PV_PLAN_RANGE

  const pvForecastTotal = preset === 'day' ? pvDayForecastTotal : pvPlanRange.plannedKwh
  const pvForecastLoading =
    preset === 'day' ? pvForecast.loading : pvPlan?.scope !== planScope
  const pvForecastCoverage = preset === 'day' ? null : pvPlanRange.coverage

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
        const rawRange = rangeParams(preset, anchorDate, now)
        const baseRange = {
          ...rawRange,
          from: clampEnergyFromIso(rawRange.from, rawRange.to, energyFloorMs),
        }
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
                  metricKeys: BASE_ENERGY_SUMMARY_METRIC_KEYS,
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
        setSocSeries(soc ? socChartRows(soc.points, 'day', anchorDate) : [])
        // On the day preset, reconstruct the power lines from the cumulative
        // energy deltas (already fetched as `energy`) for any bucket lacking
        // an instantaneous sample — that is how archive-only days, which have
        // no `*_power_kw` snapshots, still render PV/Grid/ESS/Load. Live
        // buckets keep their instantaneous values (fallback is per-bucket).
        setPowerSeries(
          power
            ? powerChartRows(
                power.points,
                DAY_POWER_METRIC_KEYS,
                anchorDate,
                now,
                energy.points,
              )
            : [],
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
  }, [organizationID, preset, anchorTime, metricsAtTime, energyFloorMs])

  // Period flows live on an independent pipeline from the
  // /timeseries chart fetch. The on-the-fly allocator inside
  // /energy-summary scans every telemetry_samples row in the
  // window and pins a Postgres backend for several seconds, which
  // would slow down the concurrent /current and /timeseries
  // requests that drive the live diagram and the daily counters
  // if we awaited it inside `tickCharts`'s Promise.all. So
  // /energy-summary runs in its own effect (see below): it fires
  // once on scope change to populate the narrative cards, and
  // then re-fires on `DASHBOARD_CHART_REFRESH_MS` so the period-
  // flow numbers don't drift behind the chart as the day
  // progresses (previously the chart hit ~4 MWh by evening while
  // the narrative card was still showing ~1.7 MWh captured at
  // mid-day — same accumulator, different staleness).

  // refreshFlows refetches /energy-summary for the currently selected
  // preset / anchor and rebuilds the period-flow numbers from the
  // returned totals. There is no shared cumulative state to drift —
  // a refresh always produces the same numbers for the same window.
  //
  // Which pipeline answers depends on the width of the window, and the
  // API reports that back in `flows_meta.source`: the day preset runs
  // the allocator live over raw Modbus counters, while month and year
  // sum the per-day totals the economics daemon persisted (the same
  // allocator, run day by day overnight, reconciled against the
  // metered charge/discharge counters). A live pass over a month would
  // take minutes, so the cache is what makes those presets possible at
  // all; the trade is that today's contribution to a month total lags
  // by up to one economics refresh interval.
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
      const rawRange = rangeParams(preset, anchorDate, now)
      const baseRange = {
        ...rawRange,
        from: clampEnergyFromIso(rawRange.from, rawRange.to, energyFloorMs),
      }

      const summaryResp = await fetchEnergySummary(
        {
          organizationID,
          from: baseRange.from,
          to: baseRange.to,
          metricKeys: FLOW_SUMMARY_METRIC_KEYS,
        },
        controller.signal,
      )
      if (controller.signal.aborted) return
      setEnergyFlows(flowsFromTotals(summaryResp.totals, summaryResp.flows ?? null))
      setFlowsGap(flowsGapFrom(summaryResp.flows_meta))
      setFlowsLoaded(true)
      setError(null)
    } catch (e) {
      if (controller.signal.aborted || isAbortError(e)) return
      setError(e instanceof Error ? e.message : 'Failed to refresh period flows')
    } finally {
      // Only the run that still owns the slot may clear the spinner.
      // A superseded run reaching here would otherwise report "done"
      // while its replacement is still fetching.
      if (flowsRefreshController.current === controller) {
        flowsRefreshController.current = null
        setFlowsRefreshing(false)
      }
    }
  }, [organizationID, preset, anchorTime, energyFloorMs])

  // Fetch the period flows for whatever scope is on screen so the
  // BatteryDayNarrative / DailySummaryNarrative cards (which read
  // pvToLoad / pvToEss / pvToGrid / essCharged / essDischarged out
  // of `energyFlows`) have data on first paint instead of zeros,
  // and re-fire on the same cadence as the chart so the narrative
  // numbers track the period forward instead of freezing at whatever
  // accumulator snapshot the first fetch captured. The refresh
  // button on the period-flow card stays useful as a force-now
  // override (it shares `refreshFlows`, so it also cancels the
  // in-flight background request via the AbortController).
  //
  // Reset `flowsLoaded` + `energyFlows` to placeholder state on
  // scope change so the period-flow card briefly shows dashes
  // instead of the previous scope's numbers labeled with the new
  // header (e.g. yesterday's flows under today's date). Background
  // re-fires inside the interval keep the previous values on
  // screen (stale-while-revalidate) — only scope changes blank
  // them out.
  //
  // Aborting on the way out matters for the same reason: the day
  // allocator takes 5–15 s on a busy day, so switching period
  // mid-flight used to let the day's answer land after the switch
  // and repopulate the card the operator had just changed away
  // from. `refreshFlows` writes nothing once its controller is
  // aborted.
  //
  // Historical snapshots (`metricsAt != null`) fire once and skip
  // the interval: the period is immutable so there is no fresher
  // data to chase.
  useEffect(() => {
    const abortInflight = () => {
      flowsRefreshController.current?.abort()
    }
    setFlowsLoaded(false)
    setEnergyFlows(EMPTY_FLOWS)
    setFlowsGap(null)
    void refreshFlows()
    if (metricsAtTime !== null) return abortInflight
    const timer = window.setInterval(() => {
      if (document.visibilityState === 'hidden') return
      void refreshFlows()
    }, DASHBOARD_CHART_REFRESH_MS)
    function onVisibilityChange() {
      if (document.visibilityState === 'visible') void refreshFlows()
    }
    document.addEventListener('visibilitychange', onVisibilityChange)
    return () => {
      abortInflight()
      window.clearInterval(timer)
      document.removeEventListener('visibilitychange', onVisibilityChange)
    }
  }, [organizationID, anchorTime, preset, metricsAtTime, refreshFlows])

  // Period plan for the month/year presets, on its own pipeline for the
  // same reason the flows are: filling a cold period's per-day plan
  // cache upstream can take seconds, and nothing else on the dashboard
  // should wait for it.
  //
  // Aborted on the way out so a slow answer for last month can't land
  // under this month's header — the same race the flows effect guards
  // against. The day preset skips the request entirely: it already
  // holds the hourly forecast for its anchor day and sums that.
  useEffect(() => {
    if (preset === 'day') return
    const controller = new AbortController()

    async function load() {
      try {
        const rawRange = rangeParams(preset, new Date(anchorTime), new Date())
        const resp = await fetchPvPlanSummary(
          {
            organizationID,
            from: clampEnergyFromIso(rawRange.from, rawRange.to, energyFloorMs),
            to: rawRange.to,
          },
          controller.signal,
        )
        if (controller.signal.aborted) return
        setPvPlan({ scope: planScope, range: pvPlanRangeFrom(resp) })
      } catch (e) {
        if (controller.signal.aborted || isAbortError(e)) return
        // Best-effort: a missing plan hides the comparison, it doesn't
        // invalidate the actuals sitting next to it, so this never
        // touches the shared `error` channel.
        setPvPlan({ scope: planScope, range: EMPTY_PV_PLAN_RANGE })
      }
    }
    void load()

    return () => {
      controller.abort()
    }
  }, [organizationID, preset, anchorTime, energyFloorMs, planScope])

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
    pvForecastLoading,
    pvForecastCoverage,
    loading,
    cardsLoading,
    flowsRefreshing,
    flowsLoaded,
    flowsGap,
    refreshFlows,
    error,
  }
}
