import { describe, expect, it } from 'vitest'
import type { AuthGrant } from '../auth/permissions'
import { grantSummary, grantsFromScopes, scopesFromGrants } from './grantScopes'

describe('grant scopes', () => {
  const grants: AuthGrant[] = [
    { role: 'admin', organization_id: null },
    { role: 'economist', organization_id: 'ze' },
    { role: 'economist', organization_id: 'pe' },
  ]

  it('round-trips through the form model', () => {
    const scopes = scopesFromGrants(grants)
    expect(scopes.admin).toEqual({ mode: 'all', organizations: [] })
    expect(scopes.economist).toEqual({ mode: 'some', organizations: ['ze', 'pe'] })
    expect(scopes.engineer.mode).toBe('none')
    expect(grantsFromScopes(scopes)).toEqual(grants)
  })

  it('lets «all organizations» absorb per-organization grants of the same role', () => {
    const scopes = scopesFromGrants([
      { role: 'engineer', organization_id: 'ze' },
      { role: 'engineer', organization_id: null },
    ])
    expect(grantsFromScopes(scopes)).toEqual([{ role: 'engineer', organization_id: null }])
  })

  it('summarizes one line per role', () => {
    const names: Record<string, string> = { ze: 'Жмеринський', pe: 'Радивилівський' }
    expect(grantSummary(grants, (id) => names[id] ?? id)).toEqual([
      'Адміністратор: усі обʼєкти',
      'Економіст: Жмеринський, Радивилівський',
    ])
  })
})
