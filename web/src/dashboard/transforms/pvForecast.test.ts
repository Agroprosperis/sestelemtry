import { describe, expect, it } from 'vitest'
import {
  forecastDayFromAnchor,
  pvForecastLabel,
  pvForecastRows,
  pvPlanCaption,
} from './pvForecast'

describe('whole-elevator labels', () => {
  it('marks the series label and the plan caption', () => {
    expect(pvForecastLabel(false)).toBe('Прогноз СЕС')
    expect(pvForecastLabel(true)).toBe('Прогноз СЕС (весь елеватор)')
    expect(pvPlanCaption()).toBe('План')
    expect(pvPlanCaption(true)).toBe('План (весь елеватор)')
  })
})

describe('forecastDayFromAnchor', () => {
  it('renders YYYY-MM-DD in local TZ', () => {
    expect(forecastDayFromAnchor(new Date(2026, 4, 7))).toBe('2026-05-07')
    expect(forecastDayFromAnchor(new Date(2026, 0, 1))).toBe('2026-01-01')
    expect(forecastDayFromAnchor(new Date(2026, 11, 31))).toBe('2026-12-31')
  })
})

describe('pvForecastRows', () => {
  it('maps the API hours to chart rows, ordered by hour', () => {
    expect(
      pvForecastRows([
        { hour: 13, planned_kwh: 4 },
        { hour: 11, planned_kwh: 15 },
      ]),
    ).toEqual([
      { hour: 11, plannedKw: 15 },
      { hour: 13, plannedKw: 4 },
    ])
  })

  // Night hours and garbage must not draw a flat line at zero.
  it('drops dark hours and values that are not usable numbers', () => {
    expect(
      pvForecastRows([
        { hour: 2, planned_kwh: 0 },
        { hour: 24, planned_kwh: 5 },
        { hour: 5, planned_kwh: Number.NaN },
        { hour: 14, planned_kwh: 11 },
      ]),
    ).toEqual([{ hour: 14, plannedKw: 11 }])
  })

  it('returns no rows without hours', () => {
    expect(pvForecastRows(undefined)).toEqual([])
    expect(pvForecastRows([])).toEqual([])
  })
})
