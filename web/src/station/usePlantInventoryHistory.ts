import { useEffect, useMemo, useState } from 'react'
import { fetchPlantInventoryHistory } from '../api'
import type { PlantInventoryHistory } from '../types'

export type UsePlantInventoryHistoryResult = {
  data: PlantInventoryHistory | null
  loading: boolean
  error: string | null
}

function isAbortError(e: unknown): boolean {
  return e instanceof DOMException && e.name === 'AbortError'
}

export function usePlantInventoryHistory(
  organizationID: string,
): UsePlantInventoryHistoryResult {
  // The last answer and the organization it is for; until the current
  // one answers it is loading and the previous history stays shown.
  const [answer, setAnswer] = useState<{ org: string; data: PlantInventoryHistory | null; error: string | null } | null>(
    null,
  )

  useEffect(() => {
    if (!organizationID) return
    const ac = new AbortController()
    fetchPlantInventoryHistory(organizationID, { signal: ac.signal })
      .then((hist) => {
        setAnswer({ org: organizationID, data: hist, error: null })
      })
      .catch((e: unknown) => {
        if (isAbortError(e)) return
        setAnswer({ org: organizationID, data: null, error: e instanceof Error ? e.message : 'Failed to load inventory history' })
      })
    return () => ac.abort()
  }, [organizationID])

  return useMemo(() => {
    if (!organizationID) return { data: null, loading: false, error: null }
    const loading = answer?.org !== organizationID
    return { data: answer?.data ?? null, loading, error: loading ? null : (answer?.error ?? null) }
  }, [organizationID, answer])
}
