import type { RangePreset } from '../range'
import { useOrganizations } from '../hooks/useOrganizations'
import { useWeather } from '../hooks/useWeather'
import {
  hourlyWeatherForDay,
  locationFor,
  summarizeWeatherDay,
  weatherDayFromAnchor,
  type HourlyWeatherSlot,
} from '../transforms/weather'
import type { WeatherCondition, WeatherDaySummary } from '../../types'

type Props = {
  organizationID: string
  anchor: Date
  preset: RangePreset
}

// The trailing U+FE0F (Variation Selector-16) flips the legacy
// monochrome glyphs (☀ U+2600, ☁ U+2601) into their colored emoji
// presentation; without it most browsers fall back to plain-text and
// render them as a black sun/cloud.
const CONDITION_ICON: Record<WeatherCondition, string> = {
  sunny: '☀\uFE0F',
  partly_cloudy: '⛅\uFE0F',
  cloudy: '☁\uFE0F',
  overcast: '🌧\uFE0F',
}

const CONDITION_LABEL: Record<WeatherCondition, string> = {
  sunny: 'Сонячно',
  partly_cloudy: 'Малохмарно',
  cloudy: 'Хмарно',
  overcast: 'Похмуро',
}

// hourIcon picks an emoji per hour using cloud cover only (the daily
// `sunshine_duration` ratio that drives the day summary is, by
// definition, not per-hour). Night hours swap the sun for a moon so a
// "100% clear at midnight" slot doesn't render a sun.
function hourIcon(slot: HourlyWeatherSlot): string {
  if (!slot.isDay) {
    return slot.cloudCoverPct < 65 ? '🌙\uFE0F' : '☁\uFE0F'
  }
  if (slot.cloudCoverPct < 25) return '☀\uFE0F'
  if (slot.cloudCoverPct < 65) return '⛅\uFE0F'
  if (slot.cloudCoverPct < 85) return '☁\uFE0F'
  return '🌧\uFE0F'
}

function formatTemp(c: number): string {
  return `${Math.round(c)}°`
}

function formatHour(h: number): string {
  return h < 10 ? `0${h}` : String(h)
}

function formatDate(day: string): string {
  // `day` is YYYY-MM-DD in local TZ; building a Date from it directly
  // would land at UTC midnight and could drift one day for users west of
  // GMT. Parse the parts manually so the displayed weekday matches what
  // the period picker shows.
  const [y, m, d] = day.split('-').map(Number)
  if (!y || !m || !d) return day
  const dt = new Date(y, m - 1, d)
  return dt.toLocaleDateString(undefined, {
    weekday: 'short',
    day: '2-digit',
    month: 'long',
  })
}

function CardShell({
  city,
  day,
  summary,
  children,
  busy,
}: {
  city: string
  day: string
  summary?: WeatherDaySummary
  children?: React.ReactNode
  busy?: boolean
}) {
  return (
    <section
      className="chart-card weather-card"
      aria-labelledby="weather-card-title"
      aria-busy={busy ? true : undefined}
    >
      <div className="weather-card-head">
        <div className="weather-card-summary">
          {summary ? (
            <span className="weather-card-icon" aria-hidden="true">
              {CONDITION_ICON[summary.condition]}
            </span>
          ) : null}
          <h2 id="weather-card-title">Погода — {city}</h2>
          {summary ? (
            <>
              <span className="weather-card-condition">
                {CONDITION_LABEL[summary.condition]}
              </span>
              <span className="weather-card-temp">
                {formatTemp(summary.tempMinC)}…{formatTemp(summary.tempMaxC)}C
              </span>
              <span className="weather-card-cloud" title="Середня хмарність">
                ☁ {Math.round(summary.cloudCoverAvgPct)}%
              </span>
            </>
          ) : null}
        </div>
        <span className="weather-card-date">{formatDate(day)}</span>
      </div>
      {children}
    </section>
  )
}

function HourlyStrip({ hours }: { hours: HourlyWeatherSlot[] }) {
  if (hours.length === 0) return null
  return (
    <ol
      className="weather-hourly"
      aria-label="Погодинний прогноз"
      // 24 columns when the data is complete; if the API returns a
      // partial day (start/end of the forecast window) we still want
      // each slot to keep the same width as a full-day strip rather
      // than stretching to fill, so we use the actual count here.
      style={{ gridTemplateColumns: `repeat(${hours.length}, minmax(28px, 1fr))` }}
    >
      {hours.map((slot) => (
        <li
          key={slot.hour}
          className={
            slot.isDay
              ? 'weather-hourly-slot'
              : 'weather-hourly-slot weather-hourly-slot--night'
          }
          title={`${formatHour(slot.hour)}:00 · ${formatTemp(slot.tempC)} · хмарність ${Math.round(slot.cloudCoverPct)}%`}
        >
          <span className="weather-hourly-time">{formatHour(slot.hour)}</span>
          <span className="weather-hourly-icon" aria-hidden="true">
            {hourIcon(slot)}
          </span>
          <span className="weather-hourly-temp">{formatTemp(slot.tempC)}</span>
        </li>
      ))}
    </ol>
  )
}

export function WeatherCard({ organizationID, anchor, preset }: Props) {
  // Coordinates come from /api/v1/organizations (sourced from the
  // server YAML config); the hook caches the response at module level
  // so multiple consumers don't refetch.
  const { data: orgs } = useOrganizations()
  const location = locationFor(organizationID, orgs)
  // The weather hook must run unconditionally to satisfy the rules of
  // hooks; it self-skips the network call when latitude/longitude are
  // null (no location configured for this org).
  const { data, loading, error } = useWeather({
    organizationID,
    latitude: location?.latitude ?? null,
    longitude: location?.longitude ?? null,
    anchor,
    // Skip the network fetch entirely for month/year presets. The
    // card still renders nothing in those cases (see the early return
    // below), so any data we'd pull would be discarded anyway.
    enabled: preset === 'day',
  })
  // Single-day weather for `month` / `year` presets is misleading — the
  // anchor day on those presets is just a cursor inside the period — so
  // we render nothing instead of guessing which day to display.
  if (!location || preset !== 'day') return null

  const day = weatherDayFromAnchor(anchor)
  const summary = summarizeWeatherDay(data, day)
  const hours = hourlyWeatherForDay(data, day)
  const city = location.city || organizationID

  if (loading) {
    return (
      <CardShell city={city} day={day} busy>
        <p className="weather-card-placeholder">Завантаження прогнозу…</p>
      </CardShell>
    )
  }
  if (error) {
    return (
      <CardShell city={city} day={day}>
        <p className="weather-card-placeholder weather-card-placeholder--error">
          Не вдалося завантажити прогноз погоди
        </p>
      </CardShell>
    )
  }
  if (!summary) {
    return (
      <CardShell city={city} day={day}>
        <p className="weather-card-placeholder">
          Прогноз недоступний для цієї дати
        </p>
      </CardShell>
    )
  }
  return (
    <CardShell city={city} day={day} summary={summary}>
      <HourlyStrip hours={hours} />
    </CardShell>
  )
}
