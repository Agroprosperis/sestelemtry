import { describe, expect, it } from 'vitest'
import type { EconomicsAnnualResponse } from '../../api'
import fixture from '../annual/__fixtures_annual_ze.json'
import { UAH_MONEY } from '../currency'
import { financeFigures, financeFormulas } from '../monthly/financeFormulas'
import { formatPercent } from '../monthly/format'
import { topFigures, topFormulas } from '../monthly/periodFormulas'

const data = fixture as unknown as EconomicsAnnualResponse
const t = data.totals

function result(lines: { op?: string; value: string }[] | undefined): string | undefined {
  return lines?.filter((l) => l.op === '=').at(-1)?.value
}

describe('period formulas', () => {
  const f = topFigures(t, { capexUah: 199_000_000, annualizeMonths: data.months_with_data })
  const fx = topFormulas(t, {
    money: UAH_MONEY,
    figures: f,
    prior: null,
    prevNote: 'до попереднього року',
    pvPlan: null,
    scope: 'year',
  })

  it('ends each card formula with the card’s own figure', () => {
    expect(result(fx.baseline.lines)).toBe(UAH_MONEY.format(t.baseline_cost_uah))
    expect(result(fx.actual.lines)).toBe(UAH_MONEY.format(t.actual_cost_uah))
    expect(result(fx.effect.lines)).toBe(UAH_MONEY.format(t.effect_uah))
    expect(result(fx.pv.lines)).toBe(UAH_MONEY.format(f.pvEffect))
    expect(result(fx.ess.lines)).toBe(UAH_MONEY.format(t.ess_net_uah))
    expect(result(fx.roce.lines)).toBe(formatPercent(f.roce))
  })

  it('splits the effect between PV and the battery without a remainder', () => {
    expect(f.pvEffect + t.ess_net_uah).toBeCloseTo(t.effect_uah, 6)
  })

  it('keeps the finance boxes the two sides of EBITDA', () => {
    const fig = financeFigures(t)
    expect(t.revenue_total_uah - fig.expenseTotal).toBeCloseTo(t.ebitda_uah, 0)
    const ff = financeFormulas(t, UAH_MONEY, 'year')
    expect(result(ff.ebitda.lines)).toBe(UAH_MONEY.format(t.ebitda_uah))
    expect(result(ff.expenses.lines)).toBe(UAH_MONEY.format(fig.expenseTotal))
    // The battery wear stays outside the expenses.
    expect(ff.expenses.lines?.some((l) => l.label.includes('Знос'))).toBe(false)
  })
})
