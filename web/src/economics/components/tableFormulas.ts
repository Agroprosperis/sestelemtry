import type { HourEconomicsRow } from '../compute'
import type { Money } from '../currency'
import { residual, sumLines, times, type Formula, type FormulaLine, type FormulaTerm } from '../formula'
import { formatDuration } from '../generatorText'
import { fuelCostUah, type Tariffs } from '../tariffs'

// The «i» of every row of the day's hourly table («Погодинна деталізація»)
// opens a formula built here: what the row is, the rule in words and the
// same rule with the day's own numbers. The builders are pure — they take
// the table's rows, the day's tariff and the money on screen — so the
// figures cannot drift from what the table shows: every Σ comes from the
// row's own `total`, and the parts are summed from the same rows.

type Row = HourEconomicsRow
type Rows = Array<Row | null>

// FormulaMetric is what a table row hands over: its kind (which decides
// how a figure is written) and the pick / total functions the table itself
// renders its cells and Σ with.
export type FormulaMetric = {
  kind: 'price' | 'price_with_zero' | 'energy' | 'uah' | 'uah_signed' | 'minutes'
  pick: (row: Row | null) => number | null
  total: (rows: Rows) => number | null
}

export type TableFormulaInput = {
  rows: Rows
  // tariffs is the version in force that day (UAH); null when unknown, and
  // the price stack is then shown as one lump.
  tariffs: Tariffs | null
  money: Money
  // metrics are the table's rows by id.
  metrics: Record<string, FormulaMetric>
}

const energyFmt = new Intl.NumberFormat('uk-UA', { minimumFractionDigits: 1, maximumFractionDigits: 1 })
const percentFmt = new Intl.NumberFormat('uk-UA', { maximumFractionDigits: 2 })
const factorFmt = new Intl.NumberFormat('uk-UA', { minimumFractionDigits: 2, maximumFractionDigits: 4 })

const GAP_LABEL = 'Решта (округлення, обмеження ≥ 0)'
const SIGMA = 'Σ за добу'
// Differences below this (kWh) are rounding, not a missing term.
const GAP_KWH = 0.05

function kwh(v: number): string {
  return `${energyFmt.format(v)} кВт·год`
}

function hourLabel(row: Row): string {
  return `${String(row.hour).padStart(2, '0')}:00`
}

function sumOf(list: Row[], f: (r: Row) => number): number {
  let acc = 0
  for (const r of list) {
    const v = f(r)
    if (Number.isFinite(v)) acc += v
  }
  return acc
}

function extreme(list: Row[], f: (r: Row) => number, dir: 'min' | 'max'): Row | null {
  let best: Row | null = null
  for (const r of list) {
    const v = f(r)
    if (!Number.isFinite(v)) continue
    if (best === null || (dir === 'min' ? v < f(best) : v > f(best))) best = r
  }
  return best
}

export function tableFormulas({ rows, tariffs, money, metrics }: TableFormulaInput): Record<string, Formula> {
  const out: Record<string, Formula> = {}
  const has = (id: string) => id in metrics
  const S = (id: string) => metrics[id]?.total(rows) ?? 0
  const set = (id: string, formula: Formula) => {
    if (has(id)) out[id] = formula
  }
  const live = rows.filter((r): r is Row => r !== null)
  const priced = live.filter((r) => r.rdnUahPerKwh !== null)
  const accounted = live.filter((r) => r.gen?.accounted)
  const uah = (v: number) => money.format(v)
  const P = (v: number) => money.formatPriceAllowZero(v)
  const avg = (amount: number, volume: number) => (volume > 0 ? amount / volume : 0)
  const unpricedNote =
    priced.length < live.length ? `Години без ціни РДН (${live.length - priced.length}) не враховано.` : undefined

  const fmtKind = (id: string, v: number): string => {
    switch (metrics[id]?.kind) {
      case 'energy':
        return kwh(v)
      case 'minutes':
        return `${Math.round(v)} хв`
      case 'price':
      case 'price_with_zero':
        return P(v)
      default:
        return uah(v)
    }
  }

  // flowLines writes an energy row's Σ as its signed terms; what the terms
  // do not cover (rounding, the clamps at 0 the hours apply) is its own
  // line, so the sum always closes.
  const flowLines = (id: string, terms: FormulaTerm[]): FormulaLine[] => {
    const total = S(id)
    const gap = residual(total, terms.map((t) => t.amount))
    const all = Math.abs(gap) >= GAP_KWH ? [...terms, { label: GAP_LABEL, amount: gap }] : terms
    return sumLines(all, { label: SIGMA, amount: total }, kwh)
  }
  const flow = (id: string, text: string, rule: string, terms: FormulaTerm[], note?: string) =>
    set(id, { text, rule, lines: flowLines(id, terms), note })
  const term = (label: string, id: string, sign: 1 | -1 = 1): FormulaTerm => ({ label, amount: sign * S(id) })

  // ── Prices ──────────────────────────────────────────────────────────
  const dayExample = (by: (r: Row) => number, why: string): { row: Row; why: string } | null => {
    const big = extreme(priced, by, 'max')
    if (big && by(big) > 0) return { row: big, why }
    return priced[0] ? { row: priced[0], why: 'перша година з ціною' } : null
  }
  const unit = money.priceUnit
  const vat = tariffs?.includeVat ? 1 + tariffs.vatRate : 1
  const vatLine = (): FormulaLine[] =>
    tariffs?.includeVat
      ? [{ op: '×', label: `ПДВ ${percentFmt.format(tariffs.vatRate * 100)} %`, value: factorFmt.format(vat) }]
      : []

  if (has('rdn')) {
    const lo = extreme(priced, (r) => r.rdnUahPerKwh ?? NaN, 'min')
    const hi = extreme(priced, (r) => r.rdnUahPerKwh ?? NaN, 'max')
    const lines: FormulaLine[] = []
    if (lo && hi) {
      lines.push({ label: 'Мінімум за добу', value: P(lo.rdnUahPerKwh ?? 0), detail: hourLabel(lo) })
      lines.push({ label: 'Максимум за добу', value: P(hi.rdnUahPerKwh ?? 0), detail: hourLabel(hi) })
    }
    set('rdn', {
      text: `Ціна електроенергії на ринку «на добу наперед» (РДН, зона 2) у цю годину, ${unit}. Без розподілу, передачі, надбавки й ПДВ.`,
      rule: 'Береться з ринку як є, окремо для кожної години.',
      lines,
      note: 'Σ немає: ціни різних годин не додаються.',
    })
  }

  if (has('import_price')) {
    const ex = dayExample((r) => r.flow.gridImport, 'найбільший імпорт за добу')
    const lines: FormulaLine[] = []
    let note = `Складові показано з округленням, ${unit}.`
    if (ex) {
      const { row } = ex
      const rdn = row.rdnUahPerKwh ?? 0
      const actual = row.economics.importPriceUahPerKwh
      lines.push({ label: `РДН, ${hourLabel(row)}`, value: P(rdn) })
      if (tariffs) {
        const margin =
          tariffs.supplierMarginMode === 'pct'
            ? (tariffs.supplierMarginPct / 100) * rdn
            : money.fromUah(tariffs.supplierMarginUahPerKwh)
        const parts: Array<[string, number, string | undefined]> = [
          ['Розподіл', money.fromUah(tariffs.distributionUahPerKwh), undefined],
          ['Передача', money.fromUah(tariffs.transmissionUahPerKwh), undefined],
          [
            'Надбавка постачальника',
            margin,
            tariffs.supplierMarginMode === 'pct' ? `${percentFmt.format(tariffs.supplierMarginPct)} % від РДН` : undefined,
          ],
          ['Інші збори', money.fromUah(tariffs.otherFeesUahPerKwh), undefined],
        ]
        let sum = rdn
        for (const [label, value, detail] of parts) {
          sum += value
          // A fee that is not set is left out to keep the bubble short;
          // the rule above still names it.
          if (value === 0 && label !== 'Розподіл' && label !== 'Передача') continue
          lines.push({ op: value < 0 ? '−' : '+', label, value: P(Math.abs(value)), detail })
        }
        if (Math.abs(sum * vat - actual) > money.fromUah(0.01)) {
          note += ' Тариф доби відрізняється від показаного: складові наближені.'
        }
      } else {
        const rest = actual / vat - rdn
        lines.push({ op: rest < 0 ? '−' : '+', label: 'Розподіл, передача, надбавка й збори', value: P(Math.abs(rest)) })
      }
      lines.push(...vatLine())
      lines.push({ op: '=', label: `Ціна імпорту, ${hourLabel(row)}`, value: P(actual) })
    }
    set('import_price', {
      text: `Що коштує кіловат-година з мережі в цю годину, ${unit}.${ex ? ` Нижче — година ${hourLabel(ex.row)} (${ex.why}).` : ''}`,
      rule:
        tariffs?.supplierMarginMode === 'pct'
          ? '(РДН + розподіл + передача + надбавка % від РДН + інші збори) × ПДВ'
          : '(РДН + розподіл + передача + надбавка постачальника + інші збори) × ПДВ',
      lines,
      note: (tariffs?.includeVat ?? true) ? note : `${note} ПДВ у тарифі вимкнено.`,
    })
  }

  if (has('export_price')) {
    const ex = dayExample((r) => r.flow.gridExport, 'найбільший експорт за добу')
    const lines: FormulaLine[] = []
    if (ex) {
      const { row } = ex
      const rdn = row.rdnUahPerKwh ?? 0
      const actual = row.economics.exportPriceUahPerKwh
      const keep = tariffs ? 1 - tariffs.exportDiscount : rdn * vat > 0 ? actual / (rdn * vat) : 1
      lines.push({ label: `РДН, ${hourLabel(row)}`, value: P(rdn) })
      lines.push({
        op: '×',
        label: 'Знижка на експорт',
        value: factorFmt.format(keep),
        detail: `1 − ${percentFmt.format((1 - keep) * 100)} %`,
      })
      lines.push(...vatLine())
      lines.push({ op: '=', label: `Ціна експорту, ${hourLabel(row)}`, value: P(actual) })
    }
    set('export_price', {
      text: `Що виручає кіловат-година, віддана в мережу в цю годину, ${unit}.${ex ? ` Нижче — година ${hourLabel(ex.row)} (${ex.why}).` : ''}`,
      rule: 'РДН × (1 − знижка на експорт) × ПДВ',
      lines,
      note: 'Розподіл, передача й надбавка в ціну експорту не входять.',
    })
  }

  // ── Grid ────────────────────────────────────────────────────────────
  flow(
    'grid_import',
    'Скільки енергії об’єкт узяв із мережі за годину (різниця показів лічильника мережі).',
    'Імпорт = Мережа → Споживання + Мережа → УЗЕ',
    [term('Мережа → Споживання', 'grid_to_load'), term('Мережа → УЗЕ', 'grid_to_ess')],
  )
  flow(
    'grid_export',
    'Скільки енергії об’єкт віддав у мережу за годину (різниця показів лічильника мережі).',
    'Експорт = СЕС → Мережа + УЗЕ → Мережа',
    [term('СЕС → Мережа', 'pv_to_grid'), term('УЗЕ → Мережа', 'ess_to_grid')],
  )

  // ── Consumption ─────────────────────────────────────────────────────
  const loadTerms = [term('СЕС → Споживання', 'pv_to_load'), term('Мережа → Споживання', 'grid_to_load'), term('УЗЕ → Споживання', 'ess_to_load')]
  if (has('gen_to_load')) loadTerms.push(term('Генератор → Споживання', 'gen_to_load'))
  flow(
    'load',
    'Скільки енергії спожив об’єкт. У кожній годині — баланс потоків: СЕС + імпорт + розряд УЗЕ' +
      (has('gen_to_load') ? ' + генератор' : '') +
      ' − експорт − заряд УЗЕ, не менше 0.',
    `Споживання = СЕС → + Мережа → + УЗЕ →${has('gen_to_load') ? ' + Генератор →' : ''} Споживання`,
    loadTerms,
  )
  const pvToLoadRule = 'СЕС → Споживання = СЕС − СЕС → Мережа − СЕС → УЗЕ (не менше 0)'
  const pvToLoadText = 'Частина виробітку СЕС, яку об’єкт спожив сам.'
  const pvToLoadTerms = [term('СЕС (виробіток)', 'pv_total'), term('СЕС → Мережа', 'pv_to_grid', -1), term('СЕС → УЗЕ', 'pv_to_ess', -1)]
  flow('pv_to_load', pvToLoadText, pvToLoadRule, pvToLoadTerms)
  flow('pv_to_load_pv', pvToLoadText, pvToLoadRule, pvToLoadTerms)
  flow(
    'grid_to_load',
    'Частина імпорту, яку об’єкт спожив сам (решта пішла на заряд УЗЕ).',
    'Мережа → Споживання = Імпорт − Мережа → УЗЕ (не менше 0)',
    [term('Імпорт всього', 'grid_import'), term('Мережа → УЗЕ', 'grid_to_ess', -1)],
  )

  // ── ESS flows ───────────────────────────────────────────────────────
  const essCharged = sumOf(live, (r) => r.flow.essCharged)
  const essDischarged = sumOf(live, (r) => r.flow.essDischarged)
  const chargeTerm: FormulaTerm = { label: 'Заряд УЗЕ всього', amount: essCharged }
  const dischargeTerm: FormulaTerm = { label: 'Розряд УЗЕ всього', amount: essDischarged }
  const chargeParts: Array<[string, string]> = [
    ['СЕС → УЗЕ', 'pv_to_ess'],
    ['Мережа → УЗЕ', 'grid_to_ess'],
    ['Генератор → УЗЕ', 'gen_to_ess'],
  ]
  // chargeFlow writes one source of the battery's charge as the whole
  // charge less the other sources.
  const chargeFlow = (id: string, text: string) => {
    const label = chargeParts.find(([, p]) => p === id)?.[0] ?? ''
    const others = chargeParts.filter(([, p]) => p !== id && has(p))
    flow(
      id,
      text,
      `${label} = Заряд УЗЕ всього − ${others.map(([l]) => l).join(' − ')}`,
      [chargeTerm, ...others.map(([l, p]) => term(l, p, -1))],
    )
  }
  chargeFlow('pv_to_ess', 'Частина виробітку СЕС, що пішла на заряд УЗЕ (з розподілу потоків за годину).')
  chargeFlow('grid_to_ess', 'Частина імпорту, що пішла на заряд УЗЕ (з розподілу потоків за годину).')
  chargeFlow('gen_to_ess', 'Частина виробітку генератора, що пішла на заряд УЗЕ.')
  const essToLoadText = 'Частина розряду УЗЕ, що пішла на споживання об’єкта (з розподілу потоків за годину).'
  const essToLoadRule = 'УЗЕ → Споживання = Розряд УЗЕ всього − УЗЕ → Мережа'
  const essToLoadTerms = [dischargeTerm, term('УЗЕ → Мережа', 'ess_to_grid', -1)]
  flow('ess_to_load', essToLoadText, essToLoadRule, essToLoadTerms)
  flow('ess_to_load_dup', essToLoadText, essToLoadRule, essToLoadTerms)
  flow(
    'ess_to_grid',
    'Частина розряду УЗЕ, що пішла в мережу (з розподілу потоків за годину).',
    'УЗЕ → Мережа = Розряд УЗЕ всього − УЗЕ → Споживання',
    [dischargeTerm, term('УЗЕ → Споживання', 'ess_to_load', -1)],
  )

  // ── PV ──────────────────────────────────────────────────────────────
  flow(
    'pv_total',
    'Скільки енергії виробила СЕС за годину (різниця показів лічильника СЕС).',
    'Виробіток = СЕС → Споживання + СЕС → Мережа + СЕС → УЗЕ',
    [term('СЕС → Споживання', 'pv_to_load'), term('СЕС → Мережа', 'pv_to_grid'), term('СЕС → УЗЕ', 'pv_to_ess')],
  )
  flow(
    'pv_to_grid',
    'Частина експорту, яку дала СЕС (решту дав розряд УЗЕ).',
    'СЕС → Мережа = Експорт − УЗЕ → Мережа (не менше 0)',
    [term('Експорт всього', 'grid_export'), term('УЗЕ → Мережа', 'ess_to_grid', -1)],
  )

  // ── Snapshot rows (no Σ) ────────────────────────────────────────────
  const snapshot = (id: string, text: string, rule: string, note: string, extra: FormulaLine[] = [], short = false) => {
    const m = metrics[id]
    if (!m) return
    const have = live.filter((r) => m.pick(r) !== null)
    const val = (r: Row) => m.pick(r) ?? NaN
    const first = have[0]
    const last = have[have.length - 1]
    const lo = extreme(have, val, 'min')
    const hi = extreme(have, val, 'max')
    const lines: FormulaLine[] = []
    const add = (label: string, r: Row | null) => {
      if (r) lines.push({ label, value: fmtKind(id, val(r)), detail: hourLabel(r) })
    }
    if (!short) add('На початок доби', first ?? null)
    add('Мінімум', lo)
    add('Максимум', hi)
    if (!short && last && last !== first) add('На початок останньої години', last)
    lines.push(...extra)
    out[id] = { text, rule, lines, note }
  }
  snapshot(
    'ess_remaining',
    'Скільки корисної енергії лежить у УЗЕ на початок години — миттєвий знімок, а не потік.',
    'Залишок(год + 1) = Залишок(год) + СЕС → УЗЕ + Мережа → УЗЕ' +
      (has('gen_to_ess') ? ' + Генератор → УЗЕ' : '') +
      ' − УЗЕ → Споживання − УЗЕ → Мережа',
    `Σ немає: залишки різних годин не додаються. Старт — із заряду батареї (SOC): (SOC − 10) ÷ 80, у межах 0…1, × корисна ємність${
      tariffs ? ` ${kwh(tariffs.essCapacityKwh)}` : ''
    }.`,
  )
  {
    const withBasis = live.filter((r) => r.essCostBasisUahStart != null && (r.essRemainingKwhStart ?? 0) > 0)
    const ex = extreme(withBasis, (r) => r.essRemainingKwhStart ?? NaN, 'max')
    const lines: FormulaLine[] = []
    if (ex) {
      lines.push({ label: `Гривні в УЗЕ, ${hourLabel(ex)}`, value: uah(ex.essCostBasisUahStart ?? 0) })
      lines.push({ op: '÷', label: 'Залишок УЗЕ', value: kwh(ex.essRemainingKwhStart ?? 0) })
      lines.push({ op: '=', label: 'Собівартість УЗЕ', value: P(ex.essAvgCostUahPerKwhStart ?? 0) })
    }
    snapshot(
      'ess_cost_basis',
      `Середня ціна енергії, що лежить у УЗЕ на початок години, ${unit}. Заряд від СЕС входить за 0, із мережі — за ціною імпорту, від генератора — за вартістю пального.`,
      'Собівартість = гривні в УЗЕ ÷ кВт·год в УЗЕ',
      'Σ немає: це знімок. Облік скидається в 0 після останньої години з SOC ≤ 10 % (дивиться на 48 год назад).',
      lines,
      true,
    )
  }

  // ── Money ───────────────────────────────────────────────────────────
  // moneyRow writes a revenue / expense leg as Σ kWh × weighted price, the
  // kWh and money summed over the hours with a РДН price like the row.
  const moneyRow = (
    id: string,
    text: string,
    rule: string,
    flowLabel: string,
    volume: number,
    note?: string,
  ) => {
    const total = S(id)
    set(id, {
      text,
      rule,
      lines: [
        { label: flowLabel, value: kwh(volume) },
        { op: '×', label: 'Середньозважена ціна', value: P(avg(total, volume)), detail: 'Σ грн ÷ Σ кВт·год' },
        { op: '=', label: SIGMA, value: uah(total) },
      ],
      note: [note, unpricedNote].filter(Boolean).join(' ') || undefined,
    })
  }
  // moneyRowOffgrid is moneyRow for a leg whose off-grid kWh are valued at
  // the diesel price instead of the import price.
  const moneyRowOffgrid = (
    id: string,
    text: string,
    rule: string,
    flowLabel: string,
    onGrid: (r: Row) => number,
    offKwh: (r: Row) => number,
    offUah: (r: Row) => number,
    price: (r: Row) => number,
  ) => {
    if (!has(id)) return
    const total = S(id)
    const offVolume = sumOf(priced, offKwh)
    const offMoney = sumOf(priced, offUah)
    if (offVolume === 0 && offMoney === 0) {
      moneyRow(id, text, rule, flowLabel, sumOf(priced, onGrid))
      return
    }
    const onVolume = sumOf(priced, onGrid)
    const onMoney = sumOf(priced, (r) => onGrid(r) * price(r))
    out[id] = {
      text,
      rule: `${rule}; без мережі кВт·год оцінено вартістю пального`,
      lines: sumLines(
        [
          { label: `${flowLabel} (мережа є)`, amount: onMoney, detail: times(kwh(onVolume), P(avg(onMoney, onVolume))) },
          { label: 'Без мережі (вартість пального)', amount: offMoney, detail: kwh(offVolume) },
        ],
        { label: SIGMA, amount: total },
        uah,
      ),
      note: unpricedNote,
    }
  }
  const importOf = (r: Row) => r.economics.importPriceUahPerKwh
  moneyRow(
    'revenue_pv_export',
    'Що СЕС виручила, віддавши енергію в мережу.',
    'Σ (СЕС → Мережа × ціна експорту за годину)',
    'СЕС → Мережа',
    sumOf(priced, (r) => r.economics.pvToGrid),
  )
  moneyRowOffgrid(
    'revenue_pv_self',
    'Скільки об’єкт не доплатив мережі, бо спожив СЕС сам.',
    'Σ (СЕС → Споживання × ціна імпорту за годину)',
    'СЕС → Споживання',
    (r) => r.economics.pvToLoad - (r.gen?.accounted ? r.gen.pvToLoadOffgrid : 0),
    (r) => (r.gen?.accounted ? r.gen.pvToLoadOffgrid : 0),
    (r) => (r.gen?.accounted ? r.gen.pvOffgridValueUah : 0),
    importOf,
  )
  moneyRow(
    'revenue_ess_export',
    'Що УЗЕ виручило, віддавши енергію в мережу.',
    'Σ (УЗЕ → Мережа × ціна експорту за годину)',
    'УЗЕ → Мережа',
    sumOf(priced, (r) => r.flow.essToGrid),
  )
  moneyRowOffgrid(
    'revenue_ess_self',
    'Скільки об’єкт не доплатив мережі, бо спожив розряд УЗЕ.',
    'Σ (УЗЕ → Споживання × ціна імпорту за годину)',
    'УЗЕ → Споживання',
    (r) => r.flow.essToLoad - (r.gen?.accounted ? r.gen.essToLoadOffgrid : 0),
    (r) => (r.gen?.accounted ? r.gen.essToLoadOffgrid : 0),
    (r) => (r.gen?.accounted ? r.gen.essOffgridValueUah : 0),
    importOf,
  )
  moneyRow(
    'expense_grid_charge',
    'Скільки заплачено мережі за енергію, яка пішла на заряд УЗЕ.',
    'Σ (Мережа → УЗЕ × ціна імпорту за годину)',
    'Мережа → УЗЕ',
    sumOf(priced, (r) => r.flow.gridToEss),
  )
  if (has('expense_gen_charge')) {
    const perHour = accounted.some((r) => r.gen?.fuelMethod === 'per_hour')
    moneyRow(
      'expense_gen_charge',
      'Пальне, яке пішло на заряд УЗЕ від генератора.',
      'Σ (Генератор → УЗЕ × вартість пального за кВт·год)',
      'Генератор → УЗЕ',
      sumOf(priced, (r) => (r.gen?.accounted ? r.gen.genToEss : 0)),
      perHour ? 'За годину роботи заряд від генератора вважається безкоштовним.' : undefined,
    )
  }
  set('revenue_total', {
    text: 'Усе, що принесли СЕС і УЗЕ: продаж у мережу і чужа енергія, якої не довелося купувати.',
    rule: 'Дохід = СЕС → мережа + СЕС → споживання + УЗЕ → мережа + УЗЕ → споживання',
    lines: sumLines(
      [
        { label: 'СЕС → мережа', amount: S('revenue_pv_export') },
        { label: 'СЕС → споживання', amount: S('revenue_pv_self') },
        { label: 'УЗЕ → мережа', amount: S('revenue_ess_export') },
        { label: 'УЗЕ → споживання', amount: S('revenue_ess_self') },
      ],
      { label: SIGMA, amount: S('revenue_total') },
      uah,
    ),
    note: unpricedNote,
  })
  set('expense_total', {
    text: 'Усе, що доплачено, щоб зарядити УЗЕ.',
    rule: has('expense_gen_charge') ? 'Витрати = заряд УЗЕ із мережі + заряд УЗЕ від генератора' : 'Витрати = заряд УЗЕ із мережі',
    lines: sumLines(
      [
        { label: 'Заряд УЗЕ із мережі', amount: S('expense_grid_charge') },
        ...(has('expense_gen_charge') ? [{ label: 'Заряд УЗЕ від генератора', amount: S('expense_gen_charge') }] : []),
      ],
      { label: SIGMA, amount: S('expense_total') },
      uah,
    ),
    note: unpricedNote,
  })
  set('ebitda', {
    text: 'Прибуток від СЕС і УЗЕ за годину до зносу батареї.',
    rule: 'EBITDA = Дохід всього − Витрати всього',
    lines: sumLines(
      [
        { label: 'Дохід всього', amount: S('revenue_total') },
        { label: 'Витрати всього', amount: -S('expense_total') },
      ],
      { label: SIGMA, amount: S('ebitda') },
      uah,
    ),
    note: `Без операційних витрат і без зносу УЗЕ: економічний ефект = EBITDA − знос УЗЕ.${unpricedNote ? ` ${unpricedNote}` : ''}`,
  })

  // ── УЗЕ cost basis ──────────────────────────────────────────────────
  if (has('ess_withdrawn_cost')) {
    const list = priced.filter((r) => r.essWithdrawnCostUah != null)
    const toLoad = sumOf(list, (r) => r.flow.essToLoad)
    const toGrid = sumOf(list, (r) => r.flow.essToGrid)
    const total = S('ess_withdrawn_cost')
    set('ess_withdrawn_cost', {
      text: 'Скільки коштувала в УЗЕ енергія, яку воно віддало цієї години — вартість розряду за ціною закладення.',
      rule: 'Σ ((УЗЕ → Споживання + УЗЕ → Мережа) × собівартість УЗЕ на середину години)',
      lines: [
        { label: 'Розряд УЗЕ', value: kwh(toLoad + toGrid), detail: `${kwh(toLoad)} → споживання + ${kwh(toGrid)} → мережа` },
        { op: '×', label: 'Середня собівартість', value: P(avg(total, toLoad + toGrid)), detail: 'Σ грн ÷ Σ кВт·год' },
        { op: '=', label: SIGMA, value: uah(total) },
      ],
      note: 'Собівартість береться після заряду цієї години (середина), тому відрізняється від рядка «Собівартість УЗЕ» — він на початок години.',
    })
  }
  if (has('ess_realized')) {
    const list = priced.filter((r) => r.essRealizedProfitUah != null)
    const total = S('ess_realized')
    const revenue = S('revenue_ess_export') + S('revenue_ess_self')
    const withdrawn = S('ess_withdrawn_cost')
    const wear = revenue - withdrawn - total
    const dis = sumOf(list, (r) => r.flow.essDischarged)
    const rate = tariffs ? money.fromUah(tariffs.degradationUahPerKwh) : null
    const wearDetail =
      rate !== null && Math.abs(wear - dis * rate) <= Math.max(money.fromUah(1), Math.abs(wear) * 0.02)
        ? times(kwh(dis), P(rate))
        : kwh(dis)
    set('ess_realized', {
      text: 'Скільки УЗЕ реально заробило за годину: виручка розряду мінус те, що коштувала в батареї енергія, і знос.',
      rule: 'Ефект = УЗЕ → мережа + УЗЕ → споживання − Списано з УЗЕ − знос',
      lines: sumLines(
        [
          { label: 'Дохід: УЗЕ → мережа', amount: S('revenue_ess_export') },
          { label: 'Дохід: УЗЕ → споживання', amount: S('revenue_ess_self') },
          { label: 'Списано з УЗЕ', amount: -withdrawn },
          { label: 'Знос УЗЕ', amount: -wear, detail: wearDetail },
        ],
        { label: SIGMA, amount: total },
        uah,
      ),
      note: 'Касовий погляд: розряд зіставлено з ціною закладеної енергії. Спотовий погляд — у смузі «Чистий ефект УЗЕ» над таблицею.',
    })
  }

  // ── Generator ───────────────────────────────────────────────────────
  if (has('gen_kwh')) {
    flow(
      'gen_kwh',
      'Скільки енергії видав генератор (за його лічильником).',
      'Генератор = Генератор → Споживання + Генератор → УЗЕ',
      [term('Генератор → Споживання', 'gen_to_load_gen'), term('Генератор → УЗЕ', 'gen_to_ess')],
    )
    const toLoadText = 'Частина виробітку генератора, що пішла на споживання об’єкта.'
    const toLoadRule = 'Генератор → Споживання = Генератор (виробіток) − Генератор → УЗЕ'
    const toLoadTerms = [term('Генератор (виробіток)', 'gen_kwh'), term('Генератор → УЗЕ', 'gen_to_ess', -1)]
    flow('gen_to_load', toLoadText, toLoadRule, toLoadTerms)
    flow('gen_to_load_gen', toLoadText, toLoadRule, toLoadTerms)
    flow(
      'load_offgrid',
      'Скільки об’єкт спожив у хвилини без мережі (генератор, острів, без живлення).',
      'Навантаження без мережі = Генератор → Споживання + СЕС без мережі + УЗЕ без мережі',
      [
        term('Генератор → Споживання', 'gen_to_load_gen'),
        { label: 'СЕС → Споживання без мережі', amount: sumOf(accounted, (r) => r.gen?.pvToLoadOffgrid ?? 0) },
        { label: 'УЗЕ → Споживання без мережі', amount: sumOf(accounted, (r) => r.gen?.essToLoadOffgrid ?? 0) },
      ],
    )
    const modes: Array<[string, string, string]> = [
      ['minutes_generator', 'генератор працював і живив об’єкт', 'Робота генератора'],
      ['minutes_island', 'об’єкт жив від УЗЕ (і СЕС) без мережі й генератора', 'Острів'],
      ['minutes_blackout', 'живлення не було', 'Без живлення'],
    ]
    for (const [id, what, label] of modes) {
      const m = metrics[id]
      if (!m) continue
      const hours = live.filter((r) => (m.pick(r) ?? 0) > 0)
      const total = S(id)
      set(id, {
        text: `Хвилини доби, коли ${what}. Решту часу об’єкт жив від мережі.`,
        rule: 'Σ хвилин по годинах цього режиму',
        lines: [
          { label: 'Годин із цим режимом', value: String(hours.length) },
          { op: '×', label: 'Середньо хвилин у такій годині', value: String(Math.round(avg(total, hours.length))) },
          { op: '=', label: `${label}, ${SIGMA}`, value: `${Math.round(total)} хв`, detail: formatDuration(total) },
        ],
      })
    }
    const method = accounted.find((r) => r.gen?.fuelMethod)?.gen?.fuelMethod ?? tariffs?.genFuelMethod
    const perHour = method === 'per_hour'
    const baseVolume = perHour ? sumOf(accounted, (r) => (r.gen?.minutes.generator ?? 0) / 60) : sumOf(accounted, (r) => r.gen?.genKwh ?? 0)
    const baseLabel = perHour ? 'Годин роботи генератора' : 'Генератор (виробіток)'
    const baseValue = perHour ? `${energyFmt.format(baseVolume)} год` : kwh(baseVolume)
    const litres = S('gen_fuel_l')
    const litresRate = avg(litres, baseVolume)
    const tariffLitres = tariffs ? (perHour ? tariffs.genFuelConsumptionLPerH : tariffs.genFuelConsumptionLPerKwh) : null
    const litreUnit = perHour ? 'л/год' : 'л/кВт·год'
    set('gen_fuel_l', {
      text: 'Скільки пального витратив генератор за годину.',
      rule: perHour ? 'Пальне = години роботи генератора × витрата, л/год' : 'Пальне = виробіток генератора × витрата, л/кВт·год',
      lines: [
        { label: baseLabel, value: baseValue },
        {
          op: '×',
          label: 'Витрата',
          value: `${factorFmt.format(litresRate)} ${litreUnit}`,
          detail: tariffLitres !== null && Math.abs(tariffLitres - litresRate) > 0.001 ? 'середня за добу' : 'з тарифу',
        },
        { op: '=', label: SIGMA, value: `${energyFmt.format(litres)} л` },
      ],
      note: 'Лише години, пораховані з генератором.',
    })
    const cost = S('gen_fuel_cost')
    const unitCost = avg(cost, baseVolume)
    const tariffCost = tariffs ? money.fromUah(fuelCostUah(tariffs)) : null
    set('gen_fuel_cost', {
      text: 'Скільки коштувало пальне, яке витратив генератор за годину.',
      rule: perHour
        ? 'Вартість = години роботи × (ціна літра × витрата л/год × ПДВ)'
        : 'Вартість = виробіток генератора × (ціна літра × витрата л/кВт·год × ПДВ)',
      lines: [
        { label: baseLabel, value: baseValue },
        {
          op: '×',
          label: 'Вартість пального',
          value: perHour ? `${uah(unitCost)}/год` : `${P(unitCost)} ${unit}`,
          detail:
            tariffs && tariffCost !== null && Math.abs(tariffCost - unitCost) <= Math.max(0.005, unitCost * 0.01)
              ? `${P(money.fromUah(tariffs.genFuelPriceUahPerL))} за літр × ${factorFmt.format(tariffLitres ?? 0)} ${litreUnit}${tariffs.includeVat ? ' × ПДВ' : ''}`
              : 'середня за добу',
        },
        { op: '=', label: SIGMA, value: uah(cost) },
      ],
      note: 'Лише години, пораховані з генератором.',
    })
  }

  return out
}

// essStripFormula explains the «Чистий ефект УЗЕ» strip over the table:
// the same spot view as the hourly bars, summed over the day's priced
// hours into the Σ that the strip shows beside its label.
export function essStripFormula(rows: Rows, tariffs: Tariffs | null, money: Money): Formula {
  const P = (v: number) => money.formatPriceAllowZero(v)
  const avg = (amount: number, volume: number) => (volume > 0 ? amount / volume : 0)
  const priced = rows.filter((r): r is Row => r !== null && r.rdnUahPerKwh !== null)
  const off = (r: Row) => r.gen?.accounted
  const total = sumOf(priced, (r) => r.economics.essNet)
  const onLoad = (r: Row) => r.flow.essToLoad - (off(r) ? r.gen?.essToLoadOffgrid ?? 0 : 0)
  const legs: Array<{ label: string; kwh: number; amount: number }> = [
    {
      label: 'Розряд у споживання',
      kwh: sumOf(priced, (r) => r.flow.essToLoad),
      amount: sumOf(priced, (r) => onLoad(r) * r.economics.importPriceUahPerKwh + (off(r) ? r.gen?.essOffgridValueUah ?? 0 : 0)),
    },
    {
      label: 'Розряд у мережу',
      kwh: sumOf(priced, (r) => r.flow.essToGrid),
      amount: sumOf(priced, (r) => r.flow.essToGrid * r.economics.exportPriceUahPerKwh),
    },
    {
      label: 'Заряд із мережі',
      kwh: sumOf(priced, (r) => r.flow.gridToEss),
      amount: -sumOf(priced, (r) => r.flow.gridToEss * r.economics.importPriceUahPerKwh),
    },
    {
      label: 'Заряд від СЕС (недоотриманий експорт)',
      kwh: sumOf(priced, (r) => r.flow.pvToEss - (off(r) ? r.gen?.pvToEssOffgrid ?? 0 : 0)),
      amount: -sumOf(priced, (r) => (r.flow.pvToEss - (off(r) ? r.gen?.pvToEssOffgrid ?? 0 : 0)) * r.economics.exportPriceUahPerKwh),
    },
    {
      label: 'Заряд від генератора (пальне)',
      kwh: sumOf(priced, (r) => (off(r) ? r.gen?.genToEss ?? 0 : 0)),
      amount: -sumOf(priced, (r) => (off(r) ? r.gen?.chargeCostUah ?? 0 : 0)),
    },
  ]
  const terms: FormulaTerm[] = legs
    .filter((l) => l.kwh > 0 || Math.abs(l.amount) > 0)
    .map((l) => ({
      label: l.label,
      amount: l.amount,
      detail: l.label.startsWith('Заряд від генератора') ? kwh(l.kwh) : times(kwh(l.kwh), P(Math.abs(avg(l.amount, l.kwh)))),
    }))
  const wear = residual(total, terms.map((t) => t.amount))
  const dis = sumOf(priced, (r) => r.flow.essDischarged)
  const rate = tariffs ? money.fromUah(tariffs.degradationUahPerKwh) : null
  terms.push({
    label: 'Знос УЗЕ',
    amount: wear,
    detail:
      rate !== null && Math.abs(wear + dis * rate) <= Math.max(money.fromUah(1), Math.abs(wear) * 0.02)
        ? times(kwh(dis), P(rate))
        : kwh(dis),
  })
  return {
    text: 'Чистий ефект УЗЕ за годину в цінах тієї ж години: стовпчик угору — УЗЕ принесло, униз — коштувало. Σ — сума стовпчиків за добу.',
    rule: 'Розряд у споживання × імпорт + розряд у мережу × експорт − заряд із мережі × імпорт − заряд від СЕС × експорт − знос',
    lines: sumLines(terms, { label: SIGMA, amount: total }, (v) => money.format(v)),
    note: 'Години заряду мінусові, розряду — плюсові. Касовий погляд — рядок «Реалізований ефект УЗЕ».',
  }
}
