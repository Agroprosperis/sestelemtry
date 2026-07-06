import { useEffect, useState } from 'react'
import { fetchEconomicsPortfolio, type EconomicsPortfolioResponse } from '../api'

// LOCAL_TZ is the canonical timezone for the economics page: month/year
// boundaries are always Europe/Kyiv regardless of the operator's zone.
const LOCAL_TZ = 'Europe/Kyiv'

export type EconomicsPortfolioData = {
  portfolio: EconomicsPortfolioResponse | null
  loading: boolean
  error: string | null
}

type Input = {
  // active is false while another range is shown, keeping this hook idle.
  active: boolean
  scope: 'month' | 'year' | 'period'
  // month is YYYY-MM (scope=month); period is YYYY (scope=year); from/to
  // are both YYYY-MM (scope=period, a sliding window).
  month: string
  period: string
  from: string
  to: string
  refreshKey?: number
}

// useEconomicsPortfolioData reads the all-objects rollup from the
// /economics/portfolio endpoint. It stays idle unless active so toggling
// away from the portfolio view never fires a fan-out request.
export function useEconomicsPortfolioData(input: Input): EconomicsPortfolioData {
  const [data, setData] = useState<EconomicsPortfolioData>(() => ({
    portfolio: null,
    loading: true,
    error: null,
  }))

  useEffect(() => {
    const sel =
      input.scope === 'month'
        ? input.month
        : input.scope === 'period'
          ? input.from && input.to
          : input.period
    if (!input.active || !sel) {
      // Reset to idle when this view isn't active / has no period selected
      // (mirrors the sibling month/year hooks; this is deliberate state
      // synchronization, not a cascading-render bug).
      // eslint-disable-next-line react-hooks/set-state-in-effect
      setData({ portfolio: null, loading: false, error: null })
      return
    }
    const controller = new AbortController()
    setData((prev) => ({ ...prev, loading: true, error: null }))

    fetchEconomicsPortfolio(
      {
        month: input.scope === 'month' ? input.month : undefined,
        period: input.scope === 'year' ? input.period : undefined,
        from: input.scope === 'period' ? input.from : undefined,
        to: input.scope === 'period' ? input.to : undefined,
        tz: LOCAL_TZ,
      },
      controller.signal,
    )
      .then((resp) => {
        setData({ portfolio: resp, loading: false, error: null })
      })
      .catch((err: unknown) => {
        if ((err as DOMException)?.name === 'AbortError') return
        const message =
          err instanceof Error
            ? err.message
            : typeof err === 'string'
              ? err
              : 'failed to load portfolio economics data'
        setData((prev) => ({ ...prev, loading: false, error: message }))
      })

    return () => controller.abort()
  }, [input.active, input.scope, input.month, input.period, input.from, input.to, input.refreshKey])

  return data
}
