// Shared formatters for economics figures. Kept in one module so every
// view that reports currency / energy / prices — the monthly page and
// the dashboard's daily recommendation alike — renders them identically.

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

// The *Number variants drop the unit for dense tables, where repeating
// "кВт·год"/"грн"/"%" in every cell costs more width than the numbers
// themselves; those tables carry the unit in the column header instead.
export function formatKwhNumber(kwh: number): string {
  if (!Number.isFinite(kwh)) return '—'
  return kwhFmt.format(Math.round(kwh))
}

export function formatUahNumber(v: number): string {
  if (!Number.isFinite(v)) return '—'
  return uahFmt.format(Math.round(v))
}

export function formatPercentNumber(v: number): string {
  if (!Number.isFinite(v)) return '—'
  return mwhFmt.format(v * 100)
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

// MONTHS_SHORT_UK are the three-letter month abbreviations used for the
// annual trend's 12 columns and the per-month table.
const MONTHS_SHORT_UK = [
  'Січ', 'Лют', 'Бер', 'Кві', 'Тра', 'Чер',
  'Лип', 'Сер', 'Вер', 'Жов', 'Лис', 'Гру',
]

// formatMonthShort turns YYYY-MM into "Січ" for compact axis ticks.
export function formatMonthShort(month: string): string {
  const m = /^(\d{4})-(\d{2})$/.exec(month)
  if (!m) return month
  const idx = Number(m[2]) - 1
  if (idx < 0 || idx >= 12) return month
  return MONTHS_SHORT_UK[idx]
}

// formatMonthName turns YYYY-MM into the full capitalised month name
// (without the year) for the annual per-month table rows.
export function formatMonthName(month: string): string {
  const m = /^(\d{4})-(\d{2})$/.exec(month)
  if (!m) return month
  const idx = Number(m[2]) - 1
  if (idx < 0 || idx >= 12) return month
  return MONTHS_UK[idx]
}

// formatYearTitle turns YYYY into "РРРР рік" for the page subtitle.
export function formatYearTitle(period: string): string {
  return /^\d{4}$/.test(period) ? `${period} рік` : period
}

// formatPeriodTitle renders an annual window title from its first/last
// month (YYYY-MM): a full Jan–Dec span of one year reads "РРРР рік",
// any other window reads "Лип 2025 — Трав 2026".
export function formatPeriodTitle(from: string, to: string): string {
  const f = /^(\d{4})-(\d{2})$/.exec(from)
  const t = /^(\d{4})-(\d{2})$/.exec(to)
  if (!f || !t) return ''
  if (f[1] === t[1] && f[2] === '01' && t[2] === '12') return `${f[1]} рік`
  const label = (m: RegExpExecArray) => `${MONTHS_SHORT_UK[Number(m[2]) - 1]} ${m[1]}`
  return `${label(f)} — ${label(t)}`
}
