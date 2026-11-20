import type { EconomicsMonthlyTotals } from '../../api'
import type { Formula, FormulaLine } from '../formula'
import { residual, sumLines } from '../formula'
import type { Money } from '../currency'
import { formatKwh, formatMonthTitle, formatMwh, formatPercent } from '../monthly/format'
import { fundedCapexSteps, paybackLabel, type CapexStep, type PaybackModel } from '../payback'

// Formulas of the «Окупність» page: every figure's rule in words and the
// same rule with the period's own numbers. They are built from the very
// variables the page renders (the PaybackModel and the window totals), so
// the substituted numbers are what the page shows.

const monthsFmt = new Intl.NumberFormat('uk-UA', { maximumFractionDigits: 2 })
const factorFmt = new Intl.NumberFormat('uk-UA', { minimumFractionDigits: 2, maximumFractionDigits: 2 })

// monthTitleLower renders "травень 2035" (lowercase month + year).
export function monthTitleLower(monthKey: string): string {
  const title = formatMonthTitle(monthKey)
  return title.charAt(0).toLocaleLowerCase('uk-UA') + title.slice(1)
}

function fmtMonths(n: number): string {
  return `${monthsFmt.format(n)} міс.`
}

// dottedDate turns YYYY-MM-DD into DD.MM.YYYY.
function dottedDate(iso: string): string {
  return `${iso.slice(8, 10)}.${iso.slice(5, 7)}.${iso.slice(0, 4)}`
}

const EBITDA_DEFINITION =
  'EBITDA = доходи й економія СЕС і УЗЕ мінус заряд УЗЕ з мережі та від генератора. Операційні витрати й знос УЗЕ не віднімаються.'

export type PaybackFormulaInput = {
  model: PaybackModel
  // totals are the window totals of /economics/annual.
  totals: EconomicsMonthlyTotals
  money: Money
  // capexSteps are the dated CAPEX versions the page was given (already in
  // the page's currency).
  capexSteps: CapexStep[]
}

export type PaybackFormulas = {
  capex: Formula
  returned: Formula
  remaining: Formula
  covered: Formula
  prior: Formula
  operation: Formula
  avgMonthly: Formula
  roi: Formula
  pv: Formula
  tariff: Formula
  gridBill: Formula
  netEffect: Formula
  cumulativeChart: Formula
  effectChart: Formula
}

// avgMonthlyFact is the «Середньомісячний EBITDA (факт)» figure: NaN when
// no month is covered. The page shows it through the same function.
export function avgMonthlyFact(model: PaybackModel): number {
  return model.effectiveMonths > 0 ? model.allTimeEbitda / model.effectiveMonths : NaN
}

// ebitdaWindowLines writes the window's EBITDA as its parts: the revenue legs and the two charge legs of
// the window, closed with residuals so the lines add up to the EBITDA the
// server computed.
function ebitdaWindowLines(t: EconomicsMonthlyTotals, money: Money): FormulaLine[] {
  const fmt = money.format
  const n = (v: number | undefined) => (Number.isFinite(v) ? (v as number) : 0)
  const revenueTotal = n(t.revenue_total_uah)
  const legs = [n(t.revenue_pv_self_uah), n(t.revenue_pv_export_uah), n(t.revenue_ess_self_uah), n(t.revenue_ess_export_uah)]
  const otherRevenue = residual(revenueTotal, legs)
  const expenseTotal = n(t.expense_total_uah)
  const gridCharge = n(t.expense_grid_charge_uah)
  const genCharge = expenseTotal - gridCharge
  const terms = [
    { label: 'СЕС → споживання', amount: legs[0], detail: 'виробіток СЕС × ціна імпорту' },
    { label: 'СЕС → мережа', amount: legs[1], detail: 'експорт СЕС × ціна експорту' },
    { label: 'УЗЕ → споживання', amount: legs[2], detail: 'розряд УЗЕ × ціна імпорту' },
    { label: 'УЗЕ → мережа', amount: legs[3], detail: 'розряд УЗЕ × ціна експорту' },
  ]
  if (Math.abs(otherRevenue) >= 0.5) terms.push({ label: 'Інші доходи', amount: otherRevenue, detail: '' })
  terms.push({ label: 'Заряд УЗЕ з мережі', amount: -gridCharge, detail: 'заряд з мережі × ціна імпорту' })
  if (Math.abs(genCharge) >= 0.5) terms.push({ label: 'Заряд УЗЕ від генератора', amount: -genCharge, detail: '' })
  const lines = sumLines(
    terms.map((x) => ({ ...x, detail: x.detail || undefined })),
    { label: 'EBITDA за період', amount: t.ebitda_uah },
    fmt,
  )
  // The legs add up to revenue − expenses; whatever EBITDA differs by is
  // shown rather than hidden.
  const sum = revenueTotal - expenseTotal
  const gap = t.ebitda_uah - sum
  if (Math.abs(gap) >= 0.5) {
    lines.splice(lines.length - 1, 0, { op: gap < 0 ? '−' : '+', label: 'Інше', value: fmt(Math.abs(gap)) })
  }
  return lines
}

export function buildPaybackFormulas({ model, totals, money, capexSteps }: PaybackFormulaInput): PaybackFormulas {
  const fmt = money.format
  const {
    allTimeEbitda,
    prior,
    hasPrior,
    priorMonths,
    capexNow,
    capexAvg,
    capexWeighted,
    capexStages,
    effectiveMonths,
    totalMonthsWithData,
    seasonalWeight,
    monthlyPace,
    annualEbitda,
    seasonalFactors,
    remaining,
    coveredShare,
    paidOff,
    operationYears,
    avgAnnualRoi,
    lastFactMonthKey,
  } = model
  const windowMonths = model.monthsWithData.length
  const windowEbitda = allTimeEbitda - prior
  const lastMonth = lastFactMonthKey ? monthTitleLower(lastFactMonthKey) : 'останній місяць'

  // --- CAPEX ---
  const funded = fundedCapexSteps(capexSteps)
  const inForce = lastFactMonthKey ? funded.filter((s) => s.effectiveFrom <= `${lastFactMonthKey}-31`) : funded
  const shown = (inForce.length > 0 ? inForce : funded.slice(0, 1)).slice(-5)
  const capexLines: FormulaLine[] =
    funded.length > 0
      ? [
          ...shown.map((s) => ({ label: `Версія CAPEX з ${dottedDate(s.effectiveFrom)}`, value: fmt(s.capexUah) })),
          {
            op: '=' as const,
            label: `CAPEX на ${lastMonth}`,
            value: fmt(capexNow),
            detail: capexStages > 1 ? `остання версія, що діє; ${capexStages} етапи в графіку` : 'єдина версія, що діє',
          },
        ]
      : [{ op: '=' as const, label: 'CAPEX із «Параметрів тарифів»', value: fmt(capexNow), detail: 'у версіях тарифів CAPEX не заповнений' }]
  const capex: Formula = {
    text: 'Скільки всього вкладено в проєкт на останній місяць із даними.',
    rule: 'Береться версія CAPEX із тарифів, що діє в останньому місяці з даними. Кожна версія — уся сума вкладень, а не окремий етап, тому версії не додаються.',
    lines: capexLines,
    note:
      funded.length > 0
        ? 'Версія, що почалась посеред місяця, діє на весь цей місяць; місяці до першої версії беруть її значення.'
        : undefined,
  }

  // --- EBITDA to date (Повернуто / Чистий ефект) ---
  const revenueTotal = totals.revenue_total_uah
  const expenseTotal = totals.expense_total_uah
  const windowDetail = [
    `${windowMonths} міс. із даними`,
    Number.isFinite(revenueTotal) && Number.isFinite(expenseTotal) ? `${fmt(revenueTotal)} − ${fmt(expenseTotal)}` : '',
  ]
    .filter(Boolean)
    .join(' · ')
  const ebitdaToDate = (text: string): Formula => ({
    text,
    rule: 'Накопичений EBITDA з початку експлуатації = EBITDA до періоду + EBITDA місяців періоду. EBITDA місяця = дохід і економія СЕС та УЗЕ − заряд УЗЕ з мережі та від генератора.',
    lines: sumLines(
      [
        ...(hasPrior || prior !== 0
          ? [{ label: 'EBITDA до помісячних даних', amount: prior, detail: `${priorMonths} міс.` }]
          : []),
        { label: 'EBITDA за період', amount: windowEbitda, detail: windowDetail },
      ],
      { label: 'Накопичений EBITDA', amount: allTimeEbitda },
      fmt,
    ),
    note: 'Операційні витрати й знос УЗЕ не віднімаються.',
  })
  const returned = ebitdaToDate('Скільки інвестицій уже повернуто — накопичений EBITDA з початку експлуатації.')
  const netEffect = ebitdaToDate('Чистий ефект проєкту від початку експлуатації — той самий накопичений EBITDA.')

  // --- Залишилось повернути ---
  const remainingShown = paidOff ? 0 : remaining
  const remainingFormula: Formula = {
    text: 'Скільки ще потрібно повернути, щоб окупити вкладення.',
    rule: 'Залишилось = CAPEX − накопичений EBITDA; коли повернуто більше за CAPEX, залишку немає (0).',
    lines: [
      { label: 'CAPEX проєкту', value: fmt(capexNow) },
      { op: '−', label: 'Повернуто інвестицій', value: fmt(allTimeEbitda) },
      {
        op: '=',
        label: 'Залишилось повернути',
        value: fmt(remainingShown),
        detail: paidOff ? 'повернуто не менше за CAPEX — залишку немає' : undefined,
      },
    ],
  }

  // --- Окуплено ---
  const raw = capexNow > 0 ? allTimeEbitda / capexNow : 0
  const covered: Formula = {
    text: 'Яку частку вкладень уже повернуто.',
    rule: 'Окуплено = накопичений EBITDA ÷ CAPEX, у межах від 0 до 100%.',
    lines: [
      { label: 'Повернуто інвестицій', value: fmt(allTimeEbitda) },
      { op: '÷', label: 'CAPEX проєкту', value: fmt(capexNow) },
      { op: '=', label: 'Окуплено', value: formatPercent(coveredShare) },
    ],
    note:
      raw > 1
        ? `Фактично ${formatPercent(raw)}, але шкала закінчується на 100%.`
        : raw < 0
          ? `Фактично ${formatPercent(raw)}, але шкала починається з 0%.`
          : undefined,
  }

  // --- EBITDA до періоду ---
  const priorFormula: Formula = {
    text: 'EBITDA, накопичений до початку помісячних даних цієї сторінки.',
    rule: 'Сума EBITDA усіх місяців з даними до першого місяця вікна; вона входить у «Повернуто інвестицій» і «Чистий ефект».',
    lines: [
      {
        op: '=',
        label: 'EBITDA до помісячних даних',
        value: fmt(prior),
        detail: `за ${priorMonths} міс. із даними`,
      },
    ],
    note: 'Порахований на сервері за місяцями до вікна; тут показаний підсумком.',
  }

  // --- Період експлуатації ---
  const coverageShare = totalMonthsWithData > 0 ? effectiveMonths / totalMonthsWithData : NaN
  const operation: Formula = {
    text: 'Скільки триває експлуатація, рахуючи місяці з даними.',
    rule: 'Період = (місяці з даними у вікні + місяці з даними до вікна) ÷ 12.',
    lines: [
      {
        label: 'Місяців з даними',
        value: `${totalMonthsWithData} міс.`,
        detail: `${windowMonths} у вікні + ${priorMonths} до вікна`,
      },
      { op: '÷', label: 'Місяців у році', value: '12' },
      { op: '=', label: 'Період експлуатації', value: paybackLabel(operationYears) },
    ],
    note: Number.isFinite(coverageShare)
      ? `Покриття даними ${formatPercent(coverageShare)}: ${fmtMonths(effectiveMonths)} повних місяців телеметрії із ${totalMonthsWithData} (місяць з неповними годинами рахується частково).`
      : undefined,
  }

  // --- Середньомісячний EBITDA (факт) ---
  const avg = avgMonthlyFact(model)
  const avgMonthly: Formula = {
    text: 'Скільки EBITDA припадає на один повний місяць даних.',
    rule: 'Накопичений EBITDA ÷ місяці з даними, у яких кожен місяць важить за покриттям телеметрією (години з даними ÷ години місяця).',
    lines: [
      { label: 'Накопичений EBITDA', value: fmt(allTimeEbitda) },
      {
        op: '÷',
        label: 'Місяців з даними (за покриттям)',
        value: fmtMonths(effectiveMonths),
        detail: `${priorMonths} до вікна + ${monthsFmt.format(effectiveMonths - priorMonths)} у вікні`,
      },
      { op: '=', label: 'Середньомісячний EBITDA', value: Number.isFinite(avg) ? fmt(avg) : '—' },
    ],
  }

  // --- Середньорічний ROI (факт) ---
  const roiKnown = capexAvg > 0 && effectiveMonths > 0
  const roi: Formula = {
    text: 'Середня річна віддача вкладень за весь час експлуатації.',
    rule: 'ROI = (накопичений EBITDA ÷ середні вкладення) × (12 ÷ місяці з даними). Середні вкладення — CAPEX, що діяв у кожному місяці, зважений за покриттям.',
    lines: roiKnown
      ? [
          { label: 'Накопичений EBITDA', value: fmt(allTimeEbitda) },
          {
            op: '÷',
            label: 'Середні вкладення',
            value: fmt(capexAvg),
            detail: `${fmt(capexWeighted)} (Σ CAPEX × покриття) ÷ ${fmtMonths(effectiveMonths)}`,
          },
          {
            op: '×',
            label: 'Приведення до року',
            value: factorFmt.format(12 / effectiveMonths),
            detail: `12 ÷ ${fmtMonths(effectiveMonths)}`,
          },
          { op: '=', label: 'Середньорічний ROI', value: Number.isFinite(avgAnnualRoi) ? formatPercent(avgAnnualRoi) : '—' },
        ]
      : undefined,
    note: 'У «Портфелі» ROCE рахується інакше: EBITDA періоду ×12 ÷ місяці з даними ÷ CAPEX на кінець періоду.',
  }

  // --- Виробіток ---
  const pv: Formula = {
    text: 'Скільки електроенергії виробила СЕС.',
    rule: 'Сума виробітку СЕС за всі місяці з даними телеметрії у вікні.',
    lines: [{ op: '=', label: 'Виробіток СЕС', value: formatMwh(totals.pv_kwh), detail: `${windowMonths} міс. із даними` }],
  }

  // --- Тариф ---
  const priceText = `${money.formatPrice(totals.avg_import_price_uah_per_kwh)} ${money.priceUnit}`
  const tariff: Formula = {
    text: 'Середня ціна кіловат-години, купленої з мережі.',
    rule: 'Середній тариф = Σ (ціна години × імпорт години) ÷ імпорт з мережі: ціна, зважена на обсяг імпорту.',
    lines: [
      { label: 'Вартість імпорту', value: fmt(totals.import_cost_uah), detail: 'усе куплене з мережі за період' },
      { op: '÷', label: 'Імпорт з мережі', value: formatKwh(totals.grid_import_kwh) },
      { op: '=', label: 'Середній тариф (екв.)', value: priceText },
    ],
  }

  // --- Витрати на придбання електроенергії з мережі ---
  const chargeNote = Number.isFinite(totals.expense_grid_charge_uah)
    ? `У EBITDA з цієї суми віднімається лише заряд УЗЕ з мережі (${fmt(totals.expense_grid_charge_uah)} за період) і заряд від генератора; решта — споживання, яке СЕС і УЗЕ не покрили, до EBITDA не входить.`
    : 'У EBITDA з цієї суми віднімається лише заряд УЗЕ з мережі й від генератора.'
  const gridBill: Formula = {
    text: 'Скільки коштував увесь імпорт з мережі за період: споживання й заряд УЗЕ.',
    rule: 'Витрати = імпорт з мережі × середня ціна імпорту.',
    lines: [
      { label: 'Імпорт з мережі', value: formatKwh(totals.grid_import_kwh) },
      { op: '×', label: 'Середній тариф (екв.)', value: priceText },
      { op: '=', label: 'Витрати на придбання електроенергії з мережі', value: fmt(totals.import_cost_uah) },
    ],
    note: chargeNote,
  }

  // --- Накопичена окупність (графік, точка окупності) ---
  const factors = seasonalFactors.length > 0 ? seasonalFactors : [1]
  const factorRange = `${factorFmt.format(Math.min(...factors))} – ${factorFmt.format(Math.max(...factors))}`
  let cumulativeLines: FormulaLine[]
  if (paidOff) {
    cumulativeLines = [
      { label: 'Накопичений EBITDA', value: fmt(allTimeEbitda) },
      { op: '−', label: 'CAPEX проєкту', value: fmt(capexNow), detail: 'різниця невід’ємна — вкладення повернуто' },
      {
        op: '=',
        label: 'Точка окупності',
        value: model.paybackMonthKey ? monthTitleLower(model.paybackMonthKey) : '—',
        detail: 'місяць, коли накопичене вперше покрило CAPEX',
      },
    ]
  } else {
    cumulativeLines = [
      { label: 'Накопичений EBITDA', value: fmt(allTimeEbitda) },
      {
        op: '÷',
        label: 'Сезонна вага місяців',
        value: factorFmt.format(seasonalWeight),
        detail: `Σ коефіцієнт місяця × покриття, ${fmtMonths(effectiveMonths)} даних`,
      },
      { op: '=', label: 'Базовий темп EBITDA на місяць', value: fmt(monthlyPace), detail: `× 12 = ${fmt(annualEbitda)} на рік` },
      { label: 'Залишилось повернути', value: fmt(remainingShown) },
      {
        op: '=',
        label: 'Точка окупності (прогноз)',
        value: model.paybackMonthKey ? monthTitleLower(model.paybackMonthKey) : '—',
        detail: model.paybackMonthKey ? `через ${paybackLabel(model.paybackYears)} від початку експлуатації` : 'далі за межею прогнозу',
      },
    ]
  }
  const cumulativeChart: Formula = {
    text: 'Фактичний накопичений EBITDA й сезонний прогноз до повернення CAPEX.',
    rule: paidOff
      ? 'Точка окупності — перший місяць, у якому накопичений EBITDA дорівняв або перевищив CAPEX, що діяв тоді.'
      : 'Прогноз іде місяць за місяцем: EBITDA місяця = базовий темп × коефіцієнт календарного місяця; сума росте, доки не дійде до CAPEX, що діє в цьому місяці.',
    lines: cumulativeLines,
    note:
      !paidOff && annualEbitda > 0 && capexNow > 0
        ? `Коефіцієнти місяців: ${factorRange}. Без сезонності (лінійно) окупність була б через ${paybackLabel(capexNow / annualEbitda)}.`
        : undefined,
  }

  // --- Щомісячний економічний ефект (стовпці) ---
  const effectChart: Formula = {
    text: 'EBITDA за період: сині стовпці — факт, фіолетові — сезонний прогноз; лінія — накопичений EBITDA.',
    rule: 'EBITDA місяця = доходи й економія СЕС та УЗЕ (споживання й мережа) − заряд УЗЕ з мережі та від генератора. Мінусовий стовпець — місяць, у якому цей заряд перевищив доходи й економію.',
    lines: ebitdaWindowLines(totals, money),
    note: `Рядки — за весь період вікна (${windowMonths} міс.). Прогноз = базовий темп × коефіцієнт місяця. ${EBITDA_DEFINITION}`,
  }

  return {
    capex,
    returned,
    remaining: remainingFormula,
    covered,
    prior: priorFormula,
    operation,
    avgMonthly,
    roi,
    pv,
    tariff,
    gridBill,
    netEffect,
    cumulativeChart,
    effectChart,
  }
}
