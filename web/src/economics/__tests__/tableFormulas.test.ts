import { cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import { createElement, type ReactElement } from 'react'
import { afterEach, describe, expect, it } from 'vitest'
import { EconomicsTable } from '../components/EconomicsTable'
import { hourEconomics, type HourEconomicsRow, type HourFlows } from '../compute'
import { rollHour, type EssState } from '../costBasis'
import { makeMoney, MoneyContext, type Money } from '../currency'
import { DEFAULT_TARIFFS, type Tariffs } from '../tariffs'

afterEach(cleanup)

const TARIFFS: Tariffs = { ...DEFAULT_TARIFFS, genFuelPriceUahPerL: 26.67, genFuelConsumptionLPerKwh: 0.3 }

const ZERO: HourFlows = {
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

function flowOf(h: number): HourFlows {
  if (h <= 3) return { ...ZERO, gridImport: 20, gridToEss: 8, essCharged: 8 }
  if (h >= 10 && h <= 14) return { ...ZERO, pv: 50, pvToEss: 10, essCharged: 10, gridExport: 20 }
  if (h >= 18 && h <= 21) return { ...ZERO, essDischarged: 15, essToLoad: 10, essToGrid: 5, gridExport: 5 }
  return { ...ZERO, gridImport: 5 }
}

// scaleTariffs is the day's tariff in another currency: the money of the
// rows is converted at the day's rate, the tariff on the page stays UAH.
function scaleTariffs(t: Tariffs, k: number): Tariffs {
  return {
    ...t,
    distributionUahPerKwh: t.distributionUahPerKwh * k,
    transmissionUahPerKwh: t.transmissionUahPerKwh * k,
    supplierMarginUahPerKwh: t.supplierMarginUahPerKwh * k,
    otherFeesUahPerKwh: t.otherFeesUahPerKwh * k,
    degradationUahPerKwh: t.degradationUahPerKwh * k,
    genFuelPriceUahPerL: t.genFuelPriceUahPerL * k,
  }
}

// buildDay is a day with a night grid charge, a PV midday with export and
// an evening discharge; hour 23 has no РДН price. Money is `k` times the
// UAH of the tariff.
function buildDay(tariffs: Tariffs, k = 1, withGen = false): HourEconomicsRow[] {
  const t = scaleTariffs(tariffs, k)
  let state: EssState = { kwh: 10, uah: 30 * k }
  let remaining = 10
  return Array.from({ length: 24 }, (_, h) => {
    const flow = flowOf(h)
    const priced = h !== 23
    const rdn = (2 + h * 0.4) * k
    const e = hourEconomics(priced ? rdn : 0, flow, t)
    const start = remaining
    const startAvg = state.kwh > 0 ? state.uah / state.kwh : 0
    const startUah = state.uah
    let withdrawn: number | null = null
    let realized: number | null = null
    let end: number | null = null
    if (priced) {
      const b = rollHour(state, flow, e.importPriceUahPerKwh, e.exportPriceUahPerKwh, t.degradationUahPerKwh)
      state = b.next
      withdrawn = b.withdrawnCostUah
      realized = b.realizedProfitUah
      end = b.avgCostEndUahPerKwh
    }
    remaining += flow.pvToEss + flow.gridToEss - flow.essToLoad - flow.essToGrid
    const row: HourEconomicsRow = {
      hour: h,
      hourStart: `2026-11-15T${String(h).padStart(2, '0')}:00:00+02:00`,
      rdnUahPerKwh: priced ? rdn : null,
      flow,
      economics: e,
      essRemainingKwhStart: start,
      essCostBasisUahStart: priced ? startUah : null,
      essAvgCostUahPerKwhStart: priced ? startAvg : null,
      essWithdrawnCostUah: withdrawn,
      essRealizedProfitUah: realized,
      essCostBasisUahEnd: priced ? state.uah : null,
      essAvgCostUahPerKwhEnd: end,
      essResidualKwhEnd: priced ? state.kwh : null,
    }
    if (withGen && h === 20) {
      const c = 8.001 * k
      row.gen = {
        genKwh: 20,
        estimated: false,
        minutes: { generator: 30, island: 15, blackout: 0, transition: 0, unknown: 0 },
        accounted: true,
        genToLoad: 15,
        genToEss: 5,
        loadOffgrid: 25,
        pvToLoadOffgrid: 0,
        essToLoadOffgrid: 4,
        pvToEssOffgrid: 0,
        fuelCostUah: 20 * c,
        fuelL: 6,
        fuelMethod: 'per_kwh',
        pvOffgridValueUah: 0,
        essOffgridValueUah: 4 * c,
        chargeCostUah: 5 * c,
      }
    }
    return row
  })
}

function mount(rows: HourEconomicsRow[], tariffs: Tariffs, money?: Money): ReactElement {
  const table = createElement(EconomicsTable, { rows, organizationID: 'ke', date: '2026-11-15', tariffs })
  return money ? createElement(MoneyContext.Provider, { value: money }, table) : table
}

const PRICE_ROWS = new Set(['Ціна імпорту', 'Ціна експорту', 'Собівартість УЗЕ'])
const UNIT = /\s*(кВт·год|хв|грн|\$|€|л)$/

// num reads the figure at the head of a value: «1 234,5 кВт·год», «0,30 л/кВт·год».
function num(text: string): number {
  const head = /^[-−]?[\d\s\u00a0\u202f]*\d(,\d+)?/.exec(text)?.[0] ?? ''
  return Number(head.replace(/[\s\u00a0\u202f]/g, '').replace(',', '.').replace('−', '-'))
}

type Bubble = { lines: Array<{ op: string; label: string; value: string }>; text: string }

// openBubble opens the «i» of a table row and reads the bubble.
function openBubble(row: HTMLElement): Bubble | null {
  const button = row.querySelector<HTMLButtonElement>('th button.eco-formula-trigger')
  if (!button) return null
  fireEvent.click(button)
  const tip = screen.getByRole('tooltip')
  const lines = Array.from(tip.querySelectorAll('.eco-formula-line')).map((l) => ({
    op: l.querySelector('.eco-formula-op')?.textContent ?? '',
    label: l.querySelector('.eco-formula-label')?.firstChild?.textContent ?? '',
    value: l.querySelector('.eco-formula-value')?.textContent ?? '',
  }))
  const bubble = { lines, text: tip.textContent ?? '' }
  fireEvent.keyDown(document, { key: 'Escape' })
  return bubble
}

function bodyRows(): HTMLElement[] {
  return Array.from(document.querySelectorAll<HTMLElement>('table tbody tr'))
}

function labelOf(row: HTMLElement): string {
  return row.querySelector('th')?.firstChild?.textContent ?? ''
}

describe('hourly table formulas', () => {
  const days: Array<[string, () => ReturnType<typeof mount>]> = [
    ['a grid-and-PV day', () => mount(buildDay(TARIFFS), TARIFFS)],
    ['a day with VAT', () => mount(buildDay({ ...TARIFFS, includeVat: true }), { ...TARIFFS, includeVat: true })],
    ['a day with the generator', () => mount(buildDay(TARIFFS, 1, true), TARIFFS)],
    ['a day in dollars', () => mount(buildDay(TARIFFS, 1 / 40), TARIFFS, makeMoney('USD', 40))],
  ]

  for (const [name, make] of days) {
    describe(name, () => {
      it('gives every row an «i» and closes the formula on the row’s own Σ', () => {
        render(make())
        let tips = 0
        for (const row of bodyRows()) {
          const bubble = openBubble(row)
          expect(bubble, labelOf(row)).not.toBeNull()
          tips++
          const sigma = row.querySelector('td.economics-table-total-cell')?.textContent ?? ''
          const result = bubble?.lines.find((l) => l.op === '=')
          if (PRICE_ROWS.has(labelOf(row))) {
            // A price has no Σ either; its formula works one hour through.
            expect(sigma).toBe('—')
            expect(result?.label, labelOf(row)).toMatch(/^(Ціна (імпорту|експорту), \d\d:00|Собівартість УЗЕ)$/)
          } else if (sigma === '—') {
            // No Σ, no result: the bubble explains why instead.
            expect(result, `${labelOf(row)} has no Σ`).toBeUndefined()
            expect(bubble?.text).toMatch(/Σ немає/)
          } else {
            expect(result, labelOf(row)).toBeDefined()
            expect(result?.value.replace(UNIT, ''), labelOf(row)).toBe(sigma)
          }
        }
        expect(tips).toBe(bodyRows().length)
        expect(tips).toBeGreaterThan(25)
      })

      it('adds the terms up to the result', () => {
        render(make())
        for (const row of bodyRows()) {
          const bubble = openBubble(row)
          const lines = bubble?.lines ?? []
          const ops = new Set(lines.map((l) => l.op))
          const result = lines.find((l) => l.op === '=')
          if (!result) continue
          const money = /грн|\$|€/.test(result.value)
          if ([...ops].every((o) => ['', '+', '−', '='].includes(o))) {
            let acc = 0
            for (const l of lines) {
              if (l.op === '=') break
              acc += l.op === '−' ? -num(l.value) : num(l.value)
            }
            const tolerance = (money ? 0.5 : 0.05) * lines.length + 0.01
            expect(Math.abs(acc - num(result.value)), `${labelOf(row)}: ${JSON.stringify(lines)}`).toBeLessThanOrEqual(tolerance)
          } else if (lines.length === 3 && lines[1].op === '×') {
            // Σ kWh × weighted price = Σ money.
            const volume = num(lines[0].value)
            const price = num(lines[1].value)
            const product = volume * price
            const slack = 0.006 * Math.max(volume, 1) + (money ? 1 : 0.1)
            expect(Math.abs(product - num(result.value)), `${labelOf(row)}: ${JSON.stringify(lines)}`).toBeLessThanOrEqual(slack)
          }
        }
      })
    })
  }

  it('works the import and export price through for the hour of the largest flow', () => {
    const vatTariffs = { ...TARIFFS, includeVat: true, supplierMarginUahPerKwh: 0.3 }
    const rows = buildDay(vatTariffs)
    render(mount(rows, vatTariffs))
    const byLabel = (label: string) => bodyRows().find((r) => labelOf(r) === label) as HTMLElement

    // Import peaks at 20 kWh in hours 0–3: the first of them is the example.
    const imp = openBubble(byLabel('Ціна імпорту'))!
    const rdn0 = rows[0].rdnUahPerKwh!
    expect(imp.lines.map((l) => l.label)).toEqual([
      'РДН, 00:00',
      'Розподіл',
      'Передача',
      'Надбавка постачальника',
      'ПДВ 20 %',
      'Ціна імпорту, 00:00',
    ])
    const stack = rdn0 + 2.75218 + 0.74291 + 0.3
    expect(num(imp.lines[0].value)).toBeCloseTo(rdn0, 2)
    expect(num(imp.lines[4].value)).toBeCloseTo(1.2, 5)
    expect(num(imp.lines[5].value)).toBeCloseTo(stack * 1.2, 1)
    expect(num(imp.lines[5].value)).toBeCloseTo(rows[0].economics.importPriceUahPerKwh, 2)

    // Export peaks at 20 kWh in hours 10–14.
    const exp = openBubble(byLabel('Ціна експорту'))!
    expect(exp.lines[0].label).toBe('РДН, 10:00')
    expect(exp.lines[1].value).toBe('0,95')
    expect(num(exp.lines[3].value)).toBeCloseTo(rows[10].economics.exportPriceUahPerKwh, 2)
  })

  it('converts the UAH tariff to the page currency', () => {
    const rows = buildDay(TARIFFS, 1 / 40)
    render(mount(rows, TARIFFS, makeMoney('USD', 40)))
    const imp = openBubble(bodyRows().find((r) => labelOf(r) === 'Ціна імпорту') as HTMLElement)!
    expect(imp.lines[1].value).toBe(makeMoney('USD', 40).formatPriceAllowZero(2.75218 / 40))
    expect(imp.lines[1].value).toBe('0,069')
  })

  it('shows the min and max of the rows that have no Σ', () => {
    const rows = buildDay(TARIFFS)
    render(mount(rows, TARIFFS))
    const rdn = openBubble(bodyRows().find((r) => labelOf(r) === 'РДН') as HTMLElement)!
    expect(rdn.lines.map((l) => l.label)).toEqual(['Мінімум за добу', 'Максимум за добу'])
    expect(rdn.lines[0].value).toBe('2,00')
    expect(rdn.text).toContain('00:00')
    expect(rdn.lines[1].value).toBe('10,80')
    expect(rdn.text).toContain('22:00')

    const left = openBubble(bodyRows().find((r) => labelOf(r) === 'Залишок УЗЕ') as HTMLElement)!
    expect(left.lines[0]).toMatchObject({ label: 'На початок доби', value: '10,0 кВт·год' })
    const peak = Math.max(...rows.map((r) => r.essRemainingKwhStart ?? 0))
    expect(num(left.lines.find((l) => l.label === 'Максимум')!.value)).toBeCloseTo(peak, 1)

    const basis = openBubble(bodyRows().find((r) => labelOf(r) === 'Собівартість УЗЕ') as HTMLElement)!
    expect(basis.lines.map((l) => l.op)).toEqual(['', '', '', '÷', '='])
  })

  it('writes the generator’s fuel from its hours and tariff', () => {
    render(mount(buildDay(TARIFFS, 1, true), TARIFFS))
    const cost = openBubble(bodyRows().find((r) => labelOf(r) === 'Вартість пального') as HTMLElement)!
    expect(cost.lines[0].value).toBe('20,0 кВт·год')
    expect(cost.lines[1].value).toBe('8,00 грн/кВт·год')
    expect(cost.text).toContain('26,67 за літр × 0,30 л/кВт·год')
    expect(cost.lines[2].value).toBe('160 грн')
    const gen = openBubble(bodyRows().find((r) => labelOf(r) === 'Витрати: Заряд УЗЕ від генератора') as HTMLElement)!
    expect(gen.lines[0].value).toBe('5,0 кВт·год')
    const own = within(document.body).queryAllByText('Без мережі (вартість пального)')
    expect(own).toHaveLength(0)
    const essSelf = openBubble(bodyRows().find((r) => labelOf(r) === 'Дохід: УЗЕ → споживання') as HTMLElement)!
    expect(essSelf.lines.map((l) => l.label)).toContain('Без мережі (вартість пального)')
  })

  it('explains the «Чистий ефект УЗЕ» strip down to its total', () => {
    const rows = buildDay(TARIFFS)
    const { container } = render(mount(rows, TARIFFS))
    const strip = container.querySelector('.economics-ess-strip') as HTMLElement
    fireEvent.click(within(strip).getByRole('button', { name: 'Як рахується: Чистий ефект УЗЕ' }))
    const tip = screen.getByRole('tooltip')
    const lines = Array.from(tip.querySelectorAll('.eco-formula-line')).map((l) => ({
      op: l.querySelector('.eco-formula-op')?.textContent ?? '',
      value: l.querySelector('.eco-formula-value')?.textContent ?? '',
    }))
    const result = lines.find((l) => l.op === '=')!
    expect(result.value).toBe((strip.querySelector('.economics-ess-strip-total') as HTMLElement).textContent)
    let acc = 0
    for (const l of lines) {
      if (l.op === '=') break
      acc += l.op === '−' ? -num(l.value) : num(l.value)
    }
    expect(Math.abs(acc - num(result.value))).toBeLessThanOrEqual(0.5 * lines.length)
    expect(num(result.value)).toBeCloseTo(
      rows.filter((r) => r.rdnUahPerKwh !== null).reduce((a, r) => a + r.economics.essNet, 0),
      0,
    )
  })
})
