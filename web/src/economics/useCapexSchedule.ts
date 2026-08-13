import { useEffect, useState } from 'react'
import { fetchTariffSchedule } from './orgTariffsClient'
import type { CapexStep } from './payback'

// useCapexSchedule pulls the dated CAPEX out of the org's tariff
// schedule for the payback page. The schedule stores a whole tariff
// bundle per effective date, so a staged investment (an extra УЗЕ pack,
// more panels) is already versioned there — this hook just narrows the
// versions down to the CAPEX column.
//
// Versions with no CAPEX filled in are dropped rather than treated as
// zero: the payback page then falls back to the single value from the
// tariff form, which is how every org looked before staging existed.
// A failed read is silent for the same reason — the page stays usable
// on the flat CAPEX instead of showing an error over a whole report.
export function useCapexSchedule(organizationID: string, refreshKey = 0): CapexStep[] {
  const [steps, setSteps] = useState<CapexStep[]>([])

  // Every setState runs inside the async worker (not the effect body)
  // to stay clear of the React 19 set-state-in-effect lint, matching
  // useOrgTariffs.
  useEffect(() => {
    const controller = new AbortController()
    let cancelled = false
    void (async () => {
      if (!organizationID) {
        setSteps([])
        return
      }
      try {
        const versions = await fetchTariffSchedule(organizationID, controller.signal)
        if (cancelled) return
        setSteps(
          versions
            .filter((v) => Number.isFinite(v.tariffs.capexUah) && v.tariffs.capexUah > 0)
            .map((v) => ({ effectiveFrom: v.effectiveFrom, capexUah: v.tariffs.capexUah })),
        )
      } catch {
        if (cancelled) return
        setSteps([])
      }
    })()
    return () => {
      cancelled = true
      controller.abort()
    }
  }, [organizationID, refreshKey])

  return steps
}
