import { useEffect, useState } from 'react'
import { fetchDAMPrices } from '../../api'
import type { DAMPrice } from '../../types'
import { endOfPeriod, startOfPeriod, type RangePreset } from '../range'

function isAbortError(e: unknown): boolean {
  return e instanceof DOMException && e.name === 'AbortError'
}

function toDateString(date: Date): string {
  const y = date.getFullYear()
  const m = String(date.getMonth() + 1).padStart(2, '0')
  const d = String(date.getDate()).padStart(2, '0')
  return `${y}-${m}-${d}`
}

export type DAMPricesData = {
  prices: DAMPrice[]
  loading: boolean
  error: string | null
}

// useDAMPrices fetches DAM hourly prices for the period defined by the
// dashboard preset and anchor: a single delivery_date for `day`, the
// anchored month/year for `month`/`year`. The same DAM data feed is reused
// for the period summaries.
export function useDAMPrices(input: { preset: RangePreset; anchor: Date; zone?: number }): DAMPricesData {
  const { preset, zone } = input
  const anchorTime = input.anchor.getTime()
  const [prices, setPrices] = useState<DAMPrice[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    const controller = new AbortController()
    let cancelled = false

    async function load() {
      setLoading(true)
      try {
        const anchor = new Date(anchorTime)
        const start = startOfPeriod(preset, anchor)
        const end = endOfPeriod(preset, anchor)
        // endOfPeriod is exclusive; the API expects an inclusive upper bound.
        const lastDay = new Date(end)
        lastDay.setDate(lastDay.getDate() - 1)
        const resp = await fetchDAMPrices(
          { from: toDateString(start), to: toDateString(lastDay), zone },
          controller.signal,
        )
        if (cancelled) return
        setPrices(resp.prices)
        setError(null)
      } catch (e) {
        if (cancelled || isAbortError(e)) return
        setError(e instanceof Error ? e.message : 'Failed to load DAM prices')
      } finally {
        if (!cancelled) setLoading(false)
      }
    }
    void load()

    return () => {
      cancelled = true
      controller.abort()
    }
  }, [preset, anchorTime, zone])

  return { prices, loading, error }
}
