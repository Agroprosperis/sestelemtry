import { useEffect, useState } from 'react'
import { fetchCurrent } from '../../api'
import type { CurrentResponse } from '../../types'
import { GRID_VOLTAGE_KEYS, gridVoltageFrom, type GridVoltage } from '../transforms/gridVoltage'

const POLL_MS = 15_000

// EDGE_ORG_SUFFIX names the shadow organization the on-site edge
// controller reports under (EDGE_ORG_SUFFIX on the API, "-edge" by
// default); its meter reads carry the grid voltage the VM collector
// does not poll.
const EDGE_ORG_SUFFIX = '-edge'

// useGridVoltage polls the grid voltage for the «Мережа» node. It asks
// the site and its edge shadow at once and keeps whichever has a fresh
// reading; a site that reports none (or a shadow the user may not read)
// just yields null.
export function useGridVoltage(organizationID: string, enabled: boolean): GridVoltage | null {
  const [voltage, setVoltage] = useState<GridVoltage | null>(null)

  useEffect(() => {
    let cancelled = false
    let inflight: AbortController | null = null
    // A site without an edge controller has no shadow to read (the API
    // refuses it); stop asking after the first refusal.
    let askEdge = true

    async function tick() {
      if (cancelled) return
      if (!enabled || !organizationID) {
        setVoltage(null)
        return
      }
      if (document.visibilityState === 'hidden') return
      if (inflight) inflight.abort()
      const controller = new AbortController()
      inflight = controller
      const orgs = askEdge ? [organizationID, organizationID + EDGE_ORG_SUFFIX] : [organizationID]
      const results = await Promise.allSettled(
        orgs.map((org) => fetchCurrent({ organizationID: org, metricKeys: GRID_VOLTAGE_KEYS }, controller.signal)),
      )
      if (cancelled || controller.signal.aborted) return
      if (askEdge && results[1]?.status === 'rejected') askEdge = false
      const snapshots: Array<CurrentResponse | null> = results.map((r) => (r.status === 'fulfilled' ? r.value : null))
      setVoltage(gridVoltageFrom(snapshots, Date.now()))
    }

    void tick()
    const timer = window.setInterval(() => void tick(), POLL_MS)
    return () => {
      cancelled = true
      if (inflight) inflight.abort()
      window.clearInterval(timer)
    }
  }, [organizationID, enabled])

  return enabled ? voltage : null
}
