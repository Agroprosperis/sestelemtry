// Client of the cloud's work without the grid (internal/api
// offgrid_handlers.go, ems_offgrid_advice.md): hints only.

import { apiFetch, buildURL } from '../api'

export type LoadSource = 'plan' | 'avg7d' | 'none' | 'mixed'

export type OutageHourNeed = { ts: string; load_kw: number | null; source: LoadSource; fraction: number }

export type OutageReserve = {
  start: string
  end: string
  kind: string
  hours: OutageHourNeed[]
  source: LoadSource
  error_margin_pct: number
  need_kwh: number | null
  target_pct: number
  short: boolean
  runs_out_at?: string
}

export type OffgridSettings = {
  apply: boolean
  island_soc_floor_pct: number
  outage_reserve_max_pct: number
  outage_error_margin_pct: number
  extra_stop_threshold_uah: number
  generator_min_kw: number
  generator_target_kw: number
  generator_rated_kw: number
  island_power_max_kw: number
  generator_switch_soc_pct: number
}

export type OffgridReserveResponse = {
  site_id: string
  now: string
  timezone: string
  settings: OffgridSettings
  reserves: OutageReserve[]
}

export async function fetchOffgridReserve(site: string, signal?: AbortSignal): Promise<OffgridReserveResponse> {
  const res = await apiFetch(buildURL('/api/v1/offgrid/reserve', { site_id: site }), { signal })
  if (!res.ok) throw new Error('запас під відключення: ' + res.status)
  return (await res.json()) as OffgridReserveResponse
}
