import { describe, expect, it } from 'vitest'
import type { TimeseriesPoint } from '../../types'
import { cumulativeBucketDeltaRows, cumulativeTotals } from './cumulative'

const ENERGY_KEYS = [
  'accumulated_pv_energy_yield_kwh',
  'accumulated_electricity_purchased_kwh',
  'accumulated_electricity_sold_kwh',
  'total_energy_charged_kwh',
  'total_energy_discharged_kwh',
  'accumulated_power_consumption_kwh',
]

function dayBucketTime(anchor: Date, dayIndex: number): string {
  const d = new Date(anchor)
  d.setDate(d.getDate() + dayIndex)
  d.setHours(0, 0, 0, 0)
  return d.toISOString()
}

function monthBucketTime(yearAnchor: Date, monthIndex: number): string {
  const d = new Date(yearAnchor)
  d.setMonth(monthIndex, 1)
  d.setHours(0, 0, 0, 0)
  return d.toISOString()
}

describe('cumulativeBucketDeltaRows (month preset)', () => {
  const anchor = new Date(2026, 4, 1)

  it('returns 31 rows aligned to the month timeline', () => {
    const rows = cumulativeBucketDeltaRows(
      { bucketPoints: [], seed: { accumulated_pv_energy_yield_kwh: 0 } },
      ['accumulated_pv_energy_yield_kwh'],
      'month',
      anchor,
    )
    expect(rows).toHaveLength(31)
    expect(rows.every((r) => r.accumulated_pv_energy_yield_kwh === 0)).toBe(true)
  })

  it('telescopes per-day deltas using the seed for day 0', () => {
    const points: TimeseriesPoint[] = [
      { time: dayBucketTime(anchor, 0), metric_key: 'accumulated_pv_energy_yield_kwh', value: 110 },
      { time: dayBucketTime(anchor, 1), metric_key: 'accumulated_pv_energy_yield_kwh', value: 130 },
      { time: dayBucketTime(anchor, 2), metric_key: 'accumulated_pv_energy_yield_kwh', value: 145 },
    ]
    const rows = cumulativeBucketDeltaRows(
      { bucketPoints: points, seed: { accumulated_pv_energy_yield_kwh: 100 } },
      ['accumulated_pv_energy_yield_kwh'],
      'month',
      anchor,
    )
    expect(rows[0].accumulated_pv_energy_yield_kwh).toBe(10)
    expect(rows[1].accumulated_pv_energy_yield_kwh).toBe(20)
    expect(rows[2].accumulated_pv_energy_yield_kwh).toBe(15)
    expect(rows[3].accumulated_pv_energy_yield_kwh).toBe(0)
  })

  it('uses first observed bucket as implicit seed when seed is missing', () => {
    const points: TimeseriesPoint[] = [
      { time: dayBucketTime(anchor, 5), metric_key: 'accumulated_pv_energy_yield_kwh', value: 50 },
      { time: dayBucketTime(anchor, 6), metric_key: 'accumulated_pv_energy_yield_kwh', value: 70 },
    ]
    const rows = cumulativeBucketDeltaRows(
      { bucketPoints: points, seed: {} },
      ['accumulated_pv_energy_yield_kwh'],
      'month',
      anchor,
    )
    expect(rows[5].accumulated_pv_energy_yield_kwh).toBe(0)
    expect(rows[6].accumulated_pv_energy_yield_kwh).toBe(20)
  })

  it('clamps negative deltas to zero (counter rollback)', () => {
    const points: TimeseriesPoint[] = [
      { time: dayBucketTime(anchor, 0), metric_key: 'accumulated_pv_energy_yield_kwh', value: 110 },
      { time: dayBucketTime(anchor, 1), metric_key: 'accumulated_pv_energy_yield_kwh', value: 90 },
    ]
    const rows = cumulativeBucketDeltaRows(
      { bucketPoints: points, seed: { accumulated_pv_energy_yield_kwh: 100 } },
      ['accumulated_pv_energy_yield_kwh'],
      'month',
      anchor,
    )
    expect(rows[0].accumulated_pv_energy_yield_kwh).toBe(10)
    expect(rows[1].accumulated_pv_energy_yield_kwh).toBe(0)
  })

  it('applies sign direction for sink metrics', () => {
    const points: TimeseriesPoint[] = [
      { time: dayBucketTime(anchor, 0), metric_key: 'accumulated_electricity_sold_kwh', value: 5 },
      { time: dayBucketTime(anchor, 1), metric_key: 'accumulated_electricity_sold_kwh', value: 12 },
    ]
    const rows = cumulativeBucketDeltaRows(
      { bucketPoints: points, seed: { accumulated_electricity_sold_kwh: 0 } },
      ['accumulated_electricity_sold_kwh'],
      'month',
      anchor,
    )
    expect(rows[0].accumulated_electricity_sold_kwh).toBe(-5)
    expect(rows[1].accumulated_electricity_sold_kwh).toBe(-7)
  })

  it('recomputes appliance consumption from formula', () => {
    const points: TimeseriesPoint[] = [
      { time: dayBucketTime(anchor, 0), metric_key: 'accumulated_electricity_purchased_kwh', value: 5 },
      { time: dayBucketTime(anchor, 0), metric_key: 'accumulated_pv_energy_yield_kwh', value: 4 },
      { time: dayBucketTime(anchor, 0), metric_key: 'total_energy_discharged_kwh', value: 3 },
      { time: dayBucketTime(anchor, 0), metric_key: 'total_energy_charged_kwh', value: 2 },
      { time: dayBucketTime(anchor, 0), metric_key: 'accumulated_power_consumption_kwh', value: 0 },
    ]
    const seed: Record<string, number> = {}
    for (const k of ENERGY_KEYS) seed[k] = 0
    const rows = cumulativeBucketDeltaRows(
      { bucketPoints: points, seed },
      ENERGY_KEYS,
      'month',
      anchor,
    )
    // 5 + 4 + 3 - 2 = 10, then sign direction (-1) flips it to -10.
    expect(rows[0].accumulated_power_consumption_kwh).toBe(-10)
  })

  it('holds the previous cumulative value across empty buckets', () => {
    const points: TimeseriesPoint[] = [
      { time: dayBucketTime(anchor, 0), metric_key: 'accumulated_pv_energy_yield_kwh', value: 110 },
      // day 1 missing
      { time: dayBucketTime(anchor, 2), metric_key: 'accumulated_pv_energy_yield_kwh', value: 150 },
    ]
    const rows = cumulativeBucketDeltaRows(
      { bucketPoints: points, seed: { accumulated_pv_energy_yield_kwh: 100 } },
      ['accumulated_pv_energy_yield_kwh'],
      'month',
      anchor,
    )
    expect(rows[0].accumulated_pv_energy_yield_kwh).toBe(10)
    expect(rows[1].accumulated_pv_energy_yield_kwh).toBe(0)
    // The two missing days collapse into the next observed delta, so the
    // displayed bar is bigger but the telescoping sum remains correct.
    expect(rows[2].accumulated_pv_energy_yield_kwh).toBe(40)
  })
})

describe('cumulativeBucketDeltaRows (year preset)', () => {
  const yearAnchor = new Date(2026, 0, 1)

  it('produces 12 monthly rows from monthly cumulative readings', () => {
    const points: TimeseriesPoint[] = [
      { time: monthBucketTime(yearAnchor, 0), metric_key: 'accumulated_pv_energy_yield_kwh', value: 1100 },
      { time: monthBucketTime(yearAnchor, 1), metric_key: 'accumulated_pv_energy_yield_kwh', value: 1300 },
      { time: monthBucketTime(yearAnchor, 2), metric_key: 'accumulated_pv_energy_yield_kwh', value: 1450 },
    ]
    const rows = cumulativeBucketDeltaRows(
      { bucketPoints: points, seed: { accumulated_pv_energy_yield_kwh: 1000 } },
      ['accumulated_pv_energy_yield_kwh'],
      'year',
      yearAnchor,
    )
    expect(rows).toHaveLength(12)
    expect(rows[0].accumulated_pv_energy_yield_kwh).toBe(100)
    expect(rows[1].accumulated_pv_energy_yield_kwh).toBe(200)
    expect(rows[2].accumulated_pv_energy_yield_kwh).toBe(150)
    expect(rows[3].accumulated_pv_energy_yield_kwh).toBe(0)
  })
})

describe('cumulativeTotals', () => {
  const anchor = new Date(2026, 4, 1)

  it('returns last observed minus seed', () => {
    const points: TimeseriesPoint[] = [
      { time: dayBucketTime(anchor, 0), metric_key: 'accumulated_pv_energy_yield_kwh', value: 110 },
      { time: dayBucketTime(anchor, 5), metric_key: 'accumulated_pv_energy_yield_kwh', value: 200 },
    ]
    const totals = cumulativeTotals(
      { bucketPoints: points, seed: { accumulated_pv_energy_yield_kwh: 100 } },
      ['accumulated_pv_energy_yield_kwh'],
      'month',
      anchor,
    )
    expect(totals.accumulated_pv_energy_yield_kwh).toBe(100)
  })

  it('returns 0 when there is neither seed nor data', () => {
    const totals = cumulativeTotals(
      { bucketPoints: [], seed: {} },
      ['accumulated_pv_energy_yield_kwh'],
      'month',
      anchor,
    )
    expect(totals.accumulated_pv_energy_yield_kwh).toBe(0)
  })

  it('uses implicit seed for fresh deployments without pre-period samples', () => {
    const points: TimeseriesPoint[] = [
      { time: dayBucketTime(anchor, 5), metric_key: 'accumulated_pv_energy_yield_kwh', value: 50 },
      { time: dayBucketTime(anchor, 6), metric_key: 'accumulated_pv_energy_yield_kwh', value: 70 },
    ]
    const totals = cumulativeTotals(
      { bucketPoints: points, seed: {} },
      ['accumulated_pv_energy_yield_kwh'],
      'month',
      anchor,
    )
    expect(totals.accumulated_pv_energy_yield_kwh).toBe(20)
  })

  it('clamps negative totals to zero', () => {
    const points: TimeseriesPoint[] = [
      { time: dayBucketTime(anchor, 0), metric_key: 'accumulated_pv_energy_yield_kwh', value: 90 },
    ]
    const totals = cumulativeTotals(
      { bucketPoints: points, seed: { accumulated_pv_energy_yield_kwh: 100 } },
      ['accumulated_pv_energy_yield_kwh'],
      'month',
      anchor,
    )
    expect(totals.accumulated_pv_energy_yield_kwh).toBe(0)
  })

  it('applies appliance consumption rule to totals', () => {
    const seed: Record<string, number> = {}
    for (const k of ENERGY_KEYS) seed[k] = 0
    const points: TimeseriesPoint[] = [
      { time: dayBucketTime(anchor, 0), metric_key: 'accumulated_electricity_purchased_kwh', value: 50 },
      { time: dayBucketTime(anchor, 0), metric_key: 'accumulated_pv_energy_yield_kwh', value: 30 },
      { time: dayBucketTime(anchor, 0), metric_key: 'total_energy_discharged_kwh', value: 10 },
      { time: dayBucketTime(anchor, 0), metric_key: 'total_energy_charged_kwh', value: 20 },
      // Device-reported consumption is intentionally absurd to prove the
      // appliance rule overrides it with the formula result.
      { time: dayBucketTime(anchor, 0), metric_key: 'accumulated_power_consumption_kwh', value: 1500 },
    ]
    const totals = cumulativeTotals(
      { bucketPoints: points, seed },
      ENERGY_KEYS,
      'month',
      anchor,
    )
    expect(totals.accumulated_power_consumption_kwh).toBe(70)
  })
})
