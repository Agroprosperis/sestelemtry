import type { CurrentResponse, DashboardConfig, TimeseriesResponse } from './types'

const API_BASE = (import.meta.env.VITE_API_BASE_URL as string | undefined) || 'http://localhost:8080'

function buildURL(path: string, params: Record<string, string | undefined>) {
  const url = new URL(path, API_BASE)
  for (const [k, v] of Object.entries(params)) {
    if (!v) continue
    url.searchParams.set(k, v)
  }
  return url.toString()
}

export async function fetchDashboardConfig(): Promise<DashboardConfig> {
  const res = await fetch(`${API_BASE}/api/v1/dashboard-config`)
  if (!res.ok) {
    throw new Error(`dashboard-config request failed: ${res.status}`)
  }
  return res.json()
}

export async function fetchCurrent(organizationID: string): Promise<CurrentResponse> {
  const url = buildURL('/api/v1/current', {
    organization_id: organizationID,
  })
  const res = await fetch(url)
  if (!res.ok) {
    throw new Error(`current request failed: ${res.status}`)
  }
  return res.json()
}

export async function fetchTimeseries(input: {
  organizationID: string
  metricKeys: string[]
  from: string
  to: string
  bucket: string
}): Promise<TimeseriesResponse> {
  const url = buildURL('/api/v1/timeseries', {
    organization_id: input.organizationID,
    metric_keys: input.metricKeys.join(','),
    from: input.from,
    to: input.to,
    bucket: input.bucket,
  })
  const res = await fetch(url)
  if (!res.ok) {
    throw new Error(`timeseries request failed: ${res.status}`)
  }
  return res.json()
}
