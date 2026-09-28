import { ROLES, type AuthGrant, type Role } from '../auth/permissions'

// RoleScope is how the form edits one role: not granted, granted on
// every organization, or on the listed ones.
export type RoleScope = { mode: 'none' | 'all' | 'some'; organizations: string[] }

export type RoleScopes = Record<Role, RoleScope>

export function scopesFromGrants(grants: AuthGrant[]): RoleScopes {
  const out = {} as RoleScopes
  for (const { id } of ROLES) {
    const mine = grants.filter((g) => g.role === id)
    if (mine.some((g) => g.organization_id === null)) {
      out[id] = { mode: 'all', organizations: [] }
    } else if (mine.length > 0) {
      out[id] = { mode: 'some', organizations: mine.map((g) => g.organization_id as string) }
    } else {
      out[id] = { mode: 'none', organizations: [] }
    }
  }
  return out
}

export function grantsFromScopes(scopes: RoleScopes): AuthGrant[] {
  const out: AuthGrant[] = []
  for (const { id } of ROLES) {
    const scope = scopes[id]
    if (scope.mode === 'all') out.push({ role: id, organization_id: null })
    if (scope.mode === 'some') {
      for (const org of scope.organizations) out.push({ role: id, organization_id: org })
    }
  }
  return out
}

// grantSummary renders one line per role, e.g.
// "Економіст: Жмеринський елеватор, Радивилівський елеватор".
export function grantSummary(grants: AuthGrant[], orgName: (id: string) => string): string[] {
  const scopes = scopesFromGrants(grants)
  const lines: string[] = []
  for (const role of ROLES) {
    const scope = scopes[role.id]
    if (scope.mode === 'all') lines.push(`${role.label}: усі обʼєкти`)
    if (scope.mode === 'some') lines.push(`${role.label}: ${scope.organizations.map(orgName).join(', ')}`)
  }
  return lines
}
