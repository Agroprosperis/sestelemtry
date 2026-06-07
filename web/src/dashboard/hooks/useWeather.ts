import { useEffect, useState } from 'react'
import { fetchOpenMeteoWeather, fetchWeatherForecastFromAPI } from '../../api'
import type { OpenMeteoForecast } from '../../types'
import { weatherDayFromAnchor } from '../transforms/weather'

const WEATHER_TTL_MS = 5 * 60 * 1000

type CacheEntry = {
  data: OpenMeteoForecast
  fetchedAt: number
}

// Module-level cache shared across hook instances. Keyed by
// `${orgID}:${from}:${to}` — the requested date window is part of the
// key so navigating to an archive day doesn't reuse a different
// anchor's cached window (which wouldn't contain the archive day and
// would render the card as "forecast unavailable" even though the data
// exists for the correct range).
const cache = new Map<string, CacheEntry>()

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
  // `data` / `error` are stale from a previous org / disabled state.
  key: string | null
  data: OpenMeteoForecast | null
  error: string | null
}

// fetchRangeForAnchor returns the inclusive YYYY-MM-DD range we ask the
// backend for. We always request a 3-day window (yesterday..tomorrow)
// so flipping between anchor days in the WeatherCard's selector reuses
// the same cached fetch and the request stays small.
function fetchRangeForAnchor(anchor: Date): { from: string; to: string } {
  const yesterday = new Date(anchor)
  yesterday.setDate(yesterday.getDate() - 1)
  const tomorrow = new Date(anchor)
  tomorrow.setDate(tomorrow.getDate() + 1)
  return {
    from: weatherDayFromAnchor(yesterday),
    to: weatherDayFromAnchor(tomorrow),
  }
}

// useWeather fetches the Open-Meteo forecast for the given organization
// and anchor day. It tries the backend's cached forecast first
// (`/api/v1/weather-forecast`) so the weather-collector's centrally
// stored data is the source of truth, then falls back to a direct
// Open-Meteo call if the backend returns empty / errors out. Render
// stays pure (React's `set-state-in-effect` rule) and a 5-minute
// in-memory cache prevents the WeatherCard from refetching on every
// anchor flick.
//
// `enabled` lets the caller short-circuit the fetch when the surrounding
// view doesn't actually render weather (e.g. month/year presets in the
// dashboard). The hook still has to run unconditionally to satisfy the
// rules of hooks, but no network request fires while disabled.
export function useWeather(input: {
  organizationID: string
  latitude: number | null
  longitude: number | null
  anchor: Date
  enabled?: boolean
}): UseWeatherResult {
  const { organizationID, latitude, longitude } = input
  const enabled = input.enabled !== false
  const canFetch = enabled && latitude !== null && longitude !== null
  const range = canFetch ? fetchRangeForAnchor(input.anchor) : null
  const from = range?.from ?? null
  const to = range?.to ?? null
  const key = range ? `${organizationID}:${range.from}:${range.to}` : null

  const [state, setState] = useState<FetchState>({
    key: null,
    data: null,
    error: null,
  })

  useEffect(() => {
    if (!key || latitude === null || longitude === null || from === null || to === null)
      return

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

    const range = { from, to }
    void (async () => {
      let data: OpenMeteoForecast | null = null
      try {
        data = await fetchWeatherForecastFromAPI(
          { organizationID, from: range.from, to: range.to },
          controller.signal,
        )
      } catch (e) {
        if (cancelled || isAbortError(e)) return
        // API hiccup shouldn't blank the widget — `data` stays null
        // and we fall through to the direct Open-Meteo path below.
      }
      if (!data) {
        try {
          data = await fetchOpenMeteoWeather(
            { latitude, longitude },
            controller.signal,
          )
        } catch (e) {
          if (cancelled || isAbortError(e)) return
          setState({
            key,
            data: null,
            error: e instanceof Error ? e.message : 'Failed to load weather',
          })
          return
        }
      }
      if (cancelled) return
      cache.set(key, { data, fetchedAt: Date.now() })
      setState({ key, data, error: null })
    })()

    return () => {
      cancelled = true
      controller.abort()
    }
  }, [key, organizationID, latitude, longitude, from, to])

  if (!key) return { data: null, loading: false, error: null }
  if (state.key !== key) return { data: null, loading: true, error: null }
  return { data: state.data, loading: false, error: state.error }
}
