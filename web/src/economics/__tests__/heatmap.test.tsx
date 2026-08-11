import { describe, expect, it } from 'vitest'
import { render } from '@testing-library/react'
import type { EconomicsMonthlyDayMargin } from '../../api'
import { MonthlyHeatmap } from '../monthly/EconomicsMonthlyView'

const row: EconomicsMonthlyDayMargin = {
  date: '2026-08-03',
  hours: Array.from({ length: 24 }, (_, h) =>
    h === 20
      ? {
          margin_uah_per_kwh: 13.1,
          discharged_kwh: 42,
          revenue_uah: 703,
          cost_uah: 128,
          wear_uah: 25,
        }
      : null,
  ),
}

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

    const cells = container.querySelectorAll('tbody td')
    expect(cells).toHaveLength(24)
    const titled = [...cells].filter((c) => c.hasAttribute('title'))
    expect(titled).toHaveLength(1)
  })
})
