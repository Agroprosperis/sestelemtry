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
  per_metric: Record<string, number>
  warnings?: string[]
}

// runFusionSolarImport triggers a synchronous server-side backfill of
// historical FusionSolar telemetry for `organizationID` over the
// [from, to) window (RFC3339). The backend pulls 5-minute device
// history from the Huawei Northbound API, normalizes the cumulative
// counters into telemetry_samples, and returns a summary.
//
// The `accessToken` (and optional `apiBase`) are entered by the
// operator on the import page and travel in the JSON body — never the
// query string — so they don't land in access logs. Error bodies are
// surfaced verbatim (e.g. an upstream failCode) so the operator sees
// the cause without grepping API logs.
export async function runFusionSolarImport(
  input: {
    organizationID: string
    from: string
    to: string
    accessToken: string
    apiBase?: string
  },
  signal?: AbortSignal,
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
      access_token: input.accessToken,
      api_base: input.apiBase || undefined,
    }),
    signal,
  })
  if (!res.ok) {
    const body = await res.text().catch(() => '')
    const suffix = body ? ` — ${body.trim()}` : ''
    throw new Error(`fusionsolar import failed: ${res.status}${suffix}`)
  }
  return res.json()
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
