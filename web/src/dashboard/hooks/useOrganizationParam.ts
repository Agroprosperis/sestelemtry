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

// allowedOrganizations is the union of the objects the user holds any of
// permissions on, in the order the first permission lists them; null
// when the access isn't restricted.
function allowedOrganizations(
  organizations: (permission: Permission) => string[] | null,
  permissions: Permission[],
): string[] | null {
  const out: string[] = []
  for (const p of permissions) {
    const ids = organizations(p)
    if (ids === null) return null
    for (const id of ids) if (!out.includes(id)) out.push(id)
  }
  return out
}

// useOrganizationParam owns the page's object, synced with
// ?organization_id=. With permissions, the options are the objects the
// signed-in user holds any of them on.
export function useOrganizationParam(permission?: Permission | Permission[]) {
  const access = useAccess()
  const permissions = permission === undefined ? [] : Array.isArray(permission) ? permission : [permission]
  const allowed = permissions.length > 0 ? allowedOrganizations(access.organizations, permissions) : null
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
