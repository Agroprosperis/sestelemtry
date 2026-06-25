// Shared, non-component helpers for the monthly + annual economics
// rollup views. They live outside the component files so React Fast
// Refresh keeps working (a component module must export only
// components) and so both views share one source of truth.
import type { EconomicsMonthlyTotals } from '../../api'
import { formatMwh, formatPercent, formatPrice, formatUah } from './format'

// signedUah prints an explicit +/− prefix based on the value's sign,
// using the absolute amount so the sign is never doubled (e.g. a
// negative delta renders "−123 грн", a positive one "+123 грн").
export function signedUah(delta: number): string {
  const sign = delta < 0 ? '−' : '+'
  return `${sign}${formatUah(Math.abs(delta))}`
}

// signClass tints a currency cell green/red by sign so a loss-making
// period (negative EBITDA / effect) is never shown in the "good" colour.
export function signClass(v: number): string {
  return v >= 0 ? 'cell-positive' : 'cell-negative'
}

// PeriodScope lets the shared rollup blocks (KPIs / Finance / Waterfall /
// Balance / narrative) read identically for the month and year views,
// swapping only the period wording.
export type PeriodScope = 'month' | 'year' | 'period'

type PeriodWords = {
  // "за місяць" / "за рік" — appended to amounts and chart units.
  per: string
  // genitive "місяця" / "року" — used in "Ключові показники <of>".
  of: string
  // nominative "місяць" / "рік" — used in the narrative sentences.
  noun: string
}

export const PERIOD_WORDS: Record<PeriodScope, PeriodWords> = {
  month: { per: 'за місяць', of: 'місяця', noun: 'місяць' },
  year: { per: 'за рік', of: 'року', noun: 'рік' },
  // Sliding window that isn't a full calendar year.
  period: { per: 'за період', of: 'періоду', noun: 'період' },
}

export type Narrative = {
  title: string
  howItWent: string
  mainReserve: string
  toImprove: string
}

type ReserveCause = 'timing' | 'soc' | 'pv'

const RESERVE_CAUSE_TEXT: Record<ReserveCause, string> = {
  timing: 'розряд не завжди потрапляв у найдорожчі години',
  soc: 'бракувало заряду батареї перед вечірнім піком',
  pv: 'частина надлишку СЕС не потрапила в УЗЕ',
}

const RESERVE_IMPROVE_TEXT: Record<ReserveCause, string> = {
  timing: 'зміщувати розряд УЗЕ ближче до вечірнього піку цін',
  soc: 'тримати вищий SOC перед вечірнім піком',
  pv: 'більше заряджати УЗЕ надлишком СЕС вдень',
}

export function dominantReserveCause(totals: EconomicsMonthlyTotals): ReserveCause {
  const causes: { key: ReserveCause; uah: number }[] = [
    { key: 'timing', uah: totals.ess_reserve_timing_uah },
    { key: 'soc', uah: totals.ess_reserve_soc_uah },
    { key: 'pv', uah: totals.ess_reserve_pv_uah },
  ]
  return causes.reduce((best, c) => (c.uah > best.uah ? c : best), causes[0]).key
}

// buildNarrative turns a period's totals into four deterministic
// sentences. Branching keys off profitability and whether the site is
// export-heavy (more energy exported than consumed) plus the dominant
// ESS reserve cause. periodNoun swaps "місяць" / "рік" for the views.
export function buildNarrative(
  totals: EconomicsMonthlyTotals,
  heading: string,
  periodNoun = 'місяць',
): Narrative {
  const profitable = totals.effect_uah >= 0
  const exportHeavy = totals.grid_export_kwh > totals.load_kwh
  const cause = dominantReserveCause(totals)
  const priceGap = totals.avg_import_price_uah_per_kwh - totals.avg_export_price_uah_per_kwh

  let title: string
  if (!profitable) {
    title = `${heading}: ${periodNoun} збитковий — ефект ${formatUah(totals.effect_uah)}.`
  } else if (exportHeavy) {
    title = `${heading}: ${periodNoun} прибутковий, але більшість СЕС пішла в експорт, а не в економію об'єкта.`
  } else {
    title = `${heading}: ${periodNoun} прибутковий, основний ефект — від власного споживання СЕС та УЗЕ.`
  }

  const howItWent =
    `СЕС виробила ${formatMwh(totals.pv_kwh)}, об'єкт спожив ${formatMwh(totals.load_kwh)}. ` +
    `Імпорт ${formatMwh(totals.grid_import_kwh)}, експорт ${formatMwh(totals.grid_export_kwh)}. ` +
    `Ефект проєкту: ${formatUah(totals.effect_uah)}.`

  const captureTxt =
    totals.ess_optimum_uah > 0 ? `захоплено ${formatPercent(totals.ess_captured_share)}` : 'оцінка за фактом'
  let mainReserve = `Підтверджений резерв УЗЕ ${formatUah(totals.ess_reserve_uah)} (${captureTxt}). Головна причина — ${RESERVE_CAUSE_TEXT[cause]}.`
  if (exportHeavy) {
    mainReserve +=
      ` Об'єкт масово експортує денну СЕС (${formatMwh(totals.grid_export_kwh)}) по ~${formatPrice(totals.avg_export_price_uah_per_kwh)} грн/кВт·год ` +
      `при імпорті ~${formatPrice(totals.avg_import_price_uah_per_kwh)} — перенесення споживання в денні години дає різницю ~${formatPrice(priceGap)} грн/кВт·год.`
  }

  const improveParts: string[] = []
  if (exportHeavy) {
    improveParts.push('підняти денне навантаження в години 10:00–16:00, коли власна СЕС інакше йде в мережу')
  }
  improveParts.push(RESERVE_IMPROVE_TEXT[cause])
  const toImprove =
    improveParts
      .map((p, i) => (i === 0 ? p.charAt(0).toUpperCase() + p.slice(1) : p))
      .join('; ') + '.'

  return { title, howItWent, mainReserve, toImprove }
}

// uahShort renders a compact "N тис. ₴" / "N млн ₴" amount for the AI
// impact chips; small amounts fall back to the full formatter.
export function uahShort(v: number): string {
  const a = Math.abs(v)
  if (a >= 1_000_000) {
    return `${(v / 1_000_000).toLocaleString('uk-UA', { maximumFractionDigits: 1 })} млн ₴`
  }
  if (a >= 1000) {
    return `${Math.round(v / 1000).toLocaleString('uk-UA')} тис. ₴`
  }
  return formatUah(v)
}

// AI panel model — the deterministic management narrative shared by the
// month and year views. All text is built from already-computed figures.
export type AiBriefRow = { kind: 'summary' | 'reserve' | 'action'; label: string; text: string }
export type AiResultRow = { label: string; value: string; amber?: boolean }
export type AiCardVariant = 'plan' | 'warn' | 'context'
export type AiCard = {
  variant: AiCardVariant
  status: string
  title: string
  impact: string
  action: string
  chips: string[]
}
export type AiReserveSplit = { elevator: number; ess: number; total: number }
export type AiPanel = {
  summaryLine: string
  briefRows: AiBriefRow[]
  sources: string[]
  weatherNote?: string
  result: AiResultRow[]
  cards: AiCard[]
  reserves: AiReserveSplit
}

// reserveSplit estimates the two headline opportunities: shifting
// flexible daytime load to consume PV that is currently exported
// (elevator schedule) valued at the import–export price gap, and the
// confirmed ESS dispatch reserve from the optimum model.
export function reserveSplit(totals: EconomicsMonthlyTotals): AiReserveSplit {
  const priceGap = Math.max(0, totals.avg_import_price_uah_per_kwh - totals.avg_export_price_uah_per_kwh)
  const shiftableKwh = Math.min(totals.pv_to_grid_kwh, totals.grid_to_load_kwh)
  const elevator = shiftableKwh * priceGap
  const ess = Math.max(0, totals.ess_reserve_uah)
  return { elevator, ess, total: elevator + ess }
}

// buildAiPanel composes the full AI narrative for a period. periodLabel
// is the human period ("Травень 2026" / "Липень 2025 — Травень 2026"),
// weakest is the weakest day (month view) used for the context card, and
// monthsCount feeds the year summary / sources.
export function buildAiPanel(
  totals: EconomicsMonthlyTotals,
  opts: {
    heading: string
    scope: PeriodScope
    periodLabel: string
    weakest?: { label: string; sub: string } | null
    monthsCount?: number
  },
): AiPanel {
  const r = reserveSplit(totals)
  const exportHeavy = totals.grid_export_kwh > totals.load_kwh
  const consumption = totals.pv_to_load_kwh + totals.ess_to_load_kwh + totals.grid_to_load_kwh
  const captured = totals.ess_optimum_uah > 0 ? totals.ess_captured_share : 0

  if (opts.scope !== 'month') {
    const months = opts.monthsCount ?? 0
    return {
      summaryLine: `${opts.heading}: за ${months} міс. телеметрії СЕС ${formatMwh(totals.pv_kwh)}, ефект проєкту ${formatUah(totals.effect_uah)}.`,
      briefRows: [
        {
          kind: 'summary',
          label: 'Як пройшов період',
          text: `СЕС ${formatMwh(totals.pv_kwh)}, споживання ${formatMwh(consumption)}, імпорт ${formatMwh(totals.grid_import_kwh)} / експорт ${formatMwh(totals.grid_export_kwh)}. Розрахунок за архівом FusionSolar + РДН.`,
        },
        {
          kind: 'reserve',
          label: 'Головний резерв',
          text: `Перенесення гнучких робіт на денні години — ${uahShort(r.elevator)} за період. Помісячна деталізація нижче.`,
        },
      ],
      sources: ['FusionSolar / SmartLogger', 'РДН: Оператор ринку', `${months} місячних зрізів`],
      weatherNote:
        'Архів FusionSolar: повна телеметрія СЕС/УЗЕ/мережі за весь період. Можливі невеликі пробіли після імпорту.',
      result: [
        { label: 'Період аналізу', value: opts.periodLabel },
        { label: 'Резерв графіка', value: uahShort(r.elevator), amber: true },
        { label: 'Резерв таймінгу УЗЕ', value: uahShort(r.ess) },
        { label: 'Сумарний резерв', value: uahShort(r.total) },
      ],
      cards: [
        {
          variant: 'plan',
          status: 'найбільший резерв',
          title: 'Графік робіт елеватора',
          impact: uahShort(r.elevator),
          action: `За період експортовано ${formatMwh(totals.grid_export_kwh)} і куплено ${formatMwh(totals.grid_import_kwh)} — велика частина СЕС пішла в мережу замість денного навантаження.`,
          chips: [`експорт ${formatMwh(totals.grid_export_kwh)}`, `імпорт ${formatMwh(totals.grid_import_kwh)}`],
        },
        {
          variant: 'warn',
          status: 'режим УЗЕ',
          title: 'Таймінг батареї',
          impact: uahShort(r.ess),
          action:
            totals.ess_optimum_uah > 0
              ? `УЗЕ захопила ${formatPercent(captured)} доступного оптимуму. Основні втрати — на переходах між датами, коли розряд потрапляв у нижчі години РДН.`
              : 'Недостатньо активності УЗЕ за період для оцінки оптимуму.',
          chips: [`резерв ${uahShort(r.ess)}`, `розряд ${formatMwh(totals.ess_discharged_kwh)}`],
        },
      ],
      reserves: r,
    }
  }

  // Month scope.
  const reserveText = exportHeavy
    ? `Високий експорт СЕС при низькому денному навантаженні — перенесення гнучких робіт дає до ${uahShort(r.elevator)} за місяць.`
    : `Головний резерв — таймінг УЗЕ ${uahShort(r.ess)}: розряд не завжди потрапляв у найдорожчі години.`
  const actionText = exportHeavy
    ? `Пріоритет — графік елеватора в сонячні години. Резерв таймінгу УЗЕ — ${uahShort(r.ess)}.`
    : `Зміщувати розряд УЗЕ ближче до вечірнього піку. Резерв графіка робіт — ${uahShort(r.elevator)}.`

  const cards: AiCard[] = [
    {
      variant: 'plan',
      status: 'найбільший резерв',
      title: 'Графік робіт елеватора',
      impact: `до ${uahShort(r.elevator)}`,
      action: `За місяць експортовано ${formatMwh(totals.grid_export_kwh)} при імпорті ${formatMwh(totals.grid_import_kwh)} — велика частина СЕС пішла в мережу.`,
      chips: [
        `експорт ${formatMwh(totals.grid_export_kwh)}`,
        `імпорт ${formatMwh(totals.grid_import_kwh)}`,
        ...(opts.weakest ? [`${opts.weakest.label}: ${opts.weakest.sub}`] : []),
      ],
    },
    {
      variant: 'warn',
      status: 'режим УЗЕ',
      title: 'Добовий план батареї',
      impact: `≈${uahShort(r.ess)}`,
      action:
        'Резерв таймінгу розряду — ковзне планування на 24–36 годин, а не окремими календарними днями.',
      chips: [`резерв ≈${uahShort(r.ess)}`, `розряд ${formatMwh(totals.ess_discharged_kwh)}`],
    },
  ]
  if (opts.weakest) {
    cards.push({
      variant: 'context',
      status: 'контекст',
      title: 'Слабкий день СЕС',
      impact: `${opts.weakest.label} · низька СЕС`,
      action: `Найслабший день за ефектом — ${opts.weakest.label} (${opts.weakest.sub}). Деталі в таблиці внизу.`,
      chips: [],
    })
  }

  return {
    summaryLine: `${opts.heading}: СЕС ${formatMwh(totals.pv_kwh)}, ефект ${formatUah(totals.effect_uah)}, експорт ${formatMwh(totals.grid_export_kwh)}.`,
    briefRows: [
      {
        kind: 'summary',
        label: 'Як пройшов місяць',
        text: `СЕС ${formatMwh(totals.pv_kwh)}, імпорт ${formatMwh(totals.grid_import_kwh)} / експорт ${formatMwh(totals.grid_export_kwh)}. Розрахунок за FusionSolar + РДН.`,
      },
      { kind: 'reserve', label: 'Головний резерв', text: reserveText },
      { kind: 'action', label: 'Що покращити', text: actionText },
    ],
    sources: ['телеметрія SmartLogger', 'РДН: Оператор ринку', 'погодний архів'],
    result: [
      { label: 'Період аналізу', value: opts.periodLabel },
      { label: 'Основний резерв', value: `до ${uahShort(r.elevator)}`, amber: true },
      { label: 'УЗЕ підтверджено', value: `≈${uahShort(r.ess)}` },
      { label: 'Разом до дій', value: `до ${uahShort(r.total)}` },
    ],
    cards,
    reserves: r,
  }
}

// heatTier maps an ESS discharge margin (UAH/kWh) to its heatmap colour
// tier; null / non-finite values render an empty cell.
export function heatTier(v: number | null): string {
  if (v === null || !Number.isFinite(v)) return 'economics-hm-empty'
  if (v < 2) return 'economics-hm0'
  if (v < 6) return 'economics-hm1'
  if (v < 12) return 'economics-hm2'
  if (v < 18) return 'economics-hm3'
  return 'economics-hm4'
}

export const HOURS = Array.from({ length: 24 }, (_, h) => h)
