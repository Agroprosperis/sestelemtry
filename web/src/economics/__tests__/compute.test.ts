import { describe, expect, it } from 'vitest'
import { dailyTotals, hourEconomics, deriveDerivedFlows, type HourEconomicsRow, type HourFlows } from '../compute'
import { DEFAULT_TARIFFS, parseTariffsFromSearch, serializeTariffsToSearch } from '../tariffs'

// emptyFlow is a zero-valued HourFlows for assembling specific test
// scenarios without enumerating the full record each time.
const emptyFlow: HourFlows = {
  pv: 0,
  gridImport: 0,
  gridExport: 0,
  essCharged: 0,
  essDischarged: 0,
  pvToEss: 0,
  gridToEss: 0,
  essToLoad: 0,
  essToGrid: 0,
}

describe('deriveDerivedFlows', () => {
  it('reproduces the energy-balance identity on a clean PV-only hour', () => {
    // 5 kWh PV, 0 from grid, 0 from battery → load = 5 (everything PV-to-load).
    const out = deriveDerivedFlows({
      ...emptyFlow,
      pv: 5,
    })
    expect(out.load).toBeCloseTo(5, 9)
    expect(out.pvToLoad).toBeCloseTo(5, 9)
    expect(out.pvToGrid).toBe(0)
    expect(out.gridToLoad).toBe(0)
  })

  it('clamps tiny negative drift to zero', () => {
    // The hourly energy balance can produce -0.0001 kWh from
    // accumulator rounding; we render that as 0.
    const out = deriveDerivedFlows({
      ...emptyFlow,
      pv: 1.0,
      gridImport: 0,
      essCharged: 1.0001,
    })
    expect(out.load).toBe(0)
  })

  it('splits PV between load, ESS charge and grid', () => {
    const out = deriveDerivedFlows({
      ...emptyFlow,
      pv: 10,
      pvToEss: 3,
      gridExport: 2,
      essCharged: 3,
    })
    expect(out.pvToGrid).toBe(2)
    expect(out.pvToLoad).toBe(5)
  })
})

describe('hourEconomics', () => {
  it('zeros out when no flow exists', () => {
    const r = hourEconomics(1, emptyFlow, DEFAULT_TARIFFS)
    expect(r.baselineCost).toBe(0)
    expect(r.actualCost).toBe(0)
    expect(r.effect).toBe(0)
    expect(r.essNet).toBe(0)
  })

  it('charges the load at the full import price stack', () => {
    // 10 kWh load entirely from grid, RDN = 1 UAH/kWh, no VAT, no fees.
    // Import price = 1 + 0.4881 + 0.74291 = 2.23101.
    // baseline = actual = 22.3101; effect = 0 (no PV / ESS).
    const r = hourEconomics(
      1.0,
      { ...emptyFlow, gridImport: 10 },
      { ...DEFAULT_TARIFFS, supplierMarginUahPerKwh: 0, otherFeesUahPerKwh: 0 },
    )
    expect(r.importPriceUahPerKwh).toBeCloseTo(2.23101, 5)
    expect(r.baselineCost).toBeCloseTo(22.3101, 5)
    expect(r.actualCost).toBeCloseTo(22.3101, 5)
    expect(r.effect).toBeCloseTo(0, 9)
  })

  it('credits exports at the discounted RDN price', () => {
    // PV 10 kWh, all sold to grid, RDN = 1 UAH/kWh, exportDiscount=5%.
    // export price = 1 * 0.95 = 0.95; actual cost = -9.5; baseline = 0
    // (no load); effect = 9.5 (the 9.5 UAH the operator earned).
    const r = hourEconomics(
      1,
      { ...emptyFlow, pv: 10, gridExport: 10 },
      DEFAULT_TARIFFS,
    )
    expect(r.exportPriceUahPerKwh).toBeCloseTo(0.95, 9)
    expect(r.actualCost).toBeCloseTo(-9.5, 9)
    expect(r.effect).toBeCloseTo(9.5, 9)
  })

  it('isolates the ESS contribution in essNet', () => {
    // Battery cycles 5 kWh from grid → load (gridToEss=5, essToLoad=5).
    // RDN=1, full import 2.23101, full export 0.95.
    // essNet = 5*2.23101 (revenue) - 5*2.23101 (cost) - 5*0.6 (degradation)
    //        = -3.0
    const r = hourEconomics(
      1,
      {
        ...emptyFlow,
        gridImport: 5,
        essCharged: 5,
        essDischarged: 5,
        gridToEss: 5,
        essToLoad: 5,
      },
      DEFAULT_TARIFFS,
    )
    expect(r.essNet).toBeCloseTo(-3.0, 9)
  })

  it('applies VAT when includeVat is true', () => {
    const r = hourEconomics(
      1,
      { ...emptyFlow, gridImport: 1 },
      { ...DEFAULT_TARIFFS, includeVat: true },
    )
    // 2.23101 * 1.20 = 2.677212
    expect(r.importPriceUahPerKwh).toBeCloseTo(2.677212, 5)
  })
})

// daily-totals scenario: a synthetic 24h profile derived from the
// spec's reference daily totals (load 2010.5, PV 2660.4, gridImport
// 417.3, gridExport 917.6, essDischarge 321.9). Note that
// `essCharged` is back-solved from the energy-balance identity
// `load = pv + gridImport + essDis - gridExport - essCh` so the
// per-hour flow set stays internally consistent — naively reusing
// `essDischarge` for `essCharged` (as a previous draft did) would
// inflate the per-hour `load` by ~150 kWh and skew the daily effect.
//
// With a constant RDN of 1.0 UAH/kWh the analytic answer is
// `(load - gridImport) * importPrice + gridExport * exportPrice
//  - essDischarge * degradation` ≈ 4233 UAH. The reference 5266
// figure in the spec assumes a non-constant RDN profile (peak
// hours weigh heavier than off-peak), which we don't try to
// reproduce here — this test's job is to lock the formula's
// output for the deterministic synthetic profile so future
// refactors of compute.ts can't silently drift the daily KPI.
describe('dailyTotals (spec calibration)', () => {
  function buildSpecCalibrationProfile(): Array<HourEconomicsRow | null> {
    const tariffs = DEFAULT_TARIFFS
    const rdn = 1.0
    const targets = {
      load: 2010.5,
      pv: 2660.4,
      gridImport: 417.3,
      gridExport: 917.6,
      essDischarge: 321.9,
    }
    // Solve the balance for essCharged so the per-hour load equals
    // targets.load / 24 to within rounding.
    const essCharged = targets.pv + targets.gridImport + targets.essDischarge - targets.gridExport - targets.load
    const hours = 24
    const flowPerHour: HourFlows = {
      pv: targets.pv / hours,
      gridImport: targets.gridImport / hours,
      gridExport: targets.gridExport / hours,
      essCharged: essCharged / hours,
      essDischarged: targets.essDischarge / hours,
      pvToEss: 0,
      gridToEss: essCharged / hours,
      essToLoad: targets.essDischarge / hours,
      essToGrid: 0,
    }
    const out: Array<HourEconomicsRow | null> = []
    for (let h = 0; h < hours; h++) {
      out.push({
        hour: h,
        hourStart: `2026-05-09T${String(h).padStart(2, '0')}:00:00+03:00`,
        rdnUahPerKwh: rdn,
        flow: flowPerHour,
        economics: hourEconomics(rdn, flowPerHour, tariffs),
      })
    }
    return out
  }

  it('produces a daily project effect within [4100, 4400] UAH for the constant-RDN profile', () => {
    const rows = buildSpecCalibrationProfile()
    const totals = dailyTotals(rows)
    // Analytic value with rdn=1.0 across all 24 hours, no VAT:
    // baseline = 2010.5 * 2.23101 = 4485.45
    // actual   = 417.3 * 2.23101 - 917.6 * 0.95 + 321.9 * 0.6
    //          = 930.94 - 871.72 + 193.14 = 252.36
    // effect   = 4233.09 UAH
    expect(totals.effect).toBeGreaterThan(4100)
    expect(totals.effect).toBeLessThan(4400)
  })

  it('reproduces the targeted daily load (2010.5 kWh) via the energy-balance identity', () => {
    const totals = dailyTotals(buildSpecCalibrationProfile())
    expect(totals.load).toBeCloseTo(2010.5, 0)
  })

  it('preserves the 24h coverage counter when every hour has a price', () => {
    const totals = dailyTotals(buildSpecCalibrationProfile())
    expect(totals.hoursWithData).toBe(24)
    expect(totals.hoursMissingPrice).toBe(0)
  })

  it('skips hours with a null RDN price and tallies hoursMissingPrice', () => {
    const rows = buildSpecCalibrationProfile()
    rows[5] = { ...rows[5]!, rdnUahPerKwh: null }
    rows[6] = { ...rows[6]!, rdnUahPerKwh: null }
    const totals = dailyTotals(rows)
    expect(totals.hoursWithData).toBe(22)
    expect(totals.hoursMissingPrice).toBe(2)
  })
})

describe('tariffs URL round-trip', () => {
  it('parseTariffsFromSearch returns defaults on empty input', () => {
    expect(parseTariffsFromSearch('')).toEqual(DEFAULT_TARIFFS)
  })

  it('writes only non-default values to the URL', () => {
    const params = new URLSearchParams()
    serializeTariffsToSearch(DEFAULT_TARIFFS, params)
    expect(params.toString()).toBe('')
  })

  it('overrides defaults via URL parameters', () => {
    const t = parseTariffsFromSearch('distribution=0.5&include_vat=true')
    expect(t.distributionUahPerKwh).toBe(0.5)
    expect(t.includeVat).toBe(true)
    expect(t.transmissionUahPerKwh).toBe(DEFAULT_TARIFFS.transmissionUahPerKwh)
  })

  it('round-trips a custom tariffs object', () => {
    const custom = { ...DEFAULT_TARIFFS, distributionUahPerKwh: 0.55, includeVat: true }
    const params = new URLSearchParams()
    serializeTariffsToSearch(custom, params)
    const reparsed = parseTariffsFromSearch(params)
    expect(reparsed).toEqual(custom)
  })
})
