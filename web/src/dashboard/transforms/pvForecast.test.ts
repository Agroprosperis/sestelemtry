import { describe, expect, it } from 'vitest'
import type { PvForecastPoint } from '../../types'
import {
  aggregatePvForecastHourly,
  elevatorCodeFor,
  forecastDayFromAnchor,
} from './pvForecast'

function point(over: Partial<PvForecastPoint>): PvForecastPoint {
  return {
    elevator_code: 'JE',
    orientation_idx: 1,
    hour_ending: 12,
    interval_start_local: '2026-05-07T11:00:00+03:00',
    gti_weighted_wm2: 0,
    pdc_total_kwp: 0,
    pac_limit_kw: 0,
    planned_dc_kw: 0,
    planned_ac_kw: 0,
    planned_kwh: 0,
    clip_loss_kwh: 0,
    temperature_2m_c: 0,
    cloud_cover_pct: 0,
    model_version: 'pv_v1',
    ...over,
  }
}

describe('elevatorCodeFor', () => {
  it('maps known organizations to elevator codes', () => {
    expect(elevatorCodeFor('ze')).toBe('JE')
    expect(elevatorCodeFor('pe')).toBe('RE')
  })

  it('returns null for organizations without a forecast', () => {
    expect(elevatorCodeFor('demo-org')).toBeNull()
    expect(elevatorCodeFor('')).toBeNull()
    expect(elevatorCodeFor('unknown')).toBeNull()
  })
})

describe('forecastDayFromAnchor', () => {
  it('renders YYYY-MM-DD in local TZ', () => {
    expect(forecastDayFromAnchor(new Date(2026, 4, 7))).toBe('2026-05-07')
    expect(forecastDayFromAnchor(new Date(2026, 0, 1))).toBe('2026-01-01')
    expect(forecastDayFromAnchor(new Date(2026, 11, 31))).toBe('2026-12-31')
  })
})

describe('aggregatePvForecastHourly', () => {
  it('sums planned_kwh across orientations per hour and shifts hour_ending to hour start', () => {
    const points = [
      point({ orientation_idx: 1, hour_ending: 12, planned_kwh: 5 }),
      point({ orientation_idx: 2, hour_ending: 12, planned_kwh: 7 }),
      point({ orientation_idx: 3, hour_ending: 12, planned_kwh: 3 }),
      point({ orientation_idx: 1, hour_ending: 13, planned_kwh: 4 }),
    ]
    const out = aggregatePvForecastHourly(points)
    expect(out).toEqual([
      { hour: 11, plannedKw: 15 },
      { hour: 12, plannedKw: 4 },
    ])
  })

  it('deduplicates same (hour_ending, orientation_idx); last record wins', () => {
    const points = [
      point({ orientation_idx: 1, hour_ending: 10, planned_kwh: 2 }),
      point({ orientation_idx: 1, hour_ending: 10, planned_kwh: 9 }),
      point({ orientation_idx: 2, hour_ending: 10, planned_kwh: 1 }),
    ]
    const out = aggregatePvForecastHourly(points)
    expect(out).toEqual([{ hour: 9, plannedKw: 10 }])
  })

  it('skips invalid records (NaN, out-of-range hour_ending)', () => {
    const points = [
      point({ orientation_idx: 1, hour_ending: 0, planned_kwh: 5 }),
      point({ orientation_idx: 1, hour_ending: 25, planned_kwh: 5 }),
      point({ orientation_idx: 1, hour_ending: 5, planned_kwh: Number.NaN }),
      point({ orientation_idx: 2, hour_ending: 5, planned_kwh: 4 }),
    ]
    const out = aggregatePvForecastHourly(points)
    expect(out).toEqual([{ hour: 4, plannedKw: 4 }])
  })

  it('drops hours that aggregate to zero or negative (nighttime)', () => {
    const points = [
      point({ orientation_idx: 1, hour_ending: 1, planned_kwh: 0 }),
      point({ orientation_idx: 2, hour_ending: 1, planned_kwh: 0 }),
      point({ orientation_idx: 1, hour_ending: 14, planned_kwh: 11 }),
    ]
    const out = aggregatePvForecastHourly(points)
    expect(out).toEqual([{ hour: 13, plannedKw: 11 }])
  })

  it('returns empty array for empty input', () => {
    expect(aggregatePvForecastHourly([])).toEqual([])
  })

  it('returns hours sorted ascending', () => {
    const points = [
      point({ orientation_idx: 1, hour_ending: 20, planned_kwh: 1 }),
      point({ orientation_idx: 1, hour_ending: 5, planned_kwh: 2 }),
      point({ orientation_idx: 1, hour_ending: 12, planned_kwh: 3 }),
    ]
    const out = aggregatePvForecastHourly(points)
    expect(out.map((r) => r.hour)).toEqual([4, 11, 19])
  })
})
