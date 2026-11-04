import { PeriodPicker } from '../../dashboard/components/PeriodPicker'
import { CURRENCIES, CURRENCY_SYMBOL, type Currency } from '../currency'
import { EconomicsPeriodPicker } from './EconomicsPeriodPicker'

const CURRENCY_NAME: Record<Currency, string> = { UAH: 'Гривні', USD: 'Долари США', EUR: 'Євро' }

// parseDateString and formatDateString translate between the
// "YYYY-MM-DD" string the economics page already keeps in state
// (and on the URL) and the Date object PeriodPicker expects. We
// construct local-time Dates so the picker's day index matches the
// operator's wall clock — using `new Date('YYYY-MM-DD')` instead
// would parse as UTC midnight and shift back a day in negative
// offsets relative to UTC.
function parseDateString(value: string): Date {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(value)
  if (!m) return new Date()
  return new Date(Number(m[1]), Number(m[2]) - 1, Number(m[3]))
}

function formatDateString(d: Date): string {
  const y = d.getFullYear()
  const mo = String(d.getMonth() + 1).padStart(2, '0')
  const da = String(d.getDate()).padStart(2, '0')
  return `${y}-${mo}-${da}`
}

// EconomicsRange is the period granularity toggle: a single day, a whole
// calendar month, a calendar year, the all-time payback page, or the
// all-objects portfolio rollup.
export type EconomicsRange = 'day' | 'month' | 'year' | 'payback' | 'portfolio'

type Props = {
  range: EconomicsRange
  onRangeChange: (next: EconomicsRange) => void
  // Portfolio granularity (month / year / custom window). Only meaningful
  // when range is 'portfolio'; it drives whether the period picker is a
  // month picker, a year picker, or the sliding-window picker so it tracks
  // the portfolio's own Місяць/Рік/Період toggle.
  portfolioScope?: 'month' | 'year' | 'period'
  // Sliding-period window (year view only), both YYYY-MM. onWindowChange
  // with empty strings resets to the calendar year of the anchor.
  windowFrom: string
  windowTo: string
  onWindowChange: (from: string, to: string) => void
  date: string
  onDateChange: (next: string) => void
  // asOf is the «Дані станом на …» line (see economicsAsOfLabel). Null
  // hides it (no data yet, or a view that has no compute stamp).
  asOf?: string | null
  // currency is what the page shows money in; rateNote (see rateNote in
  // ../currency) is the switch's hint about the rate behind the figures.
  currency?: Currency
  onCurrencyChange?: (next: Currency) => void
  rateNote?: string | null
}

export function EconomicsHeader({
  range,
  onRangeChange,
  portfolioScope = 'month',
  windowFrom,
  windowTo,
  onWindowChange,
  date,
  onDateChange,
  asOf = null,
  currency = 'UAH',
  onCurrencyChange,
  rateNote = null,
}: Props) {
  return (
    <header className="economics-header">
      <div className="economics-header-row">
        <div className="economics-range-switch" role="group" aria-label="Гранулярність періоду">
          <button
            type="button"
            className={range === 'day' ? 'active' : ''}
            aria-pressed={range === 'day'}
            onClick={() => onRangeChange('day')}
          >
            День
          </button>
          <button
            type="button"
            className={range === 'month' ? 'active' : ''}
            aria-pressed={range === 'month'}
            onClick={() => onRangeChange('month')}
          >
            Місяць
          </button>
          <button
            type="button"
            className={range === 'year' ? 'active' : ''}
            aria-pressed={range === 'year'}
            onClick={() => onRangeChange('year')}
          >
            Рік
          </button>
          <button
            type="button"
            className={range === 'payback' ? 'active' : ''}
            aria-pressed={range === 'payback'}
            onClick={() => onRangeChange('payback')}
          >
            Окупність
          </button>
          <button
            type="button"
            className={range === 'portfolio' ? 'active' : ''}
            aria-pressed={range === 'portfolio'}
            onClick={() => onRangeChange('portfolio')}
          >
            Портфель
          </button>
        </div>
        {range === 'payback' ? null : range === 'year' ||
          (range === 'portfolio' && portfolioScope === 'period') ? (
          <EconomicsPeriodPicker from={windowFrom} to={windowTo} onChange={onWindowChange} />
        ) : (
          <PeriodPicker
            preset={range === 'portfolio' ? (portfolioScope === 'year' ? 'year' : 'month') : range}
            anchor={parseDateString(date)}
            onChange={(next) => onDateChange(formatDateString(next))}
            todaySide="end"
          />
        )}
        {onCurrencyChange ? (
          <div
            className="economics-range-switch economics-currency-switch"
            role="group"
            aria-label="Валюта"
            title={rateNote ?? undefined}
          >
            {CURRENCIES.map((c) => (
              <button
                key={c}
                type="button"
                className={currency === c ? 'active' : ''}
                aria-pressed={currency === c}
                aria-label={CURRENCY_NAME[c]}
                onClick={() => onCurrencyChange(c)}
              >
                {CURRENCY_SYMBOL[c]}
              </button>
            ))}
          </div>
        ) : null}
      </div>
      {asOf && (
        <p className="economics-as-of" role="status">
          {asOf}
        </p>
      )}
    </header>
  )
}
