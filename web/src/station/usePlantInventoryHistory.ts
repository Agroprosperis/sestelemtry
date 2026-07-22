import { useEffect, useState } from 'react'
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
  const [data, setData] = useState<PlantInventoryHistory | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    if (!organizationID) {
      setData(null)
      setLoading(false)
      setError(null)
      return
    }
    const ac = new AbortController()
    setLoading(true)
    setError(null)
    fetchPlantInventoryHistory(organizationID, { signal: ac.signal })
      .then((hist) => {
        setData(hist)
        setLoading(false)
      })
      .catch((e: unknown) => {
        if (isAbortError(e)) return
        setData(null)
        setLoading(false)
        setError(e instanceof Error ? e.message : 'Failed to load inventory history')
      })
    return () => ac.abort()
  }, [organizationID])

  return { data, loading, error }
}
