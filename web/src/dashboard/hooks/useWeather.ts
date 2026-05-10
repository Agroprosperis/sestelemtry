import { useEffect, useState } from 'react'
import { fetchOpenMeteoWeather } from '../../api'
import type { OpenMeteoForecast } from '../../types'
import { weatherDayFromAnchor } from '../transforms/weather'

const WEATHER_TTL_MS = 5 * 60 * 1000

type CacheEntry = {
  data: OpenMeteoForecast
  fetchedAt: number
}

// Module-level cache shared across hook instances. Keyed by
// `${latitude}:${longitude}:${anchorDay}` so flipping between
// today/yesterday (within the forecast window) reuses recent results,
// and switching between organizations doesn't clobber the other org's
// cached payload.
const cache = new Map<string, CacheEntry>()

function cacheKey(latitude: number, longitude: number, day: string): string {
  return `${latitude}:${longitude}:${day}`
}

function readFreshCache(key: string, now: number): OpenMeteoForecast | null {
  const hit = cache.get(key)
  if (!hit) return null
  if (now - hit.fetchedAt > WEATHER_TTL_MS) return null
  return hit.data
}

function isAbortError(e: unknown): boolean {
  return e instanceof DOMException && e.name === 'AbortError'
}

export type UseWeatherResult = {
  data: OpenMeteoForecast | null
  loading: boolean
  error: string | null
}

type FetchState = {
  // The cache key the most recent successful (or failed) fetch belongs
  // to. We compare it with the current render's key to know whether
  // `data` / `error` are stale from a previous (lat, lon, day) combo.
  key: string | null
  data: OpenMeteoForecast | null
  error: string | null
}

// useWeather fetches the Open-Meteo forecast for the given coordinates
// and anchor day, with a 5-minute in-memory cache and a graceful
// fallback to `null` data when coords are absent (e.g. demo-org has
// no location configured) or the fetch fails. Mirrors the structure
// of usePvForecast so render stays pure and React's
// `set-state-in-effect` rule is satisfied.
export function useWeather(input: {
  latitude: number | null
  longitude: number | null
  anchor: Date
}): UseWeatherResult {
  const { latitude, longitude } = input
  const day = weatherDayFromAnchor(input.anchor)
  const key =
    latitude !== null && longitude !== null ? cacheKey(latitude, longitude, day) : null

  const [state, setState] = useState<FetchState>({
    key: null,
    data: null,
    error: null,
  })

  useEffect(() => {
    if (!key || latitude === null || longitude === null) return

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
        const data = await fetchOpenMeteoWeather(
          { latitude, longitude },
          controller.signal,
        )
        if (cancelled) return
        cache.set(key, { data, fetchedAt: Date.now() })
        setState({ key, data, error: null })
      } catch (e) {
        if (cancelled || isAbortError(e)) return
        setState({
          key,
          data: null,
          error: e instanceof Error ? e.message : 'Failed to load weather',
        })
      }
    })()

    return () => {
      cancelled = true
      controller.abort()
    }
  }, [key, latitude, longitude])

  if (!key) return { data: null, loading: false, error: null }
  if (state.key !== key) return { data: null, loading: true, error: null }
  return { data: state.data, loading: false, error: state.error }
}
