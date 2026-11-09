import { render, screen } from '@testing-library/react'
import type { ReactNode } from 'react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { useOrganizationParam } from '../dashboard/hooks/useOrganizationParam'
import { ModeTopBar } from '../shell/ModeTopBar'
import { AuthContext } from './authContext'
import { accessFor, type AuthMe, type Permission } from './permissions'

const mixed: AuthMe = {
  user: { id: 7, email: 'olena@example.com', name: 'Олена' },
  global_admin: false,
  must_change_password: false,
  grants: [
    { role: 'economist', organization_id: 'ze' },
    { role: 'engineer', organization_id: 'pe' },
  ],
  organizations: [
    { id: 'ze', name: 'ZE', permissions: ['economics.read'] },
    { id: 'pe', name: 'PE', permissions: ['analytics.day'] },
  ],
}

function signedIn(me: AuthMe, children: ReactNode) {
  return (
    <AuthContext.Provider value={{ me, access: accessFor(me), logout: vi.fn(async () => {}) }}>
      {children}
    </AuthContext.Provider>
  )
}

function Probe({ permission, groups }: { permission: Permission; groups?: boolean }) {
  const { organizationID, options } = useOrganizationParam(permission, { groups })
  return <output data-testid="org">{`${organizationID}|${options.join(',')}`}</output>
}

// Дубовʼязівський: an economist on both sites reads them as the one
// elevator `de-all` in economics, and as its two sites everywhere else.
const dubovyazivka: AuthMe = {
  ...mixed,
  grants: [
    { role: 'economist', organization_id: 'de' },
    { role: 'economist', organization_id: 'de1' },
    { role: 'economist', organization_id: 'ze' },
  ],
  organizations: [
    { id: 'ze', name: 'ZE', permissions: ['economics.read', 'analytics.day'] },
    { id: 'de', name: 'Т2', permissions: ['economics.read', 'analytics.day'] },
    { id: 'de1', name: 'Т1', permissions: ['economics.read', 'analytics.day'] },
  ],
  groups: [{ id: 'de-all', name: 'Елеватор', members: ['de', 'de1'], permissions: ['economics.read'] }],
}

describe('access in the shell', () => {
  beforeEach(() => {
    window.history.replaceState({}, '', '/')
  })

  it('offers only the modes the user may open', () => {
    render(
      signedIn(
        mixed,
        <ModeTopBar mode="analytics" organizationID="pe" options={['pe']} onOrganizationChange={() => {}} status={null} />,
      ),
    )
    const tabs = screen.getAllByRole('tab').map((t) => t.textContent)
    expect(tabs).toEqual(['Система', 'Економіка'])
    expect(screen.getByRole('button', { name: /Олена/ })).toBeInTheDocument()
  })

  it('replaces an object the user may not open with the first allowed one', () => {
    window.history.replaceState({}, '', '/?organization_id=ze')
    render(signedIn(mixed, <Probe permission="analytics.day" />))
    expect(screen.getByTestId('org').textContent).toBe('pe|pe')
    expect(new URLSearchParams(window.location.search).get('organization_id')).toBe('pe')
  })

  it('keeps the edge shadow of an allowed site', () => {
    window.history.replaceState({}, '', '/?organization_id=pe-edge')
    render(signedIn(mixed, <Probe permission="analytics.day" />))
    expect(screen.getByTestId('org').textContent).toBe('pe-edge|pe-edge,pe')
  })

  it('puts an economic group in place of its sites in economics', () => {
    window.history.replaceState({}, '', '/?organization_id=de1')
    render(signedIn(dubovyazivka, <Probe permission="economics.read" groups />))
    expect(screen.getByTestId('org').textContent).toBe('de-all|ze,de-all')
    expect(new URLSearchParams(window.location.search).get('organization_id')).toBe('de-all')
  })

  it('opens a link to a group as its first site elsewhere', () => {
    window.history.replaceState({}, '', '/?organization_id=de-all')
    render(signedIn(dubovyazivka, <Probe permission="analytics.day" />))
    expect(screen.getByTestId('org').textContent).toBe('de|ze,de,de1')
  })

  it('keeps a site whose group the user cannot read whole', () => {
    window.history.replaceState({}, '', '/?organization_id=de')
    const half: AuthMe = { ...dubovyazivka, organizations: dubovyazivka.organizations.slice(0, 2), groups: [] }
    render(signedIn(half, <Probe permission="economics.read" groups />))
    expect(screen.getByTestId('org').textContent).toBe('de|ze,de')
  })

  it('takes what the user may do on a group from the API', () => {
    const access = accessFor(dubovyazivka)
    expect(access.can('economics.read', 'de-all')).toBe(true)
    expect(access.can('economics.write', 'de-all')).toBe(false)
    expect(access.organizations('economics.read')).toEqual(['ze', 'de', 'de1'])
  })
})
