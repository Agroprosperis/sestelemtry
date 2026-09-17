import { useEffect, useState } from 'react'
import { fetchPvPlanSummary } from '../api'
import type { EconomicsPvPlan } from './components/EconomicsTopSection'

// LOCAL_TZ matches the rest of the economics page: period boundaries
// are civil days in Ukraine, not the operator's browser zone.
const LOCAL_TZ = 'Europe/Kyiv'

type Input = {
  organizationID: string
  // Inclusive civil-day span of the visible period (YYYY-MM-DD both).
  // Empty strings keep the hook idle.
  fromDay: string
  toDay: string
  refreshKey?: number
}

// usePvPlanSummary loads the planned PV generation for the period shown
// by the «Генерація СЕС» card. Best-effort by design: a missing plan
// (unsupported org, future period, upstream failure) only hides the
// plan line, so errors resolve to null instead of an error channel.
export function usePvPlanSummary(input: Input): EconomicsPvPlan | null {
  const [plan, setPlan] = useState<EconomicsPvPlan | null>(null)

  useEffect(() => {
    const controller = new AbortController()
    let cancelled = false
    void (async () => {
      if (!input.organizationID || !input.fromDay || !input.toDay) {
        setPlan(null)
        return
      }
      try {
        // Noon-UTC anchors always fall inside the intended Kyiv civil
        // day regardless of DST, so the server's civilDaySpan resolves
        // the window to exactly [fromDay..toDay].
        const resp = await fetchPvPlanSummary(
          {
            organizationID: input.organizationID,
            from: `${input.fromDay}T12:00:00Z`,
            to: `${input.toDay}T12:00:00Z`,
            tz: LOCAL_TZ,
          },
          controller.signal,
        )
        if (cancelled) return
        setPlan(
          resp.supported && resp.planned_kwh > 0
            ? {
                plannedKwh: resp.planned_kwh,
                daysCovered: resp.days_covered,
                daysExpected: resp.days_expected,
              }
            : null,
        )
      } catch {
        if (cancelled) return
        setPlan(null)
      }
    })()
    return () => {
      cancelled = true
      controller.abort()
    }
  }, [input.organizationID, input.fromDay, input.toDay, input.refreshKey])

  return plan
}
