import { describe, expect, it } from 'vitest'
import type { EconomicsAnnualMonthRollup, EconomicsMonthlyTotals } from '../../api'
import { buildPaybackModel, capexResolver, type CapexStep } from '../payback'

const M = 1_000_000

// The payback math only reads three totals; the rest of the monthly
// rollup is irrelevant here, so the fixture fills those and casts.
function month(monthKey: string, ebitdaUah: number): EconomicsAnnualMonthRollup {
  return {
    month: monthKey,
    totals: {
      ebitda_uah: ebitdaUah,
      hours_with_data: 24 * 31,
      pv_kwh: 50_000,
    } as unknown as EconomicsMonthlyTotals,
  }
}

function halfYear(ebitdaPerMonth: number): EconomicsAnnualMonthRollup[] {
  return ['2026-01', '2026-02', '2026-03', '2026-04', '2026-05', '2026-06'].map((k) =>
    month(k, ebitdaPerMonth),
  )
}

const STAGES: CapexStep[] = [
  { effectiveFrom: '2026-01-01', capexUah: 6 * M },
  { effectiveFrom: '2026-06-15', capexUah: 20 * M },
]

describe('capexResolver', () => {
  it('falls back to the flat value when no version carries CAPEX', () => {
    const at = capexResolver([], 18 * M)
    expect(at('2026-05')).toBe(18 * M)
    expect(at(null)).toBe(18 * M)
  })

  it('holds each stage until the next one takes effect', () => {
    const at = capexResolver(STAGES, 0)
    expect(at('2026-01')).toBe(6 * M)
    expect(at('2026-05')).toBe(6 * M)
    // A stage effective mid-June counts for the whole of June: the money
    // left the account inside that month.
    expect(at('2026-06')).toBe(20 * M)
    expect(at('2027-03')).toBe(20 * M)
  })

  it('carries the first funded stage back before its effective date', () => {
    // CAPEX is spent before the plant produces anything, and a version
    // saved with CAPEX left at 0 means "not filled in" — reading it as a
    // free project would show instant payback.
    const at = capexResolver([{ effectiveFrom: '1970-01-01', capexUah: 0 }, ...STAGES], 0)
    expect(at('2025-11')).toBe(6 * M)
  })

  it('ignores the epoch snapshot even when it carries a bigger CAPEX', () => {
    // The catch-all version mirrors the tariff form, which may already
    // hold the full project cost while the dated versions still carry the
    // first stage. Treating it as a stage would step the target down.
    const at = capexResolver([{ effectiveFrom: '1970-01-01', capexUah: 53 * M }, ...STAGES], 0)
    expect(at('2025-11')).toBe(6 * M)
    expect(at('2026-05')).toBe(6 * M)
    expect(at('2026-06')).toBe(20 * M)
  })
})

describe('buildPaybackModel with a staged CAPEX', () => {
  const staged = buildPaybackModel({
    capexUah: 0,
    // The epoch snapshot rides along in the real payload and must not
    // register as a third stage.
    capexSteps: [{ effectiveFrom: '1970-01-01', capexUah: 53 * M }, ...STAGES],
    months: halfYear(2 * M),
    ebitda: 12 * M,
    priorEbitda: 0,
    priorMonthsWithData: 0,
  })

  it('measures progress against everything invested so far', () => {
    expect(staged.capexNow).toBe(20 * M)
    expect(staged.coveredShare).toBeCloseTo(0.6, 6)
    expect(staged.remaining).toBe(8 * M)
    // The first stage was covered back in March, but an expansion in June
    // raised the bar — the project is not paid off.
    expect(staged.paidOff).toBe(false)
    expect(staged.capexStages).toBe(2)
  })

  it('carries the CAPEX in effect on every chart row', () => {
    const capexOf = (key: string) => staged.rows.find((r) => r.monthKey === key)?.capex
    expect(capexOf('2026-01')).toBe(6 * M)
    expect(capexOf('2026-05')).toBe(6 * M)
    expect(capexOf('2026-06')).toBe(20 * M)
    // The forecast keeps the last known stage.
    expect(staged.rows[staged.rows.length - 1].capex).toBe(20 * M)
  })

  it('rates ROI against the capital that was actually at work', () => {
    // Five months on 6 млн, one on 20 млн: 8,33 млн average, not the
    // 20 млн the project only reached at the end.
    expect(staged.capexAvg).toBeCloseTo((5 * 6 * M + 20 * M) / 6, 6)
    expect(staged.avgAnnualRoi).toBeCloseTo((12 * M / staged.capexAvg) * 2, 6)
  })
})

describe('buildPaybackModel without a CAPEX schedule', () => {
  const flat = buildPaybackModel({
    capexUah: 20 * M,
    capexSteps: [],
    months: halfYear(1 * M),
    ebitda: 6 * M,
    priorEbitda: 0,
    priorMonthsWithData: 0,
  })

  it('keeps the single tariff-form value on every row', () => {
    expect(flat.capexNow).toBe(20 * M)
    expect(flat.capexStages).toBe(0)
    expect(flat.rows.every((r) => r.capex === 20 * M)).toBe(true)
    expect(flat.avgAnnualRoi).toBeCloseTo((6 * M / (20 * M)) * 2, 6)
  })

  it('pushes the forecast crossing out when a later stage raises the bar', () => {
    const withFutureStage = buildPaybackModel({
      capexUah: 20 * M,
      capexSteps: [
        { effectiveFrom: '2026-01-01', capexUah: 20 * M },
        { effectiveFrom: '2027-01-01', capexUah: 40 * M },
      ],
      months: halfYear(1 * M),
      ebitda: 6 * M,
      priorEbitda: 0,
      priorMonthsWithData: 0,
    })

    // Same EBITDA pace, twice the target from 2027 on: the same money
    // takes longer to cover it.
    expect(withFutureStage.capexNow).toBe(20 * M)
    expect(flat.paybackT).not.toBeNull()
    expect(withFutureStage.paybackT).not.toBeNull()
    expect(withFutureStage.paybackT!).toBeGreaterThan(flat.paybackT!)
  })
})
