import { useEffect, useMemo, useState } from 'react'
import { fetchOutages, type OutageInterval } from '../../outages/outagesClient'
import { outageAreas, type OutageArea } from '../transforms/outages'
import { forecastDayFromAnchor } from '../transforms/pvForecast'

const OUTAGE_TTL_MS = 5 * 60 * 1000

type CacheEntry = { intervals: OutageInterval[]; fetchedAt: number }

// Shared across hook instances; keyed by `${organization}:${day}`.
const cache = new Map<string, CacheEntry>()

type FetchState = { key: string | null; intervals: OutageInterval[] }

const NONE: OutageInterval[] = []

// DAY_LABELS is the day chart's 5-minute axis ("00:00" … "23:55").
const DAY_LABELS: string[] = Array.from({ length: 288 }, (_, i) => {
  const minutes = i * 5
  return `${String(Math.floor(minutes / 60)).padStart(2, '0')}:${String(minutes % 60).padStart(2, '0')}`
})

export type UseOutageScheduleResult = {
  intervals: OutageInterval[]
  // areas are the intervals clipped to the day and snapped to the
  // chart's timeline, ready for EnergyChart's outageAreas.
  areas: OutageArea[]
  // day is the YYYY-MM-DD (Kyiv) the intervals belong to.
  day: string
}

// useOutageSchedule returns the planned outages of one organization-day.
// It idles (no request, no intervals) while `enabled` is false — the day
// chart is the only place that draws them. A failed fetch or an
// organization with no point reads as no outages: the band is a hint,
// never a reason to break the chart.
export function useOutageSchedule(input: {
  organizationID: string
  anchor: Date
  enabled: boolean
}): UseOutageScheduleResult {
  const { organizationID, enabled } = input
  const day = forecastDayFromAnchor(input.anchor)
  const key = enabled && organizationID ? `${organizationID}:${day}` : null
  const [state, setState] = useState<FetchState>({ key: null, intervals: NONE })

  useEffect(() => {
    if (!key) return
    let cancelled = false
    const controller = new AbortController()
    const hit = cache.get(key)
    if (hit && Date.now() - hit.fetchedAt <= OUTAGE_TTL_MS) {
      queueMicrotask(() => {
        if (!cancelled) setState({ key, intervals: hit.intervals })
      })
      return () => {
        cancelled = true
      }
    }
    void (async () => {
      try {
        const intervals = await fetchOutages(organizationID, day, day, controller.signal)
        if (cancelled) return
        cache.set(key, { intervals, fetchedAt: Date.now() })
        setState({ key, intervals })
      } catch {
        if (cancelled) return
        setState({ key, intervals: NONE })
      }
    })()
    return () => {
      cancelled = true
      controller.abort()
    }
  }, [organizationID, day, key])

  const intervals = key && state.key === key ? state.intervals : NONE
  const areas = useMemo(() => outageAreas(intervals, day, DAY_LABELS), [intervals, day])
  return { intervals, areas, day }
}
