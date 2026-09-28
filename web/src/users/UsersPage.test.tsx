import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { AuthContext } from '../auth/authContext'
import { accessFor, type AuthMe } from '../auth/permissions'
import { UsersPage } from './UsersPage'

const root: AuthMe = {
  user: { id: 1, email: 'root@example.com', name: 'Root' },
  global_admin: true,
  must_change_password: false,
  grants: [{ role: 'admin', organization_id: null }],
  organizations: [{ id: 'ze', name: 'Жмеринський елеватор', permissions: ['analytics.day', 'service'] }],
}

const engineer = {
  id: 2,
  email: 'eng@example.com',
  name: 'Інженер Зміни',
  disabled: false,
  auth_provider: 'local',
  // "old" was removed from config.yaml after the grant was made.
  grants: [{ role: 'engineer', organization_id: 'old' }],
  created_at: '2026-09-01T00:00:00Z',
}

function json(body: unknown) {
  return new Response(JSON.stringify(body), { status: 200, headers: { 'content-type': 'application/json' } })
}

describe('UsersPage', () => {
  let puts: unknown[]

  beforeEach(() => {
    puts = []
    vi.stubGlobal(
      'fetch',
      vi.fn<typeof fetch>(async (input, init) => {
        const url = String(input)
        if (url.includes('/api/v1/users') && init?.method === 'PUT') {
          puts.push(JSON.parse(String(init.body)))
          return json(engineer)
        }
        if (url.includes('/api/v1/users')) return json({ users: [engineer] })
        return json({ sites: [] })
      }),
    )
  })
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('lets an administrator take off a grant whose object left the config', async () => {
    render(
      <AuthContext.Provider value={{ me: root, access: accessFor(root), logout: vi.fn(async () => {}) }}>
        <UsersPage />
      </AuthContext.Provider>,
    )
    expect(await screen.findByText('Інженер: old')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Змінити' }))

    const stale = screen.getByLabelText('old — немає в конфігурації') as HTMLInputElement
    expect(stale.checked).toBe(true)
    fireEvent.click(stale)
    fireEvent.click(screen.getByLabelText('Жмеринський елеватор'))
    fireEvent.click(screen.getByRole('button', { name: 'Зберегти' }))

    await waitFor(() => expect(puts).toHaveLength(1))
    expect(puts[0]).toEqual({
      name: 'Інженер Зміни',
      disabled: false,
      grants: [{ role: 'engineer', organization_id: 'ze' }],
    })
  })
})
