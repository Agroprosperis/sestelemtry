import { useEffect, useRef, useState } from 'react'
import { fetchCurrent, fetchDAMPrices, fetchDashboardConfig, fetchTimeseries } from '../../api'
import type { CurrentResponse, DashboardConfig } from '../../types'
import {
  DASHBOARD_REFRESH_MS,
  FALLBACK_DASHBOARD_CONFIG,
  MIN_RELIABLE_DATA_AT,
} from '../config'
import { DAY_POWER_METRIC_KEYS } from '../metrics'
import { endOfPeriod, rangeParams, startOfPeriod, type RangePreset } from '../range'
import { energyBucketDeltaRows, type EnergyRow } from '../transforms/buckets'
import { damChartRows, type DAMChartRow } from '../transforms/dam'
import { powerChartRows, type PowerChartRow } from '../transforms/power'
import { socChartRows, type SOCChartRow } from '../transforms/soc'
import { energySummaryFromTotals, type EnergySummary } from '../transforms/summary'
import { summaryTotalsFromReadings } from '../transforms/summaryTotals'

export type DashboardData = {
  config: DashboardConfig
  current: CurrentResponse | null
  energySeries: EnergyRow[]
  energySummary: EnergySummary
  damSeries: DAMChartRow[]
  socSeries: SOCChartRow[]
  powerSeries: PowerChartRow[]
  // loading reflects the charts/summary fetch state and is what `EnergyChart`
  // shows the "Loading..." placeholder for. Cards have their own
  // `cardsLoading` flag so they don't go blank between live ticks.
  loading: boolean
  cardsLoading: boolean
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

// readingsFromCurrent collapses a /current response into a flat
// {metric_key: value} map, dropping any non-finite samples so callers can
// safely subtract two readings without checking for nulls.
function readingsFromCurrent(resp: CurrentResponse | null): Record<string, number> {
  const out: Record<string, number> = {}
  if (!resp) return out
  for (const [key, m] of Object.entries(resp.metrics)) {
    if (Number.isFinite(m.value)) out[key] = m.value
  }
  return out
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
  const [damSeries, setDamSeries] = useState<DAMChartRow[]>([])
  const [socSeries, setSocSeries] = useState<SOCChartRow[]>([])
  const [powerSeries, setPowerSeries] = useState<PowerChartRow[]>([])
  const [loading, setLoading] = useState(true)
  const [cardsLoading, setCardsLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

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

  // Charts and summary fetch on mount and whenever the user changes preset
  // or anchor; no setInterval. The user explicitly asked for live updates
  // only on the cards — charts are heavy (5 parallel queries) and the
  // numbers don't drift visibly within a few minutes, so on-demand
  // refresh is the right tradeoff here.
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
        // The Energy Summary cards live independently of the chart series.
        // Two `fetchCurrent` lookups (cumulative reading at start of period
        // and at min(endOfPeriod, now)) are subtracted per metric to get
        // the period total — six subtractions, no per-bucket summing for
        // any preset. The chart still owns its own fetch shape.
        //
        // Both timestamps are clamped to MIN_RELIABLE_DATA_AT to avoid
        // pulling lifetime counters from before the deployment was healthy
        // (which would inflate the totals). Periods that sit entirely
        // before the floor collapse to a zero-total summary.
        const minReliable = MIN_RELIABLE_DATA_AT.getTime()
        const startCandidate = startOfPeriod(preset, anchorDate)
        const endCandidate = endOfPeriod(preset, anchorDate)
        const cappedEnd = endCandidate.getTime() > now.getTime() ? now : endCandidate
        const summaryStart = new Date(Math.max(startCandidate.getTime(), minReliable))
        const summaryEnd = new Date(Math.max(cappedEnd.getTime(), minReliable))
        // SOC is an instantaneous metric, so we fetch it with an `avg`
        // aggregation instead of the default accumulator-delta. We only
        // need it for the day preset (the energy chart overlays it as a
        // background band).
        const needsSOC = preset === 'day'
        // Day preset additionally fetches three instantaneous power metrics
        // (kW snapshots) with `last` aggregation. They drive the redesigned
        // day-chart lines (ESS/Grid/Load) instead of the energy delta areas.
        const needsPower = preset === 'day'
        // Energy series uses the server's per-bucket `delta` aggregation for
        // every preset (5min for day, 1day for month, 1month for year). The
        // server applies `last(value, time) - lag(...)` per bucket, so the
        // frontend just renders the rows it gets — no local cumulative
        // diffing. The `from` edge is clamped to MIN_RELIABLE_DATA_AT to
        // avoid pulling in lifetime-counter readings from before the
        // deployment was healthy; periods that sit entirely before the
        // floor return empty bars.
        const rawRange = rangeParams(preset, anchorDate, now)
        const energyFrom = new Date(
          Math.max(new Date(rawRange.from).getTime(), minReliable),
        )
        const baseRange = { ...rawRange, from: energyFrom.toISOString() }
        const [energy, seed, end, soc, power] = await Promise.all([
          fetchTimeseries(
            {
              organizationID,
              metricKeys: energyKeys,
              ...baseRange,
            },
            controller.signal,
          ),
          fetchCurrent(
            { organizationID, at: summaryStart.toISOString() },
            controller.signal,
          ),
          fetchCurrent(
            { organizationID, at: summaryEnd.toISOString() },
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
                  metricKeys: DAY_POWER_METRIC_KEYS,
                  ...rangeParams('day', anchorDate, now),
                  aggregation: 'last',
                },
                controller.signal,
              )
            : Promise.resolve(null),
        ])
        if (cancelled || controller.signal.aborted) return
        const seedValues = readingsFromCurrent(seed)
        const endValues = readingsFromCurrent(end)
        const series: EnergyRow[] = energyBucketDeltaRows(
          energy.points,
          energyKeys,
          preset,
          anchorDate,
          now,
        )
        const summary = energySummaryFromTotals(
          summaryTotalsFromReadings({ seed: seedValues, end: endValues }, energyKeys),
        )
        setEnergySeries(series)
        setEnergySummary(summary)
        setSocSeries(soc ? socChartRows(soc.points, 'day', anchorDate) : [])
        setPowerSeries(
          power ? powerChartRows(power.points, DAY_POWER_METRIC_KEYS, anchorDate, now) : [],
        )
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

  useEffect(() => {
    const controller = new AbortController()
    let cancelled = false
    const anchorDate = new Date(anchorTime)
    const fromDate = startOfPeriod(preset, anchorDate)
    const toDateExclusive = endOfPeriod(preset, anchorDate)
    const toDate = new Date(toDateExclusive)
    toDate.setDate(toDate.getDate() - 1)
    const from = toDateOnly(fromDate)
    const to = toDateOnly(toDate)

    async function load() {
      try {
        const resp = await fetchDAMPrices({ zone: DAM_DEFAULT_ZONE, from, to }, controller.signal)
        if (cancelled) return
        setDamSeries(damChartRows(resp.prices, preset, anchorDate))
      } catch (e) {
        if (cancelled || isAbortError(e)) return
        setDamSeries([])
      }
    }
    void load()

    return () => {
      cancelled = true
      controller.abort()
    }
  }, [preset, anchorTime])

  return {
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
  }
}
