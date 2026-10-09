import { useEffect, useMemo, useState } from 'react'
import { fetchPlantInventory } from '../api'
import type { PlantInventory } from '../types'

export type UsePlantInventoryResult = {
  data: PlantInventory | null
  loading: boolean
  error: string | null
}

function isAbortError(e: unknown): boolean {
  return e instanceof DOMException && e.name === 'AbortError'
}

export function usePlantInventory(organizationID: string): UsePlantInventoryResult {
  // The last answer and the organization it is for; until the current
  // one answers it is loading and the previous inventory stays shown.
  const [answer, setAnswer] = useState<{ org: string; data: PlantInventory | null; error: string | null } | null>(null)

  useEffect(() => {
    if (!organizationID) return
    const ac = new AbortController()
    fetchPlantInventory(organizationID, ac.signal)
      .then((inv) => {
        setAnswer({ org: organizationID, data: inv, error: null })
      })
      .catch((e: unknown) => {
        if (isAbortError(e)) return
        setAnswer({ org: organizationID, data: null, error: e instanceof Error ? e.message : 'Failed to load plant inventory' })
      })
    return () => ac.abort()
  }, [organizationID])

  return useMemo(() => {
    if (!organizationID) return { data: null, loading: false, error: null }
    const loading = answer?.org !== organizationID
    return { data: answer?.data ?? null, loading, error: loading ? null : (answer?.error ?? null) }
  }, [organizationID, answer])
}
