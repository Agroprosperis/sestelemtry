import { useCallback, useMemo, useState } from 'react'
import { KNOWN_ORGANIZATIONS } from '../config'

export function useOrganizationParam() {
  const initial = useMemo(() => {
    const search = new URLSearchParams(window.location.search)
    return search.get('organization_id') || 'demo-org'
  }, [])

  const [organizationID, setOrganizationID] = useState(initial)

  const options = useMemo(() => {
    if (KNOWN_ORGANIZATIONS.includes(organizationID)) {
      return KNOWN_ORGANIZATIONS
    }
    return [organizationID, ...KNOWN_ORGANIZATIONS]
  }, [organizationID])

  const change = useCallback((nextID: string) => {
    setOrganizationID(nextID)
    const url = new URL(window.location.href)
    url.searchParams.set('organization_id', nextID)
    window.history.replaceState({}, '', url)
  }, [])

  return { organizationID, options, change }
}
