import { useEffect, useState } from 'react'
import { fetchUzeDayPlan, type UzePlanResponse } from '../../api'

// The economics engine defines civil days (and DAM hour numbering) in
// Europe/Kyiv regardless of the operator's browser zone, same as the
// monthly / annual economics views.
const PLAN_TZ = 'Europe/Kyiv'

const PLAN_TTL_MS = 5 * 60 * 1000

type CacheEntry = {
  data: UzePlanResponse
  fetchedAt: number
}

// Module-level cache shared across hook instances, keyed by
// `${organizationID}:${date}`. Paging back and forth between days reuses
// recent results instead of re-running the dynamic program server-side.
const cache = new Map<string, CacheEntry>()

function cacheKey(organizationID: string, date: string): string {
  return `${organizationID}:${date}`
}

function readFreshCache(key: string, now: number): UzePlanResponse | null {
  const hit = cache.get(key)
  if (!hit) return null
  if (now - hit.fetchedAt > PLAN_TTL_MS) return null
  return hit.data
}

function isAbortError(e: unknown): boolean {
  return e instanceof DOMException && e.name === 'AbortError'
}

function pad(n: number): string {
  return n < 10 ? `0${n}` : String(n)
}

// dateOnly mirrors useDashboardData's local-calendar-date derivation so
// the plan covers exactly the day the chart's x-axis is showing.
function dateOnly(d: Date): string {
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
}

export type UseUzeDayPlanResult = {
  data: UzePlanResponse | null
  loading: boolean
  error: string | null
}

type FetchState = {
  // The key the most recent settled fetch belongs to. Comparing it with
  // the current render's key tells us whether `data` is stale from a
  // previous (org, day) combo.
  key: string | null
  data: UzePlanResponse | null
  error: string | null
}

const IDLE: UseUzeDayPlanResult = { data: null, loading: false, error: null }

// useUzeDayPlan fetches the recommended УЗЕ dispatch for the anchored day
// on its own pipeline, deliberately outside the main chart fetch: the
// server-side dynamic program can take a moment and must never gate the
// power lines, SOC band or DAM bands from painting.
//
// A failure resolves to `data: null` — the recommendation is an overlay,
// so the day chart simply renders without it rather than erroring out.
export function useUzeDayPlan(input: {
  organizationID: string
  anchor: Date
  enabled: boolean
}): UseUzeDayPlanResult {
  const { organizationID, enabled } = input
  const date = dateOnly(input.anchor)
  const key = enabled && organizationID ? cacheKey(organizationID, date) : null

  const [state, setState] = useState<FetchState>({
    key: null,
    data: null,
    error: null,
  })

  useEffect(() => {
    if (!key) return

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
        const plan = await fetchUzeDayPlan(
          { organizationID, date, tz: PLAN_TZ },
          controller.signal,
        )
        if (cancelled) return
        cache.set(key, { data: plan, fetchedAt: Date.now() })
        setState({ key, data: plan, error: null })
      } catch (e) {
        if (cancelled || isAbortError(e)) return
        setState({
          key,
          data: null,
          error: e instanceof Error ? e.message : 'Failed to load УЗЕ plan',
        })
      }
    })()

    return () => {
      cancelled = true
      controller.abort()
    }
  }, [key, organizationID, date])

  if (!key) return IDLE
  if (state.key !== key) return { data: null, loading: true, error: null }
  return { data: state.data, loading: false, error: state.error }
}
