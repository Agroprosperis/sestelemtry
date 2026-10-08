// Feature-local API client for the manual-control desk (mirrors
// internal/api/dispatch_handlers.go and internal/dispatch).

import { apiFetch, apiError, buildURL } from '../api'

export type CommandType = 'cover' | 'solar' | 'target' | 'cap' | 'export' | 'fixed' | 'hold' | 'auto'

export type Command = {
  type: CommandType
  value: number
  direction?: 'charge' | 'discharge'
  id?: string
}

export type Constraints = {
  reserve_pct: number
  grid_charge: boolean
  ess_sale: boolean
  block_export: boolean
  import_cap_kw: number
  export_cap_kw: number | null
}

export type HourResult = {
  p_kw: number | null
  wanted_kw: number | null
  soc_pct: number | null
  soc_before_pct: number | null
  import_kw: number | null
  export_kw: number | null
  curtailed_kw: number | null
  charge_kw: number
  discharge_kw: number
  reasons: string[]
  unknown: boolean
}

export type Issue = {
  hour: number
  text: string
  wanted_kw: number | null
  p_kw: number
  source: 'manual' | 'network'
  blocking: boolean
}

export type SimResult = {
  hours: HourResult[]
  issues: Issue[]
  known_hours: number
  end_soc_pct: number | null
  min_soc_pct: number | null
  peak_import_kw: number | null
}

export type SiteInfo = {
  capacity_kwh: number
  charge_kw: number
  discharge_kw: number
  import_kw: number
  import_set: boolean
  soc_min_pct: number
  soc_max_pct: number
  eta: number
  pv_rated_kw: number
  export_regime: string
  export_ceiling_kw: number | null
}

export type Tariffs = {
  distribution_uah_per_kwh: number
  transmission_uah_per_kwh: number
  supplier_margin_uah_per_kwh: number
  supplier_margin_mode: string
  supplier_margin_pct: number
  other_fees_uah_per_kwh: number
  export_discount: number
  degradation_uah_per_kwh: number
  include_vat: boolean
  vat_rate: number
  roundtrip_efficiency: number
}

export type Fact = {
  pv_kw: number | null
  load_kw: number | null
  grid_kw: number | null
  ess_kw: number | null
  soc_pct: number | null
}

export type PlanHour = {
  ts: string
  load_kw: number
  pv_kw: number
  ess_kw: number
  soc_pct: number
  import_kw: number
  export_kw: number
  curtailed_kw: number
  command?: Command
}

export type DeskHour = {
  ts: string
  offset: number
  rdn_uah_per_kwh: number | null
  buy_uah_per_kwh: number | null
  sell_uah_per_kwh: number | null
  pv_kw: number
  fact?: Fact
  plan?: PlanHour
}

export type WireModel = {
  loads: (number | null)[]
  commands: (Command | null)[]
  cfg: Constraints
}

export type DeskState = {
  site_id: string
  timezone: string
  now: string
  start_hour: string
  history_hours: number
  future_hours: number
  site: SiteInfo
  tariffs: Tariffs
  start_soc_pct: number
  start_soc_known: boolean
  hours: DeskHour[]
  defaults: Constraints
  applied: {
    version: number
    confirmed_at: string | null
    confirmed_by: string
    model: WireModel
  }
  result: SimResult
}

export type PreviewResponse = {
  start_hour: string
  invalid?: string
  result?: SimResult
}

export type ConfirmResponse = {
  version: number
  publish?: { manifest_id: string; published: boolean; intervals: number }
  invalid?: string
  issues?: Issue[]
  conflict?: boolean
  message?: string
}

export type DayResponse = {
  site_id: string
  timezone: string
  date: string
  site: SiteInfo
  hours: {
    ts: string
    rdn_uah_per_kwh: number | null
    buy_uah_per_kwh: number | null
    sell_uah_per_kwh: number | null
    fact?: Fact
    plan?: PlanHour
  }[]
}

async function ensureOK(res: Response, what: string): Promise<Response> {
  if (!res.ok) throw await apiError(res, what)
  return res
}

export async function fetchDeskState(site: string, signal?: AbortSignal): Promise<DeskState> {
  const res = await ensureOK(await apiFetch(buildURL('/api/v1/dispatch/state', { site_id: site }), { signal }), 'стан пульта')
  return (await res.json()) as DeskState
}

export type DraftBody = WireModel & { start_hour: string; base_version: number }

export async function previewDraft(site: string, body: DraftBody, signal?: AbortSignal): Promise<PreviewResponse> {
  const res = await ensureOK(
    await apiFetch(buildURL('/api/v1/dispatch/preview', { site_id: site }), {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
      signal,
    }),
    'розрахунок плану',
  )
  return (await res.json()) as PreviewResponse
}

// confirmDraft answers 422 (shortfalls / invalid input) and 409 (someone
// confirmed another version) with a JSON body the desk shows as is.
export async function confirmDraft(site: string, body: DraftBody): Promise<{ status: number; body: ConfirmResponse }> {
  const res = await apiFetch(buildURL('/api/v1/dispatch/confirm', { site_id: site }), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
  if (res.status === 200 || res.status === 409 || res.status === 422) {
    return { status: res.status, body: (await res.json()) as ConfirmResponse }
  }
  throw await apiError(res, 'підтвердження плану')
}

export async function fetchDeskDay(site: string, date: string, signal?: AbortSignal): Promise<DayResponse> {
  const res = await ensureOK(
    await apiFetch(buildURL('/api/v1/dispatch/day', { site_id: site, date }), { signal }),
    'день',
  )
  return (await res.json()) as DayResponse
}
