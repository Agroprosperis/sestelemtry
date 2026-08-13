import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import {
  type AlertSettings,
  fetchAlertSettings,
  fetchOrgAlertSettings,
  parseRecipients,
  saveAlertSettings,
  sendAlertTestEmail,
} from './alertsClient'

function settings(): AlertSettings {
  return {
    enabled: true,
    check_interval: '1m',
    stale_after: '10m',
    repeat_interval: '6h',
    notify_recovery: true,
    smtp: {
      host: 'smtp.example.com',
      port: 587,
      tls: 'starttls',
      username: '',
      from: 'alerts@example.com',
      timeout: '20s',
    },
    recipients: ['ops@example.com'],
  }
}

describe('alertsClient', () => {
  beforeEach(() => vi.unstubAllGlobals())
  afterEach(() => vi.unstubAllGlobals())

  it('reads the settings and the password flag', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(
        async () =>
          new Response(
            JSON.stringify({ ...settings(), smtp_password_configured: true, saved: true }),
            { status: 200 },
          ),
      ),
    )
    const got = await fetchAlertSettings()
    expect(got.smtp_password_configured).toBe(true)
    expect(got.stale_after).toBe('10m')
  })

  // Omitting the password is how the page edits recipients without ever
  // holding the mail credentials.
  it('omits smtp_password when the field was not touched', async () => {
    const fetchMock = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => new Response(null, { status: 204 }))
    vi.stubGlobal('fetch', fetchMock)
    await saveAlertSettings(settings(), null)
    const body = JSON.parse(String(fetchMock.mock.calls[0]?.[1]?.body))
    expect('smtp_password' in body).toBe(false)
  })

  it('sends an empty smtp_password to clear it', async () => {
    const fetchMock = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => new Response(null, { status: 204 }))
    vi.stubGlobal('fetch', fetchMock)
    await saveAlertSettings(settings(), '')
    const body = JSON.parse(String(fetchMock.mock.calls[0]?.[1]?.body))
    expect(body.smtp_password).toBe('')
  })

  // The server answers failures with a plain-text reason; losing it
  // would leave the operator with a bare status code.
  it('surfaces the server error text', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => new Response('stale_after must be >= check_interval', { status: 400 })),
    )
    await expect(saveAlertSettings(settings(), null)).rejects.toThrow(
      /400 stale_after must be >= check_interval/,
    )
  })

  it('defaults an absent organizations map to empty', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response('{}', { status: 200 })))
    expect(await fetchOrgAlertSettings()).toEqual({})
  })

  it('returns the recipients a test email reached', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(
        async () =>
          new Response(JSON.stringify({ recipients: ['ops@example.com'] }), { status: 200 }),
      ),
    )
    expect(await sendAlertTestEmail('ke')).toEqual(['ops@example.com'])
  })

  it('reports a rejected test email with the relay message', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => new Response('alerts: smtp auth: 535 failed', { status: 502 })),
    )
    await expect(sendAlertTestEmail()).rejects.toThrow(/535 failed/)
  })
})

describe('parseRecipients', () => {
  it('accepts every delimiter an operator might paste', () => {
    expect(parseRecipients('a@x.com, b@x.com;c@x.com\n d@x.com')).toEqual([
      'a@x.com',
      'b@x.com',
      'c@x.com',
      'd@x.com',
    ])
  })

  it('drops blanks and duplicates', () => {
    expect(parseRecipients(' , a@x.com , a@x.com ')).toEqual(['a@x.com'])
  })

  it('returns an empty list for an empty field', () => {
    expect(parseRecipients('   ')).toEqual([])
  })
})
