import { describe, expect, it } from 'vitest'
import type { EnergyRow } from './buckets'
import type { DAMChartRow } from './dam'
import { revenueChartRows, totalRevenue } from './revenue'

function energy(pv: Array<number | undefined>): EnergyRow[] {
  return pv.map((v, i) => {
    const row: EnergyRow = { time: `t${i}` }
    if (v !== undefined) row.pv_energy_yield_day_kwh = v
    return row
  })
}

function dam(prices: Array<number | null>): DAMChartRow[] {
  return prices.map((price, i) => ({ time: `t${i}`, bucketStart: i, price }))
}

describe('revenueChartRows', () => {
  it('multiplies pv kWh by DAM price and converts MWh→kWh (divides by 1000)', () => {
    const rows = revenueChartRows(energy([2, 3]), dam([1000, 500]))
    expect(rows).toEqual([
      { time: 't0', revenue: 2 },
      { time: 't1', revenue: 1.5 },
    ])
  })

  it('returns null when pv is missing (future hour on day preset)', () => {
    const rows = revenueChartRows(energy([undefined, 4]), dam([1000, 1000]))
    expect(rows[0].revenue).toBeNull()
    expect(rows[1].revenue).toBe(4)
  })

  it('returns null when DAM price is null or missing', () => {
    const rows = revenueChartRows(energy([2, 3, 4]), dam([null, 1000, 500]))
    expect(rows[0].revenue).toBeNull()
    expect(rows[1].revenue).toBe(3)
    expect(rows[2].revenue).toBe(2)
  })

  it('aligns rows by time label, not by index', () => {
    const energyRows: EnergyRow[] = [
      { time: 'a', pv_energy_yield_day_kwh: 2 },
      { time: 'b', pv_energy_yield_day_kwh: 5 },
    ]
    const damRows: DAMChartRow[] = [
      { time: 'b', bucketStart: 0, price: 2000 },
      { time: 'a', bucketStart: 1, price: 1000 },
    ]
    const rows = revenueChartRows(energyRows, damRows)
    expect(rows).toEqual([
      { time: 'a', revenue: 2 },
      { time: 'b', revenue: 10 },
    ])
  })

  it('clamps zero pv to zero revenue without emitting null', () => {
    const rows = revenueChartRows(energy([0, 0]), dam([1500, 1500]))
    expect(rows[0].revenue).toBe(0)
    expect(rows[1].revenue).toBe(0)
  })
})

describe('totalRevenue', () => {
  it('sums numeric revenue rows and ignores null', () => {
    expect(
      totalRevenue([
        { time: 'a', revenue: 1.5 },
        { time: 'b', revenue: null },
        { time: 'c', revenue: 2.5 },
      ]),
    ).toBe(4)
  })

  it('returns 0 for empty input', () => {
    expect(totalRevenue([])).toBe(0)
  })
})
