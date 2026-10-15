import { describe, expect, it } from 'vitest'
import type { EconomicsAnnualMonthRollup, EconomicsMonthlyDay, EconomicsMonthlyTotals } from '../../api'
import { monthTrendRows, yearTrendRows } from './energyTrend'

// A day that drew 600 kWh: PV covered 280, УЗЕ 80, the grid 240; PV
// exported 30 and УЗЕ 10 on top of that.
const FLOWS = {
  load_kwh: 600,
  pv_to_load_kwh: 280,
  ess_to_load_kwh: 80,
  grid_to_load_kwh: 240,
  pv_to_grid_kwh: 30,
  ess_to_grid_kwh: 10,
  hours_with_data: 24,
}

function day(date: string, over: Partial<EconomicsMonthlyDay> = {}): EconomicsMonthlyDay {
  return { date, ...FLOWS, ...over } as unknown as EconomicsMonthlyDay
}

function month(key: string): EconomicsAnnualMonthRollup {
  return { month: key, totals: { ...FLOWS } as unknown as EconomicsMonthlyTotals }
}

describe('monthTrendRows', () => {
  it('spans every day of the month, in MWh', () => {
    const rows = monthTrendRows([day('2026-02-03')], new Date(2026, 1, 14))

    expect(rows).toHaveLength(28)
    expect(rows[0].key).toBe('2026-02-01')
    expect(rows[27].key).toBe('2026-02-28')
    expect(rows[2]).toEqual({
      key: '2026-02-03',
      load: 0.6,
      pv: 0.28,
      ess: 0.08,
      grid: 0.24,
      pvExport: 0.03,
      essExport: 0.01,
    })
  })

  it('splits consumption into what PV, УЗЕ and the grid covered', () => {
    const [row] = monthTrendRows([day('2026-10-01')], new Date(2026, 9, 1))

    expect((row.pv ?? 0) + (row.ess ?? 0) + (row.grid ?? 0)).toBeCloseTo(row.load ?? 0, 9)
  })

  it('leaves days without data empty instead of zero', () => {
    const rows = monthTrendRows([day('2026-10-01'), day('2026-10-02', { hours_with_data: 0 })], new Date(2026, 9, 1))

    expect(rows[1].load).toBeNull()
    expect(rows[20].load).toBeNull()
    expect(rows[20].pvExport).toBeNull()
  })
})

describe('yearTrendRows', () => {
  it('spans the twelve months of the year', () => {
    const rows = yearTrendRows([month('2026-03'), month('2025-03')], 2026)

    expect(rows.map((r) => r.key)).toEqual([
      '2026-01', '2026-02', '2026-03', '2026-04', '2026-05', '2026-06',
      '2026-07', '2026-08', '2026-09', '2026-10', '2026-11', '2026-12',
    ])
    expect(rows[2].load).toBe(0.6)
    expect(rows[0].load).toBeNull()
  })
})
