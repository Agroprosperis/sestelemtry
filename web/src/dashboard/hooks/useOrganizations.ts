import { useEffect, useState } from 'react'
import { fetchOrganizations } from '../../api'
import type { OrganizationInfo } from '../../types'

export type UseOrganizationsResult = {
  data: OrganizationInfo[]
  loading: boolean
  error: string | null
}

// useOrganizations loads the public org metadata from
// /api/v1/organizations. The underlying fetch is module-cached (see
// `fetchOrganizations`), so multiple components sharing this hook
// trigger at most one network request per page load.
//
// We deliberately do NOT pass an AbortSignal into the cached fetch —
// the response is shared across every consumer, so abort-on-unmount
// from one mount (e.g. React 19 StrictMode's mount/unmount/mount
// double-invoke) would also kill the in-flight request for the
// remount, leaving every subscriber stuck on `loading: true` forever.
// `cancelled` still gates `setState` so an unmounted instance doesn't
// log a state-update warning. The payload is tiny and idempotent, so
// letting the request finish in the background is the right tradeoff.
//
// Failure mode: any error returns `error` + an empty `data` so the
// caller (e.g. the weather card) can hide itself silently rather
// than blowing up the dashboard.
export function useOrganizations(): UseOrganizationsResult {
  const [state, setState] = useState<UseOrganizationsResult>({
    data: [],
    loading: true,
    error: null,
  })

  useEffect(() => {
    let cancelled = false
    void (async () => {
      try {
        const resp = await fetchOrganizations()
        if (cancelled) return
        setState({ data: resp.organizations ?? [], loading: false, error: null })
      } catch (e) {
        if (cancelled) return
        setState({
          data: [],
          loading: false,
          error: e instanceof Error ? e.message : 'Failed to load organizations',
        })
      }
    })()
    return () => {
      cancelled = true
    }
  }, [])

  return state
}
