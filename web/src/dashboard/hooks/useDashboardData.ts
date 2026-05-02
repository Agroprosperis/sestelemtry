import { useEffect, useRef, useState } from 'react'
import { fetchCurrent, fetchDAMPrices, fetchDashboardConfig, fetchTimeseries } from '../../api'
import type { CurrentResponse, DashboardConfig } from '../../types'
import { DASHBOARD_REFRESH_MS, FALLBACK_DASHBOARD_CONFIG } from '../config'
import { DAY_POWER_METRIC_KEYS } from '../metrics'
import { endOfPeriod, rangeParams, startOfPeriod, type RangePreset } from '../range'
import { energyBucketDeltaRows, type EnergyRow } from '../transforms/buckets'
import {
  cumulativeBucketDeltaRows,
  cumulativeTotals,
  type CumulativeInput,
} from '../transforms/cumulative'
import { damChartRows, type DAMChartRow } from '../transforms/dam'
import { powerChartRows, type PowerChartRow } from '../transforms/power'
import { socChartRows, type SOCChartRow } from '../transforms/soc'
import {
  energySummaryFromSeries,
  energySummaryFromTotals,
  type EnergySummary,
} from '../transforms/summary'

export type DashboardData = {
  config: DashboardConfig
  current: CurrentResponse | null
  energySeries: EnergyRow[]
  energySummary: EnergySummary
  damSeries: DAMChartRow[]
  socSeries: SOCChartRow[]
  powerSeries: PowerChartRow[]
  loading: boolean
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
  const [damSeries, setDamSeries] = useState<DAMChartRow[]>([])
  const [socSeries, setSocSeries] = useState<SOCChartRow[]>([])
  const [powerSeries, setPowerSeries] = useState<PowerChartRow[]>([])
  const [loading, setLoading] = useState(true)
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

  useEffect(() => {
    let cancelled = false
    let inflight: AbortController | null = null
    let timer: number | null = null

    async function tick(showLoading: boolean) {
      if (cancelled) return
      if (document.visibilityState === 'hidden') return
      if (inflight) inflight.abort()
      const controller = new AbortController()
      inflight = controller
      if (showLoading) setLoading(true)
      try {
        const cfg = configRef.current
        const energyKeys = cfg.energy_chart.map((m) => m.key)
        const anchorDate = new Date(anchorTime)
        const now = new Date()
        // For month/year we feed the chart cumulative readings + a
        // start-of-period seed and compute deltas on the client. The seed
        // request is a `current?at=startOfPeriod` lookup that returns the
        // last sample at-or-before period start per metric. Combined with
        // bucket-level `aggregation=last` it yields telescoping deltas
        // without losing the first bucket to a missing lag (the cause of
        // the old "10 vs 127 kWh" symptom) and turns Energy Summary totals
        // into a single subtraction instead of a sum of 30+ small values.
        const isCumulative = preset === 'month' || preset === 'year'
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
        const [cur, energy, seed, soc, power] = await Promise.all([
          fetchCurrent(
            {
              organizationID,
              at: metricsAtTime ? new Date(metricsAtTime).toISOString() : undefined,
            },
            controller.signal,
          ),
          fetchTimeseries(
            isCumulative
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
          isCumulative
            ? fetchCurrent(
                {
                  organizationID,
                  at: startOfPeriod(preset, anchorDate).toISOString(),
                },
                controller.signal,
              )
            : Promise.resolve(null),
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
        setCurrent(cur)
        let series: EnergyRow[]
        let summary: EnergySummary
        if (isCumulative) {
          const seedValues: Record<string, number> = {}
          if (seed) {
            for (const [key, m] of Object.entries(seed.metrics)) {
              if (Number.isFinite(m.value)) seedValues[key] = m.value
            }
          }
          const cumInput: CumulativeInput = {
            bucketPoints: energy.points,
            seed: seedValues,
          }
          series = cumulativeBucketDeltaRows(cumInput, energyKeys, preset, anchorDate)
          summary = energySummaryFromTotals(
            cumulativeTotals(cumInput, energyKeys, preset, anchorDate),
          )
        } else {
          series = energyBucketDeltaRows(energy.points, energyKeys, preset, anchorDate, now)
          summary = energySummaryFromSeries(series)
        }
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

    void tick(true)
    timer = window.setInterval(() => void tick(false), DASHBOARD_REFRESH_MS)

    function onVisibilityChange() {
      if (document.visibilityState === 'visible') void tick(false)
    }
    document.addEventListener('visibilitychange', onVisibilityChange)

    return () => {
      cancelled = true
      if (inflight) inflight.abort()
      if (timer !== null) window.clearInterval(timer)
      document.removeEventListener('visibilitychange', onVisibilityChange)
    }
  }, [organizationID, preset, anchorTime, metricsAtTime])

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
    error,
  }
}
