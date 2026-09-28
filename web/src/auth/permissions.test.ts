import { describe, expect, it } from 'vitest'
import { accessFor, firstAllowedView, UNRESTRICTED, type AuthMe } from './permissions'

function me(overrides: Partial<AuthMe>): AuthMe {
  return {
    user: { id: 1, email: 'u@example.com', name: '' },
    global_admin: false,
    must_change_password: false,
    grants: [],
    organizations: [],
    ...overrides,
  }
}

describe('accessFor', () => {
  const mixed = accessFor(
    me({
      organizations: [
        { id: 'ze', name: 'ZE', permissions: ['economics.read', 'economics.write'] },
        { id: 'pe', name: 'PE', permissions: ['analytics.day'] },
      ],
    }),
  )

  it('answers per organization', () => {
    expect(mixed.can('economics.read', 'ze')).toBe(true)
    expect(mixed.can('economics.read', 'pe')).toBe(false)
    expect(mixed.can('analytics.day', 'pe')).toBe(true)
    expect(mixed.organizations('analytics.day')).toEqual(['pe'])
  })

  it('lets a site grant read the edge shadow telemetry', () => {
    expect(mixed.can('analytics.day', 'pe-edge')).toBe(true)
    expect(mixed.can('analytics.day', 'ze-edge')).toBe(false)
  })

  it('opens only the views some organization allows', () => {
    expect(mixed.canView('dashboard')).toBe(true)
    expect(mixed.canView('economics')).toBe(true)
    expect(mixed.canView('control')).toBe(false)
    expect(mixed.canView('alerts')).toBe(false)
    expect(mixed.canView('users')).toBe(false)
    expect(firstAllowedView(mixed)).toBe('dashboard')
  })

  it('lands an economist on economics and a role-less user nowhere', () => {
    const economist = accessFor(
      me({ organizations: [{ id: 'ze', name: 'ZE', permissions: ['economics.read', 'economics.write'] }] }),
    )
    expect(firstAllowedView(economist)).toBe('economics')
    expect(firstAllowedView(accessFor(me({})))).toBeNull()
  })

  it('keeps user management for administrators of every organization', () => {
    const global = accessFor(me({ global_admin: true }))
    expect(global.canView('users')).toBe(true)
    // Without any configured organization the users page is still reachable.
    expect(firstAllowedView(global)).toBe('users')
  })

  it('restricts nothing outside the signed-in app', () => {
    expect(UNRESTRICTED.can('service', 'anything')).toBe(true)
    expect(UNRESTRICTED.organizations('control')).toBeNull()
  })
})
