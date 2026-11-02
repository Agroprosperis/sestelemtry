import { describe, expect, it } from 'vitest'
import { economicsAsOfLabel } from '../components/formatAsOf'

describe('economicsAsOfLabel', () => {
  const now = new Date('2026-11-02T12:00:00+02:00')

  it('gives the stamp for the day still running', () => {
    expect(economicsAsOfLabel('2026-11-02T11:16:00Z', '2026-11-02', 'day', now)).toBe(
      'Дані станом на сьогодні, 13:16 дня',
    )
  })

  it('says the whole day is in once it was computed after the day ended', () => {
    expect(economicsAsOfLabel('2026-11-02T01:00:00Z', '2026-11-01', 'day', now)).toBe(
      'Дані за весь день, розраховано сьогодні о 03:00 ночі',
    )
  })

  it('keeps a past day partial when its last compute ran before midnight', () => {
    expect(economicsAsOfLabel('2026-10-31T21:15:00Z', '2026-10-31', 'day', now)).toBe(
      'Дані станом на 31 жовтня, 23:15 вечора',
    )
  })

  it('treats the running month as partial and an ended month as whole', () => {
    expect(economicsAsOfLabel('2026-11-02T11:15:00Z', '2026-11-30', 'month', now)).toBe(
      'Дані станом на сьогодні, 13:15 дня',
    )
    expect(economicsAsOfLabel('2026-11-01T01:12:00Z', '2026-10-31', 'month', now)).toBe(
      'Дані за весь місяць, розраховано 1 листопада о 03:12 ночі',
    )
  })

  it('returns null for a bad stamp', () => {
    expect(economicsAsOfLabel('not-a-date', '2026-11-02', 'day', now)).toBeNull()
  })
})
