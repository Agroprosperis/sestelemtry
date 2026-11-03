import { fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { AuthContext } from '../auth/authContext'
import { accessFor, type AuthMe } from '../auth/permissions'
import { PvPlanSettingsPage } from './PvPlanSettingsPage'
import type { PvPlanSettingsState, PvPlanSettingsTestResult } from './pvPlanSettingsClient'

const admin: AuthMe = {
  user: { id: 1, email: 'root@example.com', name: 'Адміністратор' },
  global_admin: true,
  must_change_password: false,
  grants: [{ role: 'admin', organization_id: null }],
  organizations: [{ id: 'ab', name: 'AB', permissions: ['analytics.day', 'service'] }],
}

const defaults = {
  forecast_url: 'https://api.open-meteo.com/v1/forecast',
  previous_runs_url: 'https://previous-runs-api.open-meteo.com/v1/forecast',
  n8n_webhook_url: 'https://granary.app.n8n.cloud/webhook/x',
}

const stored: PvPlanSettingsState = {
  forecast_url: 'https://customer-api.open-meteo.com/v1/forecast',
  previous_runs_url: '',
  n8n_webhook_url: '',
  api_key_configured: true,
  saved: true,
  defaults,
}

type Call = { url: string; method: string; body: unknown }

// stubAPI answers the page's calls and records them; reply() shapes the
// answer to a PUT or a test.
function stubAPI(reply: (call: Call) => Response = () => new Response(null, { status: 204 })) {
  const calls: Call[] = []
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
        return new Response(JSON.stringify(stored), { headers: { 'Content-Type': 'application/json' } })
      }
      return reply(call)
    }),
  )
  return calls
}

function renderPage() {
  render(
    <AuthContext.Provider value={{ me: admin, access: accessFor(admin), logout: vi.fn(async () => {}) }}>
      <PvPlanSettingsPage />
    </AuthContext.Provider>,
  )
}

describe('PV plan settings page', () => {
  beforeEach(() => window.history.replaceState({}, '', '/?view=pvplan'))
  afterEach(() => vi.unstubAllGlobals())

  it('saves a new webhook without the key, which only goes to Open-Meteo', async () => {
    const calls = stubAPI()
    renderPage()
    expect(await screen.findByDisplayValue('https://customer-api.open-meteo.com/v1/forecast')).toBeInTheDocument()
    expect(screen.getByLabelText('Ключ API')).toHaveAttribute('placeholder', 'збережено — введіть новий, щоб замінити')
    expect(screen.getByLabelText('Адреса вебхука')).toHaveAttribute('placeholder', defaults.n8n_webhook_url)
    expect(screen.getByRole('button', { name: 'Зберегти' })).toBeDisabled()

    fireEvent.change(screen.getByLabelText('Адреса вебхука'), { target: { value: 'https://n8n.example.com/webhook/plan' } })
    fireEvent.click(screen.getByRole('button', { name: 'Зберегти' }))

    expect(await screen.findByText('Збережено')).toBeInTheDocument()
    expect(calls.find((c) => c.method === 'PUT')?.body).toEqual({
      forecast_url: 'https://customer-api.open-meteo.com/v1/forecast',
      previous_runs_url: '',
      n8n_webhook_url: 'https://n8n.example.com/webhook/plan',
    })
  })

  it('asks for the key again once an Open-Meteo address changes', async () => {
    stubAPI()
    renderPage()
    await screen.findByDisplayValue('https://customer-api.open-meteo.com/v1/forecast')
    // Typing the default spelled out changes nothing that's in force.
    fireEvent.change(screen.getByLabelText('Адреса архіву прогнозів'), { target: { value: defaults.previous_runs_url } })
    expect(screen.queryByText(/Адресу Open-Meteo змінено/)).not.toBeInTheDocument()

    fireEvent.change(screen.getByLabelText('Адреса прогнозу'), { target: { value: '' } })
    expect(screen.getByText(/Адресу Open-Meteo змінено/)).toBeInTheDocument()
  })

  it('shows every upstream answer of the connection test', async () => {
    const result: PvPlanSettingsTestResult = {
      forecast: { ok: true, hours: 96 },
      previous_runs: { ok: false, hours: 0, error: 'Open-Meteo відхилив запит: Invalid API key' },
      n8n: { ok: true, hours: 9 },
      n8n_organization_id: 'ab',
    }
    const calls = stubAPI((call) => (call.url.endsWith('/test') ? new Response(JSON.stringify(result)) : new Response(null, { status: 204 })))
    renderPage()
    await screen.findByDisplayValue('https://customer-api.open-meteo.com/v1/forecast')
    fireEvent.change(screen.getByLabelText('Ключ API'), { target: { value: 'om-typed' } })
    fireEvent.click(screen.getByRole('button', { name: 'Перевірити зʼєднання' }))

    expect(await screen.findByText('Прогноз Open-Meteo: працює, 96 год')).toBeInTheDocument()
    expect(screen.getByText('Архів прогнозів: Open-Meteo відхилив запит: Invalid API key')).toBeInTheDocument()
    expect(screen.getByText('n8n (ab, сьогодні): працює, 9 год з генерацією')).toBeInTheDocument()
    expect(calls.find((c) => c.url.endsWith('/test'))?.body).toMatchObject({ api_key: 'om-typed' })
    expect(calls.some((c) => c.method === 'PUT')).toBe(false)
  })
})
