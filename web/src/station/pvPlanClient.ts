import { apiError, apiFetch, buildURL, type PvPlanSource } from '../api'

// Mirrors internal/pvplan.Orientation: one plane of panels.
export type PvOrientation = {
  idx: number
  tilt_deg: number
  azimuth_deg: number
  modules_count: number
  module_power_w: number
  enabled: boolean
  note?: string
}

// Mirrors internal/pvplan.Station: the PV plan passport. Coordinates
// left null fall back to the organization's location in config.yaml.
export type PvStation = {
  organization_id: string
  source: PvPlanSource
  latitude: number | null
  longitude: number | null
  pac_limit_kw: number
  gamma_pct_per_c: number
  whole_elevator_from?: string
  orientations: PvOrientation[]
  updated_at?: string
  updated_by?: string
  history_backfilled_at?: string
}

// Mirrors internal/pvplan.HistoryStatus: a plan-history rebuild.
export type PvPlanHistoryStatus = {
  running: boolean
  from?: string
  to?: string
  done: number
  total: number
  days: number
  error?: string
  started_at?: string
  finished_at?: string
}

// Mirrors internal/api.PvStationResponse.
export type PvStationResponse = {
  organization_id: string
  station: PvStation | null
  defaults?: { latitude: number; longitude: number }
  pdc_total_kwp: number
  n8n_available: boolean
  today: { date: string; model_kwh?: number; n8n_kwh?: number }
  history_job: PvPlanHistoryStatus
}

// Mirrors internal/pvplan.Change / HistoryEntry.
export type PvStationChange = {
  orientation?: number
  field: string
  from?: unknown
  to?: unknown
}

export type PvStationHistoryEntry = {
  at: string
  by?: string
  changes: PvStationChange[]
}

export async function fetchPvStation(organizationID: string, signal?: AbortSignal): Promise<PvStationResponse> {
  const res = await apiFetch(buildURL('/api/v1/pv-orientations', { organization_id: organizationID }), { signal })
  if (!res.ok) throw await apiError(res, 'pv-orientations request failed')
  return (await res.json()) as PvStationResponse
}

export async function savePvStation(organizationID: string, station: PvStation): Promise<PvStationResponse> {
  const res = await apiFetch(buildURL('/api/v1/pv-orientations', { organization_id: organizationID }), {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(station),
  })
  if (!res.ok) throw await apiError(res, 'не вдалося зберегти паспорт')
  return (await res.json()) as PvStationResponse
}

export async function fetchPvStationHistory(
  organizationID: string,
  signal?: AbortSignal,
): Promise<PvStationHistoryEntry[]> {
  const res = await apiFetch(
    buildURL('/api/v1/pv-orientations/history', { organization_id: organizationID }),
    { signal },
  )
  if (!res.ok) throw await apiError(res, 'pv-orientations history request failed')
  const body = (await res.json()) as { entries: PvStationHistoryEntry[] | null }
  return body.entries ?? []
}

export async function fetchPvPlanHistoryStatus(
  organizationID: string,
  signal?: AbortSignal,
): Promise<PvPlanHistoryStatus> {
  const res = await apiFetch(buildURL('/api/v1/pv-plan/history', { organization_id: organizationID }), { signal })
  if (!res.ok) throw await apiError(res, 'pv-plan history request failed')
  return (await res.json()) as PvPlanHistoryStatus
}

export async function startPvPlanHistory(organizationID: string): Promise<PvPlanHistoryStatus> {
  const res = await apiFetch(buildURL('/api/v1/pv-plan/history', { organization_id: organizationID }), {
    method: 'POST',
  })
  if (!res.ok) throw await apiError(res, 'не вдалося запустити перерахунок')
  return (await res.json()) as PvPlanHistoryStatus
}
