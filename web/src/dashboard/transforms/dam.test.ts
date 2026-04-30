import { describe, expect, it } from 'vitest'
import { averagePrice, damChartRows } from './dam'

describe('damChartRows', () => {
  const dayAnchor = new Date(2026, 3, 30)
  const monthAnchor = new Date(2026, 3, 1)
  const yearAnchor = new Date(2026, 0, 1)

  it('produces 24 hourly points for day preset, filling empty hours with null', () => {
    const prices = [
      { delivery_date: '2026-04-30', hour: 1, zone: 2, price_uah_per_mwh: 1000 },
      { delivery_date: '2026-04-30', hour: 5, zone: 2, price_uah_per_mwh: 1500 },
    ]
    const rows = damChartRows(prices, 'day', dayAnchor)
    expect(rows).toHaveLength(24)
    expect(rows[0].price).toBe(1000)
    expect(rows[1].price).toBeNull()
    expect(rows[4].price).toBe(1500)
    expect(rows[23].price).toBeNull()
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
    expect(rows).toHaveLength(24)
    expect(rows[0].price).toBeNull()
    expect(rows[1].price).toBeNull()
    expect(rows[2].price).toBe(1000)
  })

  it('returns rows ordered by bucket start', () => {
    const prices = [
      { delivery_date: '2026-04-30', hour: 5, zone: 2, price_uah_per_mwh: 50 },
      { delivery_date: '2026-04-30', hour: 1, zone: 2, price_uah_per_mwh: 10 },
      { delivery_date: '2026-04-30', hour: 3, zone: 2, price_uah_per_mwh: 30 },
    ]
    const rows = damChartRows(prices, 'day', dayAnchor)
    expect(rows[0].price).toBe(10)
    expect(rows[2].price).toBe(30)
    expect(rows[4].price).toBe(50)
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
