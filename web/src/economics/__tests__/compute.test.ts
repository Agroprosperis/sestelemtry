import { describe, expect, it } from 'vitest'
import { dailyTotals, hourEconomics, deriveDerivedFlows, type HourEconomicsRow, type HourFlows } from '../compute'
import { rollHour, ZERO_ESS_STATE, type EssState } from '../costBasis'
import { DEFAULT_TARIFFS } from '../tariffs'

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
    // Import price = 1 + 2.75218 + 0.74291 = 4.49509 (2-class
    // distribution per spec §3, 2nd-stage transmission).
    // baseline = actual = 44.9509; effect = 0 (no PV / ESS).
    const r = hourEconomics(
      1.0,
      { ...emptyFlow, gridImport: 10 },
      { ...DEFAULT_TARIFFS, supplierMarginUahPerKwh: 0, otherFeesUahPerKwh: 0 },
    )
    expect(r.importPriceUahPerKwh).toBeCloseTo(4.49509, 5)
    expect(r.baselineCost).toBeCloseTo(44.9509, 5)
    expect(r.actualCost).toBeCloseTo(44.9509, 5)
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
    // RDN=1, full import 4.49509, full export 0.95. The essToLoad
    // revenue and gridToEss cost both run through the same import
    // price stack so they cancel; only the export-priced charge leg
    // (here zero, no PV) and the degradation term remain.
    // essNet = 5*4.49509 (revenue) - 5*4.49509 (cost) - 5*0.6 (degradation)
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
    // 4.49509 * 1.20 = 5.394108
    expect(r.importPriceUahPerKwh).toBeCloseTo(5.394108, 5)
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
        essRemainingKwhStart: null,
        essCostBasisUahStart: null,
        essAvgCostUahPerKwhStart: null,
        essWithdrawnCostUah: null,
        essRealizedProfitUah: null,
        essCostBasisUahEnd: null,
        essAvgCostUahPerKwhEnd: null,
        essResidualKwhEnd: null,
      })
    }
    return out
  }

  it('produces a daily project effect within [7700, 8000] UAH for the constant-RDN profile', () => {
    const rows = buildSpecCalibrationProfile()
    const totals = dailyTotals(rows)
    // Analytic value with rdn=1.0 across all 24 hours, no VAT,
    // 2-class distribution (2.75218) + transmission (0.74291):
    // importPrice = 4.49509
    // baseline    = 2010.5 * 4.49509 = 9038.78
    // actual      = 417.3 * 4.49509 - 917.6 * 0.95 + 321.9 * 0.6
    //             = 1875.79 - 871.72 + 193.14 = 1197.21
    // effect      = 7841.57 UAH
    expect(totals.effect).toBeGreaterThan(7700)
    expect(totals.effect).toBeLessThan(8000)
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

  it('EBITDA equals revenueTotal − expenseTotal by construction', () => {
    const totals = dailyTotals(buildSpecCalibrationProfile())
    expect(totals.ebitda).toBeCloseTo(totals.revenueTotal - totals.expenseTotal, 6)
    expect(totals.revenueTotal).toBeCloseTo(
      totals.revenuePvExport + totals.revenuePvSelf + totals.revenueEssExport + totals.revenueEssSelf,
      6,
    )
    expect(totals.expenseTotal).toBeCloseTo(totals.expenseGridCharge, 6)
  })

  it('EBITDA matches project effect when degradationUahPerKwh = 0', () => {
    // Rebuild the synthetic profile with zero degradation so the
    // single difference between `effect` (which subtracts wear) and
    // `ebitda` (which doesn't) collapses, and the two framings of
    // the same energy flows must agree bit-for-bit.
    const tariffs = { ...DEFAULT_TARIFFS, degradationUahPerKwh: 0 }
    const rdn = 1.0
    const targets = {
      load: 2010.5,
      pv: 2660.4,
      gridImport: 417.3,
      gridExport: 917.6,
      essDischarge: 321.9,
    }
    const essCharged =
      targets.pv + targets.gridImport + targets.essDischarge - targets.gridExport - targets.load
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
    const rows: Array<HourEconomicsRow | null> = []
    for (let h = 0; h < hours; h++) {
      rows.push({
        hour: h,
        hourStart: `2026-05-09T${String(h).padStart(2, '0')}:00:00+03:00`,
        rdnUahPerKwh: rdn,
        flow: flowPerHour,
        economics: hourEconomics(rdn, flowPerHour, tariffs),
        essRemainingKwhStart: null,
        essCostBasisUahStart: null,
        essAvgCostUahPerKwhStart: null,
        essWithdrawnCostUah: null,
        essRealizedProfitUah: null,
        essCostBasisUahEnd: null,
        essAvgCostUahPerKwhEnd: null,
        essResidualKwhEnd: null,
      })
    }
    const totals = dailyTotals(rows)
    expect(totals.ebitda).toBeCloseTo(totals.effect, 6)
  })
})

// dailyTotals + costBasis interplay: hand-craft a tiny day where
// the battery charges from grid in cheap hours and discharges to
// load in expensive hours, and verify that the realized profit
// total reproduces the per-hour math.
describe('dailyTotals (cost-basis aggregates)', () => {
  it('aggregates realized profit, withdrawn cost, and EOD avg correctly', () => {
    const tariffs = {
      ...DEFAULT_TARIFFS,
      // No fees so importPrice == RDN, exportPrice == RDN·(1−5%).
      distributionUahPerKwh: 0,
      transmissionUahPerKwh: 0,
      supplierMarginUahPerKwh: 0,
      otherFeesUahPerKwh: 0,
      degradationUahPerKwh: 0,
    }
    // 24 hours; charge 100 kWh from grid in hour 2 at RDN=2,
    // discharge 100 kWh to load in hour 18 at RDN=8.
    const rows: Array<HourEconomicsRow | null> = []
    let state: EssState = ZERO_ESS_STATE
    for (let h = 0; h < 24; h++) {
      const rdn = h === 2 ? 2 : h === 18 ? 8 : 1
      const flow: HourFlows = {
        pv: 0,
        gridImport: h === 2 ? 100 : 0,
        gridExport: 0,
        essCharged: h === 2 ? 100 : 0,
        essDischarged: h === 18 ? 100 : 0,
        pvToEss: 0,
        gridToEss: h === 2 ? 100 : 0,
        essToLoad: h === 18 ? 100 : 0,
        essToGrid: 0,
      }
      const econ = hourEconomics(rdn, flow, tariffs)
      const out = rollHour(state, flow, econ.importPriceUahPerKwh, econ.exportPriceUahPerKwh, tariffs.degradationUahPerKwh)
      rows.push({
        hour: h,
        hourStart: `2026-05-10T${String(h).padStart(2, '0')}:00:00+03:00`,
        rdnUahPerKwh: rdn,
        flow,
        economics: econ,
        essRemainingKwhStart: null,
        essCostBasisUahStart: state.uah,
        essAvgCostUahPerKwhStart: out.avgCostStartUahPerKwh,
        essWithdrawnCostUah: out.withdrawnCostUah,
        essRealizedProfitUah: out.realizedProfitUah,
        essCostBasisUahEnd: out.next.uah,
        essAvgCostUahPerKwhEnd: out.avgCostEndUahPerKwh,
        essResidualKwhEnd: out.next.kwh,
      })
      state = out.next
    }
    const totals = dailyTotals(rows)
    // withdrawn = 100·2 = 200 (charge hour was at avg=2 by EOH 2)
    expect(totals.essWithdrawnCostUah).toBeCloseTo(200, 6)
    // revenue = 100·8 (essToLoad·importPrice@hour 18) = 800
    expect(totals.revenueEssSelf).toBeCloseTo(800, 6)
    // realized = 800 − 200 − 0 (degradation) = 600
    expect(totals.essRealizedProfitUah).toBeCloseTo(600, 6)
    // EOD avg cost: battery is empty, so 0.
    expect(totals.essAvgCostBasisUahPerKwhEod).toBe(0)
    expect(totals.essResidualKwhEod).toBe(0)
    expect(totals.essCostBasisUahEod).toBe(0)
  })

  it('preserves an EOD non-empty cost basis when the battery is not fully discharged', () => {
    const tariffs = { ...DEFAULT_TARIFFS, distributionUahPerKwh: 0, transmissionUahPerKwh: 0, supplierMarginUahPerKwh: 0, otherFeesUahPerKwh: 0, degradationUahPerKwh: 0 }
    // Charge 100 @ RDN=3 in hour 2, discharge nothing → EOD avg 3.
    const rows: Array<HourEconomicsRow | null> = []
    let state: EssState = ZERO_ESS_STATE
    for (let h = 0; h < 24; h++) {
      const rdn = h === 2 ? 3 : 1
      const flow: HourFlows = {
        pv: 0,
        gridImport: h === 2 ? 100 : 0,
        gridExport: 0,
        essCharged: h === 2 ? 100 : 0,
        essDischarged: 0,
        pvToEss: 0,
        gridToEss: h === 2 ? 100 : 0,
        essToLoad: 0,
        essToGrid: 0,
      }
      const econ = hourEconomics(rdn, flow, tariffs)
      const out = rollHour(state, flow, econ.importPriceUahPerKwh, econ.exportPriceUahPerKwh, tariffs.degradationUahPerKwh)
      rows.push({
        hour: h,
        hourStart: `2026-05-10T${String(h).padStart(2, '0')}:00:00+03:00`,
        rdnUahPerKwh: rdn,
        flow,
        economics: econ,
        essRemainingKwhStart: null,
        essCostBasisUahStart: state.uah,
        essAvgCostUahPerKwhStart: out.avgCostStartUahPerKwh,
        essWithdrawnCostUah: out.withdrawnCostUah,
        essRealizedProfitUah: out.realizedProfitUah,
        essCostBasisUahEnd: out.next.uah,
        essAvgCostUahPerKwhEnd: out.avgCostEndUahPerKwh,
        essResidualKwhEnd: out.next.kwh,
      })
      state = out.next
    }
    const totals = dailyTotals(rows)
    // After hour 2 the battery sits at 100 kWh @ avg 3.
    // Hours 3..23 see no flow → state stays. EOD reads the End
    // fields of hour 23 = (kwh: 100, uah: 300, avg: 3).
    expect(totals.essAvgCostBasisUahPerKwhEod).toBeCloseTo(3, 6)
    expect(totals.essResidualKwhEod).toBeCloseTo(100, 6)
    expect(totals.essCostBasisUahEod).toBeCloseTo(300, 6)
    expect(totals.essWithdrawnCostUah).toBe(0)
    expect(totals.essRealizedProfitUah).toBe(0)
  })

  it('reports the END-of-hour-23 avg as EOD when hour 23 itself charges', () => {
    // Regression for an off-by-one where EOD was read from
    // `essAvgCostUahPerKwhStart` of hour 23 (i.e. avg AT THE
    // START of hour 23) — which differs from the post-charge avg
    // when hour 23 has activity. Charge 100 kWh @ RDN=5 in hour
    // 23 starting from an empty battery: start-avg = 0,
    // end-avg = 5, EOD must be 5.
    const tariffs = {
      ...DEFAULT_TARIFFS,
      distributionUahPerKwh: 0,
      transmissionUahPerKwh: 0,
      supplierMarginUahPerKwh: 0,
      otherFeesUahPerKwh: 0,
      degradationUahPerKwh: 0,
    }
    const rows: Array<HourEconomicsRow | null> = []
    let state: EssState = ZERO_ESS_STATE
    for (let h = 0; h < 24; h++) {
      const rdn = 1
      const flow: HourFlows = {
        pv: 0,
        gridImport: h === 23 ? 100 : 0,
        gridExport: 0,
        essCharged: h === 23 ? 100 : 0,
        essDischarged: 0,
        pvToEss: 0,
        gridToEss: h === 23 ? 100 : 0,
        essToLoad: 0,
        essToGrid: 0,
      }
      const lastHourRdn = h === 23 ? 5 : rdn
      const econ = hourEconomics(lastHourRdn, flow, tariffs)
      const out = rollHour(state, flow, econ.importPriceUahPerKwh, econ.exportPriceUahPerKwh, tariffs.degradationUahPerKwh)
      rows.push({
        hour: h,
        hourStart: `2026-05-10T${String(h).padStart(2, '0')}:00:00+03:00`,
        rdnUahPerKwh: lastHourRdn,
        flow,
        economics: econ,
        essRemainingKwhStart: null,
        essCostBasisUahStart: state.uah,
        essAvgCostUahPerKwhStart: out.avgCostStartUahPerKwh,
        essWithdrawnCostUah: out.withdrawnCostUah,
        essRealizedProfitUah: out.realizedProfitUah,
        essCostBasisUahEnd: out.next.uah,
        essAvgCostUahPerKwhEnd: out.avgCostEndUahPerKwh,
        essResidualKwhEnd: out.next.kwh,
      })
      state = out.next
    }
    const totals = dailyTotals(rows)
    expect(totals.essAvgCostBasisUahPerKwhEod).toBeCloseTo(5, 6)
    expect(totals.essResidualKwhEod).toBeCloseTo(100, 6)
    expect(totals.essCostBasisUahEod).toBeCloseTo(500, 6)
  })

  it('falls back to the last End fields when trailing hours have no RDN', () => {
    // Hours 22 and 23 have `null` RDN → cost-basis fields stay
    // null, so the EOD scan should fall back to hour 21.
    const tariffs = { ...DEFAULT_TARIFFS, distributionUahPerKwh: 0, transmissionUahPerKwh: 0, supplierMarginUahPerKwh: 0, otherFeesUahPerKwh: 0, degradationUahPerKwh: 0 }
    const rows: Array<HourEconomicsRow | null> = []
    let state: EssState = ZERO_ESS_STATE
    for (let h = 0; h < 24; h++) {
      const hasPrice = h <= 21
      const rdn = hasPrice ? (h === 2 ? 4 : 1) : null
      const flow: HourFlows = {
        pv: 0,
        gridImport: h === 2 ? 100 : 0,
        gridExport: 0,
        essCharged: h === 2 ? 100 : 0,
        essDischarged: 0,
        pvToEss: 0,
        gridToEss: h === 2 ? 100 : 0,
        essToLoad: 0,
        essToGrid: 0,
      }
      const econ = hourEconomics(rdn ?? 0, flow, tariffs)
      if (rdn === null) {
        rows.push({
          hour: h,
          hourStart: `2026-05-10T${String(h).padStart(2, '0')}:00:00+03:00`,
          rdnUahPerKwh: null,
          flow,
          economics: econ,
          essRemainingKwhStart: null,
          essCostBasisUahStart: null,
          essAvgCostUahPerKwhStart: null,
          essWithdrawnCostUah: null,
          essRealizedProfitUah: null,
          essCostBasisUahEnd: null,
          essAvgCostUahPerKwhEnd: null,
          essResidualKwhEnd: null,
        })
        continue
      }
      const out = rollHour(state, flow, econ.importPriceUahPerKwh, econ.exportPriceUahPerKwh, tariffs.degradationUahPerKwh)
      rows.push({
        hour: h,
        hourStart: `2026-05-10T${String(h).padStart(2, '0')}:00:00+03:00`,
        rdnUahPerKwh: rdn,
        flow,
        economics: econ,
        essRemainingKwhStart: null,
        essCostBasisUahStart: state.uah,
        essAvgCostUahPerKwhStart: out.avgCostStartUahPerKwh,
        essWithdrawnCostUah: out.withdrawnCostUah,
        essRealizedProfitUah: out.realizedProfitUah,
        essCostBasisUahEnd: out.next.uah,
        essAvgCostUahPerKwhEnd: out.avgCostEndUahPerKwh,
        essResidualKwhEnd: out.next.kwh,
      })
      state = out.next
    }
    const totals = dailyTotals(rows)
    // After hour 2: state = (100, 400), avg = 4. Hours 3..21 are
    // no-op priced rows so the End-fields stay (100, 400, avg 4).
    // EOD must read hour 21's End, not silently coalesce to 0.
    expect(totals.essAvgCostBasisUahPerKwhEod).toBeCloseTo(4, 6)
    expect(totals.essResidualKwhEod).toBeCloseTo(100, 6)
  })
})
