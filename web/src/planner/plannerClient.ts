// Feature-local API client for the day-planner page (mirrors
// internal/api/edge_plan_handlers.go). Kept out of the shared api.ts
// the same way economics/orgTariffsClient.ts is: the planner is the
// only consumer of these endpoints.

import { buildURL, withBase } from '../api'
import type { TimeseriesResponse } from '../types'

export type LoadPlanEntry = {
  ts: string // UTC hour start, RFC3339
  load_kw: number
}

export type PlanPreviewHour = {
  ts: string
  local_hour: number
  tomorrow: boolean
  tradable: boolean
  rdn_uah_per_kwh?: number
  import_uah_per_kwh: number
  export_uah_per_kwh: number
  pv_kw: number
  load_kw: number
  operator_load: boolean
  ess_kw: number
  charge_pv_kwh: number
  charge_grid_kwh: number
  discharge_kwh: number
  grid_kw: number
  soc_end_pct: number
  action: string
}

export type PlanDayEffect = {
  date: string
  tomorrow: boolean
  ess_to_load_uah: number
  pv_charge_cost_uah: number
  grid_charge_cost_uah: number
  degradation_uah: number
  flows_uah: number
  soc_open_pct: number
  soc_close_pct: number
  soc_carry_uah: number
  net_effect_uah: number
  baseline_cost_uah: number
  plan_cost_uah: number
  ess_to_load_kwh: number
  charge_pv_kwh: number
  charge_grid_kwh: number
}

export type PlanPreview = {
  site_id: string
  timezone: string
  now: string
  horizon_start: string
  horizon_end: string
  tomorrow_start: string
  load_source: string
  params: {
    capacity_kwh: number
    power_kw: number
    pv_rated_kw: number
    soc_min_pct: number
    soc_max_pct: number
    start_soc_pct: number
    degradation_uah_per_kwh: number
  }
  hours: PlanPreviewHour[]
  days: PlanDayEffect[]
}

export type ManifestJournalRow = {
  manifest_id: string
  issued_at: string
  valid_from?: string
  valid_until?: string
  preset: string
  load_source?: string
  intervals: number
  status: 'applied' | 'rejected' | 'pending'
  applied_at?: string
  rejected_at?: string
}

export type ManifestJournal = {
  site_id: string
  manifests: ManifestJournalRow[] | null
  heartbeat_at?: string | null
  heartbeat?: string
}

export type PublishResult = {
  site_id: string
  manifest_id: string
  published: boolean
  intervals: number
  load_source: string
  valid_until: string
}

async function ensureOK(res: Response, what: string): Promise<Response> {
  if (!res.ok) {
    const body = await res.text().catch(() => '')
    throw new Error(`${what}: ${res.status}${body ? ` — ${body.trim()}` : ''}`)
  }
  return res
}

export async function fetchEdgeSites(signal?: AbortSignal): Promise<string[]> {
  const res = await ensureOK(
    await fetch(withBase('/api/v1/edge/sites'), { signal }),
    'edge sites',
  )
  const body = (await res.json()) as { sites: string[] | null }
  return body.sites ?? []
}

export async function fetchLoadPlan(siteID: string, signal?: AbortSignal): Promise<LoadPlanEntry[]> {
  const res = await ensureOK(
    await fetch(buildURL('/api/v1/edge/load-plan', { site_id: siteID }), { signal }),
    'load plan',
  )
  const body = (await res.json()) as { entries: LoadPlanEntry[] | null }
  return body.entries ?? []
}

export async function saveLoadPlan(siteID: string, entries: LoadPlanEntry[]): Promise<void> {
  await ensureOK(
    await fetch(buildURL('/api/v1/edge/load-plan', { site_id: siteID }), {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ entries }),
    }),
    'save load plan',
  )
}

export async function clearLoadPlan(siteID: string): Promise<void> {
  await ensureOK(
    await fetch(buildURL('/api/v1/edge/load-plan', { site_id: siteID }), { method: 'DELETE' }),
    'clear load plan',
  )
}

export async function fetchPlanPreview(
  siteID: string,
  draft: LoadPlanEntry[],
  signal?: AbortSignal,
): Promise<PlanPreview> {
  const res = await ensureOK(
    await fetch(buildURL('/api/v1/edge/plan/preview', { site_id: siteID }), {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ draft }),
      signal,
    }),
    'plan preview',
  )
  return (await res.json()) as PlanPreview
}

export async function publishManifest(siteID: string): Promise<PublishResult> {
  const res = await ensureOK(
    await fetch(buildURL('/api/v1/edge/manifest/publish', { site_id: siteID }), {
      method: 'POST',
    }),
    'publish manifest',
  )
  return (await res.json()) as PublishResult
}

export async function fetchManifestJournal(
  siteID: string,
  signal?: AbortSignal,
): Promise<ManifestJournal> {
  const res = await ensureOK(
    await fetch(buildURL('/api/v1/edge/manifests', { site_id: siteID, limit: '20' }), { signal }),
    'manifest journal',
  )
  return (await res.json()) as ManifestJournal
}

// fetchYesterdayLoadByHour returns yesterday's measured load (kW,
// hourly average) keyed by local hour 0..23 — backs the editor's
// «Заповнити з учора (факт)» action.
export async function fetchYesterdayLoadByHour(
  siteID: string,
  timezone: string,
  signal?: AbortSignal,
): Promise<Map<number, number>> {
  const now = new Date()
  const from = new Date(now.getTime() - 48 * 3600_000)
  const url = buildURL('/api/v1/timeseries', {
    organization_id: siteID,
    metric_keys: 'load_power_kw',
    from: from.toISOString(),
    to: now.toISOString(),
    bucket: '1h',
    tz: timezone,
  })
  const res = await ensureOK(await fetch(url, { signal }), 'yesterday load')
  const body = (await res.json()) as TimeseriesResponse
  const out = new Map<number, number>()
  const fmt = new Intl.DateTimeFormat('en-GB', {
    timeZone: timezone,
    hour: '2-digit',
    hour12: false,
  })
  for (const p of body.points ?? []) {
    if (p.metric_key !== 'load_power_kw' || !Number.isFinite(p.value)) continue
    const hour = Number(fmt.format(new Date(p.time)))
    if (!Number.isFinite(hour)) continue
    // Later (yesterday's) points overwrite the older ones from two
    // days ago, so each local hour ends up with the freshest sample.
    out.set(hour, Math.round(p.value * 10) / 10)
  }
  return out
}
