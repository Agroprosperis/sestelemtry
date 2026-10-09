import { useEffect, useMemo, useState } from 'react'
import { fetchEconomicsAnnual, type EconomicsAnnualResponse } from '../api'

// LOCAL_TZ is the canonical timezone for the economics page: the year
// boundary and DAM hour numbering are always Europe/Kyiv, regardless of
// the operator's browser zone.
const LOCAL_TZ = 'Europe/Kyiv'

export type EconomicsAnnualData = {
  year: EconomicsAnnualResponse | null
  loading: boolean
  error: string | null
}

type Input = {
  organizationID: string
  // YYYY calendar year in LOCAL_TZ. Used when from/to are empty.
  period: string
  // Optional sliding window (both YYYY-MM); when set they override period.
  from?: string
  to?: string
  // refreshKey re-fires the fetch without changing inputs (e.g. after a
  // DAM-price refresh or a recompute). `undefined` → 0.
  refreshKey?: number
}

// useEconomicsAnnualData reads the server-computed year rollup from the
// /economics/annual endpoint. Mirrors useEconomicsMonthlyData: it stays
// idle when organizationID/period are empty so toggling Day/Month/Year
// never hits more than one period endpoint at once.
export function useEconomicsAnnualData(input: Input): EconomicsAnnualData {
  const useWindow = Boolean(input.from && input.to)
  const active = Boolean(input.organizationID && (input.period || useWindow))
  const key = JSON.stringify([input.organizationID, input.period, input.from, input.to, input.refreshKey ?? 0])
  // The last answer and the request it settled; while the current key
  // has no answer it is loading and the previous year stays on screen.
  const [answer, setAnswer] = useState<{ key: string; year: EconomicsAnnualResponse | null; error: string | null } | null>(
    null,
  )

  useEffect(() => {
    if (!active) return
    const controller = new AbortController()

    fetchEconomicsAnnual(
      {
        organizationID: input.organizationID,
        period: useWindow ? undefined : input.period,
        from: useWindow ? input.from : undefined,
        to: useWindow ? input.to : undefined,
        tz: LOCAL_TZ,
      },
      controller.signal,
    )
      .then((resp) => {
        setAnswer({ key, year: resp, error: null })
      })
      .catch((err: unknown) => {
        if ((err as DOMException)?.name === 'AbortError') return
        const message =
          err instanceof Error
            ? err.message
            : typeof err === 'string'
              ? err
              : 'failed to load annual economics data'
        setAnswer((prev) => ({ key, year: prev?.year ?? null, error: message }))
      })

    return () => controller.abort()
  }, [active, key, input.organizationID, input.period, input.from, input.to, useWindow])

  return useMemo(() => {
    if (!active) return { year: null, loading: false, error: null }
    const loading = answer?.key !== key
    return { year: answer?.year ?? null, loading, error: loading ? null : (answer?.error ?? null) }
  }, [active, answer, key])
}
