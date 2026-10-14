import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { startOfPeriod } from '../range'
import { PeriodPicker } from './PeriodPicker'

describe('PeriodPicker', () => {
  it('jumps from a past day straight back to today', () => {
    const onChange = vi.fn()
    render(<PeriodPicker preset="day" anchor={new Date(2025, 2, 14)} onChange={onChange} />)

    fireEvent.click(screen.getByRole('button', { name: 'Сьогодні' }))

    expect(onChange).toHaveBeenCalledWith(startOfPeriod('day', new Date()))
  })

  it('jumps from a past month to the current one', () => {
    const onChange = vi.fn()
    render(<PeriodPicker preset="month" anchor={new Date(2025, 2, 1)} onChange={onChange} />)

    fireEvent.click(screen.getByRole('button', { name: 'Сьогодні' }))

    expect(onChange).toHaveBeenCalledWith(startOfPeriod('month', new Date()))
  })

  it('offers no jump while the current period is already shown', () => {
    render(<PeriodPicker preset="day" anchor={new Date()} onChange={() => {}} />)

    expect(screen.queryByRole('button', { name: 'Сьогодні' })).toBeNull()
  })
})
