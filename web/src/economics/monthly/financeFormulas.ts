// Formulas of the finance breakdown, the energy balance and the УЗЕ
// marginality heatmap of the month and year views. As in periodFormulas,
// the figures the blocks print are computed here, so the formulas
// substitute exactly what is on screen.
import type { EconomicsMonthlyMarginHour, EconomicsMonthlyTotals } from '../../api'
import type { Money } from '../currency'
import { sumLines, type Formula } from '../formula'
import { formatCycles, formatDayLabel, formatKwh, formatMwh, formatPercent, formatShare } from './format'
import { MWH_EPS, moneyEps, priceText, ratioOf, withResidual } from './formulaKit'
import { divide, energySum, essDischargeFormula, joinNotes } from './periodFormulas'
import { HEATMAP_METRIC_TIP, type PeriodScope } from './rollup'

// --- Finance breakdown ----------------------------------------------------

export type FinanceLeg = { key: string; label: string; amount: number }

// financeFigures are the amounts of «Фінансова розкладка»: the four
// revenue legs, the two charge expenses (the ones EBITDA subtracts) and
// the battery wear, which EBITDA leaves out.
export function financeFigures(t: EconomicsMonthlyTotals) {
  const genCharge = t.expense_gen_charge_uah ?? 0
  const revenueLegs: FinanceLeg[] = [
    { key: 'pv_self', label: 'СЕС → споживання', amount: t.revenue_pv_self_uah },
    { key: 'pv_export', label: 'СЕС → мережа', amount: t.revenue_pv_export_uah },
    { key: 'ess_self', label: 'УЗЕ → споживання', amount: t.revenue_ess_self_uah },
    { key: 'ess_export', label: 'УЗЕ → мережа', amount: t.revenue_ess_export_uah },
  ]
  const expenseLegs: FinanceLeg[] = [
    { key: 'grid_charge', label: 'Заряд УЗЕ із мережі', amount: t.expense_grid_charge_uah },
    ...(genCharge > 0 ? [{ key: 'gen_charge', label: 'Заряд УЗЕ від генератора', amount: genCharge }] : []),
  ]
  const expenseTotal = expenseLegs.reduce((acc, l) => acc + l.amount, 0)
  const wear = Math.max(t.ess_degradation_cost_uah, 0)
  const savingShare = t.baseline_cost_uah > 0 ? t.ebitda_uah / t.baseline_cost_uah : 0
  return { revenueLegs, expenseLegs, expenseTotal, wear, savingShare }
}

export type FinanceFigures = ReturnType<typeof financeFigures>

// legFormula writes one revenue/expense leg: its energy times the mean
// price it was valued at.
function legFormula(
  m: Money,
  o: {
    text: string
    rule: string
    energyLabel: string
    kwh: number
    label: string
    amount: number
    shareOf?: { label: string; total: number }
  },
): Formula {
  return {
    text: o.text,
    rule: o.rule,
    lines: [
      { label: o.energyLabel, value: formatKwh(o.kwh) },
      { op: '×', label: 'Середня ціна', value: priceText(m, ratioOf(o.amount, o.kwh)), detail: 'сума ÷ кВт·год' },
      { op: '=', label: o.label, value: m.format(o.amount) },
    ],
    note: o.shareOf
      ? `Частка в «${o.shareOf.label}»: ${m.format(o.amount)} ÷ ${m.format(o.shareOf.total)} = ${formatShare(o.amount, o.shareOf.total)}.`
      : undefined,
  }
}

export function financeFormulas(t: EconomicsMonthlyTotals, m: Money, scope: PeriodScope) {
  const fig = financeFigures(t)
  const eps = moneyEps(m)
  const rev = { label: 'Дохід / економія', total: t.revenue_total_uah }
  const exp = { label: 'Витрати', total: fig.expenseTotal }

  const legs: Record<string, Formula> = {
    pv_self: legFormula(m, {
      text: 'Скільки коштував би імпорт того, що замість нього спожила власна СЕС.',
      rule: 'Σ за годинами: СЕС → споживання (кВт·год) × ціна імпорту цієї години.',
      energyLabel: 'СЕС → споживання',
      kwh: t.pv_to_load_kwh,
      label: 'СЕС → споживання',
      amount: t.revenue_pv_self_uah,
      shareOf: rev,
    }),
    pv_export: legFormula(m, {
      text: 'Виручка від продажу надлишку СЕС у мережу.',
      rule: 'Σ за годинами: СЕС → мережа (кВт·год) × ціна експорту години (РДН × (1 − знижка) × ПДВ).',
      energyLabel: 'СЕС → мережа',
      kwh: t.pv_to_grid_kwh,
      label: 'СЕС → мережа',
      amount: t.revenue_pv_export_uah,
      shareOf: rev,
    }),
    ess_self: legFormula(m, {
      text: 'Скільки коштував би імпорт того, що замість нього віддала УЗЕ.',
      rule: 'Σ за годинами: УЗЕ → споживання (кВт·год) × ціна імпорту цієї години.',
      energyLabel: 'УЗЕ → споживання',
      kwh: t.ess_to_load_kwh,
      label: 'УЗЕ → споживання',
      amount: t.revenue_ess_self_uah,
      shareOf: rev,
    }),
    ess_export: legFormula(m, {
      text: 'Виручка від продажу енергії з УЗЕ в мережу.',
      rule: 'Σ за годинами: УЗЕ → мережа (кВт·год) × ціна експорту години.',
      energyLabel: 'УЗЕ → мережа',
      kwh: t.ess_to_grid_kwh,
      label: 'УЗЕ → мережа',
      amount: t.revenue_ess_export_uah,
      shareOf: rev,
    }),
    grid_charge: legFormula(m, {
      text: 'Скільки заплачено мережі за енергію, якою заряджали УЗЕ.',
      rule: 'Σ за годинами: Мережа → УЗЕ (кВт·год) × ціна імпорту цієї години.',
      energyLabel: 'Мережа → УЗЕ',
      kwh: t.grid_to_ess_kwh,
      label: 'Заряд УЗЕ із мережі',
      amount: t.expense_grid_charge_uah,
      shareOf: exp,
    }),
    gen_charge: legFormula(m, {
      text: 'Скільки коштувало пальне, що пішло на заряд УЗЕ від генератора.',
      rule: 'Σ за годинами: Генератор → УЗЕ (кВт·год) × ціна кВт·год генератора (пальне).',
      energyLabel: 'Генератор → УЗЕ',
      kwh: t.gen_to_ess_kwh ?? 0,
      label: 'Заряд УЗЕ від генератора',
      amount: t.expense_gen_charge_uah ?? 0,
      shareOf: exp,
    }),
  }

  const revenue: Formula = {
    text: 'Усе, що проєкт приніс: власне споживання СЕС та УЗЕ замість імпорту плюс продаж у мережу.',
    rule: 'Сума чотирьох доходів: СЕС → споживання + СЕС → мережа + УЗЕ → споживання + УЗЕ → мережа.',
    lines: sumLines(
      withResidual(
        fig.revenueLegs.map((l) => ({ label: l.label, amount: l.amount })),
        t.revenue_total_uah,
        'Інше (округлення)',
        eps,
      ),
      { label: 'Дохід / економія', amount: t.revenue_total_uah },
      m.format,
    ),
  }

  const expenses: Formula = {
    text: 'Скільки коштував заряд УЗЕ: із мережі й, якщо був, від генератора. Саме це віднімає EBITDA.',
    rule: 'Заряд УЗЕ з мережі × ціна імпорту години + заряд УЗЕ від генератора × ціна кВт·год генератора.',
    lines: sumLines(
      fig.expenseLegs.map((l) => ({ label: l.label, amount: l.amount })),
      { label: 'Витрати', amount: fig.expenseTotal },
      m.format,
    ),
    note: 'Знос УЗЕ сюди не входить: EBITDA його не віднімає, він враховується лише в економічному ефекті.',
  }

  const ebitda: Formula = {
    text: 'Операційний результат проєкту: доходи мінус витрати на заряд УЗЕ. Без витрат на утримання й без зносу.',
    rule: 'Дохід / економія − Витрати.',
    lines: sumLines(
      withResidual(
        [
          { label: 'Дохід / економія', amount: t.revenue_total_uah },
          ...fig.expenseLegs.map((l) => ({ label: l.label, amount: -l.amount })),
        ],
        t.ebitda_uah,
        'Інше (округлення)',
        eps,
      ),
      { label: 'EBITDA', amount: t.ebitda_uah },
      m.format,
    ),
    note:
      t.baseline_cost_uah > 0
        ? `Від бази: ${m.format(t.ebitda_uah)} ÷ ${m.format(t.baseline_cost_uah)} (базова вартість) = ${formatPercent(fig.savingShare)}.`
        : undefined,
  }

  const dayFormula = (
    kind: 'best' | 'worst',
    day: { date: string; effect_uah: number },
  ): Formula => ({
    text: kind === 'best' ? 'День періоду з найбільшим економічним ефектом.' : 'День періоду з найменшим економічним ефектом.',
    rule: 'Серед усіх діб періоду береться доба з ' + (kind === 'best' ? 'найбільшим' : 'найменшим') + ' ефектом = базова вартість − фактична вартість доби.',
    lines: [
      { label: 'Доба', value: day.date ? formatDayLabel(day.date) : '—' },
      { op: '=', label: 'Економічний ефект доби', value: m.format(day.effect_uah) },
    ],
    note: 'Доби ранжуються за економічним ефектом, а не за EBITDA: доба з найбільшим EBITDA може бути іншою.',
  })

  const wear: Formula = {
    text: 'Скільки «з’їдає» ресурс батареї кожна віддана кіловат-година.',
    rule: 'Розряд УЗЕ × знос за кВт·год за тарифом.',
    lines: [
      { label: 'Розряд УЗЕ', value: formatKwh(t.ess_discharged_kwh) },
      { op: '×', label: 'Знос за кВт·год', value: priceText(m, ratioOf(fig.wear, t.ess_discharged_kwh)), detail: 'сума ÷ розряд' },
      { op: '=', label: 'Знос УЗЕ', value: m.format(fig.wear) },
    ],
    note: 'Не входить в EBITDA, але віднімається в економічному ефекті (через фактичну вартість).',
  }

  return {
    legs,
    revenue,
    expenses,
    ebitda,
    bestDay: dayFormula('best', t.best_day),
    minDay: dayFormula('worst', t.min_effect_day),
    wear,
    discharge: essDischargeFormula(t, scope),
  }
}

// --- Energy balance -------------------------------------------------------

// balanceFigures are the totals of «Енергетичний баланс»: sources and
// directions of the boundary, and the sum of the seven detailed flows.
export function balanceFigures(t: EconomicsMonthlyTotals) {
  // Sources entering the object boundary.
  const sourcesTotal = t.pv_kwh + t.grid_import_kwh
  // Directions leaving it. Consumption and export are sums of their
  // flows (never a residual); ESS is the net amount left in storage.
  const consumption = t.pv_to_load_kwh + t.ess_to_load_kwh + t.grid_to_load_kwh
  const exportTotal = t.pv_to_grid_kwh + t.ess_to_grid_kwh
  const essNet = t.ess_charged_kwh - t.ess_discharged_kwh
  const checkTotal = consumption + exportTotal + essNet
  // Taking the remainder of the total purchase beats pricing the load
  // leg at the blended average: charging is deliberately shifted into
  // cheap hours, so the average overstates what the load actually cost.
  const gridToLoadCost = t.import_cost_uah - t.expense_grid_charge_uah
  const flowTotalKwh =
    t.pv_to_load_kwh +
    t.pv_to_grid_kwh +
    t.pv_to_ess_kwh +
    t.ess_to_load_kwh +
    t.ess_to_grid_kwh +
    t.grid_to_load_kwh +
    t.grid_to_ess_kwh
  return { sourcesTotal, consumption, exportTotal, essNet, checkTotal, gridToLoadCost, flowTotalKwh }
}

export type BalanceFigures = ReturnType<typeof balanceFigures>

export type FlowId =
  | 'pv_to_load'
  | 'pv_to_grid'
  | 'pv_to_ess'
  | 'ess_to_load'
  | 'ess_to_grid'
  | 'grid_to_load'
  | 'grid_to_ess'

export function balanceFormulas(t: EconomicsMonthlyTotals, m: Money, scope: PeriodScope) {
  const b = balanceFigures(t)
  const fin = financeFormulas(t, m, scope)

  const sources: Formula = {
    text: 'Уся енергія, що зайшла в межі об’єкта.',
    rule: 'Джерела = генерація СЕС + імпорт з мережі.',
    lines: sumLines(
      [
        { label: 'Генерація СЕС', amount: t.pv_kwh },
        { label: 'Імпорт з мережі', amount: t.grid_import_kwh },
      ],
      { label: 'Джерела', amount: b.sourcesTotal },
      formatMwh,
    ),
  }

  const gap = b.sourcesTotal - b.checkTotal
  const directions: Formula = {
    text: 'Куди ця енергія поділась.',
    rule: 'Напрямки = споживання + експорт + нетто-заряд УЗЕ (заряд − розряд).',
    lines: sumLines(
      [
        { label: "Споживання об'єкта", amount: b.consumption },
        { label: 'Експорт у мережу', amount: b.exportTotal },
        {
          label: 'Нетто-заряд УЗЕ',
          amount: b.essNet,
          detail: `заряд ${formatMwh(t.ess_charged_kwh)} − розряд ${formatMwh(t.ess_discharged_kwh)}`,
        },
      ],
      { label: 'Напрямки', amount: b.checkTotal },
      formatMwh,
    ),
    note:
      Math.abs(gap) > MWH_EPS
        ? `Джерела ${formatMwh(b.sourcesTotal)} − напрямки ${formatMwh(b.checkTotal)} = ${formatMwh(gap)}: втрати та неузгодженість лічильників.`
        : 'Джерела й напрямки сходяться в межах точності.',
  }

  const dischargeKwh = t.ess_to_load_kwh + t.ess_to_grid_kwh
  const flowShare: Formula = {
    text: 'Частка потоку у сумі семи детальних потоків.',
    rule: 'Частка = кВт·год потоку ÷ сума семи потоків.',
    lines: sumLines(
      withResidual(
        [
          { label: 'Джерела (СЕС + імпорт)', amount: b.sourcesTotal },
          { label: 'Розряд УЗЕ → споживання і мережа', amount: dischargeKwh },
        ],
        b.flowTotalKwh,
        'Різниця лічильників',
        MWH_EPS,
      ),
      { label: 'Сума семи потоків', amount: b.flowTotalKwh },
      formatMwh,
    ),
    note: 'Енергія через УЗЕ рахується двічі — на вході (заряд) і на виході (розряд), тому сума потоків більша за джерела.',
  }

  const negate = (v: number) => m.format(-v)
  const flowEffects: Record<FlowId, Formula> = {
    pv_to_load: fin.legs.pv_self,
    pv_to_grid: fin.legs.pv_export,
    ess_to_load: fin.legs.ess_self,
    ess_to_grid: fin.legs.ess_export,
    pv_to_ess: {
      text: 'Заряд УЗЕ від СЕС не має власного фінансового ефекту в цій таблиці.',
      rule: 'Вартість цього заряду — експорт, від якого відмовились — враховано в ефекті СЕС та ефекті УЗЕ.',
      lines: [{ op: '=', label: 'Фін. ефект', value: m.format(0), detail: formatKwh(t.pv_to_ess_kwh) }],
    },
    grid_to_load: {
      text: 'Що коштувала мережа, куплена на споживання (без заряду УЗЕ).',
      rule: 'Фін. ефект = −(вартість усього імпорту − заряд УЗЕ із мережі).',
      lines: [
        { label: 'Вартість усього імпорту', value: negate(t.import_cost_uah) },
        { op: '+', label: 'Заряд УЗЕ із мережі (окремий потік)', value: m.format(t.expense_grid_charge_uah) },
        { op: '=', label: 'Фін. ефект Мережа → споживання', value: negate(b.gridToLoadCost) },
      ],
      note: 'Залишок після заряду точніший, ніж середня ціна: заряд навмисно зсунуто в дешеві години.',
    },
    grid_to_ess: {
      text: 'Що коштувала мережа, куплена на заряд УЗЕ.',
      rule: 'Фін. ефект = −(Мережа → УЗЕ × ціна імпорту години).',
      lines: [
        { label: 'Мережа → УЗЕ', value: formatKwh(t.grid_to_ess_kwh) },
        {
          op: '×',
          label: 'Середня ціна імпорту години',
          value: priceText(m, ratioOf(t.expense_grid_charge_uah, t.grid_to_ess_kwh)),
          detail: 'сума ÷ кВт·год',
        },
        { op: '=', label: 'Фін. ефект Мережа → УЗЕ', value: negate(t.expense_grid_charge_uah) },
      ],
    },
  }

  return { sources, directions, flowShare, flowEffects }
}

// --- УЗЕ marginality heatmap ---------------------------------------------

// heatmapFormula explains the heatmap's cell and sums all its visible
// cells into one margin, so the grid can be checked against the totals.
export function heatmapFormula(
  rows: Array<Array<EconomicsMonthlyMarginHour | null>>,
  m: Money,
  scope: 'month' | 'year',
): Formula {
  let revenue = 0
  let cost = 0
  let wear = 0
  let discharged = 0
  let cells = 0
  for (const row of rows) {
    for (const c of row) {
      if (!c) continue
      cells++
      revenue += c.revenue_uah
      cost += c.cost_uah
      wear += c.wear_uah
      discharged += c.discharged_kwh
    }
  }
  const profit = revenue - cost - wear
  const margin = discharged > 0 ? profit / discharged : NaN
  const where = scope === 'month' ? 'за місяць' : 'за рік'
  return {
    text: HEATMAP_METRIC_TIP,
    rule: 'Клітинка = (виручка − собівартість − знос) ÷ розряд цієї години. Показано години з розрядом від 1 кВт·год.',
    lines: [
      { label: `Виручка розряду ${where}`, value: m.format(revenue) },
      { op: '−', label: 'Собівартість збереженої енергії', value: m.format(cost) },
      { op: '−', label: 'Знос', value: m.format(wear) },
      { op: '=', label: 'Прибуток розряду', value: m.format(profit) },
      { op: '÷', label: 'Розряд у показаних годинах', value: formatKwh(discharged) },
      {
        op: '=',
        label: 'Середня маржа всіх клітинок',
        value: `${m.formatPrice(margin)} ${m.priceUnit}`,
        detail: `${cells} клітинок`,
      },
    ],
    note: `Це середнє за всім показаним, зважене на розряд, а не середнє клітинок. Кожну клітинку наведення розкладає так само.`,
  }
}

// --- Day / month detail columns ------------------------------------------

export type ColumnFormulaMap = Record<string, Formula>

// periodColumnFormulas are the «i» of the detail-table headers (days of a
// month, months of a year): the rule of the column and the «Разом» row's
// own numbers. The same text serves both tables; `rowWord` names a row.
export function periodColumnFormulas(
  t: EconomicsMonthlyTotals,
  m: Money,
  scope: 'month' | 'year',
): ColumnFormulaMap {
  const rowWord = scope === 'month' ? 'доби' : 'місяця'
  const fig = financeFigures(t)
  const importPrice = ratioOf(t.import_cost_uah, t.grid_import_kwh)
  const factPrice = ratioOf(t.import_cost_uah, t.load_kwh)
  const eps = moneyEps(m)
  const kwh = (parts: Array<{ label: string; kwh: number }>, total: { label: string; kwh: number }) =>
    energySum(parts, total, 'Не розподілено по потоках', 'kwh')
  const totalNote = `У рядку «Разом» — ті самі суми за весь період.`

  const f: ColumnFormulaMap = {
    СЕС: {
      text: `Генерація СЕС за кожну ${scope === 'month' ? 'добу' : 'місяць'}.`,
      rule: `Σ за годинами ${rowWord}: СЕС → споживання + СЕС → УЗЕ + СЕС → мережа. Разом — сума рядків.`,
      lines: kwh(
        [
          { label: 'СЕС → споживання', kwh: t.pv_to_load_kwh },
          { label: 'СЕС → УЗЕ', kwh: t.pv_to_ess_kwh },
          { label: 'СЕС → мережа', kwh: t.pv_to_grid_kwh },
        ],
        { label: 'Разом', kwh: t.pv_kwh },
      ),
    },
    Споживання: {
      text: `Споживання об’єкта за ${rowWord}.`,
      rule: `Σ за годинами ${rowWord}: від СЕС + від УЗЕ + з мережі (+ від генератора).`,
      lines: kwh(
        [
          { label: 'СЕС → споживання', kwh: t.pv_to_load_kwh },
          { label: 'УЗЕ → споживання', kwh: t.ess_to_load_kwh },
          { label: 'Мережа → споживання', kwh: t.grid_to_load_kwh },
          { label: 'Генератор → споживання', kwh: t.gen_to_load_kwh ?? 0 },
        ],
        { label: 'Разом', kwh: t.load_kwh },
      ),
    },
    Імпорт: {
      text: `Енергія, куплена з мережі за ${rowWord}.`,
      rule: `Σ за годинами ${rowWord}: Мережа → споживання + Мережа → УЗЕ.`,
      lines: kwh(
        [
          { label: 'Мережа → споживання', kwh: t.grid_to_load_kwh },
          { label: 'Мережа → УЗЕ', kwh: t.grid_to_ess_kwh },
        ],
        { label: 'Разом', kwh: t.grid_import_kwh },
      ),
    },
    Вартість: {
      text: `Скільки заплачено за весь імпорт ${rowWord}, разом із зарядом УЗЕ.`,
      rule: 'Σ за годинами: імпорт години × ціна імпорту години. Ціна = (РДН + розподіл + передача + надбавка + збори) × ПДВ.',
      lines: [
        { label: 'Імпорт', value: formatKwh(t.grid_import_kwh) },
        { op: '×', label: 'Середня ціна імпорту', value: priceText(m, importPrice), detail: 'зважена по годинах' },
        { op: '=', label: 'Вартість імпорту (Разом)', value: m.format(t.import_cost_uah) },
      ],
      note: 'Вартість ніколи не дорівнює РДН × кВт·год: у ціні ще розподіл, передача, надбавка, збори й ПДВ.',
    },
    'Ціна імпорту': {
      text: `Повна ціна однієї імпортованої кВт·год за ${rowWord}.`,
      rule: 'Вартість імпорту ÷ імпорт.',
      lines: divide(
        { label: 'Вартість імпорту', value: m.format(t.import_cost_uah) },
        { label: 'Імпорт', value: formatKwh(t.grid_import_kwh) },
        { label: 'Ціна імпорту (Разом)', value: priceText(m, importPrice) },
      ),
    },
    'Факт. ціна': {
      text: `Скільки в середньому коштувала кожна спожита кВт·год ${rowWord}.`,
      rule: 'Вартість усього імпорту ÷ усе споживання.',
      lines: divide(
        { label: 'Вартість імпорту', value: m.format(t.import_cost_uah) },
        { label: 'Споживання', value: formatKwh(t.load_kwh) },
        { label: 'Факт. ціна (Разом)', value: priceText(m, factPrice) },
      ),
      note: 'Нижча за ціну імпорту, бо СЕС і УЗЕ покривають частину споживання без купівлі; у вартості ж лишається й заряд УЗЕ з мережі.',
    },
    'РДН сер.': {
      text: `Середня ціна ринку «на добу наперед» за ${rowWord}, зважена по імпорту.`,
      rule: 'Σ (РДН години × імпорт години) ÷ Σ імпорт. Це лише ринкова складова, без тарифів мережі, зборів і ПДВ.',
      lines: [
        { label: 'РДН сер. (Разом)', value: priceText(m, t.rdn_avg_uah_per_kwh) },
        {
          op: '+',
          label: 'Розподіл, передача, надбавка, збори і ПДВ',
          value: priceText(m, importPrice - t.rdn_avg_uah_per_kwh),
          detail: 'решта до ціни імпорту',
        },
        { op: '=', label: 'Ціна імпорту', value: priceText(m, importPrice) },
      ],
    },
    Експорт: {
      text: `Енергія, віддана в мережу за ${rowWord}.`,
      rule: `Σ за годинами ${rowWord}: СЕС → мережа + УЗЕ → мережа.`,
      lines: kwh(
        [
          { label: 'СЕС → мережа', kwh: t.pv_to_grid_kwh },
          { label: 'УЗЕ → мережа', kwh: t.ess_to_grid_kwh },
        ],
        { label: 'Разом', kwh: t.grid_export_kwh },
      ),
    },
    'Самоспож.': {
      text: `Яка частка СЕС лишилась на об’єкті ${rowWord}.`,
      rule: '(СЕС → споживання + СЕС → УЗЕ) ÷ генерація СЕС.',
      lines: [
        { label: 'СЕС → споживання', value: formatKwh(t.pv_to_load_kwh) },
        { op: '+', label: 'СЕС → УЗЕ', value: formatKwh(t.pv_to_ess_kwh) },
        { op: '÷', label: 'Генерація СЕС', value: formatKwh(t.pv_kwh) },
        { op: '=', label: 'Самоспоживання (Разом)', value: formatPercent(t.pv_kwh > 0 ? (t.pv_to_load_kwh + t.pv_to_ess_kwh) / t.pv_kwh : 0) },
      ],
    },
    'УЗЕ цикли':
      scope === 'month'
        ? {
            text: 'Еквівалентні цикли УЗЕ за добу.',
            rule: 'Розряд доби ÷ ємність УЗЕ тієї доби. Разом — сума добових циклів.',
            lines: [
              { label: 'Розряд УЗЕ за місяць', value: formatKwh(t.ess_discharged_kwh) },
              { op: '=', label: 'Еквівалентні цикли (Разом)', value: formatCycles(t.equivalent_cycles) },
            ],
          }
        : {
            text: 'Еквівалентні цикли УЗЕ за місяць.',
            rule: 'Розряд місяця ÷ ємність УЗЕ, що діяла того місяця.',
            lines: [{ op: '=', label: 'Циклів за період (Разом)', value: '—', detail: 'навмисно не підсумовується' }],
            note: 'Цикли за період не підсумовуються: кожен місяць нормований на ємність УЗЕ, що діяла тоді, а вона змінювалась. Дивіться цикли по окремих місяцях.',
          },
    EBITDA: {
      text: `Операційний результат ${rowWord}: доходи мінус витрати на заряд УЗЕ.`,
      rule: 'Дохід / економія − заряд УЗЕ з мережі (− заряд від генератора). Без зносу й витрат на утримання.',
      lines: sumLines(
        withResidual(
          [
            { label: 'Дохід / економія', amount: t.revenue_total_uah },
            ...fig.expenseLegs.map((l) => ({ label: l.label, amount: -l.amount })),
          ],
          t.ebitda_uah,
          'Інше (округлення)',
          eps,
        ),
        { label: 'EBITDA (Разом)', amount: t.ebitda_uah },
        m.format,
      ),
    },
  }
  for (const key of Object.keys(f)) {
    f[key] = { ...f[key], note: joinNotes(f[key].note, key === 'УЗЕ цикли' && scope === 'year' ? undefined : totalNote) }
  }
  return f
}
