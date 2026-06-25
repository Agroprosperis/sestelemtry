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
export type PeriodScope = 'month' | 'year'

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
