import { useEffect, useState } from 'react'
import { fetchRegisters } from '../../api'
import type { RegisterMeta } from '../../types'

// useRegistersWhenDebug lazy-fetches the metric_key -> Modbus
// register metadata map exactly once when debug mode first turns
// on. The underlying `fetchRegisters` is already module-memoized,
// so toggling debug off and on again hits the in-memory cache
// instead of the network. Until debug is on we don't request the
// map at all — production users never pay the latency cost.
export function useRegistersWhenDebug(debug: boolean): Record<string, RegisterMeta> | null {
  const [metadata, setMetadata] = useState<Record<string, RegisterMeta> | null>(null)

  useEffect(() => {
    if (!debug || metadata !== null) return
    const controller = new AbortController()
    let cancelled = false
    void (async () => {
      try {
        const res = await fetchRegisters(controller.signal)
        if (!cancelled) setMetadata(res.metadata)
      } catch {
        // Silent failure: missing register metadata only suppresses
        // the diagnostic suffix; cards still render normally.
      }
    })()
    return () => {
      cancelled = true
      controller.abort()
    }
  }, [debug, metadata])

  return metadata
}
