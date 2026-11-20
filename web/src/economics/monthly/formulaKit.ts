// Small helpers shared by the month/year formula builders
// (periodFormulas, financeFormulas, reserveFormulas, tableFormulas).
import type { Money } from '../currency'
import { residual, type FormulaTerm } from '../formula'

// KWH_EPS: an energy remainder below this rounds to nothing in кВт·год.
export const KWH_EPS = 0.5
// MWH_EPS: …and this one in the one-decimal МВт·год of the cards.
export const MWH_EPS = 50

// moneyEps is the smallest remainder worth a line: half a hryvnia, in
// the page's currency.
export function moneyEps(m: Money): number {
  return m.fromUah(0.5)
}

// priceText is a per-kWh price with its unit, as the cards show it.
export function priceText(m: Money, v: number): string {
  return `${m.formatPrice(v)} ${m.priceUnit}`
}

// withResidual closes a breakdown: the remainder of total after the
// terms becomes the last term when it is larger than eps.
export function withResidual(
  terms: FormulaTerm[],
  total: number,
  label: string,
  eps: number,
  detail?: string,
): FormulaTerm[] {
  const r = residual(
    total,
    terms.map((t) => t.amount),
  )
  return Math.abs(r) > eps ? [...terms, { label, amount: r, detail }] : terms
}

// ratioOf is a ÷ b, NaN when b is not a usable divisor.
export function ratioOf(a: number, b: number): number {
  return Number.isFinite(a) && Number.isFinite(b) && b > 0 ? a / b : NaN
}

// nonZero drops terms that are exactly nothing, so a month without a
// generator carries no «Пальне 0 грн» line.
export function nonZero(terms: FormulaTerm[]): FormulaTerm[] {
  return terms.filter((t) => t.amount !== 0)
}
