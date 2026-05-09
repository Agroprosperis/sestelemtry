import { describe, expect, it } from 'vitest'
import {
  energySummaryFromSeries,
  energySummaryFromTotals,
  sumSeriesValue,
} from './summary'
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
  // The bus-balance rule that overrides the device-reported
  // `accumulated_power_consumption_kwh` lives in `applyApplianceConsumptionRule`
  // (covered by buckets.test.ts and the Go test in queries_test.go);
  // `energySummaryFromSeries` is the allocation step that runs on
  // already-corrected totals, so it consumes whatever consumption number
  // the row already carries.
  it('computes percentages for a balanced series', () => {
    const rows: EnergyRow[] = [
      {
        time: '00',
        accumulated_pv_energy_yield_kwh: 100,
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
        accumulated_pv_energy_yield_kwh: 10,
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

describe('energySummaryFromTotals (server-side path for month/year)', () => {
  // The month/year preset feeds totals directly from the server's
  // /api/v1/energy-summary endpoint. The grid purchase/sale values must
  // surface as fromGrid / gridExport on the cards even when the rest of
  // the totals are zero — that's the regression we hit when a counter
  // reset on a single day caused `last - seed` to clamp the whole
  // period total to zero before the SUM-of-clamped-day-deltas fix.
  it('exposes purchased/sold totals as fromGrid/gridExport', () => {
    const s = energySummaryFromTotals({
      accumulated_pv_energy_yield_kwh: 0,
      accumulated_electricity_sold_kwh: 1.25,
      accumulated_electricity_purchased_kwh: 235.56,
      accumulated_power_consumption_kwh: 235.56,
      total_energy_charged_kwh: 0,
      total_energy_discharged_kwh: 0,
    })
    expect(s.fromGrid).toBeCloseTo(235.56)
    expect(s.gridExport).toBeCloseTo(1.25)
    expect(s.consumption).toBeCloseTo(235.56)
  })

  it('treats missing keys as zero so partial server payloads still render', () => {
    const s = energySummaryFromTotals({})
    expect(s.fromGrid).toBe(0)
    expect(s.gridExport).toBe(0)
    expect(s.pvProduced).toBe(0)
    expect(s.consumption).toBe(0)
  })

  // Regression: previously `fromPV` subtracted (discharge - charge)
  // which masked battery contribution whenever the day charged more
  // than it discharged. The new allocation drops the `charge` term
  // entirely and treats discharge as the actual battery → load
  // contribution, leaving `batteryCharged` exposed only via the
  // narrative card. fromPV + fromBattery + fromGrid still equals
  // consumption by construction.
  it('does not subtract battery charge from the fromPV allocation', () => {
    const s = energySummaryFromTotals({
      accumulated_pv_energy_yield_kwh: 200,
      accumulated_electricity_sold_kwh: 0,
      accumulated_electricity_purchased_kwh: 0,
      accumulated_power_consumption_kwh: 100,
      total_energy_charged_kwh: 50,
      total_energy_discharged_kwh: 20,
    })
    expect(s.fromGrid).toBe(0)
    expect(s.fromBattery).toBe(20)
    expect(s.fromPV).toBe(80)
    expect(s.fromPV + s.fromBattery + s.fromGrid).toBe(s.consumption)
    expect(s.batteryCharged).toBe(50)
    expect(s.batteryDischarged).toBe(20)
  })

  it('uses discharge directly as battery contribution to load', () => {
    const s = energySummaryFromTotals({
      accumulated_pv_energy_yield_kwh: 0,
      accumulated_electricity_sold_kwh: 0,
      accumulated_electricity_purchased_kwh: 50,
      accumulated_power_consumption_kwh: 100,
      total_energy_charged_kwh: 0,
      total_energy_discharged_kwh: 30,
    })
    expect(s.fromGrid).toBe(50)
    expect(s.fromBattery).toBe(30)
    expect(s.fromPV).toBe(20)
  })
})
