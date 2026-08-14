// Sheet models for the "Вивантажити в Excel" buttons.
//
// Each builder mirrors the columns of the table it belongs to but keeps
// every measured quantity as a raw number, so Excel can sum, sort and
// re-format them. Units live in the header (the table shows them the
// same way) and each view contributes its own totals row — the number an
// operator most often wants and the one the old HTML export dropped.

import type {
  EconomicsAnnualMonthRollup,
  EconomicsMonthlyDay,
  EconomicsMonthlyTotals,
} from '../api'
import { formatOrganizationLabel } from '../dashboard/config'
import { formatMonthName, formatMonthTitle } from './monthly/format'
import { importPriceUahPerKwh, unitCostUahPerKwh } from './monthly/rollup'
import { dateSerial, type XlsxColumn, type XlsxFormat, type XlsxSheet } from './xlsx'

// header joins a label with its unit the way the on-screen tables do.
function header(label: string, unit?: string): string {
  return unit ? `${label}, ${unit}` : label
}

// tabName names the sheet after the period and the elevator, so a
// workbook that got renamed or forwarded still says what it holds.
// Excel caps a tab at 31 characters; when both do not fit, the period
// wins — a half-cut elevator name would be worse than none.
function tabName(period: string, orgLabel: string): string {
  const combined = `${period} · ${orgLabel}`
  return combined.length <= 31 ? combined : period
}

function col(label: string, unit: string | undefined, format: XlsxFormat): XlsxColumn {
  return { header: header(label, unit), format }
}

// nullIfNaN keeps derived ratios (prices per kWh) out of the sheet when
// their denominator was zero, leaving an empty cell instead of a NaN.
function nullIfNaN(v: number): number | null {
  return Number.isFinite(v) ? v : null
}

const MONTH_COLUMNS: XlsxColumn[] = [
  col('Дата', undefined, 'date'),
  col('СЕС', 'кВт·год', 'int'),
  col('Споживання', 'кВт·год', 'int'),
  col('Імпорт', 'кВт·год', 'int'),
  col('Вартість імпорту', 'грн', 'money'),
  col('Ціна імпорту', 'грн/кВт·год', 'price'),
  col('Факт. ціна', 'грн/кВт·год', 'price'),
  col('РДН сер.', 'грн/кВт·год', 'price'),
  col('Експорт', 'кВт·год', 'int'),
  col('Самоспоживання СЕС', '%', 'percent'),
  col('УЗЕ цикли', 'екв.', 'ratio'),
  col('EBITDA', 'грн', 'money'),
  // The screen shows this as an amber "!" on the date; a spreadsheet has
  // nowhere to hover, so the diagnosis gets its own column.
  col('Якість даних', undefined, 'text'),
]

function monthDayValues(d: EconomicsMonthlyDay, note: string | null) {
  const pvSelf = d.pv_to_load_kwh + d.pv_to_ess_kwh
  return [
    dateSerial(d.date),
    d.pv_kwh,
    d.load_kwh,
    d.grid_import_kwh,
    d.import_cost_uah,
    nullIfNaN(importPriceUahPerKwh(d.import_cost_uah, d.grid_import_kwh)),
    nullIfNaN(unitCostUahPerKwh(d.import_cost_uah, d.load_kwh)),
    d.rdn_avg_uah_per_kwh,
    d.grid_export_kwh,
    d.pv_kwh > 0 ? pvSelf / d.pv_kwh : null,
    d.equivalent_cycles,
    d.ebitda_uah,
    note,
  ]
}

// monthDetailSheet is the day-by-day breakdown of one month.
export function monthDetailSheet(
  days: EconomicsMonthlyDay[],
  totals: EconomicsMonthlyTotals,
  month: string,
  organizationID: string,
  qualityNote: (flags?: string[]) => string | null,
): XlsxSheet {
  const pvSelf = totals.pv_to_load_kwh + totals.pv_to_ess_kwh
  return {
    name: tabName(formatMonthTitle(month), formatOrganizationLabel(organizationID)),
    columns: MONTH_COLUMNS,
    freeze: { columns: 1, rows: 1 },
    autoFilter: true,
    rows: [
      ...days.map((d) => ({ values: monthDayValues(d, qualityNote(d.quality_flags)) })),
      {
        bold: true,
        values: [
          'Разом',
          totals.pv_kwh,
          totals.load_kwh,
          totals.grid_import_kwh,
          totals.import_cost_uah,
          nullIfNaN(importPriceUahPerKwh(totals.import_cost_uah, totals.grid_import_kwh)),
          nullIfNaN(unitCostUahPerKwh(totals.import_cost_uah, totals.load_kwh)),
          totals.rdn_avg_uah_per_kwh,
          totals.grid_export_kwh,
          totals.pv_kwh > 0 ? pvSelf / totals.pv_kwh : null,
          totals.equivalent_cycles,
          totals.ebitda_uah,
          null,
        ],
      },
    ],
  }
}

const ANNUAL_COLUMNS: XlsxColumn[] = [
  col('Місяць', undefined, 'text'),
  col('Факт. ціна', 'грн/кВт·год', 'price'),
  col('РДН сер.', 'грн/кВт·год', 'price'),
  col('СЕС', 'кВт·год', 'int'),
  col('Споживання', 'кВт·год', 'int'),
  col('Імпорт', 'кВт·год', 'int'),
  col('Експорт', 'кВт·год', 'int'),
  col('Самоспоживання СЕС', '%', 'percent'),
  col('УЗЕ цикли', 'екв.', 'ratio'),
  col('EBITDA', 'грн', 'money'),
  col('Дні з приблизними даними', undefined, 'int'),
]

function annualMonthValues(o: EconomicsMonthlyTotals, label: string) {
  const pvSelf = o.pv_to_load_kwh + o.pv_to_ess_kwh
  return [
    label,
    nullIfNaN(unitCostUahPerKwh(o.import_cost_uah, o.load_kwh)),
    o.rdn_avg_uah_per_kwh,
    o.pv_kwh,
    o.load_kwh,
    o.grid_import_kwh,
    o.grid_export_kwh,
    o.pv_kwh > 0 ? pvSelf / o.pv_kwh : null,
    o.equivalent_cycles,
    o.ebitda_uah,
    o.flagged_days > 0 ? o.flagged_days : null,
  ]
}

// annualDetailSheet is the month-by-month breakdown of a year (or of any
// custom window the annual view is showing).
export function annualDetailSheet(
  months: EconomicsAnnualMonthRollup[],
  totals: EconomicsMonthlyTotals,
  periodLabel: string,
  organizationID: string,
): XlsxSheet {
  return {
    name: tabName(periodLabel, formatOrganizationLabel(organizationID)),
    columns: ANNUAL_COLUMNS,
    freeze: { columns: 1, rows: 1 },
    autoFilter: true,
    rows: [
      ...months.map((m) => ({ values: annualMonthValues(m.totals, formatMonthName(m.month)) })),
      { bold: true, values: annualMonthValues(totals, 'Разом') },
    ],
  }
}

// HourMetric is one row of the hourly pivot, flattened out of the view's
// metric definitions: 24 hourly values plus the day's aggregate.
export type HourMetric = {
  label: string
  unit: string
  format: XlsxFormat
  total: number | null
  hours: (number | null)[]
}

// dayPivotSheet mirrors the hourly table: one row per metric, one column
// per hour. Row-level formats carry the unit here, since a column spans
// every metric.
export function dayPivotSheet(
  metrics: HourMetric[],
  date: string,
  organizationID: string,
): XlsxSheet {
  const columns: XlsxColumn[] = [
    { header: 'Показник', format: 'text', width: 34 },
    { header: 'Одиниця', format: 'text', width: 12 },
    { header: 'Σ за добу', format: 'money', width: 14 },
    ...Array.from({ length: 24 }, (_, h) => ({
      header: `${String(h).padStart(2, '0')}:00`,
      width: 11,
    })),
  ]
  const dotted = /^(\d{4})-(\d{2})-(\d{2})$/.exec(date)
  return {
    name: tabName(
      dotted ? `${dotted[3]}.${dotted[2]}.${dotted[1]}` : date,
      formatOrganizationLabel(organizationID),
    ),
    columns,
    freeze: { columns: 3, rows: 1 },
    rows: metrics.map((m) => ({
      // The row format covers the numeric cells; the leading label and
      // unit stay text because the writer never number-formats a string.
      format: m.format,
      values: [m.label, m.unit, m.total, ...m.hours],
    })),
  }
}

// exportFileName builds a descriptive, filesystem-safe workbook name,
// e.g. economics-2026-07-agrodar-bar.xlsx.
export function exportFileName(scope: string, organizationID: string): string {
  const org = formatOrganizationLabel(organizationID)
    .toLowerCase()
    .replace(/[^\p{L}\p{N}]+/gu, '-')
    .replace(/^-|-$/g, '')
  return `economics-${scope}${org ? `-${org}` : ''}.xlsx`
}
