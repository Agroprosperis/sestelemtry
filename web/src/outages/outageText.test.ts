import { describe, expect, it } from 'vitest'
import { formatMoment, outageDays } from './outageText'

// 20.11.2026 in Kyiv is UTC+2.
const now = new Date('2026-11-20T17:00:00Z')

describe('outageDays', () => {
  it('groups the stretches by Kyiv day and ends a day at 24:00', () => {
    expect(
      outageDays(
        [
          { start: '2026-11-21T02:00:00Z', end: '2026-11-21T04:00:00Z', kind: 'schedule' },
          { start: '2026-11-20T19:00:00Z', end: '2026-11-20T22:00:00Z', kind: 'schedule' },
          { start: '2026-11-21T18:00:00Z', end: '2026-11-21T22:00:00Z', kind: 'schedule' },
          { start: '2026-11-22T08:00:00Z', end: '2026-11-22T10:00:00Z', kind: 'other' },
        ],
        now,
      ),
    ).toEqual([
      { label: 'сьогодні', ranges: ['21:00–24:00'] },
      { label: 'завтра', ranges: ['04:00–06:00', '20:00–24:00'] },
      { label: '22.11', ranges: ['10:00–12:00'] },
    ])
  })

  it('skips what it cannot read', () => {
    expect(outageDays([{ start: 'nope', end: '2026-11-20T22:00:00Z', kind: 'schedule' }], now)).toEqual([])
  })
})

describe('formatMoment', () => {
  it('writes Kyiv time and nothing for a zero time', () => {
    expect(formatMoment('2026-11-20T17:46:00Z')).toBe('20.11.2026, 19:46')
    expect(formatMoment('0001-01-01T00:00:00Z')).toBe('')
    expect(formatMoment(undefined)).toBe('')
  })
})
