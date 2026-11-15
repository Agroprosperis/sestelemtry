import { useEffect, useState } from 'react'
import { fetchEconomicsMonthly } from '../api'

const LOCAL_TZ = 'Europe/Kyiv'

// recentDays is how far back the average load reaches, date included.
const recentDays = 30

function shiftDate(date: string, days: number): string {
  const [y, m, d] = date.split('-').map(Number)
  const t = new Date(Date.UTC(y, m - 1, d + days))
  return t.toISOString().slice(0, 10)
}

// useRecentAverageLoadKw is the organization's average load (kW) over the
// 30 days up to date, rounded to 10 kW, from the stored month rollups:
// the load of the tariff form's fuel example. null until known or when
// those days hold no data.
export function useRecentAverageLoadKw(organizationID: string, date: string): number | null {
  const [state, setState] = useState<{ key: string; kw: number | null } | null>(null)
  const key = `${organizationID}|${date}`

  useEffect(() => {
    if (!organizationID || !/^\d{4}-\d{2}-\d{2}$/.test(date)) return
    const controller = new AbortController()
    const from = shiftDate(date, -(recentDays - 1))
    const months = Array.from(new Set([from.slice(0, 7), date.slice(0, 7)]))
    Promise.all(
      months.map((month) =>
        fetchEconomicsMonthly({ organizationID, month, tz: LOCAL_TZ }, controller.signal),
      ),
    )
      .then((responses) => {
        let kwh = 0
        let hours = 0
        for (const r of responses) {
          for (const d of r.days) {
            if (d.date < from || d.date > date) continue
            kwh += d.load_kwh
            hours += d.hours_with_data
          }
        }
        const kw = hours > 0 ? Math.round(kwh / hours / 10) * 10 : null
        setState({ key, kw: kw && kw > 0 ? kw : null })
      })
      .catch((err: unknown) => {
        if ((err as DOMException)?.name === 'AbortError') return
        setState({ key, kw: null })
      })
    return () => controller.abort()
  }, [organizationID, date, key])

  return state && state.key === key ? state.kw : null
}
