import { apiError, apiFetch, withBase } from '../api'

// Wire types for /api/v1/fx-settings (internal/api/fx_handlers.go). An
// empty address stands for the default the server reports.
export type FxSettings = {
  url: string
  username: string
}

export type FxCoverage = {
  currency: string
  first_day: string
  last_day: string
  days: number
  missing_days: number
  last_fetched: string
}

export type FxSyncStatus = {
  configured: boolean
  last_attempt: string
  last_success: string
  last_error?: string
}

// FxSettingsState adds what the server reports about the stored password
// (only whether there is one), the default address and how far the rates
// reach.
export type FxSettingsState = FxSettings & {
  password_configured: boolean
  saved: boolean
  default_url: string
  first_day: string
  coverage: FxCoverage[]
  sync: FxSyncStatus
}

export type FxSettingsTestResult = {
  ok: boolean
  at: string
  rates?: Record<string, number>
  error?: string
}

const URL_PATH = '/api/v1/fx-settings'

export async function fetchFxSettings(signal?: AbortSignal): Promise<FxSettingsState> {
  const res = await apiFetch(withBase(URL_PATH), { signal })
  if (!res.ok) throw await apiError(res, 'Не вдалося завантажити налаштування')
  return (await res.json()) as FxSettingsState
}

// saveFxSettings writes the settings. password is the three-state field
// the API expects: null keeps the stored password (the page never holds
// it), '' clears it, anything else replaces it.
export async function saveFxSettings(settings: FxSettings, password: string | null, signal?: AbortSignal): Promise<void> {
  const body: FxSettings & { password?: string } = { ...settings }
  if (password !== null) body.password = password
  const res = await apiFetch(withBase(URL_PATH), {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
    signal,
  })
  if (!res.ok) throw await apiError(res, 'Не вдалося зберегти налаштування')
}

// testFxSettings asks ERP for today's rates with the settings as typed;
// without a password typed in, the server uses the stored one.
export async function testFxSettings(
  settings: FxSettings,
  password: string | null,
  signal?: AbortSignal,
): Promise<FxSettingsTestResult> {
  const body: FxSettings & { password?: string } = { ...settings }
  if (password) body.password = password
  const res = await apiFetch(withBase(`${URL_PATH}/test`), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
    signal,
  })
  if (!res.ok) throw await apiError(res, 'Перевірка не вдалася')
  return (await res.json()) as FxSettingsTestResult
}
