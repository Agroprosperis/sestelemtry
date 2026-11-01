import { apiError, apiFetch, withBase } from '../api'

// Wire types for /api/v1/ai-settings (internal/api/ai_settings.go).
export type ReasoningEffort = '' | 'minimal' | 'low' | 'medium' | 'high'

export type AISettings = {
  enabled: boolean
  model: string
  base_url: string
  reasoning_effort: ReasoningEffort
  daily_limit: number
}

// AISettingsState adds what the server reports about the stored key:
// only whether one is there, never the key.
export type AISettingsState = AISettings & {
  api_key_configured: boolean
  saved: boolean
}

export type AITestResult = {
  model: string
  reply: string
  usage: { input_tokens: number; output_tokens: number }
}

export async function fetchAISettings(signal?: AbortSignal): Promise<AISettingsState> {
  const res = await apiFetch(withBase('/api/v1/ai-settings'), { signal })
  if (!res.ok) throw await apiError(res, 'Не вдалося завантажити налаштування')
  return (await res.json()) as AISettingsState
}

// saveAISettings writes the settings. apiKey is the three-state field
// the API expects: null keeps the stored key (the page never holds
// it), '' clears it, anything else replaces it.
export async function saveAISettings(settings: AISettings, apiKey: string | null, signal?: AbortSignal): Promise<void> {
  const body: AISettings & { api_key?: string } = { ...settings }
  if (apiKey !== null) body.api_key = apiKey
  const res = await apiFetch(withBase('/api/v1/ai-settings'), {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
    signal,
  })
  if (!res.ok) throw await apiError(res, 'Не вдалося зберегти налаштування')
}

// testAISettings asks the model one short question with the settings
// as typed; without a key typed in, the server uses the stored one.
export async function testAISettings(settings: AISettings, apiKey: string | null, signal?: AbortSignal): Promise<AITestResult> {
  const body: AISettings & { api_key?: string } = { ...settings }
  if (apiKey) body.api_key = apiKey
  const res = await apiFetch(withBase('/api/v1/ai-settings/test'), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
    signal,
  })
  if (!res.ok) throw await apiError(res, 'Перевірка не вдалася')
  return (await res.json()) as AITestResult
}
