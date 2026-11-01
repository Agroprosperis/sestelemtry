import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { AuthContext } from '../auth/authContext'
import { accessFor, type AuthMe } from '../auth/permissions'
import { AISettingsPage } from './AISettingsPage'
import type { AISettingsState } from './aiSettingsClient'

const admin: AuthMe = {
  user: { id: 1, email: 'root@example.com', name: 'Адміністратор' },
  global_admin: true,
  must_change_password: false,
  grants: [{ role: 'admin', organization_id: null }],
  organizations: [{ id: 'ze', name: 'ZE', permissions: ['analytics.day', 'economics.read', 'service'] }],
  ai_chat: true,
}

const stored: AISettingsState = {
  enabled: true,
  model: 'm1',
  base_url: '',
  reasoning_effort: '',
  daily_limit: 100,
  api_key_configured: true,
  saved: true,
}

type Call = { url: string; method: string; body: unknown }

// stubAPI answers the page's three calls and records them; reply()
// shapes the answer to a PUT or a test.
function stubAPI(reply: (call: Call) => Response = () => new Response(null, { status: 204 })) {
  const calls: Call[] = []
  let state = stored
  vi.stubGlobal(
    'fetch',
    vi.fn<typeof fetch>(async (input, init) => {
      const call = {
        url: String(input),
        method: init?.method ?? 'GET',
        body: init?.body ? JSON.parse(String(init.body)) : undefined,
      }
      calls.push(call)
      if (call.method === 'GET') {
        return new Response(JSON.stringify(state), { headers: { 'Content-Type': 'application/json' } })
      }
      const res = reply(call)
      if (call.method === 'PUT' && res.ok) state = { ...state, ...(call.body as object) }
      return res
    }),
  )
  return calls
}

function renderPage() {
  const refreshMe = vi.fn(async () => {})
  render(
    <AuthContext.Provider value={{ me: admin, access: accessFor(admin), logout: vi.fn(async () => {}), refreshMe }}>
      <AISettingsPage />
    </AuthContext.Provider>,
  )
  return refreshMe
}

describe('AI assistant settings page', () => {
  beforeEach(() => window.history.replaceState({}, '', '/?view=ai'))
  afterEach(() => vi.unstubAllGlobals())

  it('saves without the key unless a new one is typed, then refreshes the session', async () => {
    const calls = stubAPI()
    const refreshMe = renderPage()
    expect(await screen.findByDisplayValue('m1')).toBeInTheDocument()
    expect(screen.getByLabelText('Ключ API')).toHaveAttribute('placeholder', 'збережено — введіть новий, щоб замінити')
    expect(screen.getByRole('button', { name: 'Зберегти' })).toBeDisabled()

    fireEvent.change(screen.getByLabelText('Запитів на користувача за добу'), { target: { value: '20' } })
    fireEvent.click(screen.getByRole('button', { name: 'Зберегти' }))

    expect(await screen.findByText('Збережено')).toBeInTheDocument()
    expect(refreshMe).toHaveBeenCalledTimes(1)
    const put = calls.find((c) => c.method === 'PUT')
    expect(put?.url).toContain('/api/v1/ai-settings')
    expect(put?.body).toEqual({ enabled: true, model: 'm1', base_url: '', reasoning_effort: '', daily_limit: 20 })
  })

  it('deletes the key only through its button, switching the assistant off', async () => {
    const calls = stubAPI()
    renderPage()
    await screen.findByDisplayValue('m1')
    fireEvent.click(screen.getByRole('button', { name: 'Видалити ключ' }))
    expect(screen.getByText('Ключ буде видалено після збереження, асистента вимкнено.')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Зберегти' }))
    await waitFor(() => expect(calls.some((c) => c.method === 'PUT')).toBe(true))
    expect(calls.find((c) => c.method === 'PUT')?.body).toMatchObject({ enabled: false, api_key: '' })
  })

  it('tests the connection with the typed key and shows the API refusal', async () => {
    let refuse = false
    const calls = stubAPI((call) =>
      call.url.endsWith('/test') && !refuse
        ? new Response(JSON.stringify({ model: 'm1', reply: 'Так, на звʼязку.', usage: { input_tokens: 30, output_tokens: 6 } }))
        : new Response('API відповіло 401: invalid_api_key Incorrect API key provided', { status: 502 }),
    )
    renderPage()
    await screen.findByDisplayValue('m1')
    fireEvent.change(screen.getByLabelText('Ключ API'), { target: { value: 'sk-typed' } })
    fireEvent.click(screen.getByRole('button', { name: 'Перевірити зʼєднання' }))
    expect(await screen.findByText(/m1 відповіла: «Так, на звʼязку.»/)).toBeInTheDocument()
    expect(calls.find((c) => c.url.endsWith('/test'))?.body).toMatchObject({ model: 'm1', api_key: 'sk-typed' })

    refuse = true
    fireEvent.click(screen.getByRole('button', { name: 'Перевірити зʼєднання' }))
    expect(await screen.findByText('API відповіло 401: invalid_api_key Incorrect API key provided')).toBeInTheDocument()
  })
})
