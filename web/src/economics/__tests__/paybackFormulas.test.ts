import { describe, expect, it } from 'vitest'
import type { EconomicsAnnualMonthRollup, EconomicsMonthlyTotals } from '../../api'
import type { Formula, FormulaLine } from '../formula'
import { UAH_MONEY } from '../currency'
import { formatKwh, formatMwh, formatPercent } from '../monthly/format'
import { buildPaybackModel, paybackLabel } from '../payback'
import { avgMonthlyFact, buildPaybackFormulas } from '../payback/paybackFormulas'

const M = 1_000_000
const money = UAH_MONEY

const DAYS: Record<string, number> = { '01': 31, '02': 28, '03': 31, '04': 30, '05': 31, '06': 30 }

// A month: 4 revenue legs, two charge legs (so EBITDA = revenue − charges).
function monthTotals(month: string): EconomicsMonthlyTotals {
  const revenue = { pv_self: 700_000, pv_export: 400_000, ess_self: 300_000, ess_export: 100_000 }
  const gridCharge = 150_000
  const genCharge = 50_000
  const total = revenue.pv_self + revenue.pv_export + revenue.ess_self + revenue.ess_export
  return {
    revenue_pv_self_uah: revenue.pv_self,
    revenue_pv_export_uah: revenue.pv_export,
    revenue_ess_self_uah: revenue.ess_self,
    revenue_ess_export_uah: revenue.ess_export,
    revenue_total_uah: total,
    expense_grid_charge_uah: gridCharge,
    expense_gen_charge_uah: genCharge,
    expense_total_uah: gridCharge + genCharge,
    ebitda_uah: total - gridCharge - genCharge,
    hours_with_data: DAYS[month.slice(5, 7)] * 24,
    pv_kwh: 100_000,
    grid_import_kwh: 200_000,
    avg_import_price_uah_per_kwh: 5.5,
    import_cost_uah: 1_100_000,
  } as unknown as EconomicsMonthlyTotals
}

const months: EconomicsAnnualMonthRollup[] = ['2026-01', '2026-02', '2026-03', '2026-04', '2026-05', '2026-06'].map(
  (month) => ({ month, totals: monthTotals(month) }),
)

function windowTotals(): EconomicsMonthlyTotals {
  const keys = Object.keys(monthTotals('2026-01')) as (keyof EconomicsMonthlyTotals)[]
  const out: Record<string, number> = {}
  for (const k of keys) out[k] = months.reduce((acc, m) => acc + (m.totals[k] as number), 0)
  out.avg_import_price_uah_per_kwh = out.import_cost_uah / out.grid_import_kwh
  return out as unknown as EconomicsMonthlyTotals
}

const amount = (v: string) => Number(v.replace(/[^\d]/g, '')) * (v.includes('−') ? -1 : 1)

// signedSum adds the lines the way the bubble reads: + / − / none lead a term.
function signedSum(lines: FormulaLine[]): number {
  return lines
    .filter((l) => l.op !== '=')
    .reduce((acc, l) => acc + (l.op === '−' ? -1 : 1) * amount(l.value), 0)
}

const result = (f: Formula) => f.lines?.find((l) => l.op === '=')

function setup(over: { capexUah?: number; capexSteps?: { effectiveFrom: string; capexUah: number }[]; prior?: number; priorMonths?: number } = {}) {
  const totals = windowTotals()
  const capexSteps = over.capexSteps ?? []
  const model = buildPaybackModel({
    capexUah: over.capexUah ?? 20 * M,
    capexSteps,
    months,
    ebitda: totals.ebitda_uah,
    priorEbitda: over.prior ?? 0,
    priorMonthsWithData: over.priorMonths ?? 0,
  })
  const f = buildPaybackFormulas({ model, totals, money, capexSteps })
  return { model, totals, f }
}

describe('payback formulas', () => {
  it('explains the CAPEX in force and lists the stages', () => {
    const { model, f } = setup({
      capexUah: 0,
      capexSteps: [
        { effectiveFrom: '2026-01-01', capexUah: 12 * M },
        { effectiveFrom: '2026-06-07', capexUah: 20 * M },
        { effectiveFrom: '2026-09-01', capexUah: 30 * M },
      ],
    })
    expect(result(f.capex)?.value).toBe(money.format(model.capexNow))
    expect(model.capexNow).toBe(20 * M)
    // The September version is not in force in June: only two stages listed.
    expect(f.capex.lines?.map((l) => l.value)).toEqual([money.format(12 * M), money.format(20 * M), money.format(20 * M)])
  })

  it('writes the accumulated EBITDA as prior + the window, adding up', () => {
    const { model, totals, f } = setup({ prior: 3 * M, priorMonths: 4 })
    for (const formula of [f.returned, f.netEffect]) {
      expect(result(formula)?.value).toBe(money.format(model.allTimeEbitda))
      expect(signedSum(formula.lines ?? [])).toBe(Math.round(model.allTimeEbitda))
    }
    expect(f.returned.lines?.[1].value).toBe(money.format(totals.ebitda_uah))
    // The window line carries revenue − charges.
    expect(f.returned.lines?.[1].detail).toContain(`${money.format(totals.revenue_total_uah)} − ${money.format(totals.expense_total_uah)}`)
    // EBITDA is not reduced by opex or wear.
    expect(JSON.stringify(f.returned)).not.toContain('операційні витрати —')
  })

  it('shows the remaining, and zero once the project has paid off', () => {
    const open = setup({ capexUah: 20 * M })
    expect(result(open.f.remaining)?.value).toBe(money.format(open.model.remaining))
    expect(signedSum(open.f.remaining.lines ?? [])).toBe(Math.round(open.model.remaining))

    const paid = setup({ capexUah: 2 * M })
    expect(paid.model.paidOff).toBe(true)
    expect(result(paid.f.remaining)?.value).toBe(money.format(0))
    expect(result(paid.f.covered)?.value).toBe(formatPercent(1))
    expect(paid.f.covered.note).toContain('100%')
  })

  it('matches the coverage, operation and average figures on the page', () => {
    const { model, f } = setup({ prior: 2 * M, priorMonths: 3 })
    expect(result(f.covered)?.value).toBe(formatPercent(model.coveredShare))
    expect(result(f.operation)?.value).toBe(paybackLabel(model.operationYears))
    expect(f.operation.lines?.[0].value).toBe(`${model.totalMonthsWithData} міс.`)
    expect(result(f.avgMonthly)?.value).toBe(money.format(avgMonthlyFact(model)))
    expect(f.prior.lines?.[0].value).toBe(money.format(model.prior))
  })

  it('writes the ROI from the same weights as the model', () => {
    const { model, f } = setup()
    expect(result(f.roi)?.value).toBe(formatPercent(model.avgAnnualRoi))
    const avg = f.roi.lines?.find((l) => l.label === 'Середні вкладення')
    expect(avg?.value).toBe(money.format(model.capexAvg))
    expect(avg?.detail).toContain(money.format(model.capexWeighted))
    expect((model.allTimeEbitda / model.capexAvg) * (12 / model.effectiveMonths)).toBeCloseTo(model.avgAnnualRoi)
  })

  it('explains the grid bill as the whole import, not the battery charge', () => {
    const { totals, f } = setup()
    expect(result(f.gridBill)?.value).toBe(money.format(totals.import_cost_uah))
    expect(f.gridBill.lines?.[0].value).toBe(formatKwh(totals.grid_import_kwh))
    expect(f.gridBill.note).toContain(money.format(totals.expense_grid_charge_uah))
    expect(f.gridBill.text).toContain('споживання')
    expect(f.gridBill.text).not.toContain('заряд УЗЕ з мережі за період')
    expect(totals.import_cost_uah).not.toBe(totals.expense_total_uah)
  })

  it('shows the PV output and the equivalent tariff', () => {
    const { totals, f } = setup()
    expect(result(f.pv)?.value).toBe(formatMwh(totals.pv_kwh))
    expect(result(f.tariff)?.value).toBe(`${money.formatPrice(totals.avg_import_price_uah_per_kwh)} ${money.priceUnit}`)
    expect(f.tariff.lines?.[0].value).toBe(money.format(totals.import_cost_uah))
  })

  it('breaks the window EBITDA into revenue legs and charges that add up', () => {
    const { totals, f } = setup()
    const lines = f.effectChart.lines ?? []
    expect(result(f.effectChart)?.value).toBe(money.format(totals.ebitda_uah))
    expect(signedSum(lines)).toBe(Math.round(totals.ebitda_uah))
    expect(lines.map((l) => l.label)).toEqual([
      'СЕС → споживання',
      'СЕС → мережа',
      'УЗЕ → споживання',
      'УЗЕ → мережа',
      'Заряд УЗЕ з мережі',
      'Заряд УЗЕ від генератора',
      'EBITDA за період',
    ])
    expect(f.effectChart.rule).not.toContain('операційн')
    expect(f.effectChart.rule).toContain('заряд УЗЕ')
  })

  it('closes the breakdown with a residual when the legs do not cover the EBITDA', () => {
    const totals = { ...windowTotals(), revenue_total_uah: windowTotals().revenue_total_uah + 5_000 } as EconomicsMonthlyTotals
    const model = buildPaybackModel({ capexUah: 20 * M, months, ebitda: totals.ebitda_uah, priorEbitda: 0, priorMonthsWithData: 0 })
    const f = buildPaybackFormulas({ model, totals, money, capexSteps: [] })
    expect(signedSum(f.effectChart.lines ?? [])).toBe(Math.round(totals.ebitda_uah))
  })

  it('names the payback month and the pace behind the forecast', () => {
    const { model, f } = setup()
    expect(model.paybackMonthKey).not.toBeNull()
    const point = f.cumulativeChart.lines?.at(-1)
    expect(point?.label).toBe('Точка окупності (прогноз)')
    expect(point?.detail).toContain(paybackLabel(model.paybackYears))
    const pace = f.cumulativeChart.lines?.find((l) => l.label === 'Базовий темп EBITDA на місяць')
    expect(pace?.value).toBe(money.format(model.monthlyPace))
    expect(model.monthlyPace * model.seasonalWeight).toBeCloseTo(model.allTimeEbitda)
  })
})
