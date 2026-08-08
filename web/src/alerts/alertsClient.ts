import { buildURL, withBase } from '../api'

// Wire types for /api/v1/alert-settings. Durations travel as Go
// duration strings ("10m") so the settings page, the database and
// config.yaml all speak the same notation — no unit conversion to get
// wrong at a boundary.
export type SmtpTlsMode = 'starttls' | 'implicit' | 'none'

export type SmtpSettings = {
  host: string
  port: number
  tls: SmtpTlsMode
  username: string
  from: string
  timeout: string
}

export type AlertSettings = {
  enabled: boolean
  check_interval: string
  stale_after: string
  repeat_interval: string
  notify_recovery: boolean
  smtp: SmtpSettings
  recipients: string[]
}

// AlertSettingsState adds the two read-only flags the server attaches:
// whether a password is stored (the value itself is never sent) and
// whether these settings were saved or are still the config.yaml
// fallback.
export type AlertSettingsState = AlertSettings & {
  smtp_password_configured: boolean
  saved: boolean
}

export type OrgAlertSettings = {
  enabled: boolean
  recipients: string[]
}

async function failure(res: Response, what: string): Promise<Error> {
  const body = await res.text().catch(() => '')
  const trimmed = body.trim()
  // The API answers validation and relay failures with a plain-text
  // reason ("535 authentication failed"); surfacing it verbatim is the
  // difference between a fixable message and "щось пішло не так".
  return new Error(`${what}: ${res.status}${trimmed ? ` ${trimmed}` : ''}`)
}

export async function fetchAlertSettings(signal?: AbortSignal): Promise<AlertSettingsState> {
  const res = await fetch(withBase('/api/v1/alert-settings'), { signal })
  if (!res.ok) throw await failure(res, 'alert-settings request failed')
  return (await res.json()) as AlertSettingsState
}

// saveAlertSettings writes the site-wide settings. `password` is the
// three-state field the API expects: null keeps the stored password
// (the normal case — the page never holds it), '' clears it, anything
// else replaces it.
export async function saveAlertSettings(
  settings: AlertSettings,
  password: string | null,
  signal?: AbortSignal,
): Promise<void> {
  const body: AlertSettings & { smtp_password?: string } = { ...settings }
  if (password !== null) body.smtp_password = password
  const res = await fetch(withBase('/api/v1/alert-settings'), {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
    signal,
  })
  if (!res.ok) throw await failure(res, 'alert-settings save failed')
}

export async function fetchOrgAlertSettings(
  signal?: AbortSignal,
): Promise<Record<string, OrgAlertSettings>> {
  const res = await fetch(withBase('/api/v1/organization-alert-settings'), { signal })
  if (!res.ok) throw await failure(res, 'organization-alert-settings request failed')
  const body = (await res.json()) as { organizations?: Record<string, OrgAlertSettings> }
  return body.organizations ?? {}
}

export async function saveOrgAlertSettings(
  organizationID: string,
  settings: OrgAlertSettings,
  signal?: AbortSignal,
): Promise<void> {
  const url = buildURL('/api/v1/organization-alert-settings', {
    organization_id: organizationID,
  })
  const res = await fetch(url, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(settings),
    signal,
  })
  if (!res.ok) throw await failure(res, 'organization-alert-settings save failed')
}

// sendAlertTestEmail delivers a test message to the addresses that
// would receive a real alert. Without an organization id it uses the
// default list. Returns the addresses the relay accepted.
export async function sendAlertTestEmail(
  organizationID?: string,
  signal?: AbortSignal,
): Promise<string[]> {
  const url = buildURL('/api/v1/alert-settings/test-email', {
    organization_id: organizationID,
  })
  const res = await fetch(url, { method: 'POST', signal })
  if (!res.ok) throw await failure(res, 'test-email failed')
  const body = (await res.json()) as { recipients?: string[] }
  return body.recipients ?? []
}

// parseRecipients turns a free-text field into an address list. Commas,
// semicolons, newlines and spaces all separate: operators paste from
// mail clients, address books and each other, and every one of those
// uses a different delimiter.
export function parseRecipients(text: string): string[] {
  const seen = new Set<string>()
  const out: string[] = []
  for (const raw of text.split(/[,;\s]+/)) {
    const addr = raw.trim()
    if (!addr || seen.has(addr)) continue
    seen.add(addr)
    out.push(addr)
  }
  return out
}
