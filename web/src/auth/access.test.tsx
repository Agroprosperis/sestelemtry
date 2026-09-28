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
  grants: [
    { role: 'economist', organization_id: 'ze' },
    { role: 'engineer', organization_id: 'pe' },
  ],
  organizations: [
    { id: 'ze', name: 'ZE', permissions: ['economics.read', 'economics.write'] },
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

function Probe({ permission }: { permission: Permission }) {
  const { organizationID, options } = useOrganizationParam(permission)
  return <output data-testid="org">{`${organizationID}|${options.join(',')}`}</output>
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
    expect(tabs).toEqual(['Аналітика', 'Економіка'])
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
})
