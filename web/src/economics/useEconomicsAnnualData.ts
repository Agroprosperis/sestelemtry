import { useEffect, useState } from 'react'
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
  const [data, setData] = useState<EconomicsAnnualData>(() => ({
    year: null,
    loading: true,
    error: null,
  }))

  const useWindow = Boolean(input.from && input.to)
  useEffect(() => {
    if (!input.organizationID || (!input.period && !useWindow)) {
      setData({ year: null, loading: false, error: null })
      return
    }
    const controller = new AbortController()
    setData((prev) => ({ ...prev, loading: true, error: null }))

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
        setData({ year: resp, loading: false, error: null })
      })
      .catch((err: unknown) => {
        if ((err as DOMException)?.name === 'AbortError') return
        const message =
          err instanceof Error
            ? err.message
            : typeof err === 'string'
              ? err
              : 'failed to load annual economics data'
        setData((prev) => ({ ...prev, loading: false, error: message }))
      })

    return () => controller.abort()
  }, [input.organizationID, input.period, input.from, input.to, useWindow, input.refreshKey])

  return data
}
