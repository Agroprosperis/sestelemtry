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

export type AdviceVariant = {
  id: 'A' | 'B' | 'C'
  label: string
  feasible: boolean
  why?: string
  cost_uah: number | null
  fuel_l: number
  stops: number
  end_soc_pct: number
  switch_at?: string
  notes?: string[]
}

export type Advice = {
  variants: AdviceVariant[]
  recommended?: string
  why?: string
  limit_load_kw?: number
}

export type OffgridAvailable = {
  island_kw: number
  generator_kw: number
  autonomy_now_min?: number
  autonomy_plan_min?: number
  runs_out_at?: string
  start_generator_by?: string
}

export type OffgridAdviceResponse = {
  site_id: string
  now: string
  timezone: string
  live: { online: boolean; mode?: string; soc_pct?: number }
  available?: OffgridAvailable
  outage?: {
    start: string
    end: string
    kind: string
    final: boolean
    soc_pct: number
    recharge_uah_per_kwh: number
    recharge_estimated: boolean
    advice: Advice
  }
  unscheduled?: { hours: number; advice: Advice }[]
  gen_charge?: { target_kw: number; why: string; gen_charge_uah_per_kwh: number; recharge_uah_per_kwh: number }
}

export async function fetchOffgridAdvice(site: string, signal?: AbortSignal): Promise<OffgridAdviceResponse> {
  const res = await apiFetch(buildURL('/api/v1/offgrid/advice', { site_id: site }), { signal })
  if (!res.ok) throw new Error('поради без мережі: ' + res.status)
  return (await res.json()) as OffgridAdviceResponse
}

export type OutageRecord = {
  scheduled_start?: string
  scheduled_end?: string
  kind: string
  happened: boolean
  start?: string
  end?: string
  sequence: string[] | null
  minutes_generator: number
  minutes_island: number
  minutes_blackout: number
  stops: number
  gen_kwh: number
  gen_below_min_minutes: number
  fuel_l: number
  fuel_uah: number
  ess_kwh: number
  soc_start_pct?: number
  soc_end_pct?: number
  reserve_target_pct?: number
  load_plan_kwh?: number
  load_fact_kwh: number
  deviation_pct?: number
  cost_fact_uah: number
  advised?: string
  variants?: AdviceVariant[]
}

export type OutagesResponse = {
  site_id: string
  from: string
  to: string
  timezone: string
  records: OutageRecord[]
  summary: {
    outages: number
    unscheduled: number
    hours_offgrid: number
    fuel_l: number
    fuel_uah: number
    cost_fact_uah: number
    cost_advised_uah: number
    underestimate_pct?: number
    with_plan: number
  }
  error_margin_pct: number
}

export async function fetchOffgridOutages(site: string, signal?: AbortSignal): Promise<OutagesResponse> {
  const res = await apiFetch(buildURL('/api/v1/offgrid/outages', { site_id: site }), { signal })
  if (!res.ok) throw new Error('відключення: ' + res.status)
  return (await res.json()) as OutagesResponse
}
