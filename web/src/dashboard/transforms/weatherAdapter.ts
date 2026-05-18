import type { OpenMeteoForecast } from '../../types'

// WeatherForecastApiHour mirrors the Go `WeatherForecastHour` shape
// served by `/api/v1/weather-forecast`. Numeric fields are nullable
// because Open-Meteo can omit individual hours at the edges of the
// model window (e.g. solar radiation before sunrise on the first day).
export type WeatherForecastApiHour = {
  hour: string
  temperature_2m_c?: number | null
  cloud_cover_pct?: number | null
  is_day?: boolean | null
  shortwave_wm2?: number | null
  direct_wm2?: number | null
  diffuse_wm2?: number | null
  gti_instant_wm2?: number | null
}

export type WeatherForecastApiDay = {
  day: string
  sunrise?: string | null
  sunset?: string | null
  daylight_duration_s?: number | null
  sunshine_duration_s?: number | null
  shortwave_radiation_sum?: number | null
}

export type WeatherForecastApiResponse = {
  organization_id: string
  from: string
  to: string
  hourly: WeatherForecastApiHour[]
  daily: WeatherForecastApiDay[]
}

function pad2(n: number): string {
  return n < 10 ? `0${n}` : String(n)
}

// utcIsoToLocalKey converts a UTC ISO timestamp returned by the
// backend into a local-TZ `YYYY-MM-DDTHH:MM` string in the same shape
// Open-Meteo returns when called with `timezone=auto`. The conversion
// uses the browser's local timezone, which for Ukrainian users matches
// the locations' Europe/Kyiv timezone (the same assumption the existing
// dashboard transforms rely on).
export function utcIsoToLocalKey(utcISO: string): string {
  const d = new Date(utcISO)
  return `${d.getFullYear()}-${pad2(d.getMonth() + 1)}-${pad2(d.getDate())}T${pad2(d.getHours())}:${pad2(d.getMinutes())}`
}

// weatherFromApi reshapes the backend response into the OpenMeteoForecast
// type the existing transforms consume. Returns null when there are no
// hourly or daily entries — the caller treats this as "no cached
// forecast, fall back to Open-Meteo directly".
export function weatherFromApi(
  api: WeatherForecastApiResponse,
): OpenMeteoForecast | null {
  if (api.hourly.length === 0 && api.daily.length === 0) return null

  const hourly: OpenMeteoForecast['hourly'] = {
    time: [],
    temperature_2m: [],
    cloud_cover: [],
    is_day: [],
  }
  for (const h of api.hourly) {
    hourly.time.push(utcIsoToLocalKey(h.hour))
    hourly.temperature_2m.push(h.temperature_2m_c ?? Number.NaN)
    hourly.cloud_cover.push(h.cloud_cover_pct ?? Number.NaN)
    hourly.is_day!.push(h.is_day === true ? 1 : 0)
  }

  const daily: OpenMeteoForecast['daily'] = {
    time: [],
    sunshine_duration: [],
    daylight_duration: [],
  }
  for (const d of api.daily) {
    // `day` is a YYYY-MM-DD string from the backend (date column).
    // Pull only the date portion to match the existing transforms.
    daily.time.push(d.day.slice(0, 10))
    daily.sunshine_duration.push(d.sunshine_duration_s ?? Number.NaN)
    daily.daylight_duration.push(d.daylight_duration_s ?? Number.NaN)
  }
  return { hourly, daily }
}
