import { useCallback, useEffect, useMemo, useState } from 'react'
import { useAccess } from '../../auth/authContext'
import { shadowSite, type Permission } from '../../auth/permissions'
import { KNOWN_ORGANIZATIONS } from '../config'

// initialOrganization reads ?organization_id= once. When the choice is
// restricted, an object outside `allowed` (a stale link, another user's
// bookmark) falls back to the first allowed one.
function initialOrganization(allowed: string[] | null): string {
  const requested = new URLSearchParams(window.location.search).get('organization_id')
  if (!allowed) return requested || 'demo-org'
  const site = requested ? shadowSite(requested) : null
  if (requested && (allowed.includes(requested) || (site !== null && allowed.includes(site)))) {
    return requested
  }
  return allowed[0] ?? ''
}

// useOrganizationParam owns the page's object, synced with
// ?organization_id=. With a permission, the options are the objects the
// signed-in user holds it on.
export function useOrganizationParam(permission?: Permission) {
  const access = useAccess()
  const allowed = permission ? access.organizations(permission) : null
  const allowedKey = allowed?.join(',') ?? null
  const restricted = allowed !== null

  const [organizationID, setOrganizationID] = useState(() => initialOrganization(allowed))

  // Keep a fallen-back object in the URL, so a shared link opens what
  // the page actually shows.
  useEffect(() => {
    if (!restricted || !organizationID) return
    const url = new URL(window.location.href)
    if (url.searchParams.get('organization_id') === organizationID) return
    url.searchParams.set('organization_id', organizationID)
    window.history.replaceState({}, '', url)
  }, [restricted, organizationID])

  const options = useMemo(() => {
    const list = allowedKey === null ? KNOWN_ORGANIZATIONS : allowedKey ? allowedKey.split(',') : []
    if (!organizationID || list.includes(organizationID)) {
      return list
    }
    return [organizationID, ...list]
  }, [organizationID, allowedKey])

  const change = useCallback((nextID: string) => {
    setOrganizationID(nextID)
    const url = new URL(window.location.href)
    url.searchParams.set('organization_id', nextID)
    window.history.replaceState({}, '', url)
  }, [])

  return { organizationID, options, change }
}
