import { describe, expect, it } from 'vitest'
import { economicsAsOfLabel } from '../components/formatAsOf'

describe('economicsAsOfLabel', () => {
  const now = new Date('2026-11-02T12:00:00+02:00')

  it('drops the date when the stamp is today in Kyiv', () => {
    expect(economicsAsOfLabel('2026-11-02T11:16:00Z', now)).toBe('Дані станом на 13:16')
  })

  it('keeps the date when the stamp is another day', () => {
    expect(economicsAsOfLabel('2026-11-01T01:12:00Z', now)).toBe('Дані станом на 1 листопада, 03:12')
  })

  it('returns null for a bad stamp', () => {
    expect(economicsAsOfLabel('not-a-date', now)).toBeNull()
  })
})
