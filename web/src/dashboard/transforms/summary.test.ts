import { describe, expect, it } from 'vitest'
import { energySummaryFromSeries, sumSeriesValue } from './summary'
import type { EnergyRow } from './buckets'

describe('sumSeriesValue', () => {
  const rows: EnergyRow[] = [
    { time: '00', a: 5, b: -3 },
    { time: '01', a: -2, b: 4 },
    { time: '02', a: 7, b: -1 },
  ]

  it('sums only positive values in positive mode', () => {
    expect(sumSeriesValue(rows, 'a', 'positive')).toBe(12)
  })

  it('sums absolute values in absolute mode', () => {
    expect(sumSeriesValue(rows, 'b', 'absolute')).toBe(8)
  })

  it('returns 0 when key missing', () => {
    expect(sumSeriesValue(rows, 'missing', 'positive')).toBe(0)
  })
})

describe('energySummaryFromSeries', () => {
  it('computes percentages for a balanced series', () => {
    const rows: EnergyRow[] = [
      {
        time: '00',
        pv_energy_yield_day_kwh: 100,
        accumulated_electricity_sold_kwh: -25,
        accumulated_power_consumption_kwh: -80,
        accumulated_electricity_purchased_kwh: 30,
      },
    ]
    const s = energySummaryFromSeries(rows)
    expect(s.pvProduced).toBe(100)
    expect(s.gridExport).toBe(25)
    expect(s.pvConsumed).toBe(75)
    expect(s.consumption).toBe(80)
    expect(s.fromGrid).toBe(30)
    expect(s.fromPV).toBe(50)
    expect(s.pvConsumedPct).toBeCloseTo(75)
    expect(s.pvExportPct).toBeCloseTo(25)
    expect(s.loadFromPVPct).toBeCloseTo(62.5)
    expect(s.loadFromGridPct).toBeCloseTo(37.5)
  })

  it('returns zeros when there is no production or consumption', () => {
    const s = energySummaryFromSeries([])
    expect(s.pvProduced).toBe(0)
    expect(s.consumption).toBe(0)
    expect(s.pvConsumedPct).toBe(0)
    expect(s.pvExportPct).toBe(0)
    expect(s.loadFromPVPct).toBe(0)
    expect(s.loadFromGridPct).toBe(0)
  })

  it('clamps pvConsumed and fromPV at zero when exports/imports exceed totals', () => {
    const rows: EnergyRow[] = [
      {
        time: '00',
        pv_energy_yield_day_kwh: 10,
        accumulated_electricity_sold_kwh: -50,
        accumulated_power_consumption_kwh: -10,
        accumulated_electricity_purchased_kwh: 100,
      },
    ]
    const s = energySummaryFromSeries(rows)
    expect(s.pvConsumed).toBe(0)
    expect(s.fromPV).toBe(0)
  })
})
