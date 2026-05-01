import { describe, expect, it } from 'vitest'
import { DAY_BUCKET_MINUTES } from '../timeline'
import { averagePrice, damChartRows } from './dam'

const DAY_BUCKETS = (24 * 60) / DAY_BUCKET_MINUTES
const BUCKETS_PER_HOUR = 60 / DAY_BUCKET_MINUTES

describe('damChartRows', () => {
  const dayAnchor = new Date(2026, 3, 30)
  const monthAnchor = new Date(2026, 3, 1)
  const yearAnchor = new Date(2026, 0, 1)

  it('produces 288 five-minute points for day preset, repeating hourly price across each hour', () => {
    const prices = [
      { delivery_date: '2026-04-30', hour: 1, zone: 2, price_uah_per_mwh: 1000 },
      { delivery_date: '2026-04-30', hour: 5, zone: 2, price_uah_per_mwh: 1500 },
    ]
    const rows = damChartRows(prices, 'day', dayAnchor)
    expect(rows).toHaveLength(DAY_BUCKETS)
    // Hour "1" on OREE = local 00:00–01:00 = buckets [0..12).
    for (let i = 0; i < BUCKETS_PER_HOUR; i++) {
      expect(rows[i].price).toBe(1000)
    }
    // Adjacent hour without data is null across its whole block.
    for (let i = BUCKETS_PER_HOUR; i < 2 * BUCKETS_PER_HOUR; i++) {
      expect(rows[i].price).toBeNull()
    }
    // Hour "5" = local 04:00–05:00 = buckets [48..60).
    for (let i = 4 * BUCKETS_PER_HOUR; i < 5 * BUCKETS_PER_HOUR; i++) {
      expect(rows[i].price).toBe(1500)
    }
    expect(rows[DAY_BUCKETS - 1].price).toBeNull()
  })

  it('aggregates by day for month preset (averages 24 hours per day)', () => {
    const prices = [
      ...Array.from({ length: 24 }, (_, i) => ({
        delivery_date: '2026-04-29',
        hour: i + 1,
        zone: 2,
        price_uah_per_mwh: 1000,
      })),
      ...Array.from({ length: 24 }, (_, i) => ({
        delivery_date: '2026-04-30',
        hour: i + 1,
        zone: 2,
        price_uah_per_mwh: 2000,
      })),
    ]
    const rows = damChartRows(prices, 'month', monthAnchor)
    expect(rows).toHaveLength(30)
    expect(rows[28].price).toBe(1000)
    expect(rows[29].price).toBe(2000)
    expect(rows[0].price).toBeNull()
  })

  it('aggregates by month for year preset (returns 12 buckets)', () => {
    const prices = [
      { delivery_date: '2026-01-15', hour: 1, zone: 2, price_uah_per_mwh: 100 },
      { delivery_date: '2026-01-20', hour: 2, zone: 2, price_uah_per_mwh: 200 },
      { delivery_date: '2026-02-01', hour: 1, zone: 2, price_uah_per_mwh: 500 },
    ]
    const rows = damChartRows(prices, 'year', yearAnchor)
    expect(rows).toHaveLength(12)
    expect(rows[0].price).toBe(150)
    expect(rows[1].price).toBe(500)
    expect(rows[11].price).toBeNull()
  })

  it('skips rows with null/undefined price', () => {
    const prices = [
      { delivery_date: '2026-04-30', hour: 1, zone: 2, price_uah_per_mwh: null },
      { delivery_date: '2026-04-30', hour: 2, zone: 2 },
      { delivery_date: '2026-04-30', hour: 3, zone: 2, price_uah_per_mwh: 1000 },
    ]
    const rows = damChartRows(prices, 'day', dayAnchor)
    expect(rows).toHaveLength(DAY_BUCKETS)
    // Hours 1 and 2 have no price → whole blocks are null.
    expect(rows[0].price).toBeNull()
    expect(rows[BUCKETS_PER_HOUR].price).toBeNull()
    // Hour 3 = 02:00–03:00 = buckets [24..36).
    expect(rows[2 * BUCKETS_PER_HOUR].price).toBe(1000)
  })

  it('returns rows ordered by bucket start', () => {
    const prices = [
      { delivery_date: '2026-04-30', hour: 5, zone: 2, price_uah_per_mwh: 50 },
      { delivery_date: '2026-04-30', hour: 1, zone: 2, price_uah_per_mwh: 10 },
      { delivery_date: '2026-04-30', hour: 3, zone: 2, price_uah_per_mwh: 30 },
    ]
    const rows = damChartRows(prices, 'day', dayAnchor)
    expect(rows[0].price).toBe(10)
    expect(rows[2 * BUCKETS_PER_HOUR].price).toBe(30)
    expect(rows[4 * BUCKETS_PER_HOUR].price).toBe(50)
  })
})

describe('averagePrice', () => {
  it('averages numeric prices and ignores null', () => {
    expect(averagePrice([
      { time: '00', bucketStart: 0, price: 100 },
      { time: '01', bucketStart: 1, price: 200 },
      { time: '02', bucketStart: 2, price: null },
    ])).toBe(150)
  })

  it('returns null for empty input', () => {
    expect(averagePrice([])).toBeNull()
  })
})
