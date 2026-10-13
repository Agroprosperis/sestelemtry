import { useEffect, useMemo, useState } from 'react'
import { fetchEconomicsMonthly, type EconomicsMonthlyResponse } from '../api'

// LOCAL_TZ is the canonical timezone for the economics page: the month
// boundary and DAM hour numbering are always Europe/Kyiv, regardless of
// the operator's browser zone.
const LOCAL_TZ = 'Europe/Kyiv'

export type EconomicsMonthlyData = {
  month: EconomicsMonthlyResponse | null
  loading: boolean
  error: string | null
}

type Input = {
  organizationID: string
  // YYYY-MM month in LOCAL_TZ.
  month: string
  // refreshKey re-fires the fetch without changing inputs (e.g. after a
  // DAM-price refresh or a recompute). `undefined` → 0.
  refreshKey?: number
}

// useEconomicsMonthlyData reads the server-computed month rollup from the
// /economics/monthly endpoint. The backend serves final days from cache
// and recomputes the open tail (today) on read, so the dashboard always
// reflects a consistent month.
export function useEconomicsMonthlyData(input: Input): EconomicsMonthlyData {
  const active = Boolean(input.organizationID && input.month)
  const key = JSON.stringify([input.organizationID, input.month, input.refreshKey ?? 0])
  // The last answer and the request it settled; while the current key
  // has no answer it is loading and the previous month stays on screen.
  const [answer, setAnswer] = useState<{ key: string; month: EconomicsMonthlyResponse | null; error: string | null } | null>(
    null,
  )

  useEffect(() => {
    if (!active) return
    const controller = new AbortController()

    fetchEconomicsMonthly(
      { organizationID: input.organizationID, month: input.month, tz: LOCAL_TZ },
      controller.signal,
    )
      .then((resp) => {
        setAnswer({ key, month: resp, error: null })
      })
      .catch((err: unknown) => {
        if ((err as DOMException)?.name === 'AbortError') return
        const message =
          err instanceof Error
            ? err.message
            : typeof err === 'string'
              ? err
              : 'failed to load monthly economics data'
        setAnswer((prev) => ({ key, month: prev?.month ?? null, error: message }))
      })

    return () => controller.abort()
  }, [active, key, input.organizationID, input.month])

  return useMemo(() => {
    if (!active) return { month: null, loading: false, error: null }
    const loading = answer?.key !== key
    return { month: answer?.month ?? null, loading, error: loading ? null : (answer?.error ?? null) }
  }, [active, answer, key])
}
