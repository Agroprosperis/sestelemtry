import { offgridMinutes, type DailyTotals, type HourEconomicsRow } from '../compute'
import type { Money } from '../currency'
import { residual, sumLines, times, type Formula, type FormulaLine, type FormulaTerm } from '../formula'
import { formatDuration } from '../generatorText'
import type { Tariffs } from '../tariffs'
import type { EconomicsPvPlan } from './EconomicsTopSection'

// --- Formatting (day scale: кВт·год with one decimal; money via Money) ---

const kwhFmt = new Intl.NumberFormat('uk-UA', { minimumFractionDigits: 1, maximumFractionDigits: 1 })
const pctFmt = new Intl.NumberFormat('uk-UA', { style: 'percent', minimumFractionDigits: 1, maximumFractionDigits: 1 })
const cyclesFmt = new Intl.NumberFormat('uk-UA', { minimumFractionDigits: 2, maximumFractionDigits: 2 })
const rateFmt = new Intl.NumberFormat('uk-UA', { minimumFractionDigits: 2, maximumFractionDigits: 3 })
const litresFmt = new Intl.NumberFormat('uk-UA', { minimumFractionDigits: 1, maximumFractionDigits: 1 })

export function kwhNumber(v: number): string {
  return Number.isFinite(v) ? kwhFmt.format(v) : '—'
}

export function kwh(v: number): string {
  return Number.isFinite(v) ? `${kwhFmt.format(v)} кВт·год` : '—'
}

export function kw(v: number): string {
  return Number.isFinite(v) ? `${kwhFmt.format(v)} кВт` : '—'
}

export function pct(v: number): string {
  return Number.isFinite(v) ? pctFmt.format(v) : '—'
}

export function cycles(v: number): string {
  return Number.isFinite(v) ? cyclesFmt.format(v) : '—'
}

// DayFigures is every number the day's cards show, computed once so the
// cards and their formulas read the same values.
export type DayFigures = ReturnType<typeof dayFigures>

// dayFigures derives the cards' numbers from the day's totals and rows.
// The tariff components are UAH, VAT'd the way the server builds the
// import price stack, so each breakdown line sums exactly to its
// headline; the day's money is in its own rate's currency.
export function dayFigures(
  t: DailyTotals,
  rows: Array<HourEconomicsRow | null>,
  tariffs: Tariffs,
  money: Money,
  pvPlan: EconomicsPvPlan | null,
) {
  const vat = tariffs.includeVat ? 1 + tariffs.vatRate : 1
  const transPerKwh = money.fromUah(tariffs.transmissionUahPerKwh * vat)
  const distPerKwh = money.fromUah(tariffs.distributionUahPerKwh * vat)
  const netPerKwh = transPerKwh + distPerKwh
  const wearPerKwh = money.fromUah(tariffs.degradationUahPerKwh)

  // Базова вартість: load × full import stack. The network legs are
  // flat per-kWh tariffs, so the market/margin part is the remainder.
  // The load without the grid is priced at the generator instead
  // (baselineOffgrid, B_off), so it leaves the grid lines.
  const gridLoad = t.load - t.loadOffgrid
  const baseTrans = gridLoad * transPerKwh
  const baseDist = gridLoad * distPerKwh
  const baseEnergy = t.baselineCost - t.baselineOffgrid - baseTrans - baseDist

  // Фактична вартість: import − export revenue + УЗЕ wear + the fuel
  // (the exact actualCost identity from hourEconomics).
  const importCost = t.avgImportPriceUahPerKwh * t.gridImport
  const importNetwork = t.gridImport * netPerKwh
  const importEnergy = importCost - importNetwork
  const exportRevenue = t.revenuePvExport + t.revenueEssExport
  const wear = t.essDischarged * wearPerKwh
  const genAccounted = rows.some((r) => r?.gen?.accounted)
  const hasGen = t.genKwh > 0 || offgridMinutes(t.supplyMinutes) > 0
  const fuelMethod = rows.find((r) => r?.gen?.accounted)?.gen?.fuelMethod

  // Економічний ефект = базова − фактична, розкладена на ті самі
  // складові: зекономлена енергія, зекономлена мережа, дохід від
  // експорту, мінус знос, і без мережі — дизель мінус пальне. Суми
  // сходяться алгебраїчно.
  const effEnergy = baseEnergy - importEnergy
  const effNetwork = (gridLoad - t.gridImport) * netPerKwh
  const effOffgrid = t.baselineOffgrid - t.genFuelCost

  // Ефекти СЕС і УЗЕ ділять економічний ефект без залишку, як на місяці
  // й році: СЕС отримує й енергію, віддану в УЗЕ, за ціною експорту, від
  // якого відмовились заради заряду, — ту саму суму, що УЗЕ платить за
  // цей заряд.
  const pvEffect = t.effect - t.essNet
  const pvToEssValue = pvEffect - t.revenuePvSelf - t.revenuePvExport
  const essChargeAndWear = t.essNet - t.revenueEssSelf - t.revenueEssExport

  // Енергетичний баланс.
  // The rest of today comes back as all-zero rows that already carry the
  // day's РДН price, so the average counts only priced hours with any
  // energy flow.
  const loadHours = rows.filter(
    (r) =>
      r !== null &&
      r.rdnUahPerKwh !== null &&
      (r.economics.load > 0 ||
        r.flow.gridImport > 0 ||
        r.flow.gridExport > 0 ||
        r.flow.pv > 0 ||
        r.flow.essCharged > 0 ||
        r.flow.essDischarged > 0),
  ).length
  const peakLoadKw = rows.reduce((m, r) => (r ? Math.max(m, r.economics.load) : m), 0)
  const avgLoadKw = t.load / Math.max(1, loadHours)
  const importShare = t.load > 0 ? t.gridImport / t.load : NaN
  const exportShare = t.pv > 0 ? t.gridExport / t.pv : NaN
  const ownEnergy = t.pvToLoad + t.essToLoad
  const selfSuff = t.load > 0 ? ownEnergy / t.load : NaN
  const importDependence = t.load > 0 ? t.gridToLoad / t.load : NaN
  const pvSelfConsumed = t.pvToLoad + t.pvToEss
  const pvSelfShare = t.pv > 0 ? pvSelfConsumed / t.pv : NaN
  const planKwh = pvPlan && pvPlan.plannedKwh > 0 ? pvPlan.plannedKwh : NaN
  const planDone = Number.isFinite(planKwh) ? t.pv / planKwh : NaN

  // Робота УЗЕ.
  const cycleRatio = tariffs.essCapacityKwh > 0 ? t.essDischarged / tariffs.essCapacityKwh : NaN
  const dischargeMargin = t.essDischarged > 0 ? t.essNet / t.essDischarged : NaN

  return {
    vat,
    transPerKwh,
    distPerKwh,
    netPerKwh,
    wearPerKwh,
    gridLoad,
    baseTrans,
    baseDist,
    baseEnergy,
    importCost,
    importNetwork,
    importEnergy,
    exportRevenue,
    wear,
    genAccounted,
    hasGen,
    fuelMethod,
    effEnergy,
    effNetwork,
    effOffgrid,
    pvEffect,
    pvToEssValue,
    essChargeAndWear,
    loadHours,
    peakLoadKw,
    avgLoadKw,
    importShare,
    exportShare,
    ownEnergy,
    selfSuff,
    importDependence,
    pvSelfConsumed,
    pvSelfShare,
    planKwh,
    planDone,
    cycleRatio,
    dischargeMargin,
  }
}

export type DayFormulaKey =
  | 'baseline'
  | 'actual'
  | 'effect'
  | 'pvEffect'
  | 'essEffect'
  | 'importPrice'
  | 'exportPrice'
  | 'load'
  | 'pv'
  | 'gridImport'
  | 'gridExport'
  | 'selfSuff'
  | 'pvSelf'
  | 'gen'
  | 'fuel'
  | 'offgrid'
  | 'effectOffgrid'
  | 'cycles'
  | 'discharge'
  | 'charge'
  | 'margin'
  | 'coverage'
  | 'hourly'

// A breakdown line under this many hryvnias (or kWh) is rounding, not a
// leg worth a line of its own.
const MONEY_EPS_UAH = 0.5
const KWH_EPS = 0.05

// dayFormulas is how each figure of the day's cards is computed, with the
// day's own numbers.
export function dayFormulas(
  t: DailyTotals,
  f: DayFigures,
  rows: Array<HourEconomicsRow | null>,
  tariffs: Tariffs,
  money: Money,
): Record<DayFormulaKey, Formula> {
  const uah = money.format
  const priceOf = (v: number) => `${money.formatPriceAllowZero(v)} ${money.priceUnit}`
  const per = (amount: number, k: number) => (k > 0 ? amount / k : NaN)
  const moneyEps = money.fromUah(MONEY_EPS_UAH)
  const keepMoney = (terms: FormulaTerm[]) => terms.filter((x, i) => i === 0 || Math.abs(x.amount) >= moneyEps)
  const keepKwh = (terms: FormulaTerm[]) => terms.filter((x, i) => i === 0 || Math.abs(x.amount) >= KWH_EPS)
  // leg is a money line «kWh × weighted price».
  const leg = (label: string, amount: number, k: number): FormulaTerm => ({
    label,
    amount,
    detail: k > 0 ? times(kwh(k), priceOf(per(Math.abs(amount), k))) : undefined,
  })
  // withRest closes money terms with what rounding or a tariff change
  // left over, so they add up to the server's figure.
  const withRest = (terms: FormulaTerm[], total: number, label: string): FormulaTerm[] => {
    const rest = residual(
      total,
      terms.map((x) => x.amount),
    )
    return Math.abs(rest) >= moneyEps ? [...terms, { label, amount: rest }] : terms
  }
  const kwhWithRest = (terms: FormulaTerm[], total: number, label: string): FormulaTerm[] => {
    const rest = residual(
      total,
      terms.map((x) => x.amount),
    )
    return Math.abs(rest) >= KWH_EPS ? [...terms, { label, amount: rest }] : terms
  }
  const ratio = (a: { label: string; value: string }, b: { label: string; value: string }, result: string, label: string): FormulaLine[] => [
    { label: a.label, value: a.value },
    { op: '÷', label: b.label, value: b.value },
    { op: '=', label, value: result },
  ]

  const vatNote = tariffs.includeVat ? `× (1 + ПДВ ${pct(tariffs.vatRate)})` : 'без ПДВ'
  const rate = (uahPerKwh: number) => `${rateFmt.format(money.fromUah(uahPerKwh))} ${money.priceUnit}`
  const margin =
    tariffs.supplierMarginMode === 'pct'
      ? `${tariffs.supplierMarginPct}% від РДН`
      : rate(tariffs.supplierMarginUahPerKwh)
  const importStack =
    `Ціна години = (РДН + розподіл ${rate(tariffs.distributionUahPerKwh)} + передача ${rate(tariffs.transmissionUahPerKwh)}` +
    ` + надбавка ${margin} + інші збори ${rate(tariffs.otherFeesUahPerKwh)}) ${vatNote} — тариф цієї доби.`
  const exportStack = `Ціна години = РДН × (1 − знижка ${pct(tariffs.exportDiscount)}) ${vatNote}.`

  const offRows = rows.filter(
    (r): r is HourEconomicsRow => !!r && r.rdnUahPerKwh !== null && !!r.gen?.accounted && offgridMinutes(r.gen.minutes) > 0,
  )
  const offBaseline = offRows.reduce((acc, r) => acc + r.economics.baselineCost, 0)
  const offActual = offRows.reduce((acc, r) => acc + r.economics.actualCost, 0)

  const fuelPerLitre = money.fromUah(tariffs.genFuelPriceUahPerL * f.vat)

  return {
    baseline: {
      text: 'Усе споживання доби, оцінене повною ціною імпорту кожної години: скільки коштувала б енергія без СЕС та УЗЕ.',
      rule: f.genAccounted
        ? 'Σ по годинах (споживання з мережею × ціна імпорту години) + споживання без мережі за пальним генератора'
        : 'Σ по годинах (споживання × ціна імпорту години)',
      lines: f.genAccounted
        ? sumLines(
            [
              leg('Споживання з мережею', t.baselineCost - t.baselineOffgrid, f.gridLoad),
              { label: 'Без мережі — генератором', amount: t.baselineOffgrid },
            ],
            { label: 'Базова вартість', amount: t.baselineCost },
            uah,
          )
        : [
            { label: 'Споживання', value: kwh(f.gridLoad) },
            { op: '×', label: 'Ціна імпорту, зважена за споживанням', value: priceOf(per(t.baselineCost, f.gridLoad)) },
            { op: '=', label: 'Базова вартість', value: uah(t.baselineCost) },
          ],
      note: `Передача й розподіл на картці — тарифи доби × споживання з мережею (${priceOf(f.transPerKwh)} і ${priceOf(f.distPerKwh)}); енергія з мережі — решта: РДН, надбавка постачальника й інші збори.`,
    },
    actual: {
      text: 'Що енергія коштувала насправді: куплене з мережі мінус виручка від експорту, плюс знос УЗЕ і пальне генератора.',
      rule: 'Імпорт × ціна імпорту − експорт × ціна експорту + розряд УЗЕ × знос' + (f.genAccounted ? ' + пальне' : ''),
      lines: sumLines(
        withRest(
          keepMoney([
            leg('Імпорт з мережі', f.importCost, t.gridImport),
            leg('Дохід від експорту', -f.exportRevenue, t.gridExport),
            { label: 'Знос УЗЕ', amount: f.wear, detail: times(`${kwh(t.essDischarged)} розряду`, priceOf(f.wearPerKwh)) },
            ...(f.genAccounted ? [{ label: 'Пальне генератора', amount: t.genFuelCost }] : []),
          ]),
          t.actualCost,
          'Округлення',
        ),
        { label: 'Фактична вартість', amount: t.actualCost },
        uah,
      ),
    },
    effect: {
      text: 'Наскільки дешевше обійшлася доба завдяки СЕС та УЗЕ.',
      rule: 'Базова вартість − Фактична вартість',
      lines: sumLines(
        [
          { label: 'Базова вартість', amount: t.baselineCost },
          { label: 'Фактична вартість', amount: -t.actualCost },
        ],
        { label: 'Економічний ефект', amount: t.effect },
        uah,
      ),
      note: 'Він же — сума ефекту СЕС і ефекту УЗЕ. Рядки на картці розкладають ту саму різницю: менше куплено енергії, менше заплачено за мережі, дохід від експорту, мінус знос.',
    },
    pvEffect: {
      text: 'Внесок СЕС: її енергія, оцінена за ціною, яку вона замінила або принесла.',
      rule: 'СЕС → споживання × ціна імпорту + СЕС → мережа × ціна експорту + СЕС → УЗЕ × ціна експорту',
      lines: sumLines(
        keepMoney([
          leg('Власне споживання', t.revenuePvSelf, t.pvToLoad),
          leg('Експорт', t.revenuePvExport, t.pvToGrid),
          leg('Заряд УЗЕ від СЕС', f.pvToEssValue, t.pvToEss),
        ]),
        { label: 'Ефект СЕС', amount: f.pvEffect },
        uah,
      ),
      note: 'Заряд від СЕС УЗЕ «купує» за ціною експорту, від якого відмовились, тож ефект СЕС + ефект УЗЕ = економічний ефект.',
    },
    essEffect: {
      text: 'Чистий внесок батареї: розряд у споживання та мережу мінус вартість заряду і знос.',
      rule: 'Розряд у споживання × ціна імпорту + розряд у мережу × ціна експорту − заряд − знос',
      lines: sumLines(
        withRest(
          keepMoney([
            leg('Зменшення імпорту', t.revenueEssSelf, t.essToLoad),
            leg('Арбітраж (експорт)', t.revenueEssExport, t.essToGrid),
            leg('Заряд із мережі', -t.expenseGridCharge, t.gridToEss),
            leg('Заряд від СЕС (недоотриманий експорт)', -f.pvToEssValue, t.pvToEss),
            ...(t.expenseGenCharge > 0 ? [{ label: 'Заряд від генератора (пальне)', amount: -t.expenseGenCharge }] : []),
          ]),
          t.essNet,
          'Знос УЗЕ',
        ),
        { label: 'Ефект УЗЕ', amount: t.essNet },
        uah,
      ),
      note: `Знос — розряд ${kwh(t.essDischarged)} × ${priceOf(f.wearPerKwh)} за тарифом доби.`,
    },
    importPrice: {
      text: 'Повна ціна імпорту, зважена за обсягом імпорту кожної години.',
      rule: 'Σ (імпорт години × ціна імпорту години) ÷ Σ імпорт',
      lines: ratio(
        { label: 'Вартість імпорту', value: uah(f.importCost) },
        { label: 'Імпорт з мережі', value: kwh(t.gridImport) },
        priceOf(t.avgImportPriceUahPerKwh),
        'Сер. ціна імпорту',
      ),
      note: importStack,
    },
    exportPrice: {
      text: 'Ціна експорту, зважена за обсягом експорту кожної години.',
      rule: 'Σ (експорт години × ціна експорту години) ÷ Σ експорт',
      lines: ratio(
        { label: 'Дохід від експорту', value: uah(f.exportRevenue) },
        { label: 'Експорт у мережу', value: kwh(t.gridExport) },
        priceOf(t.avgExportPriceUahPerKwh),
        'Сер. ціна експорту',
      ),
      note: exportStack,
    },
    load: {
      text: 'Скільки енергії спожив об’єкт, з енергобалансу кожної години.',
      rule: 'Σ по годинах (СЕС + імпорт + розряд УЗЕ + генератор − експорт − заряд УЗЕ)',
      lines: sumLines(
        kwhWithRest(
          keepKwh([
            { label: 'Генерація СЕС', amount: t.pv },
            { label: 'Імпорт з мережі', amount: t.gridImport },
            { label: 'Розряд УЗЕ', amount: t.essDischarged },
            { label: 'Генератор', amount: t.genToLoad + t.genToEss },
            { label: 'Експорт у мережу', amount: -t.gridExport },
            { label: 'Заряд УЗЕ', amount: -t.essCharged },
          ]),
          t.load,
          'Звірка балансу годин',
        ),
        { label: 'Споживання', amount: t.load },
        kwh,
      ),
      note: `Пік — найбільше споживання за годину: ${kw(f.peakLoadKw)}. Середнє — ${kwh(t.load)} ÷ ${f.loadHours} год з даними = ${kw(f.avgLoadKw)}.`,
    },
    pv: {
      text: 'Виробіток СЕС за добу за лічильниками інверторів.',
      lines: Number.isFinite(f.planKwh)
        ? ratio(
            { label: 'Генерація СЕС', value: kwh(t.pv) },
            { label: 'План на добу', value: kwh(f.planKwh) },
            pct(f.planDone),
            'Виконання плану',
          )
        : undefined,
      note: Number.isFinite(f.planKwh) ? undefined : 'Плану на цей день немає, тож виконання не рахується.',
    },
    gridImport: {
      text: 'Енергія, куплена з мережі: на споживання і на заряд УЗЕ.',
      lines: [
        ...sumLines(
          kwhWithRest(
            keepKwh([
              { label: 'Мережа → споживання', amount: t.gridToLoad },
              { label: 'Мережа → УЗЕ', amount: t.gridToEss },
            ]),
            t.gridImport,
            'Звірка балансу',
          ),
          { label: 'Імпорт з мережі', amount: t.gridImport },
          kwh,
        ),
        { op: '÷', label: 'Споживання', value: kwh(t.load) },
        { op: '=', label: 'Від споживання', value: pct(f.importShare) },
      ],
    },
    gridExport: {
      text: 'Енергія, віддана в мережу: надлишок СЕС і розряд УЗЕ.',
      lines: [
        ...sumLines(
          kwhWithRest(
            keepKwh([
              { label: 'СЕС → мережа', amount: t.pvToGrid },
              { label: 'УЗЕ → мережа', amount: t.essToGrid },
            ]),
            t.gridExport,
            'Звірка балансу',
          ),
          { label: 'Експорт у мережу', amount: t.gridExport },
          kwh,
        ),
        { op: '÷', label: 'Генерація СЕС', value: kwh(t.pv) },
        { op: '=', label: 'Від генерації СЕС', value: pct(f.exportShare) },
      ],
    },
    selfSuff: {
      text: 'Частка споживання, покрита власними СЕС та УЗЕ замість імпорту.',
      rule: '(СЕС → споживання + УЗЕ → споживання) ÷ споживання',
      lines: [
        { label: 'СЕС → споживання', value: kwh(t.pvToLoad) },
        { op: '+', label: 'УЗЕ → споживання', value: kwh(t.essToLoad) },
        { op: '=', label: 'Власна енергія', value: kwh(f.ownEnergy) },
        { op: '÷', label: 'Споживання', value: kwh(t.load) },
        { op: '=', label: 'Самозабезпечення', value: pct(f.selfSuff) },
      ],
      note:
        `Імпортозалежність = мережа → споживання ${kwh(t.gridToLoad)} ÷ споживання = ${pct(f.importDependence)}.` +
        (t.genToLoad > 0 ? ' Решту дав генератор, тож разом частки менші за 100%.' : ''),
    },
    pvSelf: {
      text: 'Частка виробітку СЕС, використана на об’єкті, а не експортована.',
      rule: '(СЕС → споживання + СЕС → УЗЕ) ÷ генерація СЕС',
      lines: [
        { label: 'СЕС → споживання', value: kwh(t.pvToLoad) },
        { op: '+', label: 'СЕС → УЗЕ', value: kwh(t.pvToEss) },
        { op: '=', label: 'Використано на об’єкті', value: kwh(f.pvSelfConsumed) },
        { op: '÷', label: 'Генерація СЕС', value: kwh(t.pv) },
        { op: '=', label: 'Самоспоживання СЕС', value: pct(f.pvSelfShare) },
      ],
    },
    gen: {
      text: 'Енергія генератора за його лічильником (контролер Encombi).',
      lines: f.genAccounted
        ? sumLines(
            kwhWithRest(
              keepKwh([
                { label: 'Генератор → споживання', amount: t.genToLoad },
                { label: 'Генератор → УЗЕ', amount: t.genToEss },
              ]),
              t.genKwh,
              'Години без обліку пального',
            ),
            { label: 'Генератор', amount: t.genKwh },
            kwh,
          )
        : undefined,
      note: `У роботі ${formatDuration(t.supplyMinutes.generator)}.`,
    },
    fuel: f.genAccounted
      ? {
          text: 'Вартість пального генератора за тарифом доби.',
          rule:
            f.fuelMethod === 'per_hour'
              ? 'Години роботи × витрата л/год × ціна літра' + (tariffs.includeVat ? ' × ПДВ' : '')
              : 'Генератор, кВт·год × витрата л/кВт·год × ціна літра' + (tariffs.includeVat ? ' × ПДВ' : ''),
          lines: [
            { label: 'Пальне', value: `${litresFmt.format(t.genFuelL)} л` },
            { op: '×', label: tariffs.includeVat ? 'Ціна літра з ПДВ' : 'Ціна літра', value: `${rateFmt.format(fuelPerLitre)} ${money.unit}/л` },
            { op: '=', label: 'Вартість пального', value: uah(t.genFuelCost) },
          ],
        }
      : { text: 'Вартість пального не задана в тарифах доби, тож генератор у грошах не рахується.' },
    offgrid: {
      text: 'Скільки часу об’єкт працював без мережі.',
      lines: [
        { label: 'Генератор', value: formatDuration(t.supplyMinutes.generator) },
        { op: '+', label: 'Острів (від УЗЕ)', value: formatDuration(t.supplyMinutes.island) },
        { op: '+', label: 'Без живлення', value: formatDuration(t.supplyMinutes.blackout) },
        { op: '=', label: 'Без мережі', value: formatDuration(offgridMinutes(t.supplyMinutes)) },
      ],
    },
    effectOffgrid: f.genAccounted
      ? {
          text: 'Ефект годин, коли об’єкт був без мережі: скільки пального заощадили СЕС і УЗЕ проти роботи лише генератора.',
          rule: 'Σ годин без мережі (базова вартість − фактична вартість)',
          lines: sumLines(
            withRest(
              [
                { label: 'Базова вартість цих годин', amount: offBaseline },
                { label: 'Фактична вартість цих годин', amount: -offActual },
              ],
              t.effectOffgrid,
              'Округлення',
            ),
            { label: 'Ефект годин без мережі', amount: t.effectOffgrid },
            uah,
          ),
          note: `Годин без мережі з цінами: ${offRows.length}. Базова — їхнє споживання на одному генераторі.`,
        }
      : { text: 'Вартість пального не задана, тож ефект годин без мережі не рахується.' },
    cycles: {
      text: 'Скільки разів за добу УЗЕ віддало енергію своєї корисної ємності.',
      rule: 'Розряд УЗЕ ÷ корисна ємність',
      lines: ratio(
        { label: 'Розряд УЗЕ', value: kwh(t.essDischarged) },
        { label: 'Корисна ємність (тариф доби)', value: kwh(tariffs.essCapacityKwh) },
        cycles(f.cycleRatio),
        'Коефіцієнт циклів',
      ),
    },
    discharge: {
      text: 'Скільки енергії віддало УЗЕ і куди.',
      lines: sumLines(
        kwhWithRest(
          keepKwh([
            { label: 'УЗЕ → споживання', amount: t.essToLoad },
            { label: 'УЗЕ → мережа', amount: t.essToGrid },
          ]),
          t.essDischarged,
          'Звірка балансу',
        ),
        { label: 'Розряд УЗЕ', amount: t.essDischarged },
        kwh,
      ),
    },
    charge: {
      text: 'Скільки енергії набрало УЗЕ і звідки.',
      lines: sumLines(
        kwhWithRest(
          keepKwh([
            { label: 'СЕС → УЗЕ', amount: t.pvToEss },
            { label: 'Мережа → УЗЕ', amount: t.gridToEss },
            { label: 'Генератор → УЗЕ', amount: t.genToEss },
          ]),
          t.essCharged,
          'Звірка балансу',
        ),
        { label: 'Заряд УЗЕ', amount: t.essCharged },
        kwh,
      ),
    },
    margin: {
      text: 'Скільки заробила кожна віддана батареєю кВт·год.',
      rule: 'Ефект УЗЕ ÷ розряд УЗЕ',
      lines: ratio(
        { label: 'Ефект УЗЕ', value: uah(t.essNet) },
        { label: 'Розряд УЗЕ', value: kwh(t.essDischarged) },
        priceOf(f.dischargeMargin),
        'Сер. маржа розряду',
      ),
      note: 'Заряд тут оцінено за цінами годин заряду цієї доби. Теплова карта місяця рахує маржу інакше — за собівартістю енергії, що лежить у батареї.',
    },
    coverage: {
      text: 'Звідки взялася енергія, яку спожив об’єкт.',
      lines: sumLines(
        keepKwh([
          { label: 'СЕС → споживання', amount: t.pvToLoad },
          { label: 'УЗЕ → споживання', amount: t.essToLoad },
          { label: 'Мережа → споживання', amount: t.gridToLoad },
          { label: 'Генератор → споживання', amount: t.genToLoad },
        ]),
        { label: 'Усього', amount: t.pvToLoad + t.essToLoad + t.gridToLoad + t.genToLoad },
        kwh,
      ),
      note: 'Частка джерела = його кВт·год ÷ усього.',
    },
    hourly: {
      text: 'Ефект кожної години: СЕС — ефект без внеску батареї, УЗЕ — чистий ефект батареї, сукупний — базова мінус фактична вартість години.',
      lines: sumLines(
        [
          { label: 'Ефект СЕС за добу', amount: f.pvEffect },
          { label: 'Ефект УЗЕ за добу', amount: t.essNet },
        ],
        { label: 'Сукупний ефект', amount: t.effect },
        uah,
      ),
      note: 'Години заряду УЗЕ мінусові: заряд оплачено в годині заряду, а виручка приходить у годині розряду.',
    },
  }
}
