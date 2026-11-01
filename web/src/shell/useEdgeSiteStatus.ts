import { useEffect, useState } from 'react'
import { fetchEdgeFleet, type EdgeSiteStatus } from '../control/controlClient'

const FLEET_POLL_MS = 60_000

// useEdgeSiteStatus polls whether the selected object's edge device is
// online. Pages that already poll the fleet pass that status instead.
export function useEdgeSiteStatus(organizationID: string, enabled: boolean): EdgeSiteStatus | null {
  const [status, setStatus] = useState<EdgeSiteStatus | null>(null)

  useEffect(() => {
    if (!enabled || !organizationID) return
    let cancelled = false
    const load = () =>
      fetchEdgeFleet()
        .then((f) => {
          if (cancelled) return
          setStatus((f.sites ?? []).find((s) => s.site_id === organizationID) ?? null)
        })
        .catch(() => !cancelled && setStatus(null))
    void load()
    const id = window.setInterval(load, FLEET_POLL_MS)
    return () => {
      cancelled = true
      window.clearInterval(id)
    }
  }, [organizationID, enabled])

  return enabled ? status : null
}
