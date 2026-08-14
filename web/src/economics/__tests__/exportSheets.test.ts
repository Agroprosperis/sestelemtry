import { describe, expect, it } from 'vitest'
import type { EconomicsMonthlyDay, EconomicsMonthlyTotals } from '../../api'
import {
  annualDetailSheet,
  dayPivotSheet,
  exportFileName,
  monthDetailSheet,
  type HourMetric,
} from '../exportSheets'
import { dateSerial } from '../xlsx'

// The sheets only read a handful of fields, so the fixtures fill those
// and cast: spelling out ~50 zeroes per record would hide what the test
// is actually about.
function day(over: Partial<EconomicsMonthlyDay>): EconomicsMonthlyDay {
  return {
    date: '2026-07-01',
    pv_kwh: 1000,
    load_kwh: 2000,
    grid_import_kwh: 1200,
    grid_export_kwh: 100,
    import_cost_uah: 6000,
    rdn_avg_uah_per_kwh: 4.65,
    equivalent_cycles: 0.8,
    ebitda_uah: 12345,
    pv_to_load_kwh: 700,
    pv_to_ess_kwh: 100,
    ...over,
  } as EconomicsMonthlyDay
}

function totals(over: Partial<EconomicsMonthlyTotals> = {}): EconomicsMonthlyTotals {
  return {
    pv_kwh: 2000,
    load_kwh: 4000,
    grid_import_kwh: 2400,
    grid_export_kwh: 200,
    import_cost_uah: 12000,
    rdn_avg_uah_per_kwh: 4.7,
    equivalent_cycles: 1.6,
    ebitda_uah: 24690,
    pv_to_load_kwh: 1400,
    pv_to_ess_kwh: 200,
    flagged_days: 0,
    ...over,
  } as EconomicsMonthlyTotals
}

describe('monthDetailSheet', () => {
  const sheet = monthDetailSheet(
    [day({ date: '2026-07-01' }), day({ date: '2026-07-02', quality_flags: ['import_lag:512'] })],
    totals(),
    '2026-07',
    'ab',
    (flags) => (flags && flags.length > 0 ? 'лічильник відставав' : null),
  )

  it('names the tab after the month and the elevator', () => {
    expect(sheet.name).toContain('Липень 2026')
    expect(sheet.name.length).toBeLessThanOrEqual(31)
  })

  it('ends with a bold totals row', () => {
    const last = sheet.rows[sheet.rows.length - 1]
    expect(sheet.rows).toHaveLength(3)
    expect(last.bold).toBe(true)
    expect(last.values[0]).toBe('Разом')
    expect(last.values[11]).toBe(24690)
  })

  it('exports numbers, not formatted strings', () => {
    const first = sheet.rows[0].values
    expect(first[0]).toBe(dateSerial('2026-07-01'))
    expect(first[3]).toBe(1200)
    // Ціна імпорту = 6000 / 1200; Факт. ціна = 6000 / 2000.
    expect(first[5]).toBeCloseTo(5, 10)
    expect(first[6]).toBeCloseTo(3, 10)
    // Self-consumption travels as a fraction so Excel's % format works.
    expect(first[9]).toBeCloseTo(0.8, 10)
    expect(first.every((v) => typeof v !== 'string' || v === '')).toBe(true)
  })

  it('carries the hover-only data-quality note into its own column', () => {
    expect(sheet.rows[0].values[12]).toBeNull()
    expect(sheet.rows[1].values[12]).toBe('лічильник відставав')
  })

  it('pins the header row and the date column', () => {
    expect(sheet.freeze).toEqual({ columns: 1, rows: 1 })
    expect(sheet.autoFilter).toBe(true)
  })
})

describe('annualDetailSheet', () => {
  it('labels months, totals the year and counts flagged days', () => {
    const sheet = annualDetailSheet(
      [
        { month: '2026-01', totals: totals({ flagged_days: 3 }) },
        { month: '2026-02', totals: totals() },
      ],
      totals({ flagged_days: 3 }),
      '2026 рік',
      'ab',
    )
    expect(sheet.name).toContain('2026 рік')
    expect(sheet.rows[0].values[0]).toBe('Січень')
    expect(sheet.rows[0].values[10]).toBe(3)
    // A clean month leaves the flag column empty rather than writing 0.
    expect(sheet.rows[1].values[10]).toBeNull()
    const last = sheet.rows[sheet.rows.length - 1]
    expect(last.bold).toBe(true)
    expect(last.values[0]).toBe('Разом')
    // Cycles are per-pack, so the period total stays empty while the
    // months keep theirs.
    expect(sheet.rows[0].values[8]).toBe(1.6)
    expect(last.values[8]).toBeNull()
  })
})

describe('dayPivotSheet', () => {
  const metric = (over: Partial<HourMetric>): HourMetric => ({
    label: 'EBITDA',
    unit: 'грн',
    format: 'money',
    total: 999,
    hours: Array.from({ length: 24 }, (_, h) => (h === 0 ? null : h)),
    ...over,
  })

  it('lays out metrics as rows and hours as columns', () => {
    const sheet = dayPivotSheet(
      [metric({}), metric({ label: 'РДН', unit: 'грн/кВт·год', format: 'price', total: null })],
      '2026-07-01',
      'ab',
    )
    expect(sheet.name).toContain('01.07.2026')
    expect(sheet.columns).toHaveLength(27)
    expect(sheet.columns[3].header).toBe('00:00')
    expect(sheet.columns[26].header).toBe('23:00')
    expect(sheet.rows[0].values.slice(0, 3)).toEqual(['EBITDA', 'грн', 999])
    // An unpriced hour stays empty instead of exporting a dash.
    expect(sheet.rows[0].values[3]).toBeNull()
    expect(sheet.rows[0].values[4]).toBe(1)
    // A price row's daily sum is meaningless, so it has no total.
    expect(sheet.rows[1].values[2]).toBeNull()
    expect(sheet.rows[1].format).toBe('price')
  })

  it('pins the label, unit and daily total columns', () => {
    const sheet = dayPivotSheet([metric({})], '2026-07-01', 'ab')
    expect(sheet.freeze).toEqual({ columns: 3, rows: 1 })
  })
})

describe('exportFileName', () => {
  it('names the workbook after the period and the elevator', () => {
    expect(exportFileName('2026-07', 'ab')).toMatch(/^economics-2026-07-[^.]+\.xlsx$/)
    expect(exportFileName('2026-07', 'ab')).not.toContain(' ')
  })
})
