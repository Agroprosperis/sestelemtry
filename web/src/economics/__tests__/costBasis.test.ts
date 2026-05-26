import { describe, expect, it } from 'vitest'
import { rollHour, seedFromCostPerKwh, ZERO_ESS_STATE, type EssState } from '../costBasis'
import type { HourFlows } from '../compute'

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

const DEGRADATION = 0.6

describe('rollHour', () => {
  it('builds a weighted average from two grid charges', () => {
    // 100 kWh @ 1 UAH/kWh → state (100, 100)
    const after1 = rollHour(
      ZERO_ESS_STATE,
      { ...emptyFlow, gridToEss: 100, essCharged: 100 },
      1,
      0,
      DEGRADATION,
    )
    expect(after1.next.kwh).toBeCloseTo(100, 9)
    expect(after1.next.uah).toBeCloseTo(100, 9)
    expect(after1.avgCostEndUahPerKwh).toBeCloseTo(1, 9)

    // + 100 kWh @ 3 UAH/kWh → state (200, 400) → avg = 2
    const after2 = rollHour(
      after1.next,
      { ...emptyFlow, gridToEss: 100, essCharged: 100 },
      3,
      0,
      DEGRADATION,
    )
    expect(after2.next.kwh).toBeCloseTo(200, 9)
    expect(after2.next.uah).toBeCloseTo(400, 9)
    expect(after2.avgCostEndUahPerKwh).toBeCloseTo(2, 9)

    // discharge 50 kWh @ avg=2 → withdrawn 100 UAH; residual (150, 300)
    const after3 = rollHour(
      after2.next,
      { ...emptyFlow, essDischarged: 50, essToLoad: 50 },
      5, // import price for the discharge hour (8 UAH/kWh, irrelevant for cost basis)
      0,
      DEGRADATION,
    )
    expect(after3.withdrawnCostUah).toBeCloseTo(100, 9)
    expect(after3.next.kwh).toBeCloseTo(150, 9)
    expect(after3.next.uah).toBeCloseTo(300, 9)
    expect(after3.avgCostEndUahPerKwh).toBeCloseTo(2, 9)
  })

  it('treats PV charge as free, diluting the existing average', () => {
    // 100 kWh @ grid 4 UAH → state (100, 400), avg = 4
    const after1 = rollHour(
      ZERO_ESS_STATE,
      { ...emptyFlow, gridToEss: 100, essCharged: 100 },
      4,
      0,
      DEGRADATION,
    )
    expect(after1.avgCostEndUahPerKwh).toBeCloseTo(4, 9)

    // + 100 kWh PV (free) → state (200, 400), avg = 2
    const after2 = rollHour(
      after1.next,
      { ...emptyFlow, pvToEss: 100, essCharged: 100 },
      4,
      0,
      DEGRADATION,
    )
    expect(after2.next.kwh).toBeCloseTo(200, 9)
    expect(after2.next.uah).toBeCloseTo(400, 9)
    expect(after2.avgCostEndUahPerKwh).toBeCloseTo(2, 9)
  })

  it('produces full realized profit when the battery was charged from PV only', () => {
    // 100 kWh PV (free) → kwh=100, uah=0, avg=0
    const charged = rollHour(
      ZERO_ESS_STATE,
      { ...emptyFlow, pvToEss: 100, essCharged: 100 },
      4,
      0,
      DEGRADATION,
    )
    expect(charged.next.kwh).toBeCloseTo(100, 9)
    expect(charged.next.uah).toBe(0)

    // discharge 50 kWh to load @ importPrice 8 UAH/kWh → withdrawn 0,
    // realized profit = 50·8 − 0 − 50·0.6 = 400 − 30 = 370.
    const discharged = rollHour(
      charged.next,
      { ...emptyFlow, essDischarged: 50, essToLoad: 50 },
      8,
      0,
      DEGRADATION,
    )
    expect(discharged.withdrawnCostUah).toBe(0)
    expect(discharged.realizedProfitUah).toBeCloseTo(370, 6)
  })

  it('matches realized profit identity (revenue − withdrawn − degradation)', () => {
    // Set up a realistic mixed-charge state: 50 grid @ 2 + 50 PV (free)
    let s: EssState = ZERO_ESS_STATE
    s = rollHour(s, { ...emptyFlow, gridToEss: 50, essCharged: 50 }, 2, 0, DEGRADATION).next
    s = rollHour(s, { ...emptyFlow, pvToEss: 50, essCharged: 50 }, 2, 0, DEGRADATION).next
    // avg should be 1 UAH/kWh (100 UAH split over 100 kWh)
    expect(s.uah / s.kwh).toBeCloseTo(1, 9)

    // Discharge 30 to load + 20 to grid at importPrice 8, exportPrice 6
    const out = rollHour(
      s,
      { ...emptyFlow, essDischarged: 50, essToLoad: 30, essToGrid: 20 },
      8,
      6,
      DEGRADATION,
    )
    const expectedRevenue = 30 * 8 + 20 * 6
    const expectedWithdrawn = 50 * 1
    const expectedDegradation = 50 * DEGRADATION
    expect(out.withdrawnCostUah).toBeCloseTo(expectedWithdrawn, 6)
    expect(out.realizedProfitUah).toBeCloseTo(
      expectedRevenue - expectedWithdrawn - expectedDegradation,
      6,
    )
  })

  it('carries cost basis across the day boundary', () => {
    // Yesterday: end the day with 50 kWh @ avg 1.5 (75 UAH inside).
    const yesterdayEnd: EssState = { kwh: 50, uah: 75 }
    // Today no charging, just discharge 50 to grid at exportPrice 8.
    const out = rollHour(
      yesterdayEnd,
      { ...emptyFlow, essDischarged: 50, essToGrid: 50 },
      0,
      8,
      DEGRADATION,
    )
    // Realized = 50·8 − 50·1.5 − 50·0.6 = 400 − 75 − 30 = 295
    expect(out.realizedProfitUah).toBeCloseTo(295, 6)
    expect(out.next.kwh).toBeCloseTo(0, 9)
    expect(out.next.uah).toBeCloseTo(0, 9)
  })

  it('clamps drained battery to zero state', () => {
    // Pretend a tiny float surplus of 0.0001 kWh from previous accounting;
    // discharge slightly more than the inventory.
    const s: EssState = { kwh: 10.0001, uah: 20 }
    const out = rollHour(
      s,
      { ...emptyFlow, essDischarged: 12, essToLoad: 12 },
      5,
      0,
      0,
    )
    expect(out.next.kwh).toBe(0)
    expect(out.next.uah).toBe(0)
  })

  it('handles a no-op hour without changing state', () => {
    const s: EssState = { kwh: 30, uah: 60 }
    const out = rollHour(s, emptyFlow, 8, 6, DEGRADATION)
    expect(out.next).toEqual(s)
    expect(out.withdrawnCostUah).toBe(0)
    expect(out.realizedProfitUah).toBe(0)
  })

  it('reports start-avg from prev state without folding the same hour\'s charges', () => {
    const s: EssState = { kwh: 100, uah: 200 } // avg = 2
    const out = rollHour(
      s,
      { ...emptyFlow, gridToEss: 100, essCharged: 100 }, // would dilute to 1.something
      0,
      0,
      DEGRADATION,
    )
    expect(out.avgCostStartUahPerKwh).toBeCloseTo(2, 9)
    // After charging 100 @ 0 → (200, 200) → avg 1
    expect(out.avgCostEndUahPerKwh).toBeCloseTo(1, 9)
  })
})

describe('seedFromCostPerKwh', () => {
  it('multiplies kwh × price', () => {
    const s = seedFromCostPerKwh(50, 1.5)
    expect(s.kwh).toBe(50)
    expect(s.uah).toBeCloseTo(75, 9)
  })

  it('clamps negative or non-finite inputs to zero', () => {
    expect(seedFromCostPerKwh(-1, 5)).toEqual({ kwh: 0, uah: 0 })
    expect(seedFromCostPerKwh(NaN, 5)).toEqual({ kwh: 0, uah: 0 })
    expect(seedFromCostPerKwh(Infinity, 5)).toEqual({ kwh: 0, uah: 0 })
    expect(seedFromCostPerKwh(50, NaN).uah).toBe(0)
  })
})
