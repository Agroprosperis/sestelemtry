import { describe, expect, it } from 'vitest'
import { rowsToCsv } from './csv'

describe('rowsToCsv', () => {
  it('writes a header row plus a row per record with CRLF line endings', () => {
    const csv = rowsToCsv(
      ['time', 'kw'],
      [
        { time: '00:00', kw: 1.5 },
        { time: '00:05', kw: 2.0 },
      ],
    )
    expect(csv).toBe('time,kw\r\n00:00,1.5\r\n00:05,2')
  })

  it('quotes fields with commas, quotes, or newlines and doubles embedded quotes', () => {
    const csv = rowsToCsv(
      ['note'],
      [{ note: 'hello, "world"\nline2' }],
    )
    expect(csv).toBe('note\r\n"hello, ""world""\nline2"')
  })

  it('renders null and undefined as empty fields', () => {
    const csv = rowsToCsv(
      ['a', 'b', 'c'],
      [{ a: 1, b: null, c: undefined }],
    )
    expect(csv).toBe('a,b,c\r\n1,,')
  })

  it('skips non-finite numbers (NaN, Infinity) as empty fields', () => {
    const csv = rowsToCsv(
      ['x', 'y', 'z'],
      [{ x: Number.NaN, y: Number.POSITIVE_INFINITY, z: 0 }],
    )
    expect(csv).toBe('x,y,z\r\n,,0')
  })

  it('rounds floating-point numbers to 6 decimals to drop binary noise', () => {
    const csv = rowsToCsv(['v'], [{ v: 0.1 + 0.2 }])
    expect(csv).toBe('v\r\n0.3')
  })

  it('serializes Date values as ISO 8601', () => {
    const csv = rowsToCsv(
      ['t'],
      [{ t: new Date('2026-05-08T07:30:00.000Z') }],
    )
    expect(csv).toBe('t\r\n2026-05-08T07:30:00.000Z')
  })

  it('returns just a header line when given no rows', () => {
    expect(rowsToCsv(['a', 'b'], [])).toBe('a,b')
  })
})
