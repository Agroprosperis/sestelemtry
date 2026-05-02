import { useEffect, useMemo, useRef, useState } from 'react'
import { fetchCurrent, fetchDAMPrices, fetchDashboardConfig, fetchTimeseries } from '../../api'
import type { CurrentResponse, DashboardConfig } from '../../types'
import { DASHBOARD_REFRESH_MS, FALLBACK_DASHBOARD_CONFIG } from '../config'
import { endOfPeriod, isCurrentPeriod, rangeParams, startOfPeriod, type RangePreset } from '../range'
import { energyBucketDeltaRows, overrideCurrentDayCell, type EnergyRow } from '../transforms/buckets'
import { damChartRows, type DAMChartRow } from '../transforms/dam'
import { socChartRows, type SOCChartRow } from '../transforms/soc'
import { energySummaryFromSeries, type EnergySummary } from '../transforms/summary'

export type DashboardData = {
  config: DashboardConfig
  current: CurrentResponse | null
  energySeries: EnergyRow[]
  energySummary: EnergySummary
  damSeries: DAMChartRow[]
  socSeries: SOCChartRow[]
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
}): DashboardData {
  const { organizationID, preset, anchor } = input
  const anchorTime = anchor.getTime()
  const [config, setConfig] = useState<DashboardConfig>(FALLBACK_DASHBOARD_CONFIG)
  const [current, setCurrent] = useState<CurrentResponse | null>(null)
  const [energySeries, setEnergySeries] = useState<EnergyRow[]>([])
  const [damSeries, setDamSeries] = useState<DAMChartRow[]>([])
  const [socSeries, setSocSeries] = useState<SOCChartRow[]>([])
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
        // When the user is on the month tab for the current month, we also
        // pull today's 5-minute bucket data so the current-day cell matches
        // the day preset exactly instead of drifting because of a 1-day
        // server bucket.
        const needsTodayOverride = preset === 'month' && isCurrentPeriod('month', anchorDate, now)
        // SOC is an instantaneous metric, so we fetch it with an `avg`
        // aggregation instead of the default accumulator-delta. We only
        // need it for the day preset (the energy chart overlays it as a
        // background band).
        const needsSOC = preset === 'day'
        const [cur, energy, today, soc] = await Promise.all([
          fetchCurrent(organizationID, controller.signal),
          fetchTimeseries(
            {
              organizationID,
              metricKeys: energyKeys,
              ...rangeParams(preset, anchorDate, now),
            },
            controller.signal,
          ),
          needsTodayOverride
            ? fetchTimeseries(
                {
                  organizationID,
                  metricKeys: energyKeys,
                  ...rangeParams('day', now, now),
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
        ])
        if (cancelled || controller.signal.aborted) return
        setCurrent(cur)
        let series = energyBucketDeltaRows(energy.points, energyKeys, preset, anchorDate, now)
        if (today) {
          series = overrideCurrentDayCell(series, today.points, energyKeys, now)
        }
        setEnergySeries(series)
        setSocSeries(soc ? socChartRows(soc.points, 'day', anchorDate) : [])
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

  const energySummary = useMemo(() => energySummaryFromSeries(energySeries), [energySeries])

  return {
    config,
    current,
    energySeries,
    energySummary,
    damSeries,
    socSeries,
    loading,
    error,
  }
}
