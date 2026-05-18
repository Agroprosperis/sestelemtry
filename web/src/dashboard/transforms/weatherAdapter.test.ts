import { describe, expect, it } from 'vitest'
import { utcIsoToLocalKey, weatherFromApi } from './weatherAdapter'

describe('utcIsoToLocalKey', () => {
  it('formats a UTC ISO timestamp as YYYY-MM-DDTHH:MM in the browser local TZ', () => {
    // Build the expected string from the same Date object the
    // function uses internally so the test isn't TZ-fragile — we just
    // verify the format, not the absolute offset (CI runners may be in
    // UTC; developer machines may be in Kyiv).
    const utcISO = '2026-05-15T00:00:00Z'
    const d = new Date(utcISO)
    const pad = (n: number) => (n < 10 ? `0${n}` : String(n))
    const want = `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`
    expect(utcIsoToLocalKey(utcISO)).toBe(want)
  })
})

describe('weatherFromApi', () => {
  it('returns null when the backend has no rows', () => {
    expect(
      weatherFromApi({
        organization_id: 'ze',
        from: '2026-05-15T00:00:00Z',
        to: '2026-05-15T00:00:00Z',
        hourly: [],
        daily: [],
      }),
    ).toBeNull()
  })

  it('reshapes hourly and daily rows into the OpenMeteoForecast type', () => {
    const utcISO = '2026-05-15T00:00:00Z'
    const got = weatherFromApi({
      organization_id: 'ze',
      from: '2026-05-15T00:00:00Z',
      to: '2026-05-15T23:59:59Z',
      hourly: [
        {
          hour: utcISO,
          temperature_2m_c: 12.3,
          cloud_cover_pct: 40,
          is_day: false,
        },
        {
          hour: '2026-05-15T01:00:00Z',
          temperature_2m_c: null,
          cloud_cover_pct: null,
          is_day: true,
        },
      ],
      daily: [
        {
          day: '2026-05-15',
          sunshine_duration_s: 40000,
          daylight_duration_s: 55080,
        },
      ],
    })
    expect(got).not.toBeNull()
    expect(got!.hourly.time).toHaveLength(2)
    expect(got!.hourly.time[0]).toBe(utcIsoToLocalKey(utcISO))
    expect(got!.hourly.temperature_2m[0]).toBe(12.3)
    expect(got!.hourly.cloud_cover[0]).toBe(40)
    expect(got!.hourly.is_day![0]).toBe(0)
    expect(got!.hourly.is_day![1]).toBe(1)
    // Missing scalar fields surface as NaN so downstream Number.isFinite
    // checks in transforms/weather.ts skip them naturally.
    expect(Number.isNaN(got!.hourly.temperature_2m[1])).toBe(true)
    expect(Number.isNaN(got!.hourly.cloud_cover[1])).toBe(true)

    expect(got!.daily.time).toEqual(['2026-05-15'])
    expect(got!.daily.sunshine_duration[0]).toBe(40000)
    expect(got!.daily.daylight_duration[0]).toBe(55080)
  })

  it('returns null when only one of hourly/daily is empty? no — both empty', () => {
    // weatherFromApi returns non-null when at least one of the arrays
    // has data; useWeather treats that as "use it". We only fall back
    // when both arrays are empty (the backend hasn't ingested this
    // org/range yet).
    const got = weatherFromApi({
      organization_id: 'ze',
      from: '2026-05-15T00:00:00Z',
      to: '2026-05-15T23:59:59Z',
      hourly: [
        {
          hour: '2026-05-15T00:00:00Z',
          temperature_2m_c: 12,
          cloud_cover_pct: 50,
          is_day: true,
        },
      ],
      daily: [],
    })
    expect(got).not.toBeNull()
  })
})
