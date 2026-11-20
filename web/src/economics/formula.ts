// A Formula is how one figure on the economics page is computed: what it
// means, the rule in words, and the same rule with the visible period's
// own numbers, line by line, down to the figure itself. FormulaTip shows
// it behind the figure's «i».

// FormulaOp leads a line: the operation it applies to what stands above
// it, or '=' for the figure itself.
export type FormulaOp = '+' | '−' | '×' | '÷' | '='

export type FormulaLine = {
  op?: FormulaOp
  label: string
  // value is formatted exactly as the page shows it.
  value: string
  // detail is the arithmetic behind the value, e.g.
  // «1 234 кВт·год × 8,12 грн/кВт·год».
  detail?: string
}

export type Formula = {
  // text says what the figure is; rule is the formula in words.
  text?: string
  rule?: string
  lines?: FormulaLine[]
  // note is a caveat: what the figure leaves out, or where it differs
  // from a like-named one elsewhere.
  note?: string
}

// explain is what a figure's «i» opens: its formula, with the figure's
// plain tip as the text when the formula brings none; null for neither.
export function explain(tip?: string, formula?: Formula): Formula | null {
  if (formula) return formula.text ? formula : { ...formula, text: tip }
  return tip ? { text: tip } : null
}

export type FormulaTerm = { label: string; amount: number; detail?: string }

// sumLines writes a total as its terms: the first as it is, every next
// one with + or − by its sign and shown without it, then the total.
export function sumLines(terms: FormulaTerm[], total: FormulaTerm, fmt: (v: number) => string): FormulaLine[] {
  const lines: FormulaLine[] = terms.map((t, i) => {
    const negative = t.amount < 0
    const op: FormulaOp | undefined = negative ? '−' : i === 0 ? undefined : '+'
    return { op, label: t.label, value: fmt(negative ? -t.amount : t.amount), detail: t.detail }
  })
  lines.push({ op: '=', label: total.label, value: fmt(total.amount), detail: total.detail })
  return lines
}

// residual is what is left of total once the terms are taken out: the
// line that makes a breakdown add up to the figure the server computed,
// whatever rounding or tariff version the parts were computed with.
export function residual(total: number, terms: number[]): number {
  return terms.reduce((acc, t) => acc - t, total)
}

// times is the «a × b» detail of a line.
export function times(a: string, b: string): string {
  return `${a} × ${b}`
}
