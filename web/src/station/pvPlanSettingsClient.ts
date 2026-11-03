import { apiError, apiFetch, withBase } from '../api'

// Wire types for /api/v1/pv-plan-settings (internal/api/pvplan_settings.go).
// An empty address stands for the default the server reports.
export type PvPlanSettings = {
  forecast_url: string
  previous_runs_url: string
  n8n_webhook_url: string
}

// PvPlanSettingsState adds what the server reports about the stored
// Open-Meteo key (only whether there is one) and the defaults.
export type PvPlanSettingsState = PvPlanSettings & {
  api_key_configured: boolean
  saved: boolean
  defaults: PvPlanSettings
}

export type PvPlanUpstreamCheck = { ok: boolean; hours: number; error?: string }

export type PvPlanSettingsTestResult = {
  forecast: PvPlanUpstreamCheck
  previous_runs: PvPlanUpstreamCheck
  // Absent when no served station has an n8n code.
  n8n?: PvPlanUpstreamCheck
  n8n_organization_id?: string
}

const URL_PATH = '/api/v1/pv-plan-settings'

export async function fetchPvPlanSettings(signal?: AbortSignal): Promise<PvPlanSettingsState> {
  const res = await apiFetch(withBase(URL_PATH), { signal })
  if (!res.ok) throw await apiError(res, 'Не вдалося завантажити налаштування')
  return (await res.json()) as PvPlanSettingsState
}

// savePvPlanSettings writes the settings. apiKey is the three-state
// field the API expects: null keeps the stored key (the page never
// holds it), '' clears it, anything else replaces it.
export async function savePvPlanSettings(
  settings: PvPlanSettings,
  apiKey: string | null,
  signal?: AbortSignal,
): Promise<void> {
  const body: PvPlanSettings & { api_key?: string } = { ...settings }
  if (apiKey !== null) body.api_key = apiKey
  const res = await apiFetch(withBase(URL_PATH), {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
    signal,
  })
  if (!res.ok) throw await apiError(res, 'Не вдалося зберегти налаштування')
}

// testPvPlanSettings asks every upstream once with the settings as
// typed; without a key typed in, the server uses the stored one.
export async function testPvPlanSettings(
  settings: PvPlanSettings,
  apiKey: string | null,
  signal?: AbortSignal,
): Promise<PvPlanSettingsTestResult> {
  const body: PvPlanSettings & { api_key?: string } = { ...settings }
  if (apiKey) body.api_key = apiKey
  const res = await apiFetch(withBase(`${URL_PATH}/test`), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
    signal,
  })
  if (!res.ok) throw await apiError(res, 'Перевірка не вдалася')
  return (await res.json()) as PvPlanSettingsTestResult
}
