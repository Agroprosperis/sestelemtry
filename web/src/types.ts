export type DashboardMetric = {
  key: string
  label: string
  unit: string
}

export type DashboardConfig = {
  cards: DashboardMetric[]
  power_chart: DashboardMetric[]
  energy_chart: DashboardMetric[]
}

export type CurrentMetric = {
  metric_key: string
  value: number
  time: string
  labels?: Record<string, string>
}

export type CurrentResponse = {
  organization_id: string
  metrics: Record<string, CurrentMetric>
}

export type TimeseriesPoint = {
  time: string
  metric_key: string
  value: number
}

export type TimeseriesResponse = {
  organization_id: string
  metric_keys: string[]
  bucket: string
  from: string
  to: string
  points: TimeseriesPoint[]
}

export type DAMPrice = {
  delivery_date: string
  hour: number
  zone: number
  price_uah_per_mwh?: number | null
  sale_volume_mwh?: number | null
  purchase_volume_mwh?: number | null
  declared_sale_volume_mwh?: number | null
  declared_purchase_volume_mwh?: number | null
}

export type DAMPricesResponse = {
  zone: number
  from: string
  to: string
  prices: DAMPrice[]
}

// RegisterMeta mirrors the api.RegisterMeta struct: vendor-documented
// Modbus information attached to a metric_key. The dashboard fetches
// the full map once at boot and uses `address` to annotate CSV
// headers (`metric_key_40388`) in the bucketed export.
export type RegisterMeta = {
  address: number
  data_type: string
  gain: number
}

export type RegistersResponse = {
  metadata: Record<string, RegisterMeta>
}

// OrganizationLocation mirrors the backend's `LocationInfo`. Both
// fields are required when present; the wrapping `location` is itself
// optional on `OrganizationInfo` (orgs without a configured location
// just don't ship the field).
export type OrganizationLocation = {
  latitude: number
  longitude: number
  city: string
}

// OrganizationInfo is the per-org metadata the dashboard receives from
// /api/v1/organizations. We deliberately keep this narrow — the API
// strips Modbus connection details before serializing — so the
// frontend never accidentally renders an internal IP.
export type OrganizationInfo = {
  id: string
  name?: string
  location?: OrganizationLocation
}

export type OrganizationsResponse = {
  organizations: OrganizationInfo[]
}

// OpenMeteoForecast is the subset of the api.open-meteo.com /v1/forecast
// response that the weather widget consumes. Only the fields we actually
// read are typed; the upstream payload contains many more keys (radiation
// breakdowns, units metadata, timezone offsets, etc.) which we ignore.
export type OpenMeteoForecast = {
  // Local-TZ ISO timestamps without offsets, e.g. `2026-05-10T13:00`.
  // Each `hourly[k][i]` shares the same index `i` as `hourly.time[i]`.
  hourly: {
    time: string[]
    temperature_2m: number[]
    cloud_cover: number[]
  }
  // Daily series — one entry per day. `time[i]` is `YYYY-MM-DD` in local TZ.
  // `sunshine_duration` and `daylight_duration` are seconds; their ratio
  // drives the condition bucket in summarizeWeatherDay.
  daily: {
    time: string[]
    sunshine_duration: number[]
    daylight_duration: number[]
  }
}

// WeatherCondition is the discrete bucket the WeatherCard renders an icon
// for. Derived from the daily sunshine/daylight ratio (and average cloud
// cover as a tiebreaker), since the supplied API URL does not include any
// precipitation field.
export type WeatherCondition = 'sunny' | 'partly_cloudy' | 'cloudy' | 'overcast'

// WeatherDaySummary collapses the hourly + daily series into the four
// numbers the dashboard card actually shows. Returned by
// summarizeWeatherDay; null when the requested day is outside the
// forecast window.
export type WeatherDaySummary = {
  // YYYY-MM-DD in the location's local TZ.
  day: string
  tempMinC: number
  tempMaxC: number
  cloudCoverAvgPct: number
  condition: WeatherCondition
}

// PvForecastPoint mirrors a single record returned by the n8n PV forecast
// webhook. Each record describes one panel orientation × one hour-of-day in
// local Kyiv time (the n8n flow already converts from UTC). For a single
// elevator with N orientations this means up to N × 24 records per day.
export type PvForecastPoint = {
  elevator_code: string
  orientation_idx: number
  // hour_ending is 1..24, where 1 means the period 00:00–01:00 and 24 means
  // 23:00–24:00. Hour-start = hour_ending - 1.
  hour_ending: number
  interval_start_local: string
  gti_weighted_wm2: number
  pdc_total_kwp: number
  pac_limit_kw: number
  planned_dc_kw: number
  planned_ac_kw: number
  planned_kwh: number
  clip_loss_kwh: number
  temperature_2m_c: number
  cloud_cover_pct: number
  model_version: string
}
