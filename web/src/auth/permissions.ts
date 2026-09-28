// Access model mirrored from internal/auth: roles are fixed bundles of
// permissions, and a grant gives a role on one organization or on all
// of them. The API enforces every rule; the UI only uses these to avoid
// offering what the API would refuse.

export type Permission =
  | 'analytics.day'
  | 'analytics.full'
  | 'economics.read'
  | 'economics.write'
  | 'control'
  | 'technical.write'
  | 'service'

export type Role = 'admin' | 'economist' | 'engineer' | 'control_engineer'

export const ROLES: { id: Role; label: string; hint: string }[] = [
  { id: 'admin', label: 'Адміністратор', hint: 'усі розділи й налаштування' },
  { id: 'economist', label: 'Економіст', hint: 'економіка, тарифи, перерахунок' },
  { id: 'engineer', label: 'Інженер', hint: 'денний енергетичний графік' },
  {
    id: 'control_engineer',
    label: 'Інженер-керування',
    hint: 'денний графік, керування, технічні ліміти',
  },
]

export function roleLabel(role: Role): string {
  return ROLES.find((r) => r.id === role)?.label ?? role
}

// A null organization_id grants the role on every organization.
export type AuthGrant = { role: Role; organization_id: string | null }

export type AuthOrganization = { id: string; name: string; permissions: Permission[] }

export type AuthMe = {
  user: { id: number; email: string; name: string }
  global_admin: boolean
  // The factory admin/admin: nothing opens until the password changes.
  must_change_password: boolean
  grants: AuthGrant[]
  organizations: AuthOrganization[]
}

export type AppView = 'dashboard' | 'economics' | 'control' | 'station' | 'import' | 'alerts' | 'users'

// The first view a user may open is where they land.
const VIEW_ORDER: AppView[] = ['dashboard', 'economics', 'control', 'station', 'import', 'alerts', 'users']

const VIEW_PERMISSION: Record<Exclude<AppView, 'users'>, Permission> = {
  dashboard: 'analytics.day',
  economics: 'economics.read',
  control: 'control',
  station: 'service',
  import: 'service',
  alerts: 'service',
}

export type Access = {
  // restricted is false when the page renders outside the signed-in
  // app (component tests): every check passes then.
  restricted: boolean
  globalAdmin: boolean
  can: (permission: Permission, organizationID: string) => boolean
  // organizations lists the ids the user holds permission on, in config
  // order; null means no restriction.
  organizations: (permission: Permission) => string[] | null
  canView: (view: AppView) => boolean
}

export const UNRESTRICTED: Access = {
  restricted: false,
  globalAdmin: true,
  can: () => true,
  organizations: () => null,
  canView: () => true,
}

// Edge shadow telemetry ("<site>-edge", the API's default
// EDGE_ORG_SUFFIX) is readable with the site's own grant.
const EDGE_SHADOW_SUFFIX = '-edge'

export function shadowSite(organizationID: string): string | null {
  if (!organizationID.endsWith(EDGE_SHADOW_SUFFIX)) return null
  const site = organizationID.slice(0, -EDGE_SHADOW_SUFFIX.length)
  return site || null
}

export function accessFor(me: AuthMe): Access {
  const byOrg = new Map(me.organizations.map((o) => [o.id, new Set(o.permissions)]))
  const can = (permission: Permission, organizationID: string) => {
    const site = shadowSite(organizationID)
    return (
      (byOrg.get(organizationID)?.has(permission) ?? false) ||
      (site !== null && (byOrg.get(site)?.has(permission) ?? false))
    )
  }
  const organizations = (permission: Permission) =>
    me.organizations.filter((o) => o.permissions.includes(permission)).map((o) => o.id)
  const canView = (view: AppView) =>
    view === 'users' ? me.global_admin : organizations(VIEW_PERMISSION[view]).length > 0
  return { restricted: true, globalAdmin: me.global_admin, can, organizations, canView }
}

export function firstAllowedView(access: Access): AppView | null {
  return VIEW_ORDER.find((v) => access.canView(v)) ?? null
}
