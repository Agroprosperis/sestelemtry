import type {
  OpenMeteoForecast,
  OrganizationInfo,
  OrganizationLocation,
  WeatherCondition,
  WeatherDaySummary,
} from '../../types'

// locationFor scans the org list (returned by /api/v1/organizations)
// for the entry matching `organizationID` and returns its location, or
// null when the org is absent or has no `location` block configured.
// Centralized here so the weather hook + card share the same lookup
// without duplicating the array search.
export function locationFor(
  organizationID: string,
  organizations: OrganizationInfo[],
): OrganizationLocation | null {
  for (const o of organizations) {
    if (o.id === organizationID) return o.location ?? null
  }
  return null
}

function pad(n: number): string {
  return n < 10 ? `0${n}` : String(n)
}

// weatherDayFromAnchor renders the anchor as YYYY-MM-DD in the browser's
// local TZ. Open-Meteo returns its `daily.time[]` as `YYYY-MM-DD` in the
// location's TZ (we pass `timezone=auto`); for Ukrainian users browser-
// local and Kyiv-local agree, so this is the same key used to index the
// API response.
export function weatherDayFromAnchor(anchor: Date): string {
  return `${anchor.getFullYear()}-${pad(anchor.getMonth() + 1)}-${pad(anchor.getDate())}`
}

// classifyCondition picks the icon bucket from the daily sunshine ratio,
// with average cloud cover as a tiebreaker on the boundary between
// `partly_cloudy` and `cloudy`. The supplied API URL has no precipitation
// field, so `overcast` (the lowest sunshine bucket) doubles as the
// "rain risk" indicator.
export function classifyCondition(
  sunshineRatio: number,
  cloudCoverAvgPct: number,
): WeatherCondition {
  if (!Number.isFinite(sunshineRatio)) {
    if (cloudCoverAvgPct < 30) return 'sunny'
    if (cloudCoverAvgPct < 65) return 'partly_cloudy'
    if (cloudCoverAvgPct < 85) return 'cloudy'
    return 'overcast'
  }
  if (sunshineRatio >= 0.7) return 'sunny'
  if (sunshineRatio >= 0.4) return 'partly_cloudy'
  if (sunshineRatio >= 0.2) return 'cloudy'
  return 'overcast'
}

function toDateKey(time: string): string {
  // Open-Meteo hourly times are local-TZ ISO strings without offset, e.g.
  // `2026-05-10T13:00`. The first 10 chars are the day key, which lines
  // up with `daily.time[]` (`YYYY-MM-DD`) without any TZ math.
  return time.slice(0, 10)
}

// summarizeWeatherDay collapses the hourly + daily series into the four
// numbers the WeatherCard renders. Returns null when the requested day
// is absent from `daily.time` or has no usable hourly samples — the
// caller treats this as "forecast not available for this date".
export function summarizeWeatherDay(
  forecast: OpenMeteoForecast | null,
  day: string,
): WeatherDaySummary | null {
  if (!forecast) return null
  const dailyIdx = forecast.daily?.time?.indexOf(day) ?? -1
  if (dailyIdx < 0) return null

  const hourlyTime = forecast.hourly?.time ?? []
  const hourlyTemp = forecast.hourly?.temperature_2m ?? []
  const hourlyCloud = forecast.hourly?.cloud_cover ?? []

  let tempMin = Number.POSITIVE_INFINITY
  let tempMax = Number.NEGATIVE_INFINITY
  let cloudSum = 0
  let cloudCount = 0
  for (let i = 0; i < hourlyTime.length; i++) {
    if (toDateKey(hourlyTime[i]) !== day) continue
    const t = hourlyTemp[i]
    if (Number.isFinite(t)) {
      if (t < tempMin) tempMin = t
      if (t > tempMax) tempMax = t
    }
    const c = hourlyCloud[i]
    if (Number.isFinite(c)) {
      cloudSum += c
      cloudCount++
    }
  }

  if (!Number.isFinite(tempMin) || !Number.isFinite(tempMax)) return null

  const cloudAvg = cloudCount > 0 ? cloudSum / cloudCount : 0
  const daylight = forecast.daily.daylight_duration?.[dailyIdx]
  const sunshine = forecast.daily.sunshine_duration?.[dailyIdx]
  const sunshineRatio =
    Number.isFinite(daylight) && Number.isFinite(sunshine) && (daylight as number) > 0
      ? (sunshine as number) / (daylight as number)
      : Number.NaN

  return {
    day,
    tempMinC: tempMin,
    tempMaxC: tempMax,
    cloudCoverAvgPct: cloudAvg,
    condition: classifyCondition(sunshineRatio, cloudAvg),
  }
}
