import { apiError, apiFetch, buildURL } from '../api'

// Wire types for the station's e-svitlo cabinet (/api/v1/outage-account)
// and its planned outages (/api/v1/outages), see
// internal/api/outages_handlers.go. The API never returns the password,
// only whether one is stored.

export type OutageInterval = {
  start: string
  end: string
  kind: 'schedule' | 'other'
  eic?: string
}

export type OutageOperator = {
  host: string
  name: string
}

export type OutageReadStatus = {
  attempted_at?: string
  succeeded_at?: string
  error?: string
}

export type OutageAccount = {
  organization_id: string
  host: string
  login: string
  eic: string
  password_configured: boolean
  // saved is false until the passport has stored an account.
  saved: boolean
  read: OutageReadStatus
  operators: OutageOperator[]
  // upcoming are the stored outages of the next 48 hours.
  upcoming: OutageInterval[]
}

// OutageAccountInput is what the passport sends. An empty login takes
// the station off the hourly read.
export type OutageAccountInput = {
  host: string
  login: string
  eic: string
}

export type OutageTestResult = {
  ok: boolean
  at: string
  signed_in: boolean
  accounts?: string[]
  outages: OutageInterval[]
  error?: string
}

const ACCOUNT_PATH = '/api/v1/outage-account'

// withPassword is the three-state password the API expects: null keeps
// the stored one (the page never holds it), '' clears it, anything else
// replaces it.
function withPassword(input: OutageAccountInput, password: string | null): OutageAccountInput & { password?: string } {
  return password === null ? { ...input } : { ...input, password }
}

export async function fetchOutageAccount(organizationID: string, signal?: AbortSignal): Promise<OutageAccount> {
  const res = await apiFetch(buildURL(ACCOUNT_PATH, { organization_id: organizationID }), { signal })
  if (!res.ok) throw await apiError(res, 'Не вдалося завантажити кабінет e-svitlo')
  return (await res.json()) as OutageAccount
}

export async function saveOutageAccount(
  organizationID: string,
  input: OutageAccountInput,
  password: string | null,
): Promise<OutageAccount> {
  const res = await apiFetch(buildURL(ACCOUNT_PATH, { organization_id: organizationID }), {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(withPassword(input, password)),
  })
  if (!res.ok) throw await apiError(res, 'Не вдалося зберегти кабінет e-svitlo')
  return (await res.json()) as OutageAccount
}

// testOutageAccount signs in with the account as typed and reads its
// outages; without a password typed in, the server uses the stored one.
export async function testOutageAccount(
  organizationID: string,
  input: OutageAccountInput,
  password: string | null,
  signal?: AbortSignal,
): Promise<OutageTestResult> {
  const res = await apiFetch(buildURL(`${ACCOUNT_PATH}/test`, { organization_id: organizationID }), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(withPassword(input, password || null)),
    signal,
  })
  if (!res.ok) throw await apiError(res, 'Перевірка не вдалася')
  return (await res.json()) as OutageTestResult
}

// fetchOutages returns the planned outage intervals overlapping the
// [from, to] days (YYYY-MM-DD, Kyiv).
export async function fetchOutages(
  organizationID: string,
  from: string,
  to: string,
  signal?: AbortSignal,
): Promise<OutageInterval[]> {
  const res = await apiFetch(buildURL('/api/v1/outages', { organization_id: organizationID, from, to }), { signal })
  if (!res.ok) throw await apiError(res, 'outages request failed')
  const body = (await res.json()) as { intervals?: OutageInterval[] | null }
  return body.intervals ?? []
}
