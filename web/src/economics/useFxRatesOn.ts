import { useEffect, useMemo, useState } from 'react'
import { fetchFxRates } from '../api'
import type { Currency } from './currency'

// FxRatesOn answers "UAH per unit on this day" for the handful of dates
// the CAPEX was invested on. ready is false until every asked day has an
// answer or the request failed.
export type FxRatesOn = {
  ready: boolean
  rateOn: (day: string) => number | null
}

function kyivToday(): string {
  return new Intl.DateTimeFormat('en-CA', {
    timeZone: 'Europe/Kyiv',
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
  }).format(new Date())
}

// useFxRatesOn fetches one rate per asked YYYY-MM-DD day. A day after
// today has no rate yet and takes today's. Idle (ready, no rates) in UAH.
export function useFxRatesOn(currency: Currency, days: string[], refreshKey = 0): FxRatesOn {
  const wantedKey = [...new Set(days.filter((d) => /^\d{4}-\d{2}-\d{2}$/.test(d)))].sort().join(',')
  const active = currency !== 'UAH' && wantedKey !== ''
  const key = `${currency}|${wantedKey}|${refreshKey}`
  const [answer, setAnswer] = useState<{ key: string; rates: Record<string, number> } | null>(null)

  useEffect(() => {
    if (!active) return
    const controller = new AbortController()
    const today = kyivToday()
    Promise.all(
      wantedKey.split(',').map(async (day) => {
        const asked = day > today ? today : day
        const resp = await fetchFxRates({ currency, from: asked, to: asked }, controller.signal)
        const rate = resp.days.find((d) => d.date === asked)?.uah_per_unit
        return [day, rate] as const
      }),
    )
      .then((pairs) => {
        const rates: Record<string, number> = {}
        for (const [day, rate] of pairs) if (rate && rate > 0) rates[day] = rate
        setAnswer({ key, rates })
      })
      .catch((err: unknown) => {
        if ((err as DOMException)?.name === 'AbortError') return
        setAnswer({ key, rates: {} })
      })
    return () => controller.abort()
  }, [active, key, wantedKey, currency])

  return useMemo(() => {
    if (!active) return { ready: true, rateOn: () => null }
    const rates = answer?.key === key ? answer.rates : null
    return { ready: rates !== null, rateOn: (day: string) => rates?.[day] ?? null }
  }, [active, answer, key])
}
