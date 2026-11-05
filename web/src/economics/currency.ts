import { createContext, useContext } from 'react'

// Currency is what the economics page shows money in. Economics are
// stored in UAH; for the other two the server converts each day at its
// own rate (?currency= on the economics routes), so a month or a year
// is a sum of converted days.
export type Currency = 'UAH' | 'USD' | 'EUR'

export const CURRENCIES: readonly Currency[] = ['UAH', 'USD', 'EUR']

export const CURRENCY_SYMBOL: Record<Currency, string> = { UAH: '₴', USD: '$', EUR: '€' }

// parseCurrency reads ?currency=; anything else is UAH.
export function parseCurrency(raw: string | null | undefined): Currency {
  const c = (raw ?? '').trim().toUpperCase()
  return c === 'USD' || c === 'EUR' ? c : 'UAH'
}

// UNIT follows an amount: hryvnias keep the word the page always used.
const UNIT: Record<Currency, string> = { UAH: 'грн', USD: '$', EUR: '€' }

const IN_WORDS: Record<Currency, string> = { UAH: 'у гривнях', USD: 'у доларах', EUR: 'в євро' }

const wholeFmt = new Intl.NumberFormat('uk-UA', { maximumFractionDigits: 0 })
const cents = new Intl.NumberFormat('uk-UA', { minimumFractionDigits: 2, maximumFractionDigits: 2 })
const mills = new Intl.NumberFormat('uk-UA', { minimumFractionDigits: 3, maximumFractionDigits: 3 })
const rateFmt = new Intl.NumberFormat('uk-UA', { minimumFractionDigits: 2, maximumFractionDigits: 4 })

export type Money = {
  currency: Currency
  // symbol is the compact sign (₴ $ €); unit follows a full amount
  // ("1 234 грн", "1 234 $"); priceUnit follows a per-kWh price.
  symbol: string
  unit: string
  priceUnit: string
  // inWords ends "…цей розрахунок у гривнях" in the currency's name.
  inWords: string
  // rate is UAH per unit the amounts on screen were converted at: the
  // day's own rate on the day view, elsewhere the period's rates weighted
  // by each day's EBITDA; 1 in UAH. fromUah converts by it what the page
  // still holds in UAH — the tariffs and the colour thresholds tuned in
  // hryvnias.
  rate: number
  fromUah: (uah: number) => number
  // format / formatNumber write an amount with / without its unit. UAH
  // stays in whole hryvnias; dollars and euros keep cents below 1 000, so
  // an hour worth a dollar or two does not round to nothing.
  format: (v: number) => string
  formatNumber: (v: number) => string
  // formatPrice writes a per-kWh price without its unit: two decimals in
  // UAH, three in dollars and euros. Zero reads "—" (no price), unlike
  // formatPriceAllowZero.
  formatPrice: (v: number) => string
  formatPriceAllowZero: (v: number) => string
  // formatCompactPrice is a price squeezed into a heatmap cell: whole
  // hryvnias, or cents of a dollar / euro.
  formatCompactPrice: (v: number) => string
}

export function makeMoney(currency: Currency, rate = 1): Money {
  const uah = currency === 'UAH'
  const r = !uah && Number.isFinite(rate) && rate > 0 ? rate : 1
  const unit = UNIT[currency]
  const price = uah ? cents : mills
  const formatNumber = (v: number) => {
    if (!Number.isFinite(v)) return '—'
    if (uah || Math.abs(v) >= 1000) return wholeFmt.format(Math.round(v))
    return cents.format(v)
  }
  return {
    currency,
    symbol: CURRENCY_SYMBOL[currency],
    unit,
    priceUnit: `${unit}/кВт·год`,
    inWords: IN_WORDS[currency],
    rate: r,
    fromUah: (v) => v / r,
    format: (v) => (Number.isFinite(v) ? `${formatNumber(v)} ${unit}` : '—'),
    formatNumber,
    formatPrice: (v) => (!Number.isFinite(v) || v === 0 ? '—' : price.format(v)),
    formatPriceAllowZero: (v) => (Number.isFinite(v) ? price.format(v) : '—'),
    formatCompactPrice: (v) => {
      if (!Number.isFinite(v)) return '—'
      return uah ? String(Math.round(v)) : cents.format(v)
    },
  }
}

export const UAH_MONEY = makeMoney('UAH')

// MoneyContext carries the page's money formatting down to every card;
// without a provider (tests, other pages) it is UAH.
export const MoneyContext = createContext<Money>(UAH_MONEY)

export function useMoney(): Money {
  return useContext(MoneyContext)
}

// RateOpts says which rate the visible figures were converted at: the
// day's own (day) or the period's, today's before 12:00 that may still
// move (provisional), and how many days had to borrow the nearest rate.
export type RateOpts = { day?: boolean; carriedDays?: number; provisional?: boolean }

function rateText(money: Money): string {
  return `${rateFmt.format(money.rate)} грн за 1 ${money.symbol}`
}

// rateLabel is the rate written beside the currency switch, null in UAH;
// rateNote is its longer hint.
export function rateLabel(money: Money, opts: RateOpts = {}): { caption: string; value: string } | null {
  if (money.currency === 'UAH') return null
  let caption = 'Середньозважений курс'
  if (opts.day) {
    caption = (opts.carriedDays ?? 0) > 0 ? 'Найближчий курс' : opts.provisional ? 'Поточний курс' : 'Курс доби'
  }
  return { caption, value: rateText(money) }
}

// rateNote is the currency switch's hint: which rate the visible
// figures were converted at, and how many days had to borrow one.
export function rateNote(money: Money, opts: RateOpts = {}): string | null {
  if (money.currency === 'UAH') return null
  const rate = rateText(money)
  let head = `Середньозважений курс за період (за EBITDA діб): ${rate} (АП Банк, на 12:00)`
  if (opts.day) {
    head = opts.provisional
      ? `Поточний курс ERP: ${rate} (АП Банк, остаточний — о 12:00)`
      : `Курс доби: ${rate} (АП Банк, на 12:00)`
  }
  const parts = [`${head}. Кожна доба перерахована за своїм курсом.`]
  const carried = opts.carriedDays ?? 0
  if (opts.day && carried > 0) parts.push('Власного курсу на цю дату ще немає — взято найближчий.')
  if (!opts.day && carried > 0) parts.push(`${carried} дн. без власного курсу — взято найближчий.`)
  parts.push('Тарифи, CAPEX у формі та вивантаження в Excel лишаються в гривнях.')
  return parts.join(' ')
}
