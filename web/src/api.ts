import type {
  CurrentResponse,
  DAMPricesResponse,
  DashboardConfig,
  PvForecastPoint,
  TimeseriesResponse,
} from './types'

const API_BASE = ((import.meta.env.VITE_API_BASE_URL as string | undefined) || '').replace(/\/+$/, '')

function withBase(path: string): string {
  if (!API_BASE) return path
  return `${API_BASE}${path}`
}

function buildURL(path: string, params: Record<string, string | undefined>) {
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

export type EnergySummaryResponse = {
  organization_id: string
  from: string
  to: string
  totals: Record<string, number>
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
  input: { elevatorCode: 'JE' | 'RE'; forecastDay: string },
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
