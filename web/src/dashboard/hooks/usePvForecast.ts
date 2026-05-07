import { useEffect, useState } from 'react'
import { fetchPvForecast } from '../../api'
import type { PvForecastPoint } from '../../types'
import {
  elevatorCodeFor,
  forecastDayFromAnchor,
  type ElevatorCode,
} from '../transforms/pvForecast'

const PV_FORECAST_TTL_MS = 5 * 60 * 1000

type CacheEntry = {
  data: PvForecastPoint[]
  fetchedAt: number
}

// Module-level cache shared across all hook instances. Keyed by
// `${elevator}:${forecast_day}` so navigating between today/yesterday/etc.
// reuses recent results, and switching between organizations doesn't
// invalidate the other org's cached forecast.
const cache = new Map<string, CacheEntry>()

function cacheKey(elevator: ElevatorCode, forecastDay: string): string {
  return `${elevator}:${forecastDay}`
}

function readFreshCache(key: string, now: number): PvForecastPoint[] | null {
  const hit = cache.get(key)
  if (!hit) return null
  if (now - hit.fetchedAt > PV_FORECAST_TTL_MS) return null
  return hit.data
}

function isAbortError(e: unknown): boolean {
  return e instanceof DOMException && e.name === 'AbortError'
}

export type UsePvForecastResult = {
  data: PvForecastPoint[]
  loading: boolean
  error: string | null
}

type FetchState = {
  // The key the most recent successful (or failed) fetch belongs to. We
  // compare it with the current render's key to know whether `data` /
  // `error` are stale from a previous (org, day) combo.
  key: string | null
  data: PvForecastPoint[]
  error: string | null
}

// usePvForecast returns the raw n8n forecast points for the given org/day,
// with a 5-minute in-memory cache and graceful fallback to an empty list.
// Organizations without a forecast mapping (e.g. `demo-org`) short-circuit
// without firing a request, mirroring the dashboard's "hide silently" UX.
//
// Implementation notes:
//   - Render is pure: the cache and `Date.now()` are only touched inside
//     the effect, and `setState` is only ever called from async callbacks
//     (microtask or fetch resolution). This keeps both
//     `react-hooks/purity` and `react-hooks/set-state-in-effect` happy.
//   - Until the effect populates `state` for the current key, callers see
//     `loading: true`; the UI hides bars in that intermediate frame.
export function usePvForecast(input: {
  organizationID: string
  anchor: Date
}): UsePvForecastResult {
  const { organizationID } = input
  const elevator = elevatorCodeFor(organizationID)
  const forecastDay = forecastDayFromAnchor(input.anchor)
  const key = elevator ? cacheKey(elevator, forecastDay) : null

  const [state, setState] = useState<FetchState>({
    key: null,
    data: [],
    error: null,
  })

  useEffect(() => {
    if (!elevator || !key) return

    let cancelled = false
    const controller = new AbortController()
    const cached = readFreshCache(key, Date.now())
    if (cached) {
      queueMicrotask(() => {
        if (cancelled) return
        setState({ key, data: cached, error: null })
      })
      return () => {
        cancelled = true
      }
    }

    void (async () => {
      try {
        const points = await fetchPvForecast(
          { elevatorCode: elevator, forecastDay },
          controller.signal,
        )
        if (cancelled) return
        cache.set(key, { data: points, fetchedAt: Date.now() })
        setState({ key, data: points, error: null })
      } catch (e) {
        if (cancelled || isAbortError(e)) return
        setState({
          key,
          data: [],
          error: e instanceof Error ? e.message : 'Failed to load PV forecast',
        })
      }
    })()

    return () => {
      cancelled = true
      controller.abort()
    }
  }, [elevator, forecastDay, key])

  if (!key) return { data: [], loading: false, error: null }
  if (state.key !== key) return { data: [], loading: true, error: null }
  return { data: state.data, loading: false, error: state.error }
}
