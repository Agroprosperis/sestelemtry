// Shared formatters for the monthly economics view. Kept in one module
// so every section renders currency / energy / prices identically.

const uahFmt = new Intl.NumberFormat('uk-UA', {
  style: 'decimal',
  useGrouping: true,
  minimumFractionDigits: 0,
  maximumFractionDigits: 0,
})

const mwhFmt = new Intl.NumberFormat('uk-UA', {
  minimumFractionDigits: 1,
  maximumFractionDigits: 1,
})

const kwhFmt = new Intl.NumberFormat('uk-UA', {
  minimumFractionDigits: 0,
  maximumFractionDigits: 0,
})

const percentFmt = new Intl.NumberFormat('uk-UA', {
  style: 'percent',
  minimumFractionDigits: 1,
  maximumFractionDigits: 1,
})

const priceFmt = new Intl.NumberFormat('uk-UA', {
  minimumFractionDigits: 2,
  maximumFractionDigits: 2,
})

const cyclesFmt = new Intl.NumberFormat('uk-UA', {
  minimumFractionDigits: 2,
  maximumFractionDigits: 2,
})

export function formatUah(v: number): string {
  if (!Number.isFinite(v)) return '—'
  return `${uahFmt.format(Math.round(v))} грн`
}

export function formatMwh(kwh: number): string {
  if (!Number.isFinite(kwh)) return '—'
  return `${mwhFmt.format(kwh / 1000)} МВт·год`
}

// formatMwhNumber returns just the numeric part ("60,3") so callers can
// render the unit separately (e.g. a muted "МВт·год" suffix).
export function formatMwhNumber(kwh: number): string {
  if (!Number.isFinite(kwh)) return '—'
  return mwhFmt.format(kwh / 1000)
}

export function formatKwh(kwh: number): string {
  if (!Number.isFinite(kwh)) return '—'
  return `${kwhFmt.format(Math.round(kwh))} кВт·год`
}

export function formatPercent(v: number): string {
  if (!Number.isFinite(v)) return '—'
  return percentFmt.format(v)
}

export function formatPrice(v: number): string {
  if (!Number.isFinite(v) || v === 0) return '—'
  return priceFmt.format(v)
}

export function formatCycles(v: number): string {
  if (!Number.isFinite(v)) return '—'
  return cyclesFmt.format(v)
}

export function formatShare(value: number, total: number): string {
  if (!Number.isFinite(value) || !Number.isFinite(total) || total <= 0) return '—'
  return percentFmt.format(value / total)
}

// formatDayLabel turns a YYYY-MM-DD into "DD.MM" for axis ticks and
// table rows without the UTC-midnight off-by-one of new Date().
export function formatDayLabel(iso: string): string {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(iso)
  if (!m) return iso
  return `${m[3]}.${m[2]}`
}

export function formatDayOfMonth(iso: string): string {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(iso)
  if (!m) return iso
  return m[3]
}

// formatMonthTitle turns YYYY-MM into "Місяць РРРР" (Ukrainian month
// name capitalised) for the page subtitle.
const MONTHS_UK = [
  'Січень', 'Лютий', 'Березень', 'Квітень', 'Травень', 'Червень',
  'Липень', 'Серпень', 'Вересень', 'Жовтень', 'Листопад', 'Грудень',
]

export function formatMonthTitle(month: string): string {
  const m = /^(\d{4})-(\d{2})$/.exec(month)
  if (!m) return month
  const idx = Number(m[2]) - 1
  if (idx < 0 || idx >= 12) return month
  return `${MONTHS_UK[idx]} ${m[1]}`
}
