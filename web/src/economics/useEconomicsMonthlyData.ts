import { useEffect, useState } from 'react'
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
  const [data, setData] = useState<EconomicsMonthlyData>(() => ({
    month: null,
    loading: true,
    error: null,
  }))

  useEffect(() => {
    if (!input.organizationID || !input.month) {
      setData({ month: null, loading: false, error: null })
      return
    }
    const controller = new AbortController()
    setData((prev) => ({ ...prev, loading: true, error: null }))

    fetchEconomicsMonthly(
      { organizationID: input.organizationID, month: input.month, tz: LOCAL_TZ },
      controller.signal,
    )
      .then((resp) => {
        setData({ month: resp, loading: false, error: null })
      })
      .catch((err: unknown) => {
        if ((err as DOMException)?.name === 'AbortError') return
        const message =
          err instanceof Error
            ? err.message
            : typeof err === 'string'
              ? err
              : 'failed to load monthly economics data'
        setData((prev) => ({ ...prev, loading: false, error: message }))
      })

    return () => controller.abort()
  }, [input.organizationID, input.month, input.refreshKey])

  return data
}
