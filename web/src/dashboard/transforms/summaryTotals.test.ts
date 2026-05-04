import { describe, expect, it } from 'vitest'
import { summaryTotalsFromReadings } from './summaryTotals'

const ENERGY_KEYS = [
  'accumulated_pv_energy_yield_kwh',
  'accumulated_electricity_purchased_kwh',
  'accumulated_electricity_sold_kwh',
  'total_energy_charged_kwh',
  'total_energy_discharged_kwh',
  'accumulated_power_consumption_kwh',
]

describe('summaryTotalsFromReadings', () => {
  it('returns end - seed per metric', () => {
    const totals = summaryTotalsFromReadings(
      {
        seed: { accumulated_pv_energy_yield_kwh: 1000 },
        end: { accumulated_pv_energy_yield_kwh: 1187 },
      },
      ['accumulated_pv_energy_yield_kwh'],
    )
    expect(totals.accumulated_pv_energy_yield_kwh).toBe(187)
  })

  it('returns 0 when neither seed nor end has data', () => {
    const totals = summaryTotalsFromReadings(
      { seed: {}, end: {} },
      ['accumulated_pv_energy_yield_kwh'],
    )
    expect(totals.accumulated_pv_energy_yield_kwh).toBe(0)
  })

  it('treats missing seed as zero (fresh deployment edge case)', () => {
    const totals = summaryTotalsFromReadings(
      { seed: {}, end: { accumulated_pv_energy_yield_kwh: 50 } },
      ['accumulated_pv_energy_yield_kwh'],
    )
    expect(totals.accumulated_pv_energy_yield_kwh).toBe(50)
  })

  it('treats missing end as seed (no new data yet)', () => {
    const totals = summaryTotalsFromReadings(
      { seed: { accumulated_pv_energy_yield_kwh: 100 }, end: {} },
      ['accumulated_pv_energy_yield_kwh'],
    )
    expect(totals.accumulated_pv_energy_yield_kwh).toBe(0)
  })

  it('clamps negative diffs (counter rollback) to zero', () => {
    const totals = summaryTotalsFromReadings(
      {
        seed: { accumulated_pv_energy_yield_kwh: 1000 },
        end: { accumulated_pv_energy_yield_kwh: 900 },
      },
      ['accumulated_pv_energy_yield_kwh'],
    )
    expect(totals.accumulated_pv_energy_yield_kwh).toBe(0)
  })

  it('keeps the device-reported consumption when the counter is alive', () => {
    const totals = summaryTotalsFromReadings(
      {
        seed: {
          accumulated_electricity_purchased_kwh: 0,
          accumulated_pv_energy_yield_kwh: 0,
          total_energy_discharged_kwh: 0,
          total_energy_charged_kwh: 0,
          accumulated_electricity_sold_kwh: 0,
          accumulated_power_consumption_kwh: 0,
        },
        end: {
          accumulated_electricity_purchased_kwh: 50,
          accumulated_pv_energy_yield_kwh: 30,
          total_energy_discharged_kwh: 10,
          total_energy_charged_kwh: 20,
          accumulated_electricity_sold_kwh: 5,
          accumulated_power_consumption_kwh: 65,
        },
      },
      ENERGY_KEYS,
    )
    expect(totals.accumulated_power_consumption_kwh).toBe(65)
    expect(totals.accumulated_electricity_sold_kwh).toBe(5)
  })

  it('falls back to algebraic formula when device counter is zero (silent counter)', () => {
    const totals = summaryTotalsFromReadings(
      {
        seed: {
          accumulated_electricity_purchased_kwh: 0,
          accumulated_pv_energy_yield_kwh: 0,
          total_energy_discharged_kwh: 0,
          total_energy_charged_kwh: 0,
          accumulated_electricity_sold_kwh: 0,
          accumulated_power_consumption_kwh: 0,
        },
        end: {
          accumulated_electricity_purchased_kwh: 50,
          accumulated_pv_energy_yield_kwh: 30,
          total_energy_discharged_kwh: 10,
          total_energy_charged_kwh: 20,
          accumulated_electricity_sold_kwh: 5,
          accumulated_power_consumption_kwh: 0,
        },
      },
      ENERGY_KEYS,
    )
    // formula: purchased + pv + discharge - charge = 50 + 30 + 10 - 20 = 70
    expect(totals.accumulated_power_consumption_kwh).toBe(70)
  })

  it('ignores keys not in metricKeys list', () => {
    const totals = summaryTotalsFromReadings(
      {
        seed: { other_metric: 100 },
        end: { other_metric: 200, accumulated_pv_energy_yield_kwh: 50 },
      },
      ['accumulated_pv_energy_yield_kwh'],
    )
    expect(totals.accumulated_pv_energy_yield_kwh).toBe(50)
    expect(totals.other_metric).toBeUndefined()
  })
})
