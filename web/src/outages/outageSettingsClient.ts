import { apiError, apiFetch, withBase } from '../api'

// Wire types for /api/v1/outage-settings: the e-svitlo cabinets (one per
// grid operator) the planned outages are read from. The API never returns
// a password, only whether one is stored.
export type OutageAccount = {
  host: string
  name: string
  login: string
  password_configured: boolean
}

export type OutageSyncStatus = {
  configured: boolean
  last_attempt?: string
  last_success?: string
  last_error?: string
}

export type OutageSettingsState = {
  accounts: OutageAccount[]
  sync: OutageSyncStatus
}

// OutageAccountInput is one account in the PUT body. password is the
// three-state field: omitted keeps the stored one, '' clears it, anything
// else replaces it.
export type OutageAccountInput = {
  host: string
  login: string
  password?: string
}

export type OutageInterval = {
  start: string
  end: string
  kind: 'schedule' | 'other'
}

export type OutageTestRequest = {
  host: string
  login: string
  password?: string
  eic?: string
}

export type OutageTestResult = {
  ok: boolean
  at: string
  signed_in: boolean
  landing?: string
  path?: string
  outages: OutageInterval[] | null
  error?: string
}

const SETTINGS_PATH = '/api/v1/outage-settings'

export async function fetchOutageSettings(signal?: AbortSignal): Promise<OutageSettingsState> {
  const res = await apiFetch(withBase(SETTINGS_PATH), { signal })
  if (!res.ok) throw await apiError(res, 'Не вдалося завантажити налаштування')
  const body = (await res.json()) as Partial<OutageSettingsState>
  return { accounts: body.accounts ?? [], sync: body.sync ?? { configured: false } }
}

export async function saveOutageSettings(accounts: OutageAccountInput[], signal?: AbortSignal): Promise<void> {
  const res = await apiFetch(withBase(SETTINGS_PATH), {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ accounts }),
    signal,
  })
  if (!res.ok) throw await apiError(res, 'Не вдалося зберегти налаштування')
}

// testOutageAccount signs in to one cabinet with the credentials as typed
// (the stored password when none is typed) and, with an EIC, reads that
// point's outages.
export async function testOutageAccount(req: OutageTestRequest, signal?: AbortSignal): Promise<OutageTestResult> {
  const body: OutageTestRequest = { host: req.host, login: req.login }
  if (req.password) body.password = req.password
  if (req.eic) body.eic = req.eic
  const res = await apiFetch(withBase(`${SETTINGS_PATH}/test`), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
    signal,
  })
  if (!res.ok) throw await apiError(res, 'Перевірка не вдалася')
  return (await res.json()) as OutageTestResult
}
