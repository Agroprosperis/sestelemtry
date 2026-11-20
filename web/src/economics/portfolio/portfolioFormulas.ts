import type { EconomicsPortfolioResponse, EconomicsPortfolioSite, EconomicsPortfolioTrendMonth } from '../../api'
import { formatOrganizationLabel } from '../../dashboard/config'
import type { Money } from '../currency'
import type { Formula, FormulaLine, FormulaTerm } from '../formula'
import { sumLines } from '../formula'
import { formatMwh, formatPercent } from '../monthly/format'
import { monthsOf, portfolioRoce, portfolioRoceParts, siteRoce } from './roce'

// Formulas of the «Портфель» page. The portfolio's JSON carries each
// object's finished figures but not the energies and prices behind the
// schedule reserve, so those are explained as the rule in words plus the
// sum over the objects.

// MAX_SITE_LINES is how many objects a bubble lists before the rest is
// folded into one «Інші» line.
const MAX_SITE_LINES = 8

const factorFmt = new Intl.NumberFormat('uk-UA', { minimumFractionDigits: 2, maximumFractionDigits: 2 })
const litresFmt = new Intl.NumberFormat('uk-UA', { maximumFractionDigits: 0 })

export const OPTIMUM_TIP =
  'Резерв розкладу УЗЕ — скільки ще могла б заробити батарея, якби заряджалась і розряджалась у найвигідніші години, з її реальною потужністю, діапазоном заряду й ККД, за цінами РДН і тарифами. Резерв = можливий максимум − факт: це недоотримана вигода, яку можна забрати кращим керуванням, а не збиток.'

// siteSumLines writes a portfolio total as the sum of the objects with
// data: the biggest ones one by one, the rest as «Інші об'єкти», closed
// with the total the card shows so the lines add up to it exactly.
export function siteSumLines(
  sites: EconomicsPortfolioSite[],
  pick: (s: EconomicsPortfolioSite) => number,
  total: number,
  totalLabel: string,
  fmt: (v: number) => string,
): FormulaLine[] {
  const withData = sites.filter((s) => s.has_data)
  const ordered = [...withData].sort((a, b) => Math.abs(pick(b)) - Math.abs(pick(a)))
  const shown = ordered.length > MAX_SITE_LINES ? ordered.slice(0, MAX_SITE_LINES - 1) : ordered
  const terms: FormulaTerm[] = shown.map((s) => ({ label: formatOrganizationLabel(s.id), amount: pick(s) }))
  const rest = total - shown.reduce((acc, s) => acc + pick(s), 0)
  const hidden = ordered.length - shown.length
  if (hidden > 0) {
    terms.push({ label: `Інші об'єкти (${hidden})`, amount: rest })
  } else if (Math.abs(rest) >= 0.5 && terms.length > 0) {
    terms.push({ label: 'Інше', amount: rest })
  }
  return sumLines(terms, { label: totalLabel, amount: total }, fmt)
}

const EBITDA_RULE =
  'EBITDA об’єкта = доходи й економія СЕС і УЗЕ (споживання й мережа) − заряд УЗЕ з мережі та від генератора. EBITDA портфеля — сума по об’єктах.'
const EBITDA_NOTE = 'Знос УЗЕ й операційні витрати не віднімаються.'

export type PortfolioFormulas = {
  ebitda: Formula
  scheduleReserve: Formula
  bessReserve: Formula
  actionReserveColumn: Formula
  withReserves: Formula
  fuel: Formula
  roce: Formula
  pv: Formula
  captured: Formula
  trend: Formula
}

export function buildPortfolioFormulas(portfolio: EconomicsPortfolioResponse, money: Money): PortfolioFormulas {
  const fmt = money.format
  const { sites, totals } = portfolio

  const ebitda: Formula = {
    text: 'Скільки заробили всі об’єкти за період: доходи й економія СЕС і УЗЕ мінус заряд УЗЕ з мережі та від генератора.',
    rule: EBITDA_RULE,
    lines: siteSumLines(sites, (s) => s.ebitda_uah, totals.ebitda_uah, 'EBITDA портфеля', fmt),
    note: EBITDA_NOTE,
  }

  const scheduleReserve: Formula = {
    text: 'Скільки ще можна зекономити, перенісши гнучкі роботи на денні години з дешевшою електроенергією та власним виробітком СЕС.',
    rule: 'Для кожного об’єкта: менше з (СЕС → мережа; мережа → споживання) × max(0; середня ціна імпорту − середня ціна експорту). Резерв портфеля — сума по об’єктах.',
    lines: siteSumLines(sites, (s) => s.schedule_reserve_uah, totals.schedule_reserve_uah, 'Резерв графіка робіт', fmt),
    note: 'Енергії й ціни об’єктів на цій сторінці не передаються, тому показані готові резерви об’єктів.',
  }

  const bessReserve: Formula = {
    text: OPTIMUM_TIP,
    rule: 'Для кожного об’єкта: резерв розкладу УЗЕ = max(0; можливий максимум УЗЕ − факт УЗЕ). Резерв портфеля — сума по об’єктах.',
    lines: siteSumLines(sites, (s) => s.bess_reserve_uah, totals.bess_reserve_uah, 'Резерв розкладу УЗЕ', fmt),
  }

  const actionReserveColumn: Formula = {
    text: 'Обидва резерви об’єкта разом: те, що ще можна забрати кращим графіком робіт і керуванням УЗЕ.',
    rule: 'Разом резерв = резерв графіка робіт + резерв розкладу УЗЕ, по кожному об’єкту; у рядку «Портфель» — сума по об’єктах.',
    lines: siteSumLines(sites, (s) => s.action_reserve_uah, totals.action_reserve_uah, 'Разом резерв', fmt),
  }

  const reserveGap = totals.action_reserve_uah - totals.schedule_reserve_uah - totals.bess_reserve_uah
  const withReservesTerms: FormulaTerm[] = [
    { label: 'EBITDA', amount: totals.ebitda_uah },
    { label: 'Резерв графіка робіт', amount: totals.schedule_reserve_uah },
    { label: 'Резерв розкладу УЗЕ', amount: totals.bess_reserve_uah },
  ]
  if (Math.abs(reserveGap) >= 0.5) withReservesTerms.push({ label: 'Інше', amount: reserveGap })
  const withReserves: Formula = {
    text: 'Потенційний результат періоду: фактична EBITDA плюс обидва резерви, якби їх забрали повністю.',
    rule: 'Разом з резервами = EBITDA + резерв графіка робіт + резерв розкладу УЗЕ.',
    lines: sumLines(
      withReservesTerms,
      { label: 'Разом з резервами', amount: totals.ebitda_uah + totals.action_reserve_uah },
      fmt,
    ),
  }

  const litres = totals.gen_fuel_l ?? 0
  const fuelCost = totals.gen_fuel_cost_uah ?? 0
  const fuel: Formula = {
    text: 'Вартість пального генераторів за тарифами об’єктів і ефект годин, коли об’єкти працювали без мережі.',
    rule: 'Для кожного об’єкта: витрачені літри × ціна пального з його тарифів. Пальне портфеля — сума по об’єктах.',
    lines: [
      { label: 'Витрачено пального', value: `${litresFmt.format(litres)} л` },
      ...siteSumLines(
        sites.filter((s) => (s.gen_fuel_cost_uah ?? 0) > 0),
        (s) => s.gen_fuel_cost_uah ?? 0,
        fuelCost,
        'Пальне генераторів',
        fmt,
      ),
    ],
    note: `Ефект годин без мережі — ${fmt(totals.effect_offgrid_uah ?? 0)}: скільки пального заощадили СЕС і УЗЕ.`,
  }

  const parts = portfolioRoceParts(sites)
  const roceValue = portfolioRoce(sites)
  const roce: Formula = {
    text: 'Річна віддача вкладеного капіталу портфеля.',
    rule: 'ROCE = Σ (EBITDA об’єкта × 12 ÷ місяці з даними) ÷ Σ CAPEX — по об’єктах, у яких задано CAPEX.',
    lines:
      parts.count > 0
        ? [
            {
              label: 'EBITDA, приведена до року',
              value: fmt(parts.ebitda),
              detail: `Σ по ${parts.count} об’єктах із CAPEX`,
            },
            { op: '÷', label: 'Сумарний CAPEX', value: fmt(parts.capex), detail: 'на кінець періоду' },
            { op: '=', label: 'ROCE портфеля', value: formatPercent(roceValue) },
          ]
        : undefined,
    note: 'Неповний період завищує оцінку: приведення до року лінійне, без сезонності. Точна окупність — на сторінці «Окупність».',
  }

  const pv: Formula = {
    text: 'Скільки електроенергії виробили СЕС усіх об’єктів.',
    rule: 'Виробіток СЕС за період; у рядку «Портфель» — сума по об’єктах.',
    lines: siteSumLines(sites, (s) => s.pv_kwh, totals.pv_kwh, 'Виробіток СЕС', formatMwh),
  }

  const captured: Formula = {
    text: 'Яку частку можливого виграшу УЗЕ вже отримано.',
    rule: 'Реалізовано = факт УЗЕ ÷ можливий максимум УЗЕ. Резерв розкладу — це максимум мінус факт, тому що більший резерв, то менша частка.',
    note: 'Для портфеля частка не рахується (—): максимум і факт об’єктів на цій сторінці не передаються.',
  }

  return {
    ebitda,
    scheduleReserve,
    bessReserve,
    actionReserveColumn,
    withReserves,
    fuel,
    roce,
    pv,
    captured,
    trend: trendFormula(portfolio.trend, money),
  }
}

// siteRoceFormula is a row's ROCE with the object's own numbers; null for
// an object without data or CAPEX.
export function siteRoceFormula(site: EconomicsPortfolioSite, money: Money): Formula | null {
  const roce = siteRoce(site)
  if (!Number.isFinite(roce)) return null
  const months = monthsOf(site)
  return {
    text: 'Річна віддача вкладеного капіталу об’єкта.',
    rule: 'ROCE = EBITDA періоду × (12 ÷ місяці з даними) ÷ CAPEX на кінець періоду.',
    lines: [
      { label: 'EBITDA за період', value: money.format(site.ebitda_uah) },
      {
        op: '×',
        label: 'Приведення до року',
        value: factorFmt.format(12 / months),
        detail: `12 ÷ ${months} міс.`,
      },
      { op: '÷', label: 'CAPEX на кінець періоду', value: money.format(site.capex_uah as number) },
      { op: '=', label: 'ROCE', value: formatPercent(roce) },
    ],
    note: months < 12 ? 'Неповний період: приведення до року лінійне, без сезонності.' : undefined,
  }
}

function trendFormula(trend: EconomicsPortfolioTrendMonth[], money: Money): Formula {
  const sum = (pick: (m: EconomicsPortfolioTrendMonth) => number) => trend.reduce((acc, m) => acc + pick(m), 0)
  return {
    text: 'Енергія всіх об’єктів по місяцях: кожен стовпчик — сума по об’єктах за місяць.',
    rule: 'Для кожного місяця: СЕС, імпорт з мережі, експорт у мережу й споживання, підсумовані по об’єктах; підказка стовпця додає EBITDA місяця.',
    lines: [
      { label: 'СЕС', value: formatMwh(sum((m) => m.pv_kwh)), detail: `Σ за ${trend.length} міс.` },
      { label: 'Імпорт', value: formatMwh(sum((m) => m.grid_import_kwh)) },
      { label: 'Експорт', value: formatMwh(sum((m) => m.grid_export_kwh)) },
      { label: 'Споживання', value: formatMwh(sum((m) => m.load_kwh)) },
      { label: 'EBITDA', value: money.format(sum((m) => m.ebitda_uah)) },
    ],
  }
}
