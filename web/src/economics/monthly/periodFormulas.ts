// How the month and year figures of the economics page are computed:
// the rule in words and the same rule with the period's own numbers.
// Every builder reads the very values the page shows (the figures the
// cards are built from live here too), so a formula's «=» line is the
// string on screen. Pure functions: no React, no fetching.
import type { EconomicsMonthlyTotals } from '../../api'
import type { Money } from '../currency'
import { sumLines, times, type Formula, type FormulaLine } from '../formula'
import { formatDuration, fuelMethodLabel, rollupOffgridMinutes } from '../generatorText'
import { formatCycles, formatKwh, formatMwh, formatPercent, formatShare } from './format'
import { KWH_EPS, MWH_EPS, moneyEps, nonZero, priceText, withResidual } from './formulaKit'
import { reserveSplit, type PeriodScope } from './rollup'

// --- Shared figures -------------------------------------------------------

const signedPctFmt = new Intl.NumberFormat('uk-UA', {
  minimumFractionDigits: 1,
  maximumFractionDigits: 1,
  signDisplay: 'exceptZero',
})

export function formatSignedPct(fraction: number): string {
  return `${signedPctFmt.format(fraction * 100)}%`
}

export function formatSignedPp(fraction: number): string {
  return `${signedPctFmt.format(fraction * 100)} п.п.`
}

// ratio is the relative change (curr − prev) / prev, or null when the
// previous value can't serve as a denominator.
export function ratio(curr: number, prev: number | null): number | null {
  if (prev === null || !Number.isFinite(prev) || Math.abs(prev) < 1e-9) return null
  if (!Number.isFinite(curr)) return null
  return (curr - prev) / prev
}

// selfSufficiency: how much of the object's consumption was served by
// its own PV + battery instead of grid import.
export function selfSufficiency(t: EconomicsMonthlyTotals): number | null {
  if (t.load_kwh <= 0) return null
  return (t.pv_to_load_kwh + t.ess_to_load_kwh) / t.load_kwh
}

// pvSelfShare: how much of the PV yield stayed on site (consumed
// directly or stored) rather than being exported.
export function pvSelfShare(t: EconomicsMonthlyTotals): number | null {
  if (t.pv_kwh <= 0) return null
  return (t.pv_to_load_kwh + t.pv_to_ess_kwh) / t.pv_kwh
}

export type PeriodOpts = {
  // capexUah is the invested capital standing at the end of the period.
  capexUah?: number
  // annualizeMonths scales the period EBITDA to a year: 1 for a month,
  // months_with_data for a year.
  annualizeMonths?: number
}

// TopFigures are the numbers behind the top section's cards, computed
// once: the cards print them and the formulas substitute them.
export type TopFigures = ReturnType<typeof topFigures>

export function topFigures(t: EconomicsMonthlyTotals, opts: PeriodOpts = {}) {
  const baseline = t.baseline_cost_uah
  const effectShare = baseline > 0 ? t.effect_uah / baseline : NaN
  const baselineUnit = t.load_kwh > 0 ? baseline / t.load_kwh : NaN
  const actualUnit = t.load_kwh > 0 ? t.actual_cost_uah / t.load_kwh : NaN
  const pvEffect = t.effect_uah - t.ess_net_uah
  // What PV put into the battery, valued at the export price it gave up:
  // the remainder of the PV effect after its two revenue legs.
  const pvToEss = pvEffect - t.revenue_pv_self_uah - t.revenue_pv_export_uah
  const pvEffectShare = t.effect_uah > 0 ? pvEffect / t.effect_uah : NaN
  const essEffectShare = t.effect_uah > 0 ? t.ess_net_uah / t.effect_uah : NaN

  // ROCE: the period EBITDA scaled to a full year, over the capital at
  // work. A partial period annualises optimistically.
  const months = Math.max(1, opts.annualizeMonths ?? 1)
  const annualEbitda = t.ebitda_uah * (12 / months)
  const capex = opts.capexUah ?? NaN
  const hasCapex = Number.isFinite(capex) && capex > 0
  const roce = hasCapex ? annualEbitda / capex : NaN

  // Realised share of the confirmed potential: fact effect vs fact +
  // the two reserve levers (work-schedule shift + УЗЕ dispatch optimum).
  const split = reserveSplit(t)
  const reserve = split.total
  const potential = t.effect_uah + reserve
  const realizationShare =
    potential > 0 && t.effect_uah >= 0 ? Math.max(0, Math.min(1, t.effect_uah / potential)) : NaN

  const importPrice = t.grid_import_kwh > 0 ? t.import_cost_uah / t.grid_import_kwh : NaN
  const exportRevenue = t.revenue_pv_export_uah + t.revenue_ess_export_uah
  const exportPrice = t.grid_export_kwh > 0 ? exportRevenue / t.grid_export_kwh : NaN
  const selfSuff = selfSufficiency(t)
  const pvSelf = pvSelfShare(t)
  const covered = t.pv_to_load_kwh + t.ess_to_load_kwh
  const pvConsumed = t.pv_to_load_kwh + t.pv_to_ess_kwh

  return {
    baseline,
    effectShare,
    baselineUnit,
    actualUnit,
    pvEffect,
    pvToEss,
    pvEffectShare,
    essEffectShare,
    months,
    annualEbitda,
    capex,
    hasCapex,
    roce,
    split,
    reserve,
    potential,
    realizationShare,
    importPrice,
    exportRevenue,
    exportPrice,
    selfSuff,
    pvSelf,
    covered,
    pvConsumed,
  }
}

// deltaNote spells out a «до попереднього …» badge: the change against
// the previous period with both values. Undefined when the badge is hidden.
export function deltaNote(
  prevNote: string,
  curr: string,
  prev: string,
  delta: number | null,
  unit: 'pct' | 'pp' = 'pct',
): string | undefined {
  if (delta === null || !Number.isFinite(delta)) return undefined
  const head = `Зміна ${prevNote}`
  return unit === 'pp'
    ? `${head}: ${curr} − ${prev} = ${formatSignedPp(delta)}.`
    : `${head}: (${curr} − ${prev}) ÷ ${prev} = ${formatSignedPct(delta)}.`
}

// joinNotes glues the caveats that apply; undefined when none do.
export function joinNotes(...parts: Array<string | undefined | false>): string | undefined {
  const text = parts.filter(Boolean).join(' ')
  return text || undefined
}

// divide is the three lines of a quotient: numerator, ÷ denominator, = result.
export function divide(
  num: { label: string; value: string },
  den: { label: string; value: string },
  result: { label: string; value: string },
): FormulaLine[] {
  return [
    { label: num.label, value: num.value },
    { op: '÷', label: den.label, value: den.value },
    { op: '=', label: result.label, value: result.value },
  ]
}

// energySum writes an energy total as its flows (МВт·год as the cards
// show it, кВт·год for the tables), with a remainder line when the flows
// leave a visible gap.
export function energySum(
  parts: Array<{ label: string; kwh: number; detail?: string }>,
  total: { label: string; kwh: number },
  remainderLabel = 'Не розподілено по потоках',
  unit: 'mwh' | 'kwh' = 'mwh',
): FormulaLine[] {
  const terms = withResidual(
    nonZero(parts.map((p) => ({ label: p.label, amount: p.kwh, detail: p.detail }))),
    total.kwh,
    remainderLabel,
    unit === 'mwh' ? MWH_EPS : KWH_EPS,
  )
  return sumLines(terms, { label: total.label, amount: total.kwh }, unit === 'mwh' ? formatMwh : formatKwh)
}

// --- Top section ----------------------------------------------------------

export type PvPlanArg = { plannedKwh: number; daysCovered: number; daysExpected: number; wholeElevator?: boolean }

export type TopContext = {
  money: Money
  figures: TopFigures
  prior?: EconomicsMonthlyTotals | null
  prevNote: string
  pvPlan?: PvPlanArg | null
  scope: PeriodScope
}

// TopFormulas is one formula per figure of the top section.
export type TopFormulas = ReturnType<typeof topFormulas>

export function topFormulas(t: EconomicsMonthlyTotals, c: TopContext) {
  const m = c.money
  const f = c.figures
  const eps = moneyEps(m)
  const share = (v: number) => (Number.isFinite(v) ? formatPercent(v) : '—')
  const offgrid = (t.load_offgrid_kwh ?? 0) > 0

  // --- Economic cards ---
  const baseline: Formula = {
    text: "Усе споживання об'єкта, оцінене повною ціною імпорту кожної години: скільки коштувала б енергія, якби СЕС та УЗЕ не було.",
    rule: 'Σ за годинами: споживання × ціна імпорту години. Ціна імпорту = (РДН + розподіл + передача + надбавка + інші збори) × ПДВ.',
    lines: [
      { label: "Споживання об'єкта", value: formatMwh(t.load_kwh) },
      {
        op: '×',
        label: 'Повна ціна імпорту, зважена по годинах',
        value: priceText(m, f.baselineUnit),
        detail: 'базова вартість ÷ споживання',
      },
      { op: '=', label: 'Базова вартість', value: m.format(f.baseline) },
    ],
    note: offgrid
      ? `Години без мережі: ${formatMwh(t.load_offgrid_kwh ?? 0)} навантаження оцінено за ціною кВт·год генератора, а не мережі.`
      : undefined,
  }

  const baselineUnit: Formula = {
    text: 'Скільки коштувала б кожна спожита кВт·год без проєкту.',
    rule: 'Базова вартість ÷ споживання.',
    lines: divide(
      { label: 'Базова вартість', value: m.format(f.baseline) },
      { label: "Споживання об'єкта", value: formatKwh(t.load_kwh) },
      { label: 'Базова ціна', value: priceText(m, f.baselineUnit) },
    ),
  }

  const actual: Formula = {
    text: `Фактичні витрати на енергію: імпорт мінус дохід від експорту плюс знос УЗЕ${(t.gen_fuel_cost_uah ?? 0) > 0 ? ' і пальне генератора' : ''}.`,
    rule: 'Імпорт × ціна імпорту − експорт × ціна експорту + розряд УЗЕ × знос за кВт·год (+ пальне генератора), за кожну годину.',
    lines: sumLines(
      withResidual(
        nonZero([
          {
            label: 'Імпорт з мережі',
            amount: t.import_cost_uah,
            detail: times(formatKwh(t.grid_import_kwh), priceText(m, f.importPrice)),
          },
          {
            label: 'Експорт у мережу (дохід)',
            amount: -f.exportRevenue,
            detail: times(formatKwh(t.grid_export_kwh), priceText(m, f.exportPrice)),
          },
          { label: 'Знос УЗЕ', amount: t.ess_degradation_cost_uah },
          { label: 'Пальне генератора', amount: t.gen_fuel_cost_uah ?? 0 },
        ]),
        t.actual_cost_uah,
        'Інше (округлення, версії тарифів)',
        eps,
      ),
      { label: 'Фактична вартість', amount: t.actual_cost_uah },
      m.format,
    ),
    note: Number.isFinite(f.effectShare)
      ? `До базової: −(економічний ефект ÷ базова вартість) = −(${m.format(t.effect_uah)} ÷ ${m.format(f.baseline)}) = ${formatSignedPct(-f.effectShare)}.`
      : undefined,
  }

  const actualUnit: Formula = {
    text: 'Скільки обійшлася кожна спожита кВт·год з урахуванням СЕС та УЗЕ.',
    rule: 'Фактична вартість ÷ споживання.',
    lines: divide(
      { label: 'Фактична вартість', value: m.format(t.actual_cost_uah) },
      { label: "Споживання об'єкта", value: formatKwh(t.load_kwh) },
      { label: 'Фактична ціна', value: priceText(m, f.actualUnit) },
    ),
    note: Number.isFinite(f.baselineUnit) ? `Без проєкту: ${priceText(m, f.baselineUnit)}.` : undefined,
  }

  const effect: Formula = {
    text: 'Базова вартість мінус фактична: наскільки дешевше обійшлася енергія завдяки СЕС та УЗЕ.',
    rule: 'Базова вартість − Фактична вартість.',
    lines: sumLines(
      [
        { label: 'Базова вартість', amount: f.baseline },
        { label: 'Фактична вартість', amount: -t.actual_cost_uah },
      ],
      { label: 'Економічний ефект', amount: t.effect_uah },
      m.format,
    ),
    note: Number.isFinite(f.effectShare)
      ? `Від базової вартості: ${m.format(t.effect_uah)} ÷ ${m.format(f.baseline)} = ${formatSignedPct(f.effectShare)}.`
      : undefined,
  }

  const pv: Formula = {
    text: 'Частина ефекту без УЗЕ: власне споживання СЕС замість імпорту, експорт у мережу і заряд УЗЕ від СЕС.',
    rule: 'Економічний ефект − Ефект УЗЕ. Те саме — сума трьох складових СЕС.',
    lines: sumLines(
      [
        {
          label: 'СЕС → споживання (замість імпорту)',
          amount: t.revenue_pv_self_uah,
          detail: times(formatKwh(t.pv_to_load_kwh), 'ціна імпорту години'),
        },
        {
          label: 'СЕС → мережа (продаж)',
          amount: t.revenue_pv_export_uah,
          detail: times(formatKwh(t.pv_to_grid_kwh), 'ціна експорту години'),
        },
        {
          label: 'СЕС → УЗЕ (за ціною експорту, від якого відмовились)',
          amount: f.pvToEss,
          detail: `${formatKwh(t.pv_to_ess_kwh)} за ціною експорту години`,
        },
      ],
      { label: 'Ефект СЕС', amount: f.pvEffect },
      m.format,
    ),
    note: joinNotes(
      'Третя складова — залишок: ефект СЕС мінус дві перші. УЗЕ платить за цей заряд ту саму суму, тому ефекти СЕС і УЗЕ разом дають економічний ефект.',
      Number.isFinite(f.pvEffectShare) &&
        `Частка від загального ефекту: ${m.format(f.pvEffect)} ÷ ${m.format(t.effect_uah)} = ${share(f.pvEffectShare)}.`,
    ),
  }

  const gridCharge = t.expense_grid_charge_uah
  const genCharge = t.expense_gen_charge_uah ?? 0
  const ess: Formula = {
    text: 'Чистий ефект УЗЕ: розряд у споживання та мережу мінус вартість заряду і знос.',
    rule: 'Σ за годинами: УЗЕ → споживання × ціна імпорту + УЗЕ → мережа × ціна експорту − заряд з мережі × ціна імпорту − заряд від СЕС × ціна експорту − розряд × знос.',
    lines: sumLines(
      withResidual(
        nonZero([
          { label: 'УЗЕ → споживання', amount: t.revenue_ess_self_uah, detail: formatKwh(t.ess_to_load_kwh) },
          { label: 'УЗЕ → мережа', amount: t.revenue_ess_export_uah, detail: formatKwh(t.ess_to_grid_kwh) },
          { label: 'Заряд УЗЕ з мережі', amount: -gridCharge, detail: formatKwh(t.grid_to_ess_kwh) },
          { label: 'Заряд УЗЕ від СЕС (за ціною експорту)', amount: -f.pvToEss, detail: formatKwh(t.pv_to_ess_kwh) },
          { label: 'Знос УЗЕ', amount: -t.ess_degradation_cost_uah, detail: formatKwh(t.ess_discharged_kwh) },
          { label: 'Заряд УЗЕ від генератора', amount: -genCharge },
        ]),
        t.ess_net_uah,
        'Інше (округлення, версії тарифів)',
        eps,
      ),
      { label: 'Ефект УЗЕ', amount: t.ess_net_uah },
      m.format,
    ),
    note: Number.isFinite(f.essEffectShare)
      ? `Частка від загального ефекту: ${m.format(t.ess_net_uah)} ÷ ${m.format(t.effect_uah)} = ${share(f.essEffectShare)}.`
      : undefined,
  }

  const roceLines: FormulaLine[] = [
    { label: 'EBITDA за період', value: m.format(t.ebitda_uah) },
    {
      op: '×',
      label: 'Приведення до року',
      value: `12 ÷ ${f.months}`,
      detail: c.scope === 'month' ? 'один місяць' : `${f.months} міс. із даними`,
    },
    { op: '=', label: 'EBITDA в річному вимірі', value: m.format(f.annualEbitda) },
  ]
  if (f.hasCapex) {
    roceLines.push(
      { op: '÷', label: 'Інвестований капітал (CAPEX)', value: m.format(f.capex) },
      { op: '=', label: 'ROCE (річний)', value: formatPercent(f.roce) },
    )
  } else {
    roceLines.push({ op: '=', label: 'ROCE (річний)', value: formatPercent(f.roce), detail: 'CAPEX не вказано в тарифах' })
  }
  const roce: Formula = {
    text: 'Віддача на вкладений капітал: річна EBITDA проєкту до інвестицій.',
    rule: 'EBITDA періоду × 12 ÷ місяців із даними ÷ CAPEX на кінець періоду.',
    lines: roceLines,
    note:
      'Неповний період завищує оцінку: лінійне приведення до року не знає про сезон. Точна окупність — на сторінці «Окупність».',
  }

  const realization: Formula = {
    text: 'Яку частку підтвердженого потенціалу проєкт уже реалізував.',
    rule: 'Ефект ÷ (ефект + резерв графіка робіт + резерв розкладу УЗЕ); від 0 до 100%.',
    lines: [
      { label: 'Факт: економічний ефект', value: m.format(t.effect_uah) },
      {
        op: '+',
        label: 'Резерв графіка робіт',
        value: m.format(f.split.elevator),
        detail: 'перенос робіт на години СЕС',
      },
      {
        op: '+',
        label: 'Резерв розкладу УЗЕ',
        value: m.format(f.split.ess),
        detail: 'можливий максимум − факт',
      },
      { op: '=', label: 'Потенціал', value: m.format(f.potential) },
      {
        op: '=',
        label: 'Реалізація потенціалу',
        value: formatPercent(f.realizationShare),
        detail: `Факт ÷ Потенціал = ${m.format(t.effect_uah)} ÷ ${m.format(f.potential)}`,
      },
    ],
    note: 'Резерв — недовикористана можливість, не збиток. При від’ємному ефекті частка не рахується.',
  }

  // --- Energy cards ---
  const prior = c.prior ?? null
  const load: Formula = {
    text: "Уся енергія, яку спожив об'єкт: від СЕС, УЗЕ, мережі та генератора.",
    rule: 'Σ споживання за годинами = СЕС → споживання + УЗЕ → споживання + Мережа → споживання (+ Генератор → споживання).',
    lines: energySum(
      [
        { label: 'СЕС → споживання', kwh: t.pv_to_load_kwh },
        { label: 'УЗЕ → споживання', kwh: t.ess_to_load_kwh },
        { label: 'Мережа → споживання', kwh: t.grid_to_load_kwh },
        { label: 'Генератор → споживання', kwh: t.gen_to_load_kwh ?? 0 },
      ],
      { label: "Споживання об'єкта", kwh: t.load_kwh },
    ),
    note: prior
      ? deltaNote(c.prevNote, formatMwh(t.load_kwh), formatMwh(prior.load_kwh), ratio(t.load_kwh, prior.load_kwh))
      : undefined,
  }

  const planDelta = c.pvPlan && c.pvPlan.plannedKwh > 0 ? ratio(t.pv_kwh, c.pvPlan.plannedKwh) : null
  const pvGen: Formula = {
    text: 'Виробіток СЕС за лічильниками інверторів.',
    rule: 'Σ за годинами = СЕС → споживання + СЕС → УЗЕ + СЕС → мережа.',
    lines: energySum(
      [
        { label: 'СЕС → споживання', kwh: t.pv_to_load_kwh },
        { label: 'СЕС → УЗЕ', kwh: t.pv_to_ess_kwh },
        { label: 'СЕС → мережа', kwh: t.pv_to_grid_kwh },
      ],
      { label: 'Генерація СЕС', kwh: t.pv_kwh },
      'Не розподілено по напрямках',
    ),
    note: joinNotes(
      planDelta !== null && c.pvPlan
        ? `До плану: ${formatMwh(t.pv_kwh)} ÷ ${formatMwh(c.pvPlan.plannedKwh)} − 1 = ${formatSignedPct(planDelta)}.`
        : prior
          ? deltaNote(c.prevNote, formatMwh(t.pv_kwh), formatMwh(prior.pv_kwh), ratio(t.pv_kwh, prior.pv_kwh))
          : undefined,
    ),
  }

  const importFormula: Formula = {
    text: 'Скільки енергії куплено з мережі: на споживання і на заряд УЗЕ.',
    rule: 'Σ за годинами = Мережа → споживання + Мережа → УЗЕ.',
    lines: energySum(
      [
        { label: 'Мережа → споживання', kwh: t.grid_to_load_kwh },
        { label: 'Мережа → УЗЕ', kwh: t.grid_to_ess_kwh },
      ],
      { label: 'Імпорт з мережі', kwh: t.grid_import_kwh },
    ),
    note: prior
      ? deltaNote(
          c.prevNote,
          formatMwh(t.grid_import_kwh),
          formatMwh(prior.grid_import_kwh),
          ratio(t.grid_import_kwh, prior.grid_import_kwh),
        )
      : undefined,
  }

  const importPrice: Formula = {
    text: 'Середня ціна, за якою куплено кожну імпортовану кВт·год.',
    rule: 'Вартість усього імпорту ÷ імпорт. Вартість = Σ ціна години × кВт·год години; ціна = (РДН + розподіл + передача + надбавка + збори) × ПДВ.',
    lines: divide(
      { label: 'Вартість імпорту', value: m.format(t.import_cost_uah) },
      { label: 'Імпорт з мережі', value: formatKwh(t.grid_import_kwh) },
      { label: 'Середня ціна імпорту', value: priceText(m, f.importPrice) },
    ),
    note: 'У вартості й обсязі — і заряд УЗЕ з мережі.',
  }

  const exportFormula: Formula = {
    text: 'Скільки енергії віддано в мережу: від СЕС і від УЗЕ.',
    rule: 'Σ за годинами = СЕС → мережа + УЗЕ → мережа.',
    lines: energySum(
      [
        { label: 'СЕС → мережа', kwh: t.pv_to_grid_kwh },
        { label: 'УЗЕ → мережа', kwh: t.ess_to_grid_kwh },
      ],
      { label: 'Експорт у мережу', kwh: t.grid_export_kwh },
    ),
    note: prior
      ? deltaNote(
          c.prevNote,
          formatMwh(t.grid_export_kwh),
          formatMwh(prior.grid_export_kwh),
          ratio(t.grid_export_kwh, prior.grid_export_kwh),
        )
      : undefined,
  }

  const exportRevenue: Formula = {
    text: 'Виручка від продажу енергії в мережу.',
    rule: 'Σ за годинами: кВт·год × ціна експорту години. Ціна експорту = РДН × (1 − знижка) × ПДВ.',
    lines: sumLines(
      [
        {
          label: 'СЕС → мережа',
          amount: t.revenue_pv_export_uah,
          detail: formatKwh(t.pv_to_grid_kwh),
        },
        {
          label: 'УЗЕ → мережа',
          amount: t.revenue_ess_export_uah,
          detail: formatKwh(t.ess_to_grid_kwh),
        },
      ],
      { label: 'Дохід від експорту', amount: f.exportRevenue },
      m.format,
    ),
  }

  const exportPrice: Formula = {
    text: 'Середня ціна, за якою продано кожну експортовану кВт·год.',
    rule: 'Дохід від експорту ÷ експорт.',
    lines: divide(
      { label: 'Дохід від експорту', value: m.format(f.exportRevenue) },
      { label: 'Експорт у мережу', value: formatKwh(t.grid_export_kwh) },
      { label: 'Середня ціна експорту', value: priceText(m, f.exportPrice) },
    ),
  }

  const priorSelfSuff = prior ? selfSufficiency(prior) : null
  const selfSuffDelta = f.selfSuff !== null && priorSelfSuff !== null ? f.selfSuff - priorSelfSuff : null
  const selfSuffFormula: Formula = {
    text: "Частка споживання об'єкта, покрита власними СЕС та УЗЕ замість імпорту.",
    rule: '(СЕС → споживання + УЗЕ → споживання) ÷ споживання.',
    lines: [
      { label: 'СЕС → споживання', value: formatMwh(t.pv_to_load_kwh) },
      { op: '+', label: 'УЗЕ → споживання', value: formatMwh(t.ess_to_load_kwh) },
      { op: '=', label: 'Покрито власними джерелами', value: formatMwh(f.covered) },
      { op: '÷', label: "Споживання об'єкта", value: formatMwh(t.load_kwh) },
      { op: '=', label: 'Самозабезпечення', value: f.selfSuff === null ? '—' : formatPercent(f.selfSuff) },
    ],
    note:
      prior && priorSelfSuff !== null
        ? deltaNote(c.prevNote, formatPercent(f.selfSuff ?? NaN), formatPercent(priorSelfSuff), selfSuffDelta, 'pp')
        : undefined,
  }

  const priorPvSelf = prior ? pvSelfShare(prior) : null
  const pvSelfDelta = f.pvSelf !== null && priorPvSelf !== null ? f.pvSelf - priorPvSelf : null
  const pvSelfFormula: Formula = {
    text: 'Частка виробітку СЕС, використана на об’єкті (споживання + заряд УЗЕ), а не експортована.',
    rule: '(СЕС → споживання + СЕС → УЗЕ) ÷ генерація СЕС.',
    lines: [
      { label: 'СЕС → споживання', value: formatMwh(t.pv_to_load_kwh) },
      { op: '+', label: 'СЕС → УЗЕ', value: formatMwh(t.pv_to_ess_kwh) },
      { op: '=', label: 'Використано на об’єкті', value: formatMwh(f.pvConsumed) },
      { op: '÷', label: 'Генерація СЕС', value: formatMwh(t.pv_kwh) },
      { op: '=', label: 'Самоспоживання СЕС', value: f.pvSelf === null ? '—' : formatPercent(f.pvSelf) },
    ],
    note:
      prior && priorPvSelf !== null
        ? deltaNote(c.prevNote, formatPercent(f.pvSelf ?? NaN), formatPercent(priorPvSelf), pvSelfDelta, 'pp')
        : undefined,
  }

  return {
    baseline,
    baselineUnit,
    actual,
    actualUnit,
    effect,
    pv,
    ess,
    roce,
    realization,
    load,
    pvGen,
    import: importFormula,
    importPrice,
    export: exportFormula,
    exportRevenue,
    exportPrice,
    selfSuff: selfSuffFormula,
    pvSelf: pvSelfFormula,
  }
}

// --- Generator section ----------------------------------------------------

const litresFmt = new Intl.NumberFormat('uk-UA', { maximumFractionDigits: 0 })
const rateFmt = new Intl.NumberFormat('uk-UA', { minimumFractionDigits: 2, maximumFractionDigits: 3 })

export type GeneratorFormulas = ReturnType<typeof generatorFormulas>

export function generatorFormulas(t: EconomicsMonthlyTotals, m: Money) {
  const genKwh = t.gen_kwh ?? 0
  const litres = t.gen_fuel_l ?? 0
  const fuel = t.gen_fuel_cost_uah ?? 0
  const method = t.gen_fuel_method
  const runHours = (t.minutes_generator ?? 0) / 60

  const energy: Formula = {
    text: 'Енергія генератора за його лічильником (контролер Encombi).',
    rule: 'Σ за годинами = Генератор → споживання + Генератор → УЗЕ.',
    lines: energySum(
      [
        { label: 'Генератор → споживання', kwh: t.gen_to_load_kwh ?? 0 },
        { label: 'Генератор → УЗЕ', kwh: t.gen_to_ess_kwh ?? 0 },
      ],
      { label: 'Генератор', kwh: genKwh },
      'Не розподілено (лічильник без розподілу)',
    ),
  }

  const fuelLines: FormulaLine[] = []
  const perLitre = litres > 0 ? fuel / litres : NaN
  if (litres > 0) {
    if (method === 'per_kwh' && genKwh > 0) {
      fuelLines.push(
        { label: 'Енергія генератора', value: formatKwh(genKwh) },
        { op: '×', label: 'Витрата, л/кВт·год', value: rateFmt.format(litres / genKwh) },
      )
    } else if (method === 'per_hour' && runHours > 0) {
      fuelLines.push(
        { label: 'Час роботи генератора', value: formatDuration(t.minutes_generator ?? 0) },
        { op: '×', label: 'Витрата, л/год', value: rateFmt.format(litres / runHours) },
      )
    }
    fuelLines.push(
      {
        op: fuelLines.length ? '=' : undefined,
        label: 'Пальне, літрів',
        value: `${litresFmt.format(litres)} л`,
      },
      {
        op: '×',
        label: 'Ціна літра з ПДВ (середня)',
        value: `${m.formatPriceAllowZero(perLitre)} ${m.unit}/л`,
      },
    )
  }
  fuelLines.push({ op: litres > 0 ? '=' : undefined, label: 'Пальне', value: m.format(fuel) })

  const methodLabel = fuelMethodLabel(method)
  const fuelFormula: Formula = {
    text: 'Вартість пального генератора за тарифом кожної доби.',
    rule:
      method === 'per_hour'
        ? 'Літри = години роботи × витрата л/год; вартість = літри × ціна літра × ПДВ.'
        : method === 'per_kwh'
          ? 'Літри = енергія генератора × витрата л/кВт·год; вартість = літри × ціна літра × ПДВ.'
          : 'Літри за способом кожної доби (за кВт·год або за годину роботи); вартість = літри × ціна літра × ПДВ.',
    lines: fuelLines,
    note: methodLabel ? `Спосіб розрахунку: ${methodLabel}.` : undefined,
  }

  const offgridMinutes = rollupOffgridMinutes(t)
  const offgrid: Formula = {
    text: 'Скільки часу об’єкт жив без мережі.',
    rule: 'Хвилини роботи генератора + хвилини острівного режиму + хвилини без живлення.',
    lines: [
      { label: 'Генератор', value: formatDuration(t.minutes_generator ?? 0) },
      { op: '+', label: 'Острівний режим', value: formatDuration(t.minutes_island ?? 0) },
      { op: '+', label: 'Без живлення', value: formatDuration(t.minutes_blackout ?? 0) },
      { op: '=', label: 'Без мережі', value: formatDuration(offgridMinutes) },
    ],
  }

  const offgridEffect: Formula = {
    text: 'Ефект годин, коли об’єкт працював без мережі: скільки пального заощадили СЕС і УЗЕ проти роботи лише генератора, мінус пальне на заряд УЗЕ.',
    rule: 'Σ за годинами, де були хвилини без мережі: ефект години = (ціна кВт·год генератора × навантаження, покрите СЕС і УЗЕ) − пальне на заряд УЗЕ від генератора.',
    lines: [
      {
        op: '=',
        label: 'Ефект годин без мережі',
        value: m.format(t.effect_offgrid_uah ?? 0),
        detail: `навантаження без мережі ${formatKwh(t.load_offgrid_kwh ?? 0)}`,
      },
    ],
    note: joinNotes(
      `За період: пального ${m.format(fuel)}${(t.expense_gen_charge_uah ?? 0) > 0 ? `, з них на заряд УЗЕ ${m.format(t.expense_gen_charge_uah ?? 0)}` : ''}.`,
      'Годинні складові (ціна кВт·год генератора на кожну годину) в підсумках періоду не зберігаються, тому розклад — лише словами.',
    ),
  }

  return { energy, fuel: fuelFormula, offgrid, offgridEffect }
}

// --- Overview row ---------------------------------------------------------

// coverageFormula explains the shares of the «Структура покриття
// споживання» donut: each source's slice of the served load.
export function coverageFormula(t: EconomicsMonthlyTotals): Formula {
  const total = t.pv_to_load_kwh + t.ess_to_load_kwh + t.grid_to_load_kwh
  const line = (op: FormulaLine['op'], label: string, kwh: number): FormulaLine => ({
    op,
    label,
    value: formatMwh(kwh),
    detail: `частка ${formatShare(kwh, total)}`,
  })
  return {
    text: 'Хто покрив споживання об’єкта: СЕС, УЗЕ чи мережа.',
    rule: 'Частка джерела = його кВт·год до споживання ÷ сума трьох джерел.',
    lines: [
      line(undefined, 'СЕС → споживання', t.pv_to_load_kwh),
      line('+', 'УЗЕ → споживання', t.ess_to_load_kwh),
      line('+', 'Імпорт з мережі → споживання', t.grid_to_load_kwh),
      { op: '=', label: 'Усього покрито', value: formatMwh(total) },
    ],
    note: 'Імпорт, спрямований на заряд УЗЕ, сюди не входить — він потрапить у споживання як «УЗЕ → споживання».',
  }
}

export type ProfileSeries = {
  loadKwh: number
  pvKwh: number
  gridImportKwh: number
  essDischargeKwh: number
}

// profileFormula explains the consumption/generation chart: what a point
// is, and the period sum of each of its four lines.
export function profileFormula(
  points: ProfileSeries[],
  unitWord: 'добу' | 'місяць',
): Formula {
  const sum = (pick: (p: ProfileSeries) => number) => points.reduce((acc, p) => acc + pick(p), 0)
  return {
    text: `Кожна точка графіка — енергія за ${unitWord}, МВт·год.`,
    rule: `Чотири лінії: споживання об’єкта, генерація СЕС, імпорт з мережі та розряд УЗЕ — за кожен ${unitWord === 'добу' ? 'день' : 'місяць'} періоду.`,
    lines: [
      { label: 'Споживання — сума точок', value: formatMwh(sum((p) => p.loadKwh)) },
      { label: 'Генерація СЕС — сума точок', value: formatMwh(sum((p) => p.pvKwh)) },
      { label: 'Імпорт з мережі — сума точок', value: formatMwh(sum((p) => p.gridImportKwh)) },
      { label: 'Розряд УЗЕ — сума точок', value: formatMwh(sum((p) => p.essDischargeKwh)) },
    ],
    note: `Точок: ${points.length}. Суми збігаються з відповідними картками «Енергетичний баланс».`,
  }
}

// essDischargeFormula is the «Розряд УЗЕ» figure — the same in the
// «Робота УЗЕ» tile and in the finance mini-card.
export function essDischargeFormula(t: EconomicsMonthlyTotals, scope: PeriodScope): Formula {
  const days = Math.max(1, t.days_with_data)
  return {
    text: 'Скільки енергії УЗЕ віддала за період.',
    rule: 'Σ розряду за годинами = УЗЕ → споживання + УЗЕ → мережа.',
    lines: [
      ...energySum(
        [
          { label: 'УЗЕ → споживання', kwh: t.ess_to_load_kwh },
          { label: 'УЗЕ → мережа', kwh: t.ess_to_grid_kwh },
        ],
        { label: 'Розряд УЗЕ', kwh: t.ess_discharged_kwh },
        'Не розподілено по напрямках',
      ),
      { op: '÷', label: 'Діб із даними', value: String(days) },
      {
        op: '=',
        label: 'Середньо на добу',
        value: `${(t.ess_discharged_kwh / 1000 / days).toLocaleString('uk-UA', { minimumFractionDigits: 1, maximumFractionDigits: 1 })} МВт·год`,
      },
    ],
    note: scope === 'month' ? undefined : 'Діб із даними — за весь період.',
  }
}

export function uzeWorkFormulas(t: EconomicsMonthlyTotals, scope: PeriodScope) {
  const days = Math.max(1, t.days_with_data)
  const effectiveCapacity = t.equivalent_cycles > 0 ? t.ess_discharged_kwh / t.equivalent_cycles : NaN

  const cycles: Formula = {
    text: 'Скільки повних циклів «розряд на всю ємність» зробила батарея.',
    rule: 'Σ за добами: розряд доби ÷ ємність УЗЕ тієї доби (так заміна чи розширення батареї не спотворює цифру).',
    lines: [
      { label: 'Розряд УЗЕ за період', value: formatMwh(t.ess_discharged_kwh) },
      {
        op: '÷',
        label: 'Ефективна ємність (розряд ÷ цикли)',
        value: formatKwh(effectiveCapacity),
        detail: 'середня за добами',
      },
      { op: '=', label: 'Еквівалентні цикли', value: formatCycles(t.equivalent_cycles) },
      { op: '÷', label: 'Діб із даними', value: String(days) },
      { op: '=', label: 'Циклів на добу', value: formatCycles(t.equivalent_cycles / days) },
    ],
    note:
      scope === 'month'
        ? undefined
        : 'Цикли підсумовано по добах з різною ємністю УЗЕ: це не знос однієї батареї.',
  }

  const chargedTerms = nonZero([
    { label: 'СЕС → УЗЕ', amount: t.pv_to_ess_kwh },
    { label: 'Мережа → УЗЕ', amount: t.grid_to_ess_kwh },
    { label: 'Генератор → УЗЕ', amount: t.gen_to_ess_kwh ?? 0 },
  ])
  const charge: Formula = {
    text: 'Скільки енергії УЗЕ прийняла за період.',
    rule: 'Σ заряду за годинами = СЕС → УЗЕ + Мережа → УЗЕ (+ Генератор → УЗЕ).',
    lines: sumLines(
      withResidual(chargedTerms, t.ess_charged_kwh, 'Не розподілено по джерелах', MWH_EPS),
      { label: 'Заряд УЗЕ', amount: t.ess_charged_kwh },
      formatMwh,
    ),
  }

  const rte: Formula = {
    text: 'Відношення енергії, яку УЗЕ віддала, до тієї, що прийняла.',
    rule: 'Розряд УЗЕ ÷ заряд УЗЕ за період.',
    lines: divide(
      { label: 'Розряд УЗЕ', value: formatMwh(t.ess_discharged_kwh) },
      { label: 'Заряд УЗЕ', value: formatMwh(t.ess_charged_kwh) },
      {
        label: 'Ефективність (RTE)',
        value: formatPercent(t.ess_charged_kwh > 0 ? t.ess_discharged_kwh / t.ess_charged_kwh : NaN),
      },
    ),
    note: 'Це відношення потоків за період, а не справжній ККД циклу: не враховує зміну заряду батареї (SOC) між початком і кінцем періоду.',
  }

  return { cycles, discharge: essDischargeFormula(t, scope), charge, rte }
}
