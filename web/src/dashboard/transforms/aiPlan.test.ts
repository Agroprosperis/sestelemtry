import { describe, expect, it } from 'vitest'
import type { UzePlanHour, UzePlanResponse } from '../../api'
import { aiPlanBuckets, aiPlanHasDispatch } from './aiPlan'

function hour(over: Partial<UzePlanHour> & { hour: number }): UzePlanHour {
  return {
    recommended_ess_kw: 0,
    soc_pct: 50,
    ess_to_load_kwh: 0,
    ess_to_grid_kwh: 0,
    pv_to_ess_kwh: 0,
    grid_to_ess_kwh: 0,
    effect_uah: 0,
    action: 'hold',
    reason_code: 'HOLD_LOW_PRICE',
    reason_text: 'Утримуємо',
    rdn_uah_per_kwh: null,
    ...over,
  }
}

function plan(hours: UzePlanHour[], over: Partial<UzePlanResponse> = {}): UzePlanResponse {
  return {
    organization_id: 'ze',
    date: '2026-04-01',
    tz: 'Europe/Kyiv',
    available: true,
    soc_start_pct: 10,
    capacity_kwh: 100,
    power_kw: 50,
    hours,
    totals: {
      optimum_uah: 0,
      fact_uah: 0,
      reserve_uah: 0,
      captured_share: 0,
      charge_pv_kwh: 0,
      charge_grid_kwh: 0,
      discharge_kwh: 0,
      export_val_uah: 0,
      load_val_uah: 0,
      charge_pv_cost_uah: 0,
      grid_cost_uah: 0,
      degradation_uah: 0,
    },
    ...over,
  }
}

describe('aiPlanBuckets', () => {
  it('repeats the hourly power across all 12 buckets of its hour', () => {
    const buckets = aiPlanBuckets(plan([hour({ hour: 2, recommended_ess_kw: -42 })]))

    expect(buckets.size).toBe(12)
    for (let i = 24; i < 36; i++) {
      expect(buckets.get(i)?.essKw).toBe(-42)
    }
    expect(buckets.get(23)).toBeUndefined()
    expect(buckets.get(36)).toBeUndefined()
  })

  it('places SOC on the hour boundary only', () => {
    const buckets = aiPlanBuckets(plan([hour({ hour: 0, soc_pct: 88 })]))

    for (let i = 0; i < 11; i++) {
      expect(buckets.get(i)?.socPct).toBeNull()
    }
    expect(buckets.get(11)?.socPct).toBe(88)
  })

  it('preserves the sign convention: negative is charge, positive is discharge', () => {
    const buckets = aiPlanBuckets(
      plan([
        hour({ hour: 1, recommended_ess_kw: -50, action: 'charge' }),
        hour({ hour: 20, recommended_ess_kw: 50, action: 'discharge' }),
      ]),
    )

    expect(buckets.get(12)?.essKw).toBe(-50)
    expect(buckets.get(12)?.action).toBe('charge')
    expect(buckets.get(240)?.essKw).toBe(50)
    expect(buckets.get(240)?.action).toBe('discharge')
  })

  it('carries the reason text so the tooltip can explain the hour', () => {
    const buckets = aiPlanBuckets(
      plan([hour({ hour: 3, recommended_ess_kw: -10, reason_text: 'Заряд від надлишку СЕС' })]),
    )

    expect(buckets.get(36)?.reasonText).toBe('Заряд від надлишку СЕС')
  })

  it('returns nothing for a missing or unavailable plan', () => {
    expect(aiPlanBuckets(null).size).toBe(0)
    expect(aiPlanBuckets(plan([hour({ hour: 0 })], { available: false })).size).toBe(0)
    expect(aiPlanBuckets(plan([])).size).toBe(0)
  })

  it('skips hours with an out-of-range index or a non-finite power', () => {
    const buckets = aiPlanBuckets(
      plan([
        hour({ hour: 24, recommended_ess_kw: 10 }),
        hour({ hour: -1, recommended_ess_kw: 10 }),
        hour({ hour: 5, recommended_ess_kw: Number.NaN }),
        hour({ hour: 6, recommended_ess_kw: 10 }),
      ]),
    )

    expect(buckets.size).toBe(12)
    expect(buckets.get(72)?.essKw).toBe(10)
  })
})

describe('aiPlanHasDispatch', () => {
  it('is false when the optimum is to do nothing all day', () => {
    expect(aiPlanHasDispatch(plan([hour({ hour: 0 }), hour({ hour: 1 })]))).toBe(false)
  })

  it('ignores rounding noise below half a kW', () => {
    expect(aiPlanHasDispatch(plan([hour({ hour: 0, recommended_ess_kw: 0.2 })]))).toBe(false)
  })

  it('is true once a single hour moves real energy', () => {
    expect(aiPlanHasDispatch(plan([hour({ hour: 0, recommended_ess_kw: -12 })]))).toBe(true)
  })

  it('is false for a missing or unavailable plan', () => {
    expect(aiPlanHasDispatch(null)).toBe(false)
    expect(
      aiPlanHasDispatch(plan([hour({ hour: 0, recommended_ess_kw: -12 })], { available: false })),
    ).toBe(false)
  })
})
