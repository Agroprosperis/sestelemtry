import { describe, expect, it } from 'vitest'
import { formatChartNumber, formatEnergyCompactKWh, formatNumber, formatTimeLabel } from './format'

describe('formatNumber', () => {
  it('uses 2 decimals for non-percent units', () => {
    expect(formatNumber(12.345, 'kWh')).toMatch(/12[.,]35/)
  })

  it('uses 1 decimal for percent unit', () => {
    expect(formatNumber(88.55, '%')).toMatch(/88[.,][56]/)
  })
})

describe('formatChartNumber', () => {
  it('formats with up to 2 decimals and no trailing zeros', () => {
    expect(formatChartNumber(0)).toBe('0')
    expect(formatChartNumber(1.2)).toMatch(/1[.,]2$/)
    expect(formatChartNumber(1.234)).toMatch(/1[.,]23$/)
  })
})

describe('formatEnergyCompactKWh', () => {
  it('switches to MWh above 1000 kWh', () => {
    expect(formatEnergyCompactKWh(2500)).toMatch(/2[.,]5 MWh$/)
  })

  it('keeps kWh below 1000', () => {
    expect(formatEnergyCompactKWh(125.4)).toMatch(/125[.,]4 kWh$/)
  })

  it('returns -- for non-finite values', () => {
    expect(formatEnergyCompactKWh(Number.NaN)).toBe('--')
  })
})

describe('formatTimeLabel', () => {
  const date = new Date('2026-04-30T05:00:00Z')

  it('returns a non-empty label for each preset', () => {
    expect(formatTimeLabel(date, 'day').length).toBeGreaterThan(0)
    expect(formatTimeLabel(date, 'month').length).toBeGreaterThan(0)
    expect(formatTimeLabel(date, 'year').length).toBeGreaterThan(0)
  })
})
