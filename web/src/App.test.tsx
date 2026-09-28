import { fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import App from './App'

const globalAdmin = {
  user: { id: 1, email: 'root@example.com', name: 'Root' },
  global_admin: true,
  must_change_password: false,
  grants: [{ role: 'admin', organization_id: null }],
  organizations: [],
}

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { 'content-type': 'application/json' } })
}

describe('App session gate', () => {
  beforeEach(() => {
    vi.unstubAllGlobals()
    window.history.replaceState({}, '', '/')
  })
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('asks an anonymous visitor to sign in, then lands on the first allowed view', async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      if (url.includes('/api/v1/auth/me')) return new Response('unauthorized', { status: 401 })
      if (url.includes('/api/v1/auth/login')) {
        const body = JSON.parse(String(init?.body))
        return body.password === 'correct horse battery'
          ? json(globalAdmin)
          : new Response('невірний логін або пароль', { status: 401 })
      }
      if (url.includes('/api/v1/users')) {
        return json({ users: [{ ...globalAdmin.user, disabled: false, auth_provider: 'local', grants: globalAdmin.grants, created_at: '2026-09-01T00:00:00Z' }] })
      }
      return json({})
    })
    vi.stubGlobal('fetch', fetchMock)
    render(<App />)

    fireEvent.change(await screen.findByLabelText('Email або логін'), { target: { value: 'root@example.com' } })
    fireEvent.change(screen.getByLabelText('Пароль'), { target: { value: 'wrong password' } })
    fireEvent.click(screen.getByRole('button', { name: 'Увійти' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('невірний логін або пароль')

    fireEvent.change(screen.getByLabelText('Пароль'), { target: { value: 'correct horse battery' } })
    fireEvent.click(screen.getByRole('button', { name: 'Увійти' }))
    // No organization is configured, so an administrator of every
    // organization lands on user management.
    expect(await screen.findByText('Облікові записи')).toBeInTheDocument()
    expect(await screen.findByText('root@example.com')).toBeInTheDocument()
    expect(new URLSearchParams(window.location.search).get('view')).toBe('users')

    const login = fetchMock.mock.calls.find(([u]) => String(u).includes('/auth/login'))
    expect(new Headers(login?.[1]?.headers).get('X-Requested-With')).toBe('fetch')
  })

  it('shows nothing but the password form to admin/admin until the password changes', async () => {
    let changed = false
    const fetchMock = vi.fn<typeof fetch>(async (input, init) => {
      const url = String(input)
      if (url.includes('/api/v1/auth/me')) {
        return json({ ...globalAdmin, user: { id: 1, email: 'admin', name: '' }, must_change_password: !changed })
      }
      if (url.includes('/api/v1/auth/password')) {
        changed = true
        expect(JSON.parse(String(init?.body))).toEqual({ current_password: 'admin', new_password: 'a proper passphrase' })
        return new Response(null, { status: 204 })
      }
      if (url.includes('/api/v1/users')) return json({ users: [] })
      return json({})
    })
    vi.stubGlobal('fetch', fetchMock)
    render(<App />)

    expect(await screen.findByText('Задайте свій пароль')).toBeInTheDocument()
    expect(screen.queryByText('Облікові записи')).toBeNull()
    fireEvent.change(screen.getByLabelText('Поточний пароль'), { target: { value: 'admin' } })
    fireEvent.change(screen.getByLabelText('Новий пароль'), { target: { value: 'a proper passphrase' } })
    fireEvent.change(screen.getByLabelText('Повторіть новий пароль'), { target: { value: 'a proper passphrase' } })
    fireEvent.click(screen.getByRole('button', { name: 'Змінити пароль' }))

    expect(await screen.findByText('Облікові записи')).toBeInTheDocument()
    expect(changed).toBe(true)
  })

  it('tells a signed-in user without roles to ask an administrator', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => json({ ...globalAdmin, global_admin: false, grants: [] })),
    )
    render(<App />)
    expect(await screen.findByText('Немає доступу')).toBeInTheDocument()
  })

  it('offers a retry when the server is unreachable', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => {
        throw new TypeError('Failed to fetch')
      }),
    )
    render(<App />)
    expect(await screen.findByRole('button', { name: 'Спробувати знову' })).toBeInTheDocument()
  })
})
