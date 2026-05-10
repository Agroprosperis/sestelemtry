import { useEffect, useState } from 'react'
import { fetchOrganizations } from '../../api'
import type { OrganizationInfo } from '../../types'

function isAbortError(e: unknown): boolean {
  return e instanceof DOMException && e.name === 'AbortError'
}

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
    const controller = new AbortController()
    void (async () => {
      try {
        const resp = await fetchOrganizations(controller.signal)
        if (cancelled) return
        setState({ data: resp.organizations ?? [], loading: false, error: null })
      } catch (e) {
        if (cancelled || isAbortError(e)) return
        setState({
          data: [],
          loading: false,
          error: e instanceof Error ? e.message : 'Failed to load organizations',
        })
      }
    })()
    return () => {
      cancelled = true
      controller.abort()
    }
  }, [])

  return state
}
