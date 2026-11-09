import { useCallback, useEffect, useMemo, useState } from 'react'
import { useAccess } from '../../auth/authContext'
import { shadowSite, type Permission } from '../../auth/permissions'
import { ECONOMIC_GROUPS, KNOWN_ORGANIZATIONS } from '../config'

function groupOf(organizationID: string): string | null {
  for (const [group, members] of Object.entries(ECONOMIC_GROUPS)) {
    if (members.includes(organizationID)) return group
  }
  return null
}

// withGroups puts each economic group in its members' place, where the
// first of them stood, once every member is on the list.
export function withGroups(list: readonly string[]): string[] {
  const out: string[] = []
  for (const id of list) {
    const group = groupOf(id)
    if (group === null || !ECONOMIC_GROUPS[group].every((m) => list.includes(m))) {
      out.push(id)
    } else if (!out.includes(group)) {
      out.push(group)
    }
  }
  return out
}

// linkedOrganization reads ?organization_id=. A page that shows groups
// opens a link to a member as its group; any other page opens a link to a
// group as the group's first member.
function linkedOrganization(groups: boolean, allowed: string[] | null): string | null {
  const id = new URLSearchParams(window.location.search).get('organization_id')
  if (id === null) return null
  if (!groups) return ECONOMIC_GROUPS[id]?.[0] ?? id
  const group = groupOf(id)
  return group !== null && (allowed === null || allowed.includes(group)) ? group : id
}

// initialOrganization reads ?organization_id= once. When the choice is
// restricted, an object outside `allowed` (a stale link, another user's
// bookmark) falls back to the first allowed one.
function initialOrganization(allowed: string[] | null, groups: boolean): string {
  const requested = linkedOrganization(groups, allowed)
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
// signed-in user holds any of them on. With groups (the economics pages),
// an economic group stands in for its members.
export function useOrganizationParam(permission?: Permission | Permission[], opts: { groups?: boolean } = {}) {
  const groups = opts.groups ?? false
  const access = useAccess()
  const permissions = permission === undefined ? [] : Array.isArray(permission) ? permission : [permission]
  const sites = permissions.length > 0 ? allowedOrganizations(access.organizations, permissions) : null
  const allowed = sites !== null && groups ? withGroups(sites) : sites
  const allowedKey = allowed?.join(',') ?? null
  const restricted = allowed !== null

  const [organizationID, setOrganizationID] = useState(() => initialOrganization(allowed, groups))

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
    let list: string[] = allowedKey ? allowedKey.split(',') : []
    if (allowedKey === null) list = groups ? withGroups(KNOWN_ORGANIZATIONS) : KNOWN_ORGANIZATIONS
    if (!organizationID || list.includes(organizationID)) {
      return list
    }
    return [organizationID, ...list]
  }, [organizationID, allowedKey, groups])

  const change = useCallback((nextID: string) => {
    setOrganizationID(nextID)
    const url = new URL(window.location.href)
    url.searchParams.set('organization_id', nextID)
    window.history.replaceState({}, '', url)
  }, [])

  return { organizationID, options, change }
}
