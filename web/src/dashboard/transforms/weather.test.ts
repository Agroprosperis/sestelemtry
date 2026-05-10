import { describe, expect, it } from 'vitest'
import type { OpenMeteoForecast, OrganizationInfo } from '../../types'
import {
  classifyCondition,
  locationFor,
  summarizeWeatherDay,
  weatherDayFromAnchor,
} from './weather'

function forecast(over: Partial<OpenMeteoForecast>): OpenMeteoForecast {
  return {
    hourly: {
      time: [],
      temperature_2m: [],
      cloud_cover: [],
    },
    daily: {
      time: [],
      sunshine_duration: [],
      daylight_duration: [],
    },
    ...over,
  }
}

describe('locationFor', () => {
  const orgs: OrganizationInfo[] = [
    {
      id: 'ze',
      name: 'ZE',
      location: { latitude: 49.0191004, longitude: 28.1260144, city: 'Жмеринка' },
    },
    { id: 'demo-org', name: 'Demo organization' },
  ]

  it('returns the location for an org with one configured', () => {
    expect(locationFor('ze', orgs)).toEqual({
      latitude: 49.0191004,
      longitude: 28.1260144,
      city: 'Жмеринка',
    })
  })

  it('returns null when the org exists but has no location', () => {
    expect(locationFor('demo-org', orgs)).toBeNull()
  })

  it('returns null when the org is absent from the list', () => {
    expect(locationFor('unknown', orgs)).toBeNull()
    expect(locationFor('', orgs)).toBeNull()
    expect(locationFor('ze', [])).toBeNull()
  })
})

describe('weatherDayFromAnchor', () => {
  it('renders YYYY-MM-DD in local TZ', () => {
    expect(weatherDayFromAnchor(new Date(2026, 4, 10))).toBe('2026-05-10')
    expect(weatherDayFromAnchor(new Date(2026, 0, 1))).toBe('2026-01-01')
    expect(weatherDayFromAnchor(new Date(2026, 11, 31))).toBe('2026-12-31')
  })
})

describe('classifyCondition', () => {
  it('maps high sunshine ratios to sunny', () => {
    expect(classifyCondition(0.95, 10)).toBe('sunny')
    expect(classifyCondition(0.7, 50)).toBe('sunny')
  })

  it('maps mid sunshine ratios to partly_cloudy', () => {
    expect(classifyCondition(0.55, 40)).toBe('partly_cloudy')
    expect(classifyCondition(0.4, 60)).toBe('partly_cloudy')
  })

  it('maps low sunshine ratios to cloudy', () => {
    expect(classifyCondition(0.3, 75)).toBe('cloudy')
    expect(classifyCondition(0.2, 90)).toBe('cloudy')
  })

  it('maps very low sunshine ratios to overcast', () => {
    expect(classifyCondition(0.05, 95)).toBe('overcast')
    expect(classifyCondition(0, 100)).toBe('overcast')
  })

  it('falls back to cloud cover when sunshine ratio is not finite', () => {
    expect(classifyCondition(Number.NaN, 10)).toBe('sunny')
    expect(classifyCondition(Number.NaN, 50)).toBe('partly_cloudy')
    expect(classifyCondition(Number.NaN, 75)).toBe('cloudy')
    expect(classifyCondition(Number.NaN, 95)).toBe('overcast')
  })
})

describe('summarizeWeatherDay', () => {
  it('returns null when the day is missing from daily.time', () => {
    const fc = forecast({
      daily: {
        time: ['2026-05-10'],
        sunshine_duration: [30000],
        daylight_duration: [50000],
      },
    })
    expect(summarizeWeatherDay(fc, '2026-05-11')).toBeNull()
  })

  it('returns null when no hourly samples match the day', () => {
    const fc = forecast({
      hourly: {
        time: ['2026-05-09T10:00', '2026-05-09T11:00'],
        temperature_2m: [10, 12],
        cloud_cover: [50, 60],
      },
      daily: {
        time: ['2026-05-10'],
        sunshine_duration: [30000],
        daylight_duration: [50000],
      },
    })
    expect(summarizeWeatherDay(fc, '2026-05-10')).toBeNull()
  })

  it('aggregates min/max temperature and average cloud cover for the day', () => {
    const fc = forecast({
      hourly: {
        time: [
          '2026-05-10T00:00',
          '2026-05-10T06:00',
          '2026-05-10T12:00',
          '2026-05-10T18:00',
          '2026-05-11T00:00',
        ],
        temperature_2m: [5, 8, 18, 14, 4],
        cloud_cover: [80, 60, 20, 40, 100],
      },
      daily: {
        time: ['2026-05-10'],
        sunshine_duration: [36000],
        daylight_duration: [50000],
      },
    })
    const out = summarizeWeatherDay(fc, '2026-05-10')
    expect(out).not.toBeNull()
    expect(out?.day).toBe('2026-05-10')
    expect(out?.tempMinC).toBe(5)
    expect(out?.tempMaxC).toBe(18)
    expect(out?.cloudCoverAvgPct).toBe(50)
    // sunshine/daylight = 36000/50000 = 0.72 → sunny.
    expect(out?.condition).toBe('sunny')
  })

  it('handles null forecast input', () => {
    expect(summarizeWeatherDay(null, '2026-05-10')).toBeNull()
  })
})
