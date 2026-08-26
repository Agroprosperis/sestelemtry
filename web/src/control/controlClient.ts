// Feature-local API client for the «Керування» mode (mirrors
// internal/api/edge_fleet_handlers.go and the settings / manual-publish
// endpoints in internal/api/edge_plan_handlers.go + edge_planner.go).

import { buildURL, withBase } from '../api'

export type EdgeHeartbeatInfo = {
  online: boolean
  updated_at?: string
  age_seconds?: number
  edge_id?: string
  status?: string
  buffer_pending: number
  last_sl_poll_ok?: string
  firmware?: string
}

export type ManifestPlanInterval = {
  ts: string
  ess_kw: number
  soc_target_pct?: number
  action?: string
  rdn_uah_per_kwh?: number
}

export type ManifestPayload = {
  schema_version: string
  manifest_id: string
  site_id: string
  issued_at: string
  valid_from: string
  valid_until: string
  mode: string
  write_enabled: boolean
  preset: string
  source?: string
  note?: string
  limits?: { ess_charge_max_kw?: number; ess_discharge_max_kw?: number }
  grid_limits?: { import_limit_kw?: number; target_import_kw?: number; pv_rated_kw?: number }
  soc_policy?: { min_economic_pct?: number; max_economic_pct?: number }
  plan?: { granularity: string; load_source?: string; intervals: ManifestPlanInterval[] | null }
}

export type EdgeManifestState = 'none' | 'pending' | 'applied' | 'expired'

export type EdgeManifestStatus = {
  state: EdgeManifestState
  manifest_id?: string
  issued_at?: string
  valid_until?: string
  applied_at?: string
  payload?: ManifestPayload
}

export type EdgeDecisionRecord = {
  site_id: string
  ts: string
  mode: string
  preset: string
  state_machine: string
  plan_source: string
  inputs?: {
    soc_percent?: number
    pv_power_kw?: number
    ess_power_kw?: number
    grid_power_kw?: number
    load_power_kw?: number
    p_bess_plan_kw?: number
    data_quality?: string
  }
  outputs?: {
    p_bess_virtual_kw?: number
    p_pv_limit_virtual_kw?: number
  }
  reason_code?: string
  rationale?: string
}

export type EdgeDecisionStatus = {
  at: string
  age_seconds: number
  record: EdgeDecisionRecord
}

export type EdgeEventInfo = {
  time: string
  severity: string
  code: string
  message: string
  context?: Record<string, unknown>
}

export type EdgeSiteStatus = {
  site_id: string
  heartbeat: EdgeHeartbeatInfo
  manifest: EdgeManifestStatus
  decision?: EdgeDecisionStatus
  events?: EdgeEventInfo[]
}

export type EdgeFleet = {
  now: string
  sites: EdgeSiteStatus[]
}

export type EdgeSiteSettings = {
  soc_target_pct?: number
  soc_reserve_pct?: number
  auto_charge_max_kw?: number
  auto_discharge_max_kw?: number
  island_charge_max_kw?: number
  island_discharge_max_kw?: number
  grid_import_kw?: number
  grid_target_kw?: number
  pv_rated_kw?: number
}

export type ManualInterval = {
  ts: string // UTC hour start, RFC3339
  ess_kw: number // + розряд / − заряд
  soc_target_pct?: number
}

export type ManualPublishRequest = {
  ttl_hours?: number
  preset?: string
  note?: string
  cancel?: boolean
  intervals?: ManualInterval[]
}

export type PublishResult = {
  site_id: string
  manifest_id: string
  published: boolean
  intervals: number
  load_source: string
  valid_until: string
  skipped?: string
  source?: string
}

async function ensureOK(res: Response, what: string): Promise<Response> {
  if (!res.ok) {
    const body = await res.text().catch(() => '')
    throw new Error(`${what}: ${res.status}${body ? ` — ${body.trim()}` : ''}`)
  }
  return res
}

export async function fetchEdgeStatus(
  siteID: string,
  events = 30,
  signal?: AbortSignal,
): Promise<EdgeSiteStatus> {
  const res = await ensureOK(
    await fetch(buildURL('/api/v1/edge/status', { site_id: siteID, events: String(events) }), {
      signal,
    }),
    'edge status',
  )
  return (await res.json()) as EdgeSiteStatus
}

export async function fetchEdgeFleet(signal?: AbortSignal): Promise<EdgeFleet> {
  const res = await ensureOK(await fetch(withBase('/api/v1/edge/fleet'), { signal }), 'edge fleet')
  return (await res.json()) as EdgeFleet
}

export async function fetchEdgeSettings(
  siteID: string,
  signal?: AbortSignal,
): Promise<{ saved: boolean; settings: EdgeSiteSettings }> {
  const res = await ensureOK(
    await fetch(buildURL('/api/v1/edge/settings', { site_id: siteID }), { signal }),
    'edge settings',
  )
  return (await res.json()) as { saved: boolean; settings: EdgeSiteSettings }
}

export async function saveEdgeSettings(
  siteID: string,
  settings: EdgeSiteSettings,
): Promise<void> {
  await ensureOK(
    await fetch(buildURL('/api/v1/edge/settings', { site_id: siteID }), {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(settings),
    }),
    'save edge settings',
  )
}

export async function publishManualManifest(
  siteID: string,
  req: ManualPublishRequest,
): Promise<PublishResult> {
  const res = await ensureOK(
    await fetch(buildURL('/api/v1/edge/manifest/publish-manual', { site_id: siteID }), {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(req),
    }),
    'publish manual manifest',
  )
  return (await res.json()) as PublishResult
}

export async function publishAutoManifest(siteID: string): Promise<PublishResult> {
  const res = await ensureOK(
    await fetch(buildURL('/api/v1/edge/manifest/publish', { site_id: siteID }), {
      method: 'POST',
    }),
    'publish manifest',
  )
  return (await res.json()) as PublishResult
}
