import { useEffect, useRef, useState } from 'react'
import { fetchCurrent, fetchDAMPrices, fetchDashboardConfig, fetchTimeseries } from '../../api'
import type { CurrentResponse, DashboardConfig } from '../../types'
import { DASHBOARD_REFRESH_MS, FALLBACK_DASHBOARD_CONFIG } from '../config'
import { DAY_POWER_METRIC_KEYS } from '../metrics'
import { endOfPeriod, rangeParams, startOfPeriod, type RangePreset } from '../range'
import { energyBucketDeltaRows, type EnergyRow } from '../transforms/buckets'
import {
  cumulativeBucketDeltaRows,
  type CumulativeInput,
} from '../transforms/cumulative'
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
        const summaryStart = startOfPeriod(preset, anchorDate)
        const summaryEndCandidate = endOfPeriod(preset, anchorDate)
        const summaryEnd = summaryEndCandidate.getTime() > now.getTime() ? now : summaryEndCandidate
        // Month/year still need a bucket-cumulative timeseries to draw
        // per-day/per-month bars. Day's chart uses 5-minute deltas (also
        // consumed by the revenue chart) and instantaneous power lines.
        const isBucketCumulative = preset === 'month' || preset === 'year'
        const cumulativeBucket = preset === 'year' ? '1 month' : '1 day'
        // SOC is an instantaneous metric, so we fetch it with an `avg`
        // aggregation instead of the default accumulator-delta. We only
        // need it for the day preset (the energy chart overlays it as a
        // background band).
        const needsSOC = preset === 'day'
        // Day preset additionally fetches three instantaneous power metrics
        // (kW snapshots) with `last` aggregation. They drive the redesigned
        // day-chart lines (ESS/Grid/Load) instead of the energy delta areas.
        const needsPower = preset === 'day'
        const baseRange = rangeParams(preset, anchorDate, now)
        const [energy, seed, end, soc, power] = await Promise.all([
          fetchTimeseries(
            isBucketCumulative
              ? {
                  organizationID,
                  metricKeys: energyKeys,
                  ...baseRange,
                  bucket: cumulativeBucket,
                  aggregation: 'last',
                }
              : {
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
        let series: EnergyRow[]
        if (isBucketCumulative) {
          const cumInput: CumulativeInput = {
            bucketPoints: energy.points,
            seed: seedValues,
          }
          series = cumulativeBucketDeltaRows(cumInput, energyKeys, preset, anchorDate)
        } else {
          series = energyBucketDeltaRows(energy.points, energyKeys, preset, anchorDate, now)
        }
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
