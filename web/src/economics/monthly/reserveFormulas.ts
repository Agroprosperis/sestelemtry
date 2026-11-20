// Formulas of the AI panel (the two reserves and their sum), the УЗЕ
// cycle accordion and the annual view's per-month lists and quarter
// cards. Builders only; the figures they substitute are the ones
// reserveSplit and the views print.
import type { EconomicsAnnualMonthRollup, EconomicsAnnualQuarter, EconomicsMonthlyTotals, EconomicsUzeCycleChart } from '../../api'
import type { Money } from '../currency'
import { sumLines, type Formula } from '../formula'
import { formatKwh, formatMonthName, formatMonthTitle, formatMwh, formatPercent } from './format'
import { moneyEps, priceText, withResidual } from './formulaKit'
import { reserveSplit, uahShort, type ReserveKey } from './rollup'

// elevatorReserveFormula: the «Резерв графіка» of a period.
export function elevatorReserveFormula(t: EconomicsMonthlyTotals, m: Money): Formula {
  const r = reserveSplit(t)
  const shiftable = Math.min(t.pv_to_grid_kwh, t.grid_to_load_kwh)
  const gap = Math.max(0, t.avg_import_price_uah_per_kwh - t.avg_export_price_uah_per_kwh)
  return {
    text: 'Скільки можна було б заощадити, якби гнучке навантаження працювало в години, коли СЕС інакше йде в мережу.',
    rule: 'менше з (СЕС → мережа, Мережа → споживання) × (середня ціна імпорту − середня ціна експорту).',
    lines: [
      { label: 'СЕС віддано в мережу', value: formatKwh(t.pv_to_grid_kwh) },
      { label: 'Куплено з мережі на споживання', value: formatKwh(t.grid_to_load_kwh) },
      { op: '=', label: 'Можна перенести (менше з двох)', value: formatKwh(shiftable) },
      {
        op: '×',
        label: 'Різниця цін: імпорт − експорт',
        value: priceText(m, gap),
        detail: `${priceText(m, t.avg_import_price_uah_per_kwh)} − ${priceText(m, t.avg_export_price_uah_per_kwh)}`,
      },
      { op: '=', label: 'Резерв графіка', value: uahShort(r.elevator, m), detail: m.format(r.elevator) },
    ],
    note: 'Оцінка зверху: припускає, що весь цей обсяг споживання можна пересунути. Не збиток, а можливість.',
  }
}

// essReserveFormula: the «Резерв таймінгу УЗЕ» of a period.
export function essReserveFormula(t: EconomicsMonthlyTotals, m: Money): Formula {
  const r = reserveSplit(t)
  const hasOptimum = t.ess_optimum_uah > 0
  const causes = `таймінг ${m.format(t.ess_reserve_timing_uah)}, SOC ${m.format(t.ess_reserve_soc_uah)}, СЕС → УЗЕ ${m.format(t.ess_reserve_pv_uah)}`
  return {
    text: 'Скільки УЗЕ могла б заробити більше, якби розряджалась у найкращі години.',
    rule: 'Оптимум − Факт. Оптимум — найкращий розклад розряду й заряду (динамічне програмування) у межах потужності, діапазону SOC, ККД і зносу; факт — Σ чистого ефекту УЗЕ.',
    lines: hasOptimum
      ? [
          { label: 'Оптимум УЗЕ', value: m.format(t.ess_optimum_uah) },
          { op: '−', label: 'Факт УЗЕ', value: m.format(t.ess_fact_uah) },
          { op: '=', label: 'Резерв таймінгу УЗЕ', value: uahShort(r.ess, m), detail: m.format(t.ess_reserve_uah) },
        ]
      : [{ op: '=', label: 'Резерв таймінгу УЗЕ', value: uahShort(r.ess, m), detail: 'оптимум не оцінено' }],
    note: hasOptimum
      ? `Захоплено ${formatPercent(t.ess_captured_share)} = факт ÷ оптимум. Причини резерву: ${causes}. Від’ємний резерв показано нулем.`
      : 'Недостатньо активності УЗЕ для оцінки оптимуму.',
  }
}

// totalReserveFormula: «Разом до дій» / «Сумарний резерв».
export function totalReserveFormula(t: EconomicsMonthlyTotals, m: Money): Formula {
  const r = reserveSplit(t)
  return {
    text: 'Усі підтверджені можливості разом.',
    rule: 'Резерв графіка + Резерв таймінгу УЗЕ.',
    lines: [
      { label: 'Резерв графіка', value: uahShort(r.elevator, m) },
      { op: '+', label: 'Резерв таймінгу УЗЕ', value: uahShort(r.ess, m) },
      { op: '=', label: 'Разом до дій', value: uahShort(r.total, m) },
    ],
    note: 'Два важелі можуть частково перекриватись (одна й та сама кВт·год), тому це верхня оцінка.',
  }
}

// panelFormulas keys the three reserve formulas by what an AI-panel row
// or card calls them.
export function panelFormulas(t: EconomicsMonthlyTotals, m: Money): Record<ReserveKey, Formula> {
  return {
    elevator: elevatorReserveFormula(t, m),
    ess: essReserveFormula(t, m),
    total: totalReserveFormula(t, m),
  }
}

// --- Annual per-month lists ----------------------------------------------

// scheduleMonthsFormula explains «Резерв переносу робіт по місяцях»: the
// months' reserves and what they add up to, against the year's headline.
export function scheduleMonthsFormula(
  months: Array<{ month: string; reserve: number }>,
  headline: number,
  m: Money,
): Formula {
  const shown = months.slice(0, 6)
  const rest = months.slice(6).reduce((acc, x) => acc + x.reserve, 0)
  const sum = months.reduce((acc, x) => acc + x.reserve, 0)
  const terms = shown.map((x) => ({ label: formatMonthTitle(x.month), amount: x.reserve }))
  if (months.length > shown.length) terms.push({ label: `Ще ${months.length - shown.length} міс.`, amount: rest })
  return {
    text: 'Резерв графіка кожного місяця, найбільші першими.',
    rule: 'За кожен місяць: менше з (СЕС → мережа, Мережа → споживання) × (середня ціна імпорту − середня ціна експорту).',
    lines: sumLines(terms, { label: 'Сума місяців', amount: sum }, (v) => uahShort(v, m)),
    note:
      Math.abs(sum - headline) > moneyEps(m)
        ? `«Резерв графіка» за період — ${uahShort(headline, m)} — рахується з підсумків усього періоду (одне «менше з двох» і одна різниця цін), тому не дорівнює сумі місяців.`
        : undefined,
  }
}

type EssMonth = { month: string; optimum: number; fact: number; reserve: number }

// essMonthsFormula explains «Резерв таймінгу УЗЕ по місяцях» and its
// «захоплення» column from the months listed.
export function essMonthsFormula(months: EssMonth[], m: Money): { reserve: Formula; capture: Formula } {
  const opt = months.reduce((acc, x) => acc + x.optimum, 0)
  const fact = months.reduce((acc, x) => acc + x.fact, 0)
  const reserve = months.reduce((acc, x) => acc + x.reserve, 0)
  const share = opt > 0 ? Math.max(0, Math.min(1, fact / opt)) : NaN
  return {
    reserve: {
      text: 'Недовикористана можливість УЗЕ: оптимум мінус факт за кожен місяць.',
      rule: 'Резерв місяця = Оптимум − Факт. Оптимум — модельний максимум у межах потужності, SOC, ККД і зносу цього місяця.',
      lines: withLines(
        [
          { label: `Оптимум, ${months.length} міс.`, value: m.format(opt) },
          { op: '−', label: 'Факт', value: m.format(fact) },
        ],
        { label: 'Резерв (сума місяців)', value: m.format(reserve) },
      ),
      note: 'Не збиток, а оцінка. Місяці без оцінки оптимуму не показано.',
    },
    capture: {
      text: 'Яку частку оптимуму УЗЕ реально захопила.',
      rule: 'Захоплення = Факт ÷ Оптимум (від 0 до 100%).',
      lines: [
        { label: 'Факт', value: m.format(fact) },
        { op: '÷', label: 'Оптимум', value: m.format(opt) },
        { op: '=', label: 'Захоплення', value: formatPercent(share) },
      ],
      note: 'У смужці кожного місяця — його власна частка: факт ÷ оптимум місяця.',
    },
  }
}

function withLines(head: Formula['lines'], total: { label: string; value: string }): Formula['lines'] {
  return [...(head ?? []), { op: '=', ...total }]
}

// monthElevatorFormula: a month's chip in the schedule-reserve list.
export function monthElevatorFormula(month: EconomicsAnnualMonthRollup, m: Money): Formula {
  const f = elevatorReserveFormula(month.totals, m)
  return { ...f, text: `${formatMonthTitle(month.month)}. ${f.text}` }
}

// --- Quarter cards --------------------------------------------------------

export type QuarterKind = 'ebitda' | 'effect' | 'pv'

function quarterMonths(q: EconomicsAnnualQuarter, months: EconomicsAnnualMonthRollup[]): EconomicsAnnualMonthRollup[] {
  return months.filter((mo) => {
    const mm = /^(\d{4})-(\d{2})$/.exec(mo.month)
    return !!mm && Number(mm[1]) === q.year && Math.floor((Number(mm[2]) - 1) / 3) + 1 === q.quarter
  })
}

// quarterFormula writes a quarter card's figure as the sum of its months.
export function quarterFormula(
  kind: QuarterKind,
  q: EconomicsAnnualQuarter,
  months: EconomicsAnnualMonthRollup[],
  m: Money,
): Formula {
  const own = quarterMonths(q, months)
  const pick = (t: EconomicsMonthlyTotals) => (kind === 'ebitda' ? t.ebitda_uah : kind === 'effect' ? t.effect_uah : t.pv_kwh)
  const total = kind === 'ebitda' ? q.ebitda_uah : kind === 'effect' ? q.effect_uah : q.pv_kwh
  const name = kind === 'ebitda' ? 'EBITDA' : kind === 'effect' ? 'Ефект' : 'Генерація СЕС'
  const fmt = kind === 'pv' ? formatMwh : m.format
  const eps = kind === 'pv' ? 50 : moneyEps(m)
  const terms = withResidual(
    own.map((mo) => ({ label: formatMonthName(mo.month), amount: pick(mo.totals) })),
    total,
    'Місяці без даних / інше',
    eps,
  )
  const word = kind === 'ebitda' ? 'EBITDA' : kind === 'effect' ? 'економічний ефект' : 'генерація СЕС'
  return {
    text: `${word[0].toUpperCase()}${word.slice(1)} кварталу.`,
    rule: `Сума місяців кварталу: ${kind === 'ebitda' ? 'дохід − витрати на заряд УЗЕ' : kind === 'effect' ? 'базова − фактична вартість' : 'Σ СЕС'} кожного місяця.`,
    lines: sumLines(terms, { label: `${name}, ${q.quarter} кв. ${q.year}`, amount: total }, fmt),
  }
}

// --- УЗЕ cycle accordion --------------------------------------------------

export type CycleFormulas = { optimum: Formula; fact: Formula; reserve: Formula; captured: Formula; captureText: string }

// cycleFormulas explains a cycle's four figures from its summary; fmt is
// the page's money writer for the chart.
export function cycleFormulas(chart: EconomicsUzeCycleChart, fmt: (v: number) => string): CycleFormulas {
  const o = chart.summary?.optimal
  const factEffect = chart.summary?.fact?.effect ?? 0
  const optEffect = o?.effect ?? 0
  const exportVal = o?.export_val ?? 0
  const loadVal = o?.load_val ?? 0
  const pvCost = o?.charge_pv_cost ?? 0
  const gridCost = o?.grid_cost ?? 0
  const degradation = o?.degradation ?? 0
  const reserve = optEffect - factEffect
  const captured = optEffect ? Math.round((factEffect / optEffect) * 1000) / 10 : 0
  const energy = (kwh: number | undefined) => (kwh ? formatKwh(kwh) : undefined)

  const terms = withResidual(
    [
      { label: 'Експорт УЗЕ → мережу', amount: exportVal, detail: energy(o?.discharge_kwh) },
      { label: 'УЗЕ → споживання', amount: loadVal },
      { label: 'Собівартість сонця (втрачений експорт)', amount: -pvCost, detail: energy(o?.charge_pv_kwh) },
      ...(gridCost ? [{ label: 'Заряд з мережі', amount: -gridCost, detail: energy(o?.charge_grid_kwh) }] : []),
      { label: 'Знос батареї', amount: -degradation },
    ],
    optEffect,
    'Інше (округлення)',
    0.5,
  )

  return {
    optimum: {
      text: 'Скільки УЗЕ заробила б цього циклу з ідеальним розкладом розряду й заряду.',
      rule: 'Розклад — динамічне програмування за годинами в межах потужності, діапазону SOC, ККД і зносу. Оптимум = розряд на експорт + розряд на споживання − собівартість сонця − заряд з мережі − знос.',
      lines: sumLines(terms, { label: 'Чистий оптимум', amount: optEffect }, fmt),
    },
    fact: {
      text: 'Скільки УЗЕ реально заробила за цей цикл.',
      rule: 'Σ чистого ефекту УЗЕ (розряд у споживання й мережу − заряд − знос) за години циклу.',
      lines: [{ op: '=', label: 'Факт', value: fmt(factEffect) }],
    },
    reserve: {
      text: 'Недовикористана можливість циклу — не збиток.',
      rule: 'Резерв = Оптимум − Факт.',
      lines: sumLines(
        [
          { label: 'Оптимум', amount: optEffect },
          { label: 'Факт', amount: -factEffect },
        ],
        { label: 'Резерв', amount: reserve },
        fmt,
      ),
    },
    captured: {
      text: 'Яку частку оптимуму цього циклу УЗЕ захопила.',
      rule: 'Захоплено = Факт ÷ Оптимум, округлено до десятих відсотка.',
      lines: [
        { label: 'Факт', value: fmt(factEffect) },
        { op: '÷', label: 'Оптимум', value: fmt(optEffect) },
        { op: '=', label: 'Захоплено', value: `${captured}%` },
      ],
    },
    captureText: `${captured}%`,
  }
}
