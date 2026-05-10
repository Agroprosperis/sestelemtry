import type { RangePreset } from '../range'
import { useOrganizations } from '../hooks/useOrganizations'
import { useWeather } from '../hooks/useWeather'
import {
  locationFor,
  summarizeWeatherDay,
  weatherDayFromAnchor,
} from '../transforms/weather'
import type { WeatherCondition, WeatherDaySummary } from '../../types'

type Props = {
  organizationID: string
  anchor: Date
  preset: RangePreset
}

const CONDITION_ICON: Record<WeatherCondition, string> = {
  sunny: '☀',
  partly_cloudy: '🌤',
  cloudy: '☁',
  overcast: '🌧',
}

const CONDITION_LABEL: Record<WeatherCondition, string> = {
  sunny: 'Сонячно',
  partly_cloudy: 'Малохмарно',
  cloudy: 'Хмарно',
  overcast: 'Похмуро',
}

function formatTemp(c: number): string {
  // Match the dashboard's general "round to whole degrees for display"
  // convention; raw decimals would imply a precision Open-Meteo's
  // hourly forecast doesn't actually claim.
  return `${Math.round(c)}°`
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
  children,
  busy,
}: {
  city: string
  day: string
  children: React.ReactNode
  busy?: boolean
}) {
  return (
    <section
      className="chart-card weather-card"
      aria-labelledby="weather-card-title"
      aria-busy={busy ? true : undefined}
    >
      <div className="weather-card-head">
        <h2 id="weather-card-title">Погода — {city}</h2>
        <span className="weather-card-date">{formatDate(day)}</span>
      </div>
      {children}
    </section>
  )
}

function CardBody({ summary }: { summary: WeatherDaySummary }) {
  return (
    <div className="weather-card-body">
      <div className="weather-card-icon" aria-hidden="true">
        {CONDITION_ICON[summary.condition]}
      </div>
      <div className="weather-card-stats">
        <div className="weather-card-condition">
          {CONDITION_LABEL[summary.condition]}
        </div>
        <div className="weather-card-temp">
          {formatTemp(summary.tempMinC)}…{formatTemp(summary.tempMaxC)}C
        </div>
        <div className="weather-card-cloud">
          Хмарність {Math.round(summary.cloudCoverAvgPct)}%
        </div>
      </div>
    </div>
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
    latitude: location?.latitude ?? null,
    longitude: location?.longitude ?? null,
    anchor,
  })
  // Single-day weather for `month` / `year` presets is misleading — the
  // anchor day on those presets is just a cursor inside the period — so
  // we render nothing instead of guessing which day to display.
  if (!location || preset !== 'day') return null

  const day = weatherDayFromAnchor(anchor)
  const summary = summarizeWeatherDay(data, day)
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
    <CardShell city={city} day={day}>
      <CardBody summary={summary} />
    </CardShell>
  )
}
