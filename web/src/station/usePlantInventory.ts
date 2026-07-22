import { useEffect, useState } from 'react'
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
  const [data, setData] = useState<PlantInventory | null>(null)
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
    fetchPlantInventory(organizationID, ac.signal)
      .then((inv) => {
        setData(inv)
        setLoading(false)
      })
      .catch((e: unknown) => {
        if (isAbortError(e)) return
        setData(null)
        setLoading(false)
        setError(e instanceof Error ? e.message : 'Failed to load plant inventory')
      })
    return () => ac.abort()
  }, [organizationID])

  return { data, loading, error }
}
