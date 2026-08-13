import { describe, expect, it } from 'vitest'
import { render } from '@testing-library/react'
import type { EconomicsAnnualMonthMargin, EconomicsMonthlyDayMargin } from '../../api'
import { MonthlyHeatmap } from '../monthly/EconomicsMonthlyView'
import { MonthHourHeatmap } from '../annual/EconomicsAnnualView'

const cells = (hour: number) =>
  Array.from({ length: 24 }, (_, h) =>
    h === hour
      ? {
          margin_uah_per_kwh: 13.1,
          discharged_kwh: 42,
          revenue_uah: 703,
          cost_uah: 128,
          wear_uah: 25,
        }
      : null,
  )

const row: EconomicsMonthlyDayMargin = { date: '2026-08-03', hours: cells(20) }

const monthRow: EconomicsAnnualMonthMargin = { month: '2026-08', hours: cells(20) }

describe('MonthlyHeatmap', () => {
  // The cell is the only place an operator can check a surprising number
  // against its inputs, so the hover text must carry every term of the
  // arithmetic and end on the figure printed in the cell.
  it('explains a cell on hover', () => {
    const { container } = render(<MonthlyHeatmap margins={[row]} />)

    const cell = container.querySelectorAll('tbody td')[20]
    expect(cell.textContent).toBe('13')
    const tip = cell.getAttribute('title') ?? ''
    expect(tip).toContain('03.08')
    expect(tip).toContain('20:00')
    expect(tip).toContain('703')
    expect(tip).toContain('128')
    expect(tip).toContain('25')
    expect(tip).toContain('13,10')
  })

  it('leaves untraded hours without a tooltip', () => {
    const { container } = render(<MonthlyHeatmap margins={[row]} />)

    const tds = container.querySelectorAll('tbody td')
    expect(tds).toHaveLength(24)
    const titled = [...tds].filter((c) => c.hasAttribute('title'))
    expect(titled).toHaveLength(1)
  })
})

describe('MonthHourHeatmap', () => {
  // The annual grid sums the hour over a month, so its cell must explain
  // itself the same way the daily one does — same arithmetic, month scope.
  it('explains a month-hour cell on hover', () => {
    const { container } = render(<MonthHourHeatmap margins={[monthRow]} />)

    const cell = container.querySelectorAll('tbody td')[20]
    expect(cell.textContent).toBe('13')
    const tip = cell.getAttribute('title') ?? ''
    expect(tip).toContain('Серпень 2026')
    expect(tip).toContain('20:00')
    expect(tip).toContain('703')
    expect(tip).toContain('13,10')
  })
})
