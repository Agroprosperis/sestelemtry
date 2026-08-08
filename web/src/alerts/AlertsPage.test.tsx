import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { resetOrganizationsCache } from '../api'
import { AlertsPage } from './AlertsPage'

const settingsBody = {
  enabled: true,
  check_interval: '1m',
  stale_after: '10m',
  repeat_interval: '6h',
  notify_recovery: true,
  smtp: {
    host: 'smtp.example.com',
    port: 587,
    tls: 'starttls',
    username: 'alerts@example.com',
    from: 'alerts@example.com',
    timeout: '20s',
  },
  recipients: ['ops@example.com'],
  smtp_password_configured: true,
  saved: true,
}

type Call = { url: string; init?: RequestInit }

function stubApi(overrides: Partial<typeof settingsBody> = {}) {
  const calls: Call[] = []
  const fetchMock = vi.fn(async (url: string, init?: RequestInit) => {
    const u = String(url)
    calls.push({ url: u, init })
    if (u.includes('/alert-settings/test-email')) {
      return new Response(JSON.stringify({ recipients: ['ke@example.com'] }), { status: 200 })
    }
    if (u.includes('/alert-settings')) {
      if (init?.method === 'PUT') return new Response(null, { status: 204 })
      return new Response(JSON.stringify({ ...settingsBody, ...overrides }), { status: 200 })
    }
    if (u.includes('/organization-alert-settings')) {
      if (init?.method === 'PUT') return new Response(null, { status: 204 })
      return new Response(
        JSON.stringify({ organizations: { ke: { enabled: false, recipients: [] } } }),
        { status: 200 },
      )
    }
    if (u.includes('/organizations')) {
      return new Response(
        JSON.stringify({
          organizations: [
            { id: 'ke', name: 'Кролевецький елеватор' },
            { id: 'pde', name: 'Поділля елеватор' },
          ],
        }),
        { status: 200 },
      )
    }
    return new Response('{}', { status: 200 })
  })
  vi.stubGlobal('fetch', fetchMock)
  return calls
}

describe('AlertsPage', () => {
  beforeEach(() => {
    vi.unstubAllGlobals()
    resetOrganizationsCache()
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    resetOrganizationsCache()
  })

  it('renders the saved settings and every organization', async () => {
    stubApi()
    render(<AlertsPage />)
    expect(await screen.findByDisplayValue('smtp.example.com')).toBeInTheDocument()
    expect(await screen.findByText('Кролевецький елеватор')).toBeInTheDocument()
    expect(screen.getByText('Поділля елеватор')).toBeInTheDocument()
    // A stored password is reported, never rendered.
    expect(
      screen.getByPlaceholderText(/збережено — введіть новий/),
    ).toBeInTheDocument()
  })

  // Organizations without a stored override default to "on"; the one
  // the operator muted stays off.
  it('reflects the per-organization switch', async () => {
    stubApi()
    render(<AlertsPage />)
    const ke = await screen.findByLabelText('Сповіщати про Кролевецький елеватор')
    const pde = screen.getByLabelText('Сповіщати про Поділля елеватор')
    expect((ke as HTMLInputElement).checked).toBe(false)
    expect((pde as HTMLInputElement).checked).toBe(true)
  })

  it('shows the config fallback notice until the form is saved', async () => {
    stubApi({ saved: false })
    render(<AlertsPage />)
    expect(await screen.findByText(/config.yaml/)).toBeInTheDocument()
  })

  // Save is disabled until something changes, so the page cannot
  // overwrite a working configuration with an accidental click.
  it('enables saving only after an edit', async () => {
    const calls = stubApi()
    render(<AlertsPage />)
    const save = await screen.findByRole('button', { name: 'Зберегти' })
    expect(save).toBeDisabled()

    fireEvent.change(screen.getByDisplayValue('smtp.example.com'), {
      target: { value: 'smtp.other.example' },
    })
    expect(save).toBeEnabled()

    fireEvent.click(save)
    await waitFor(() => {
      expect(calls.some((c) => c.init?.method === 'PUT')).toBe(true)
    })
    const put = calls.find((c) => c.init?.method === 'PUT')
    const body = JSON.parse(String(put?.init?.body))
    expect(body.smtp.host).toBe('smtp.other.example')
    // Untouched password field: the stored secret must be left alone.
    expect('smtp_password' in body).toBe(false)
  })

  it('reports where a test email landed', async () => {
    stubApi()
    render(<AlertsPage />)
    const button = await screen.findByRole('button', { name: 'Надіслати тестовий лист' })
    fireEvent.click(button)
    expect(await screen.findByText(/Лист надіслано: ke@example.com/)).toBeInTheDocument()
  })
})
