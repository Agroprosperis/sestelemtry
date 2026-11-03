import { useEffect, useState } from 'react'
import { fetchPvForecast } from '../../api'
import {
  forecastDayFromAnchor,
  pvForecastRows,
  type PvForecastHourlyRow,
} from '../transforms/pvForecast'

const PV_FORECAST_TTL_MS = 5 * 60 * 1000

type ForecastData = {
  rows: PvForecastHourlyRow[]
  supported: boolean
  wholeElevator: boolean
}

const EMPTY: ForecastData = { rows: [], supported: false, wholeElevator: false }

type CacheEntry = {
  data: ForecastData
  fetchedAt: number
}

// Module-level cache shared across all hook instances (the day chart and
// the export dialog ask for the same day). Keyed by
// `${organization}:${forecast_day}` so navigating between days reuses
// recent results, and switching organizations doesn't drop the other
// one's.
const cache = new Map<string, CacheEntry>()

function cacheKey(organizationID: string, forecastDay: string): string {
  return `${organizationID}:${forecastDay}`
}

function readFreshCache(key: string, now: number): ForecastData | null {
  const hit = cache.get(key)
  if (!hit) return null
  if (now - hit.fetchedAt > PV_FORECAST_TTL_MS) return null
  return hit.data
}

function isAbortError(e: unknown): boolean {
  return e instanceof DOMException && e.name === 'AbortError'
}

export type UsePvForecastResult = ForecastData & {
  loading: boolean
  error: string | null
}

type FetchState = {
  // The key the most recent successful (or failed) fetch belongs to. We
  // compare it with the current render's key to know whether the data
  // is stale from a previous (org, day) combo.
  key: string | null
  data: ForecastData
  error: string | null
}

// usePvForecast returns the hourly plan of one organization-day, from
// whichever source the station's passport selects, with a 5-minute
// in-memory cache and graceful fallback to no plan. `supported` is false
// for organizations with no plan source (demo-org), which hides the plan
// line rather than drawing zeros.
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
  const forecastDay = forecastDayFromAnchor(input.anchor)
  const key = organizationID ? cacheKey(organizationID, forecastDay) : null

  const [state, setState] = useState<FetchState>({
    key: null,
    data: EMPTY,
    error: null,
  })

  useEffect(() => {
    if (!organizationID || !key) return

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
        const resp = await fetchPvForecast(
          { organizationID, date: forecastDay },
          controller.signal,
        )
        if (cancelled) return
        const data: ForecastData = {
          rows: resp.supported ? pvForecastRows(resp.hours) : [],
          supported: resp.supported,
          wholeElevator: Boolean(resp.whole_elevator),
        }
        cache.set(key, { data, fetchedAt: Date.now() })
        setState({ key, data, error: null })
      } catch (e) {
        if (cancelled || isAbortError(e)) return
        setState({
          key,
          data: EMPTY,
          error: e instanceof Error ? e.message : 'Failed to load PV forecast',
        })
      }
    })()

    return () => {
      cancelled = true
      controller.abort()
    }
  }, [organizationID, forecastDay, key])

  if (!key) return { ...EMPTY, loading: false, error: null }
  if (state.key !== key) return { ...EMPTY, loading: true, error: null }
  return { ...state.data, loading: false, error: state.error }
}
