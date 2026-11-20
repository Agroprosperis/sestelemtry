import { cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'
import type { EconomicsPortfolioResponse, EconomicsPortfolioSite } from '../../api'
import type { FormulaLine } from '../formula'
import { UAH_MONEY } from '../currency'
import { formatMwh, formatPercent } from '../monthly/format'
import { buildPortfolioFormulas, siteRoceFormula, siteSumLines } from '../portfolio/portfolioFormulas'
import { EconomicsPortfolioView } from '../portfolio/EconomicsPortfolioView'
import { portfolioRoce, siteRoce } from '../portfolio/roce'

afterEach(cleanup)

const money = UAH_MONEY

function site(over: Partial<EconomicsPortfolioSite>): EconomicsPortfolioSite {
  return {
    id: 'x',
    name: 'x',
    has_data: true,
    effect_uah: 0,
    ebitda_uah: 0,
    schedule_reserve_uah: 0,
    bess_reserve_uah: 0,
    action_reserve_uah: 0,
    bess_data_ok: true,
    bess_anomalous_hours: 0,
    bess_anomalous_days: 0,
    pv_kwh: 0,
    load_kwh: 0,
    grid_import_kwh: 0,
    grid_export_kwh: 0,
    ess_net_uah: 0,
    ess_captured_share: 0,
    ...over,
  }
}

const amount = (v: string) => Number(v.replace(/[^\d]/g, ''))
const signedSum = (lines: FormulaLine[]) =>
  lines.filter((l) => l.op !== '=').reduce((acc, l) => acc + (l.op === '−' ? -1 : 1) * amount(l.value), 0)

function fixture(count = 3): EconomicsPortfolioResponse {
  const sites = Array.from({ length: count }, (_, i) =>
    site({
      id: `s${i}`,
      ebitda_uah: (i + 1) * 100_000,
      schedule_reserve_uah: (i + 1) * 10_000,
      bess_reserve_uah: (i + 1) * 5_000,
      action_reserve_uah: (i + 1) * 15_000,
      pv_kwh: (i + 1) * 50_000,
      capex_uah: (i + 1) * 6_000_000,
      months_with_data: 6,
    }),
  )
  const sum = (pick: (s: EconomicsPortfolioSite) => number) => sites.reduce((acc, s) => acc + pick(s), 0)
  return {
    scope: 'year',
    label: '2026',
    tz: 'Europe/Kyiv',
    months_with_data: 6,
    sites,
    totals: site({
      id: '__total__',
      name: 'Портфель',
      ebitda_uah: sum((s) => s.ebitda_uah),
      schedule_reserve_uah: sum((s) => s.schedule_reserve_uah),
      bess_reserve_uah: sum((s) => s.bess_reserve_uah),
      action_reserve_uah: sum((s) => s.action_reserve_uah),
      pv_kwh: sum((s) => s.pv_kwh),
    }),
    trend: [],
  }
}

describe('portfolio formulas', () => {
  it('writes the EBITDA as the sites that add up to the card', () => {
    const p = fixture(3)
    const f = buildPortfolioFormulas(p, money)
    const lines = f.ebitda.lines ?? []
    expect(lines.at(-1)?.op).toBe('=')
    expect(lines.at(-1)?.value).toBe(money.format(p.totals.ebitda_uah))
    expect(signedSum(lines)).toBe(p.totals.ebitda_uah)
    // The biggest object first, every object listed (≤ 8).
    expect(lines).toHaveLength(4)
    expect(f.ebitda.rule).toContain('заряд УЗЕ з мережі та від генератора')
    expect(f.ebitda.rule).not.toContain('операційні витрати —')
    expect(f.ebitda.note).toContain('Знос УЗЕ й операційні витрати не віднімаються')
  })

  it('folds the smallest sites into «Інші» past eight and still adds up', () => {
    const p = fixture(11)
    const lines = buildPortfolioFormulas(p, money).ebitda.lines ?? []
    expect(lines).toHaveLength(9)
    expect(lines[7].label).toBe("Інші об'єкти (4)")
    expect(signedSum(lines)).toBe(p.totals.ebitda_uah)
    expect(lines.at(-1)?.value).toBe(money.format(p.totals.ebitda_uah))
  })

  it('closes with a residual line for objects without data', () => {
    const p = fixture(2)
    p.sites.push(site({ id: 'none', has_data: false }))
    p.totals.ebitda_uah += 7_000
    const lines = siteSumLines(p.sites, (s) => s.ebitda_uah, p.totals.ebitda_uah, 'EBITDA', money.format)
    expect(lines.map((l) => l.label)).toContain('Інше')
    expect(signedSum(lines)).toBe(p.totals.ebitda_uah)
  })

  it('adds EBITDA and both reserves into «Разом з резервами»', () => {
    const p = fixture(3)
    const f = buildPortfolioFormulas(p, money)
    const lines = f.withReserves.lines ?? []
    expect(lines.at(-1)?.value).toBe(money.format(p.totals.ebitda_uah + p.totals.action_reserve_uah))
    expect(signedSum(lines)).toBe(p.totals.ebitda_uah + p.totals.action_reserve_uah)
    expect(lines.map((l) => l.label)).toEqual(['EBITDA', 'Резерв графіка робіт', 'Оптимум УЗЕ (резерв)', 'Разом з резервами'])
    expect(f.scheduleReserve.rule).toContain('менше з (СЕС → мережа; мережа → споживання)')
    expect(signedSum(f.scheduleReserve.lines ?? [])).toBe(p.totals.schedule_reserve_uah)
    expect(signedSum(f.bessReserve.lines ?? [])).toBe(p.totals.bess_reserve_uah)
    expect(signedSum(f.actionReserveColumn.lines ?? [])).toBe(p.totals.action_reserve_uah)
    expect(f.pv.lines?.at(-1)?.value).toBe(formatMwh(p.totals.pv_kwh))
  })

  it('shows the portfolio ROCE from the annualised sums and a site row from its own numbers', () => {
    const p = fixture(3)
    const f = buildPortfolioFormulas(p, money)
    // Σ ebitda×12/6 = 2 × 600k = 1 200 000; Σ capex = 36 000 000.
    expect(f.roce.lines?.[0].value).toBe(money.format(1_200_000))
    expect(f.roce.lines?.[1].value).toBe(money.format(36_000_000))
    expect(f.roce.lines?.at(-1)?.value).toBe(formatPercent(portfolioRoce(p.sites)))

    const row = siteRoceFormula(p.sites[0], money)
    expect(row?.lines?.at(-1)?.value).toBe(formatPercent(siteRoce(p.sites[0])))
    expect(row?.lines?.[0].value).toBe(money.format(100_000))
    expect(row?.lines?.[1].detail).toBe('12 ÷ 6 міс.')
    expect(row?.lines?.[2].value).toBe(money.format(6_000_000))
    expect(siteRoceFormula(site({ capex_uah: 0 }), money)).toBeNull()
  })

  it('lists the fuel litres and the sites that burnt it', () => {
    const p = fixture(2)
    p.sites[0].gen_fuel_cost_uah = 4_000
    p.sites[1].gen_fuel_cost_uah = 6_000
    p.totals.gen_fuel_cost_uah = 10_000
    p.totals.gen_fuel_l = 250
    const lines = buildPortfolioFormulas(p, money).fuel.lines ?? []
    expect(lines[0].value).toBe('250 л')
    expect(lines.at(-1)?.value).toBe(money.format(10_000))
    expect(signedSum(lines.slice(1))).toBe(10_000)
  })
})

describe('portfolio view «i»', () => {
  it('replaces the wrong EBITDA definition with the real one and the portfolio numbers', () => {
    const portfolio = fixture(3)
    const { container } = render(
      <EconomicsPortfolioView
        data={{ portfolio, loading: false, error: null }}
        month="2026-11"
        period="2026"
        from="2026-01"
        to="2026-06"
        scope="year"
        onScopeChange={() => {}}
      />,
    )
    expect(container.textContent).not.toContain('мінус операційні витрати')

    const card = screen.getAllByText('EBITDA', { selector: '.kpi-label' })[0].closest('.kpi-card') as HTMLElement
    fireEvent.click(within(card).getByRole('button', { name: 'Як рахується: EBITDA' }))
    const tip = screen.getByRole('tooltip')
    const text = (tip.textContent ?? '').replace(/\s/g, ' ')
    expect(text).toContain('заряд УЗЕ з мережі та від генератора')
    expect(text).toContain('Знос УЗЕ й операційні витрати не віднімаються')
    expect(text).toContain('600 000 грн')
    expect(text).not.toContain('мінус операційні витрати')
    fireEvent.keyDown(document, { key: 'Escape' })

    // A site's ROCE cell has its own «i».
    const row = screen.getAllByRole('row').find((r) => r.textContent?.includes(formatPercent(siteRoce(portfolio.sites[0]))))
    expect(row).toBeTruthy()
    expect(within(row as HTMLElement).getByRole('button', { name: /Як рахується: ROCE/ })).toBeTruthy()
  })
})
