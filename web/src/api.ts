import type { ElevatorCode } from './dashboard/transforms/pvForecast'
import {
  type WeatherForecastApiResponse,
  weatherFromApi,
} from './dashboard/transforms/weatherAdapter'
import type {
  CurrentResponse,
  DAMPricesResponse,
  DashboardConfig,
  EnergyFlowHourlyResponse,
  OpenMeteoForecast,
  OrganizationsResponse,
  PlantInventory,
  PlantInventoryHistory,
  PvForecastPoint,
  RegistersResponse,
  TimeseriesResponse,
} from './types'

const API_BASE = ((import.meta.env.VITE_API_BASE_URL as string | undefined) || '').replace(/\/+$/, '')

// withBase prefixes a path with the configured API base (empty in
// dev / SPA-served-by-API setups). Exported so feature-local API
// clients (e.g. economics/orgTariffsClient.ts) can compose URLs the
// same way without duplicating the env wiring.
export function withBase(path: string): string {
  if (!API_BASE) return path
  return `${API_BASE}${path}`
}

// buildURL appends only truthy query params so callers can pass
// optional values as `undefined` without manually filtering. Public
// for the same reason as withBase.
export function buildURL(path: string, params: Record<string, string | undefined>) {
  const url = new URL(withBase(path), window.location.origin)
  for (const [k, v] of Object.entries(params)) {
    if (!v) continue
    url.searchParams.set(k, v)
  }
  return url.toString()
}

export async function fetchDashboardConfig(signal?: AbortSignal): Promise<DashboardConfig> {
  const res = await fetch(withBase('/api/v1/dashboard-config'), { signal })
  if (!res.ok) {
    throw new Error(`dashboard-config request failed: ${res.status}`)
  }
  return res.json()
}

let organizationsCache: Promise<OrganizationsResponse> | null = null

// fetchOrganizations returns the public org metadata (id, display
// name, optional location). Memoized at module level because the
// backend serves a static list derived from YAML config — no need to
// re-fetch on every dashboard mount or every weather card render. A
// failed first attempt clears the cache so a transient hiccup at boot
// doesn't poison every later request.
export async function fetchOrganizations(signal?: AbortSignal): Promise<OrganizationsResponse> {
  if (organizationsCache) return organizationsCache
  organizationsCache = (async () => {
    const res = await fetch(withBase('/api/v1/organizations'), { signal })
    if (!res.ok) {
      throw new Error(`organizations request failed: ${res.status}`)
    }
    return (await res.json()) as OrganizationsResponse
  })().catch((e) => {
    organizationsCache = null
    throw e
  })
  return organizationsCache
}

// resetOrganizationsCache is used by the test suite to drop the
// memoized response between cases. Production code should never call
// this — refreshing the static map mid-session has no observable
// benefit.
export function resetOrganizationsCache(): void {
  organizationsCache = null
}

// fetchPlantInventory returns the newest plant-passport snapshot for an
// organization. 404 (no snapshot yet) maps to null so the station page can
// show an empty state without treating it as a hard error.
export async function fetchPlantInventory(
  organizationID: string,
  signal?: AbortSignal,
): Promise<PlantInventory | null> {
  const url = buildURL('/api/v1/plant-inventory', {
    organization_id: organizationID,
  })
  const res = await fetch(url, { signal })
  if (res.status === 404) {
    return null
  }
  if (!res.ok) {
    throw new Error(`plant-inventory request failed: ${res.status}`)
  }
  return (await res.json()) as PlantInventory
}

// fetchPlantInventoryHistory returns per-field change events derived from
// recent plant-inventory snapshots (identical polls are filtered out).
export async function fetchPlantInventoryHistory(
  organizationID: string,
  opts?: { limit?: number; signal?: AbortSignal },
): Promise<PlantInventoryHistory> {
  const url = buildURL('/api/v1/plant-inventory/history', {
    organization_id: organizationID,
    limit: opts?.limit != null ? String(opts.limit) : undefined,
  })
  const res = await fetch(url, { signal: opts?.signal })
  if (!res.ok) {
    throw new Error(`plant-inventory history request failed: ${res.status}`)
  }
  return (await res.json()) as PlantInventoryHistory
}

let registersCache: Promise<RegistersResponse> | null = null

// fetchRegisters returns the metric_key → Modbus metadata map. The
// promise is memoized at module level because the data is static (a
// hand-maintained mirror of registers/huawei_smartlogger.yaml on the
// API server) and the export dialog calls it on every open. Cache
// failures are not retained — a network blip on the first attempt
// shouldn't poison every subsequent export.
export async function fetchRegisters(signal?: AbortSignal): Promise<RegistersResponse> {
  if (registersCache) return registersCache
  registersCache = (async () => {
    const res = await fetch(withBase('/api/v1/registers'), { signal })
    if (!res.ok) {
      throw new Error(`registers request failed: ${res.status}`)
    }
    return (await res.json()) as RegistersResponse
  })().catch((e) => {
    registersCache = null
    throw e
  })
  return registersCache
}

// resetRegistersCache is only used by the test suite to drop the
// memoized response between cases. Production code should never call
// this — refreshing the static map mid-session has no observable
// benefit.
export function resetRegistersCache(): void {
  registersCache = null
}

export async function fetchCurrent(
  input: string | { organizationID: string; at?: string },
  signal?: AbortSignal,
): Promise<CurrentResponse> {
  const params = typeof input === 'string' ? { organizationID: input } : input
  const url = buildURL('/api/v1/current', {
    organization_id: params.organizationID,
    at: params.at,
  })
  const res = await fetch(url, { signal })
  if (!res.ok) {
    throw new Error(`current request failed: ${res.status}`)
  }
  return res.json()
}

export async function fetchTimeseries(
  input: {
    organizationID: string
    metricKeys: string[]
    from: string
    to: string
    bucket: string
    tz?: string
    aggregation?: 'delta' | 'avg' | 'last'
  },
  signal?: AbortSignal,
): Promise<TimeseriesResponse> {
  const url = buildURL('/api/v1/timeseries', {
    organization_id: input.organizationID,
    metric_keys: input.metricKeys.join(','),
    from: input.from,
    to: input.to,
    bucket: input.bucket,
    tz: input.tz || Intl.DateTimeFormat().resolvedOptions().timeZone || undefined,
    aggregation: input.aggregation,
  })
  const res = await fetch(url, { signal })
  if (!res.ok) {
    throw new Error(`timeseries request failed: ${res.status}`)
  }
  return res.json()
}

// EnergyFlowTotals mirrors `internal/api/types.go:EnergyFlowTotals`.
// The API returns this object as `flows` only when the caller
// requested at least one synthetic flow key AND the [from, to]
// window is inside the on-the-fly compute budget (currently
// day-sized). When the field is absent the dashboard knows the
// allocator did not run — distinct from "ran and got zero".
export type EnergyFlowTotals = {
  pv_to_ess_kwh: number
  grid_to_ess_kwh: number
  ess_to_load_kwh: number
  ess_to_grid_kwh: number
}

export type EnergySummaryResponse = {
  organization_id: string
  from: string
  to: string
  totals: Record<string, number>
  flows?: EnergyFlowTotals | null
}

export async function fetchEnergySummary(
  input: {
    organizationID: string
    from: string
    to: string
    metricKeys?: string[]
  },
  signal?: AbortSignal,
): Promise<EnergySummaryResponse> {
  const url = buildURL('/api/v1/energy-summary', {
    organization_id: input.organizationID,
    from: input.from,
    to: input.to,
    metric_keys: input.metricKeys && input.metricKeys.length > 0 ? input.metricKeys.join(',') : undefined,
  })
  const res = await fetch(url, { signal })
  if (!res.ok) {
    throw new Error(`energy-summary request failed: ${res.status}`)
  }
  return res.json()
}

// The four synthetic `*_to_*_kwh` keys (pv_to_ess, grid_to_ess,
// ess_to_load, ess_to_grid) are NOT stored in the database. The
// `/api/v1/energy-summary` handler computes them on the fly from the
// raw Modbus accumulators for every request, so there is no
// "recompute" endpoint and no DB state to drift: re-querying any
// historical period always returns the same numbers, and a fresh
// deployment renders flows immediately without an operator restart
// or backfill. See `internal/api/handlers.go:computeEnergyFlowTotals`.

// rawSamplesZipURL builds the /api/v1/samples download URL with
// `format=zip`, so the browser saves a compact streamed `.zip` straight
// to disk. Used for the raw export instead of fetch+pivot: a multi-week
// pull is gigabytes uncompressed and can't be held in browser memory,
// but a native download streams to disk with the gzip-class size win.
export function rawSamplesZipURL(input: {
  organizationID: string
  metricKeys: string[]
  from: string
  to: string
  tz?: string
}): string {
  return buildURL('/api/v1/samples', {
    organization_id: input.organizationID,
    metric_keys: input.metricKeys.join(','),
    from: input.from,
    to: input.to,
    tz: input.tz,
    format: 'zip',
  })
}

// fetchRawSamplesZip downloads the server-built `.zip` (format=zip)
// while reporting progress. We read the response as a stream and sum
// the received bytes so the dialog can show a live "downloaded X MB"
// status — a plain <a download> gives no progress and looked like
// "nothing happened" on a slow multi-minute pull. The body is the
// already-compressed archive (no client-side decompression), so memory
// stays at the zip size, an order of magnitude below the raw CSV.
export async function fetchRawSamplesZip(
  input: {
    organizationID: string
    metricKeys: string[]
    from: string
    to: string
    tz?: string
  },
  opts?: { signal?: AbortSignal; onProgress?: (bytes: number) => void },
): Promise<{ blob: Blob; filename: string }> {
  const res = await fetch(rawSamplesZipURL(input), { signal: opts?.signal })
  if (!res.ok) {
    const body = await res.text().catch(() => '')
    const trimmed = body.trim()
    throw new Error(
      `samples zip request failed: ${res.status}${trimmed ? ` ${trimmed}` : ''}`,
    )
  }
  const cd = res.headers.get('content-disposition') || ''
  const m = /filename="?([^";]+)"?/i.exec(cd)
  const filename = m ? m[1] : 'samples.csv.zip'

  // Fall back to a buffered read when the stream reader is unavailable
  // (older browsers / no res.body) — no progress, but the download
  // still works.
  if (!res.body) {
    const blob = await res.blob()
    opts?.onProgress?.(blob.size)
    return { blob, filename }
  }

  const reader = res.body.getReader()
  const chunks: Uint8Array[] = []
  let received = 0
  let lastTick = 0
  for (;;) {
    const { done, value } = await reader.read()
    if (done) break
    if (!value) continue
    chunks.push(value)
    received += value.byteLength
    // Throttle progress callbacks so a 1 s-cadence stream of small
    // chunks doesn't thrash React state on every packet.
    const now = Date.now()
    if (now - lastTick > 200) {
      lastTick = now
      opts?.onProgress?.(received)
    }
  }
  opts?.onProgress?.(received)
  return { blob: new Blob(chunks as BlobPart[], { type: 'application/zip' }), filename }
}

export type RawSamplesResult = {
  // Pre-formatted CSV body, including the UTF-8 BOM and trailing
  // truncation sentinel (when present). The caller is expected to
  // hand this directly to a Blob/`<a download>` flow — we don't
  // re-serialize it because the server already runs through Go's
  // RFC4180-compliant csv.Writer.
  text: string
  filename: string
  truncated: boolean
  // rows excludes the header row and the trailing __TRUNCATED__
  // sentinel so the dashboard can render an honest "X rows exported"
  // counter to the analyst.
  rows: number
}

const RAW_SAMPLES_TRUNCATION_PREFIX = '__TRUNCATED__,'

// fetchRawSamplesCsv pulls the raw `telemetry_samples` rows from
// /api/v1/samples as one streamed CSV response. Unlike the bucketed
// /api/v1/timeseries path, the body is plain text/csv that we hand
// directly to the user — no JSON re-parse, no per-row aggregation.
//
// We post-process the body just enough to detect the server's
// `__TRUNCATED__` sentinel (last non-empty line) so the dialog can
// warn the analyst that the result was capped. HTTP trailers would
// be the cleaner channel but the browser Fetch API doesn't surface
// them, so the in-band sentinel is what survives the round-trip.
export async function fetchRawSamplesCsv(
  input: {
    organizationID: string
    metricKeys: string[]
    from: string
    to: string
    limit?: number
    // tz is the IANA name passed through to /api/v1/samples so the
    // CSV's `time` column is rendered in that zone instead of UTC.
    // Empty / undefined falls back to the server default (UTC) for
    // backwards compatibility with anything that previously called
    // this without a tz parameter.
    tz?: string
  },
  signal?: AbortSignal,
): Promise<RawSamplesResult> {
  const url = buildURL('/api/v1/samples', {
    organization_id: input.organizationID,
    metric_keys: input.metricKeys.join(','),
    from: input.from,
    to: input.to,
    limit: input.limit !== undefined ? String(input.limit) : undefined,
    tz: input.tz,
  })
  const res = await fetch(url, { signal })
  if (!res.ok) {
    const body = await res.text().catch(() => '')
    const trimmed = body.trim()
    throw new Error(
      `samples request failed: ${res.status}${trimmed ? ` ${trimmed}` : ''}`,
    )
  }
  const text = await res.text()
  const cd = res.headers.get('content-disposition') || ''
  const m = /filename="?([^";]+)"?/i.exec(cd)
  const filename = m ? m[1] : 'samples.csv'

  // Strip the BOM before scanning so the truncation prefix matches
  // even when present on the very first line; we keep it on `text`
  // because that's what we hand to the Blob downloader.
  const noBom = text.replace(/^\ufeff/, '')
  const lines = noBom.split(/\r?\n/)
  let lastNonEmpty = lines.length - 1
  while (lastNonEmpty >= 0 && lines[lastNonEmpty].trim() === '') lastNonEmpty--
  const truncated =
    lastNonEmpty > 0 && lines[lastNonEmpty].startsWith(RAW_SAMPLES_TRUNCATION_PREFIX)
  // Header row + 0..N data rows + maybe sentinel; the caller wants
  // the data-row count.
  let rows = Math.max(0, lastNonEmpty)
  if (truncated) rows -= 1
  if (rows < 0) rows = 0

  return { text, filename, truncated, rows }
}

// fetchEnergyFlowHourly hits /api/v1/energy-flow-hourly which
// runs the same on-the-fly Recompute() as /api/v1/energy-summary,
// but partitioned across the 24 hours of the requested calendar day
// in `tz`. Returns a fixed-length array of 24 rows (zero-filled for
// hours with no underlying telemetry); the caller never has to deal
// with sparse indices. See README §"Daily economics" for the
// dashboard-side flow.
export async function fetchEnergyFlowHourly(
  input: {
    organizationID: string
    // ISO calendar day (YYYY-MM-DD) interpreted in `tz`.
    date: string
    tz?: string
  },
  signal?: AbortSignal,
): Promise<EnergyFlowHourlyResponse> {
  const url = buildURL('/api/v1/energy-flow-hourly', {
    organization_id: input.organizationID,
    date: input.date,
    tz: input.tz || Intl.DateTimeFormat().resolvedOptions().timeZone || undefined,
  })
  const res = await fetch(url, { signal })
  if (!res.ok) {
    throw new Error(`energy-flow-hourly request failed: ${res.status}`)
  }
  return res.json()
}

// EconomicsHourApi is one hour of the server-computed economics result
// (flat snake_case, mirroring internal/api.EconomicsHour). Nullable
// fields (no RDN price, missing SOC anchor) arrive as null.
export type EconomicsHourApi = {
  hour: number
  hour_start: string
  rdn_uah_per_kwh: number | null
  pv_kwh: number
  grid_import_kwh: number
  grid_export_kwh: number
  ess_charged_kwh: number
  ess_discharged_kwh: number
  pv_to_ess_kwh: number
  grid_to_ess_kwh: number
  ess_to_load_kwh: number
  ess_to_grid_kwh: number
  load_kwh: number
  pv_to_load_kwh: number
  pv_to_grid_kwh: number
  grid_to_load_kwh: number
  import_price_uah_per_kwh: number
  export_price_uah_per_kwh: number
  baseline_cost_uah: number
  actual_cost_uah: number
  effect_uah: number
  ess_net_uah: number
  ess_remaining_kwh_start: number | null
  ess_cost_basis_uah_start: number | null
  ess_avg_cost_uah_per_kwh_start: number | null
  ess_withdrawn_cost_uah: number | null
  ess_realized_profit_uah: number | null
  ess_cost_basis_uah_end: number | null
  ess_avg_cost_uah_per_kwh_end: number | null
  ess_residual_kwh_end: number | null
}

// EconomicsDailyResponse mirrors internal/api.EconomicsDailyResponse:
// the 24 server-computed hourly economics rows for one day. `hours`
// entries are null for hours with no flow data.
// EconomicsReconcileField is one quantity's reconciliation detail:
// computed daily sum, canonical FusionSolar KPI, and applied scale factor.
export type EconomicsReconcileField = {
  computed: number
  canonical: number
  factor: number
}

export type EconomicsDailyResponse = {
  organization_id: string
  date: string
  tz: string
  is_final: boolean
  hours_missing_price: number
  hours: Array<EconomicsHourApi | null>
  // reconciled is true when the day's flows were scaled to the canonical
  // FusionSolar daily KPIs; quality_flags / reconciliation are diagnostics.
  reconciled?: boolean
  quality_flags?: string[]
  reconciliation?: Record<string, EconomicsReconcileField>
}

// fetchEconomicsDaily reads the server-computed economics for one day.
// The backend serves a final day from cache and recomputes non-final
// (today/recent) days on read, so the dashboard always reads the table.
export async function fetchEconomicsDaily(
  input: { organizationID: string; date: string; tz?: string },
  signal?: AbortSignal,
): Promise<EconomicsDailyResponse> {
  const url = buildURL('/api/v1/economics/daily', {
    organization_id: input.organizationID,
    date: input.date,
    tz: input.tz || undefined,
  })
  const res = await fetch(url, { signal })
  if (!res.ok) {
    const body = await res.text().catch(() => '')
    const suffix = body ? ` — ${body.trim()}` : ''
    throw new Error(`economics/daily request failed: ${res.status}${suffix}`)
  }
  return res.json()
}

// EconomicsAnomalyHour is one excluded УЗЕ hour with classified reasons.
export type EconomicsAnomalyHour = {
  at: string
  date: string
  hour: number
  reasons: string[]
  peak_kw: number
  charged_kwh: number
  discharged_kwh: number
}

// EconomicsDataQuality mirrors internal/api.EconomicsDataQuality — the
// ESS (УЗЕ) anomaly filter outcome: anomalous hours are excluded from the
// fact/optimum/reserve.
export type EconomicsDataQuality = {
  data_ok: boolean
  total_days: number
  anomalous_hours: number
  anomalous_days: number
  anomalous_dates: string[] | null
  anomalies?: EconomicsAnomalyHour[] | null
  reason_counts?: Record<string, number> | null
  max_charge_kwh_per_interval: number
  max_discharge_kwh_per_interval: number
  power_limit_kwh_per_interval: number
  max_interval_power_kw: number
}

// EconomicsMonthlyTotals mirrors internal/api.EconomicsMonthlyTotals —
// the month rollup of the per-day economics.
export type EconomicsMonthlyTotals = {
  baseline_cost_uah: number
  actual_cost_uah: number
  effect_uah: number
  ess_net_uah: number

  load_kwh: number
  pv_kwh: number
  grid_import_kwh: number
  grid_export_kwh: number
  ess_charged_kwh: number
  ess_discharged_kwh: number
  pv_to_load_kwh: number
  pv_to_ess_kwh: number
  pv_to_grid_kwh: number
  grid_to_load_kwh: number
  grid_to_ess_kwh: number
  ess_to_load_kwh: number
  ess_to_grid_kwh: number

  avg_import_price_uah_per_kwh: number
  avg_export_price_uah_per_kwh: number
  rdn_avg_uah_per_kwh: number
  rdn_max_uah_per_kwh: number

  revenue_pv_export_uah: number
  revenue_pv_self_uah: number
  revenue_ess_export_uah: number
  revenue_ess_self_uah: number
  revenue_total_uah: number
  expense_grid_charge_uah: number
  expense_total_uah: number
  ebitda_uah: number

  ess_withdrawn_cost_uah: number
  ess_realized_profit_uah: number
  ess_degradation_cost_uah: number
  ess_avg_cost_basis_uah_per_kwh_eod: number
  ess_residual_kwh_eod: number
  ess_cost_basis_uah_eod: number

  equivalent_cycles: number
  days_with_data: number
  hours_with_data: number
  hours_missing_price: number

  ess_fact_uah: number
  ess_optimum_uah: number
  ess_reserve_uah: number
  ess_captured_share: number
  ess_reserve_timing_uah: number
  ess_reserve_soc_uah: number
  ess_reserve_pv_uah: number
  ess_pv_missed_kwh: number

  ess_data_quality: EconomicsDataQuality

  best_day: { date: string; effect_uah: number }
  min_effect_day: { date: string; effect_uah: number }
}

// EconomicsMonthlyDay is one day of the month breakdown.
export type EconomicsMonthlyDay = {
  date: string
  is_final: boolean
  rdn_avg_uah_per_kwh: number
  equivalent_cycles: number

  baseline_cost_uah: number
  actual_cost_uah: number
  effect_uah: number
  ess_net_uah: number
  ebitda_uah: number

  ess_fact_uah: number
  ess_optimum_uah: number
  ess_reserve_uah: number
  ess_reserve_timing_uah: number
  ess_reserve_soc_uah: number
  ess_reserve_pv_uah: number
  ess_pv_missed_kwh: number

  load_kwh: number
  pv_kwh: number
  grid_import_kwh: number
  grid_export_kwh: number
  ess_charged_kwh: number
  ess_discharged_kwh: number
  pv_to_load_kwh: number
  pv_to_ess_kwh: number
  pv_to_grid_kwh: number
  grid_to_load_kwh: number
  grid_to_ess_kwh: number
  ess_to_load_kwh: number
  ess_to_grid_kwh: number

  hours_with_data: number
  hours_missing_price: number
}

// EconomicsMonthlyDayMargin is one heatmap row: 24 hourly ESS margins
// (UAH per kWh discharged; null when the hour had no discharge/price).
export type EconomicsMonthlyDayMargin = {
  date: string
  hours: Array<number | null>
}

// EconomicsUzeCycle is one significant УЗЕ day (reserve ≥ 1000 ₴) with the
// full hourly optimal-vs-fact schedule the cycle chart renders.
export type EconomicsUzeCycle = {
  start_date: string
  end_date: string
  label: string
  actual_effect_uah: number
  opt_effect_uah: number
  reserve_uah: number
  capture_pct: number
  chart: EconomicsUzeCycleChart
}

export type EconomicsUzeCycleChart = {
  labels: string[]
  capacity_kwh: number
  power_kw: number
  optimal: {
    to_load_kwh: number[]
    to_grid_kwh: number[]
    chg_pv_kwh: number[]
    chg_grid_kwh: number[]
    soc_pct: Array<number | null>
    soc_start: number
    export_uah: number[]
    load_uah: number[]
    grid_cost_uah: number[]
  }
  fact: {
    ess_kw: number[]
    soc_pct: Array<number | null>
    soc_start: number | null
    rdn: number[]
  }
  summary: {
    optimal: {
      effect: number
      export_val: number
      load_val: number
      charge_pv_cost: number
      grid_cost: number
      degradation: number
      charge_pv_kwh: number
      charge_grid_kwh: number
      discharge_kwh: number
    }
    fact: { effect: number }
  }
}

export type EconomicsMonthlyResponse = {
  organization_id: string
  month: string
  tz: string
  days_in_month: number
  totals: EconomicsMonthlyTotals
  days: EconomicsMonthlyDay[]
  hourly_margin: EconomicsMonthlyDayMargin[]
  uze_cycles: EconomicsUzeCycle[]
}

// fetchEconomicsMonthly reads the server-computed month rollup. The
// backend serves final days from cache and recomputes the open tail
// (today) on read, so a request always returns a consistent month.
export async function fetchEconomicsMonthly(
  input: { organizationID: string; month: string; tz?: string },
  signal?: AbortSignal,
): Promise<EconomicsMonthlyResponse> {
  const url = buildURL('/api/v1/economics/monthly', {
    organization_id: input.organizationID,
    month: input.month,
    tz: input.tz || undefined,
  })
  const res = await fetch(url, { signal })
  if (!res.ok) {
    const body = await res.text().catch(() => '')
    const suffix = body ? ` — ${body.trim()}` : ''
    throw new Error(`economics/monthly request failed: ${res.status}${suffix}`)
  }
  return res.json()
}

// EconomicsAnnualMonthRollup is one month's contribution to the annual
// view: the YYYY-MM label plus that month's totals (same shape as the
// monthly dashboard, so the year trend/table reuse the fields).
export type EconomicsAnnualMonthRollup = {
  month: string
  totals: EconomicsMonthlyTotals
}

// EconomicsAnnualQuarter is one quarter card: project effect, EBITDA + PV.
export type EconomicsAnnualQuarter = {
  year: number
  quarter: number
  effect_uah: number
  ebitda_uah: number
  pv_kwh: number
}

// EconomicsAnnualMonthMargin is one annual-heatmap row: 24 hour-of-day
// ESS margins (UAH per kWh discharged) averaged across the month; null
// when that hour had no discharge all month.
export type EconomicsAnnualMonthMargin = {
  month: string
  hours: Array<number | null>
}

export type EconomicsAnnualResponse = {
  organization_id: string
  period: string
  // First/last month of the served window (YYYY-MM).
  from: string
  to: string
  tz: string
  months_with_data: number
  // Cumulative EBITDA of stored days before the window start — the ROI
  // opening balance so a single-year view shows CAPEX remaining since the
  // start of operation.
  prior_ebitda_uah: number
  // Distinct months with data before the window, so the ROI payback can
  // annualise all-time EBITDA over the full operating span.
  prior_months_with_data: number
  totals: EconomicsMonthlyTotals
  months: EconomicsAnnualMonthRollup[]
  quarters: EconomicsAnnualQuarter[]
  monthly_margin: EconomicsAnnualMonthMargin[]
}

// fetchEconomicsAnnual reads the server-computed rollup. Pass `period`
// (YYYY) for a calendar year, or `from`/`to` (both YYYY-MM) for a sliding
// month window. The backend reads whatever the recompute daemon
// persisted, so a request always returns a consistent period.
export async function fetchEconomicsAnnual(
  input: { organizationID: string; period?: string; from?: string; to?: string; tz?: string },
  signal?: AbortSignal,
): Promise<EconomicsAnnualResponse> {
  const url = buildURL('/api/v1/economics/annual', {
    organization_id: input.organizationID,
    period: input.from && input.to ? undefined : input.period || undefined,
    from: input.from || undefined,
    to: input.to || undefined,
    tz: input.tz || undefined,
  })
  const res = await fetch(url, { signal })
  if (!res.ok) {
    const body = await res.text().catch(() => '')
    const suffix = body ? ` — ${body.trim()}` : ''
    throw new Error(`economics/annual request failed: ${res.status}${suffix}`)
  }
  return res.json()
}

// EconomicsPortfolioSite is one object's row in the portfolio rollup:
// project effect + the two reserve levers (work-schedule + УЗЕ optimum,
// project_net) + УЗЕ data-quality flags + key energy totals.
export type EconomicsPortfolioSite = {
  id: string
  name: string
  has_data: boolean
  effect_uah: number
  ebitda_uah: number
  schedule_reserve_uah: number
  bess_reserve_uah: number
  action_reserve_uah: number
  bess_data_ok: boolean
  bess_anomalous_hours: number
  bess_anomalous_days: number
  // Civil dates that contain ≥1 excluded УЗЕ hour; the portfolio ⚠
  // drills into the first of them when present.
  bess_anomalous_dates?: string[] | null
  // Distinct reason codes (peak_spike / hourly_over_limit / after_gap).
  bess_anomaly_reasons?: string[] | null
  pv_kwh: number
  load_kwh: number
  grid_import_kwh: number
  grid_export_kwh: number
  ess_net_uah: number
}

// EconomicsPortfolioTrendMonth is one month of the portfolio energy trend
// (year scope): the YYYY-MM key plus the sum across all objects.
export type EconomicsPortfolioTrendMonth = {
  month: string
  pv_kwh: number
  load_kwh: number
  grid_import_kwh: number
  grid_export_kwh: number
  effect_uah: number
}

export type EconomicsPortfolioResponse = {
  scope: 'month' | 'year'
  label: string
  tz: string
  months_with_data: number
  sites: EconomicsPortfolioSite[]
  totals: EconomicsPortfolioSite
  trend: EconomicsPortfolioTrendMonth[]
}

// fetchEconomicsPortfolio reads the all-objects rollup. Pass `month`
// (YYYY-MM) for a month, `period` (YYYY) for a calendar year, or
// `from`/`to` (both YYYY-MM) for a sliding window.
export async function fetchEconomicsPortfolio(
  input: { month?: string; period?: string; from?: string; to?: string; tz?: string },
  signal?: AbortSignal,
): Promise<EconomicsPortfolioResponse> {
  const url = buildURL('/api/v1/economics/portfolio', {
    month: input.month || undefined,
    period: input.from && input.to ? undefined : input.period || undefined,
    from: input.from || undefined,
    to: input.to || undefined,
    tz: input.tz || undefined,
  })
  const res = await fetch(url, { signal })
  if (!res.ok) {
    const body = await res.text().catch(() => '')
    const suffix = body ? ` — ${body.trim()}` : ''
    throw new Error(`economics/portfolio request failed: ${res.status}${suffix}`)
  }
  return res.json()
}

// EconomicsRecomputeResult mirrors internal/economics.RangeResult.
export type EconomicsRecomputeResult = {
  from: string
  to: string
  days: number
  days_ok: number
  days_failed: number
  errors?: { date: string; error: string }[]
}

// recomputeEconomics recomputes (and persists) economics for every day
// in [from, to] (YYYY-MM-DD, inclusive), streaming NDJSON progress so a
// month/year backfill can show a progress bar and be cancelled.
export async function recomputeEconomics(
  input: { organizationID: string; from: string; to: string; tz?: string },
  opts?: ImportRunOptions,
): Promise<EconomicsRecomputeResult> {
  const url = buildURL('/api/v1/economics/recompute', {
    organization_id: input.organizationID,
    from: input.from,
    to: input.to,
    tz: input.tz || undefined,
  })
  const res = await fetch(url, { method: 'POST', signal: opts?.signal })
  try {
    return await consumeImportStream<EconomicsRecomputeResult>(res, opts?.onProgress)
  } catch (err) {
    if (isAbortError(err)) throw err
    if (err instanceof Error) throw new Error(`economics recompute failed: ${err.message}`, { cause: err })
    throw err
  }
}

// EconomicsDataRange is the civil-date span (YYYY-MM-DD) covered by an
// organization's raw telemetry — the input economics is computed from.
// has_data is false (and the dates empty) when the org has no samples.
export type EconomicsDataRange = {
  from: string
  to: string
  has_data: boolean
}

// fetchEconomicsDataRange returns the earliest/latest telemetry dates for
// an organization so the recompute UI can auto-fill the full period.
export async function fetchEconomicsDataRange(
  input: { organizationID: string; tz?: string },
  signal?: AbortSignal,
): Promise<EconomicsDataRange> {
  const url = buildURL('/api/v1/economics/data-range', {
    organization_id: input.organizationID,
    tz: input.tz || undefined,
  })
  const res = await fetch(url, { signal })
  if (!res.ok) {
    throw new Error(`economics data-range request failed: ${res.status}`)
  }
  return res.json()
}

export async function fetchDAMPrices(
  input: { from: string; to: string; zone?: number },
  signal?: AbortSignal,
): Promise<DAMPricesResponse> {
  const url = buildURL('/api/v1/dam-prices', {
    zone: input.zone !== undefined ? String(input.zone) : undefined,
    from: input.from,
    to: input.to,
  })
  const res = await fetch(url, { signal })
  if (!res.ok) {
    throw new Error(`dam-prices request failed: ${res.status}`)
  }
  return res.json()
}

// refreshDAMPrices triggers a synchronous server-side pull of one
// day's DAM XLS from OREE for `date` (+ optional `zone`, defaults to
// the configured deployment zone). The backend upserts the resulting
// 24 hourly rows into `market_dam_prices` and returns the same shape
// as fetchDAMPrices so the caller can drop the response straight
// into its existing price-map plumbing.
//
// Operator escape hatch when the daily collector either hasn't run
// yet or fetched too early (OREE published the file late, network
// blip, etc). Error bodies are surfaced verbatim in the thrown
// message so the operator sees the upstream cause without
// grep-ing API logs.
export async function refreshDAMPrices(
  input: { date: string; zone?: number },
  signal?: AbortSignal,
): Promise<DAMPricesResponse> {
  const url = buildURL('/api/v1/dam-prices/refresh', {
    date: input.date,
    zone: input.zone !== undefined ? String(input.zone) : undefined,
  })
  const res = await fetch(url, { method: 'POST', signal })
  if (!res.ok) {
    const body = await res.text().catch(() => '')
    const suffix = body ? ` — ${body.trim()}` : ''
    throw new Error(`dam-prices refresh failed: ${res.status}${suffix}`)
  }
  return res.json()
}

// DAMRefreshRangeResult mirrors `internal/api.DAMRefreshRangeResult`:
// a bulk DAM-price backfill over an inclusive [from, to] day span.
// Per-day failures are tolerated and listed in `errors`.
export type DAMRefreshRangeResult = {
  from: string
  to: string
  zone: number
  days: number
  days_ok: number
  days_failed: number
  rows_written: number
  errors?: { date: string; error: string }[]
}

// refreshDAMPricesRange triggers a synchronous server-side backfill of
// DAM (РДН) prices for every delivery date in [from, to] (YYYY-MM-DD,
// inclusive). The backend loops day-by-day pulling each OREE XLS and
// upserting it; missing publications are counted, not fatal. Use it to
// load a month or a year of prices at once.
export async function refreshDAMPricesRange(
  input: { from: string; to: string; zone?: number },
  opts?: ImportRunOptions,
): Promise<DAMRefreshRangeResult> {
  const url = buildURL('/api/v1/dam-prices/refresh-range', {
    from: input.from,
    to: input.to,
    zone: input.zone !== undefined ? String(input.zone) : undefined,
  })
  const res = await fetch(url, { method: 'POST', signal: opts?.signal })
  try {
    return await consumeImportStream<DAMRefreshRangeResult>(res, opts?.onProgress)
  } catch (err) {
    if (isAbortError(err)) throw err
    if (err instanceof Error) throw new Error(`dam-prices range refresh failed: ${err.message}`, { cause: err })
    throw err
  }
}

// FusionSolarConfig is the non-secret server-side default set returned
// by GET /api/v1/fusionsolar/config so the import page can prefill its
// form. Secrets are never sent — only booleans say whether they exist.
export type FusionSolarConfig = {
  client_id: string
  api_base: string
  oauth_base: string
  oauth_resolve: string
  refresh_token_configured: boolean
  client_secret_configured: boolean
}

export async function fetchFusionSolarConfig(signal?: AbortSignal): Promise<FusionSolarConfig> {
  const res = await fetch(buildURL('/api/v1/fusionsolar/config', {}), { signal })
  if (!res.ok) throw new Error(`fusionsolar config failed: ${res.status}`)
  return res.json()
}

// ImportProgress is one progress tick from a streaming import: how many
// units (24h windows for FusionSolar, days for DAM) are done out of the
// total, plus an optional label (e.g. the date being processed).
export type ImportProgress = { done: number; total: number; label?: string }

// ImportRunOptions carries an AbortSignal (so the operator's cancel
// button can interrupt the request — the server cancels its work when
// the connection drops) and an onProgress callback fed by the NDJSON
// progress stream.
export type ImportRunOptions = {
  signal?: AbortSignal
  onProgress?: (progress: ImportProgress) => void
}

// consumeImportStream reads the NDJSON progress stream the long-running
// import endpoints emit (one JSON object per line: progress | done |
// error) and resolves with the final `result`. It tolerates an older,
// non-streaming server that returns a single JSON result object.
async function consumeImportStream<T>(res: Response, onProgress?: ImportRunOptions['onProgress']): Promise<T> {
  if (!res.ok) {
    const body = await res.text().catch(() => '')
    const suffix = body ? ` — ${body.trim()}` : ''
    throw new Error(`${res.status}${suffix}`)
  }
  if (!res.body) {
    return (await res.json()) as T
  }

  const reader = res.body.getReader()
  const decoder = new TextDecoder()
  let buffer = ''
  let result: T | undefined

  const handleLine = (raw: string) => {
    const line = raw.trim()
    if (!line) return
    const ev = JSON.parse(line) as {
      type?: string
      done?: number
      total?: number
      label?: string
      error?: string
      result?: T
    }
    if (ev.type === 'progress') {
      onProgress?.({ done: ev.done ?? 0, total: ev.total ?? 0, label: ev.label })
    } else if (ev.type === 'error') {
      throw new Error(ev.error || 'import failed')
    } else if (ev.type === 'done') {
      result = ev.result
    } else if (ev.type === undefined) {
      // Legacy non-streaming server: the line is the result object.
      result = ev as unknown as T
    }
  }

  for (;;) {
    const { done, value } = await reader.read()
    if (done) break
    buffer += decoder.decode(value, { stream: true })
    let nl: number
    while ((nl = buffer.indexOf('\n')) >= 0) {
      handleLine(buffer.slice(0, nl))
      buffer = buffer.slice(nl + 1)
    }
  }
  handleLine(buffer)

  if (result === undefined) {
    throw new Error('import stream ended without a result')
  }
  return result
}

// FusionSolarImportResult mirrors `internal/fusionsolar.ImportResult`.
// Returned by POST /api/v1/fusionsolar/import after a backfill run so
// the import page can report how many rows landed (and any per-pack
// warnings) without re-querying the dashboard endpoints.
export type FusionSolarImportResult = {
  organization_id: string
  plant_code: string
  from: string
  to: string
  windows: number
  rows_written: number
  deleted_rows: number
  // skipped_live_windows counts 24h windows skipped wholesale because every
  // 5-minute slot already has live data — those days are left untouched.
  skipped_live_windows?: number
  // skipped_live_samples counts archive samples dropped because their own
  // 5-minute slot already had live data (transition days filled partially).
  skipped_live_samples?: number
  per_metric: Record<string, number>
  warnings?: string[]
}

// runFusionSolarImport triggers a synchronous server-side backfill of
// historical FusionSolar telemetry for `organizationID` over the
// [from, to) window (RFC3339). The backend pulls 5-minute device
// history from the Huawei Northbound API, normalizes the cumulative
// counters into telemetry_samples, and returns a summary.
//
// Auth is one of two styles, both travelling in the JSON body (never
// the query string, so they don't land in access logs): a ready
// `accessToken`, or a long-lived `refreshToken` + `clientSecret` that
// the backend exchanges for an access token via the FusionSolar OAuth
// server. Error bodies are surfaced verbatim (e.g. an upstream
// failCode) so the operator sees the cause without grepping API logs.
export async function runFusionSolarImport(
  input: {
    organizationID: string
    from: string
    to: string
    accessToken?: string
    apiBase?: string
    refreshToken?: string
    clientId?: string
    clientSecret?: string
    oauthBase?: string
    oauthResolve?: string
  },
  opts?: ImportRunOptions,
): Promise<FusionSolarImportResult> {
  const url = buildURL('/api/v1/fusionsolar/import', {
    organization_id: input.organizationID,
    from: input.from,
    to: input.to,
  })
  const res = await fetch(url, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      access_token: input.accessToken || undefined,
      api_base: input.apiBase || undefined,
      refresh_token: input.refreshToken || undefined,
      client_id: input.clientId || undefined,
      client_secret: input.clientSecret || undefined,
      oauth_base: input.oauthBase || undefined,
      oauth_resolve: input.oauthResolve || undefined,
    }),
    signal: opts?.signal,
  })
  try {
    return await consumeImportStream<FusionSolarImportResult>(res, opts?.onProgress)
  } catch (err) {
    if (isAbortError(err)) throw err
    if (err instanceof Error) throw new Error(`fusionsolar import failed: ${err.message}`, { cause: err })
    throw err
  }
}

const PV_FORECAST_WEBHOOK_URL =
  'https://granary.app.n8n.cloud/webhook/96bac28d-5020-48b3-8f23-0bc189029c00'
// Two retries on transient failures (5xx, network) with exponential backoff.
// Total worst-case latency ~1s before giving up — short enough not to hold
// up the chart, long enough to ride out a single node hiccup in n8n.
const PV_FORECAST_RETRY_DELAYS_MS = [200, 600]

function isAbortError(e: unknown): boolean {
  return e instanceof DOMException && e.name === 'AbortError'
}

function delay(ms: number, signal?: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    if (signal?.aborted) {
      reject(new DOMException('Aborted', 'AbortError'))
      return
    }
    const timer = setTimeout(() => {
      signal?.removeEventListener('abort', onAbort)
      resolve()
    }, ms)
    function onAbort() {
      clearTimeout(timer)
      signal?.removeEventListener('abort', onAbort)
      reject(new DOMException('Aborted', 'AbortError'))
    }
    signal?.addEventListener('abort', onAbort)
  })
}

export async function fetchPvForecast(
  input: { elevatorCode: ElevatorCode; forecastDay: string },
  signal?: AbortSignal,
): Promise<PvForecastPoint[]> {
  const url = new URL(PV_FORECAST_WEBHOOK_URL)
  url.searchParams.set('elevator_code', input.elevatorCode)
  url.searchParams.set('forecast_day', input.forecastDay)

  let lastError: unknown = null
  for (let attempt = 0; attempt <= PV_FORECAST_RETRY_DELAYS_MS.length; attempt++) {
    if (signal?.aborted) {
      throw new DOMException('Aborted', 'AbortError')
    }
    try {
      const res = await fetch(url.toString(), { signal })
      if (res.status >= 500 && res.status < 600) {
        throw new Error(`pv-forecast request failed: ${res.status}`)
      }
      if (!res.ok) {
        // Non-5xx errors (4xx, etc.) are not transient — bail immediately
        // so a misconfigured elevator_code doesn't burn three attempts.
        throw new Error(`pv-forecast request failed: ${res.status}`)
      }
      const body = (await res.json()) as unknown
      if (!Array.isArray(body)) return []
      return body as PvForecastPoint[]
    } catch (e) {
      if (isAbortError(e)) throw e
      lastError = e
      const isTransient =
        e instanceof TypeError ||
        (e instanceof Error && /pv-forecast request failed: 5\d\d/.test(e.message))
      if (!isTransient) throw e
      const nextDelay = PV_FORECAST_RETRY_DELAYS_MS[attempt]
      if (nextDelay === undefined) break
      await delay(nextDelay, signal)
    }
  }
  throw lastError instanceof Error
    ? lastError
    : new Error('pv-forecast request failed')
}

const OPEN_METEO_FORECAST_URL = 'https://api.open-meteo.com/v1/forecast'
// Same backoff schedule as the n8n forecast — Open-Meteo is third-party,
// so a single transient hiccup shouldn't blank the weather card.
const OPEN_METEO_RETRY_DELAYS_MS = [200, 600]

// fetchWeatherForecastFromAPI tries the backend's cached forecast
// first. Returns null on empty/missing data (so the caller can fall
// back to Open-Meteo directly) and throws on transport-level errors.
//
// `from` / `to` are local YYYY-MM-DD strings; the backend treats them
// as inclusive UTC dates and expands `to` to end-of-day on its side.
// The UTC ISO → local-TZ shape conversion lives in the adapter at
// `dashboard/transforms/weatherAdapter.ts`.
export async function fetchWeatherForecastFromAPI(
  input: { organizationID: string; from: string; to: string },
  signal?: AbortSignal,
): Promise<OpenMeteoForecast | null> {
  const url = buildURL('/api/v1/weather-forecast', {
    organization_id: input.organizationID,
    from: input.from,
    to: input.to,
  })
  const res = await fetch(url, { signal })
  if (!res.ok) {
    throw new Error(`weather-forecast request failed: ${res.status}`)
  }
  const body = (await res.json()) as WeatherForecastApiResponse
  return weatherFromApi(body)
}

// fetchOpenMeteoWeather calls the public Open-Meteo /v1/forecast endpoint
// with the exact `daily=` / `hourly=` shape the dashboard PV pipeline uses
// elsewhere, so caching CDNs see the same canonical URL. The browser hits
// Open-Meteo directly (no backend proxy), mirroring fetchPvForecast.
export async function fetchOpenMeteoWeather(
  input: { latitude: number; longitude: number },
  signal?: AbortSignal,
): Promise<OpenMeteoForecast> {
  const url = new URL(OPEN_METEO_FORECAST_URL)
  url.searchParams.set('latitude', String(input.latitude))
  url.searchParams.set('longitude', String(input.longitude))
  url.searchParams.set(
    'daily',
    'sunrise,sunset,daylight_duration,sunshine_duration,shortwave_radiation_sum',
  )
  url.searchParams.set(
    'hourly',
    'temperature_2m,cloud_cover,is_day,shortwave_radiation,direct_radiation,diffuse_radiation,global_tilted_irradiance_instant',
  )
  url.searchParams.set('timezone', 'auto')

  let lastError: unknown = null
  for (let attempt = 0; attempt <= OPEN_METEO_RETRY_DELAYS_MS.length; attempt++) {
    if (signal?.aborted) {
      throw new DOMException('Aborted', 'AbortError')
    }
    try {
      const res = await fetch(url.toString(), { signal })
      if (!res.ok) {
        throw new Error(`open-meteo request failed: ${res.status}`)
      }
      return (await res.json()) as OpenMeteoForecast
    } catch (e) {
      if (isAbortError(e)) throw e
      lastError = e
      const isTransient =
        e instanceof TypeError ||
        (e instanceof Error && /open-meteo request failed: 5\d\d/.test(e.message))
      if (!isTransient) throw e
      const nextDelay = OPEN_METEO_RETRY_DELAYS_MS[attempt]
      if (nextDelay === undefined) break
      await delay(nextDelay, signal)
    }
  }
  throw lastError instanceof Error
    ? lastError
    : new Error('open-meteo request failed')
}
