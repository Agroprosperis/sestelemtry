import { apiError, apiFetch, buildURL, readNdjson } from '../api'
import { energyFloorFor } from '../dashboard/config'

export type ChatRole = 'user' | 'assistant'

export type ChatMessage = { role: ChatRole; text: string }

// ChatPageContext is where the user is in the dashboard: a hint for the
// assistant's prompt (internal/api chatContext). The organization goes
// in the query string, where the API authorizes it.
export type ChatPageContext = {
  view?: string
  tab?: string
  preset?: string
  range?: string
  anchor?: string
  from?: string
  to?: string
  operation_start?: string
}

export type ChatSource = { label: string }

export type ChatUsage = { input_tokens: number; output_tokens: number }

// ChatEvent is one NDJSON line of POST /api/v1/chat.
export type ChatEvent =
  | { type: 'tool'; name: string; label: string }
  | { type: 'status'; label: string }
  | { type: 'delta'; text: string }
  | { type: 'sources'; sources: ChatSource[] }
  | { type: 'suggestions'; suggestions: string[] }
  | { type: 'done'; usage?: ChatUsage }
  | { type: 'error'; error: string }

// streamChat asks the assistant about an organization and feeds the
// answer's events to onEvent as they arrive. Aborting signal hangs up,
// which also stops the server's model request.
export async function streamChat(
  organizationID: string,
  body: { messages: ChatMessage[]; context: ChatPageContext },
  onEvent: (event: ChatEvent) => void,
  signal?: AbortSignal,
): Promise<void> {
  const res = await apiFetch(buildURL('/api/v1/chat', { organization_id: organizationID }), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
    signal,
  })
  if (!res.ok) throw await apiError(res, 'ШІ-асистент недоступний')
  await readNdjson(res, (event) => onEvent(event as ChatEvent))
}

// readPageContext reads the page's period from the query string at the
// moment a question is sent (pages keep it there, see useRangeParams and
// EconomicsPage).
export function readPageContext(organizationID: string): ChatPageContext {
  const params = new URLSearchParams(window.location.search)
  const get = (key: string) => params.get(key) || undefined
  const floor = energyFloorFor(organizationID)
  return {
    view: get('view') ?? 'dashboard',
    tab: get('tab'),
    preset: get('preset'),
    range: get('range'),
    anchor: get('anchor'),
    from: get('from'),
    to: get('to'),
    operation_start: `${floor.getFullYear()}-${String(floor.getMonth() + 1).padStart(2, '0')}-${String(floor.getDate()).padStart(2, '0')}`,
  }
}
