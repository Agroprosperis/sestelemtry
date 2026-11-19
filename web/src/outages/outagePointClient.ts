import { apiError, apiFetch, buildURL } from '../api'
import type { OutageInterval } from './outageSettingsClient'

export type { OutageInterval } from './outageSettingsClient'

export type OutageOperator = {
  host: string
  name: string
}

export type OutageReadStatus = {
  attempted_at?: string
  succeeded_at?: string
  error?: string
}

// Mirrors the /api/v1/outage-point response: the organization's EIC (the
// supply point's code) and the operator whose cabinet it belongs to.
export type OutagePointResponse = {
  organization_id: string
  eic: string
  host: string
  operators: OutageOperator[]
  read?: OutageReadStatus
  saved: boolean
}

const POINT_PATH = '/api/v1/outage-point'

export async function fetchOutagePoint(organizationID: string, signal?: AbortSignal): Promise<OutagePointResponse> {
  const res = await apiFetch(buildURL(POINT_PATH, { organization_id: organizationID }), { signal })
  if (!res.ok) throw await apiError(res, 'Не вдалося завантажити точку обліку')
  return (await res.json()) as OutagePointResponse
}

// saveOutagePoint writes the point; an empty eic clears it.
export async function saveOutagePoint(
  organizationID: string,
  point: { eic: string; host: string },
): Promise<OutagePointResponse> {
  const res = await apiFetch(buildURL(POINT_PATH, { organization_id: organizationID }), {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(point),
  })
  if (!res.ok) throw await apiError(res, 'Не вдалося зберегти точку обліку')
  return (await res.json()) as OutagePointResponse
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
