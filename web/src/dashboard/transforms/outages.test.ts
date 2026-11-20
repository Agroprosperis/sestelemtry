import { describe, expect, it } from 'vitest'
import { outageAreas, outageKindAt } from './outages'

const labels = Array.from({ length: 288 }, (_, i) => {
  const m = i * 5
  return `${String(Math.floor(m / 60)).padStart(2, '0')}:${String(m % 60).padStart(2, '0')}`
})

describe('outageAreas', () => {
  it('maps a Kyiv-time interval onto timeline labels', () => {
    // 2026-01-15 is winter: Kyiv is UTC+2.
    const areas = outageAreas(
      [{ start: '2026-01-15T10:00:00+02:00', end: '2026-01-15T13:30:00+02:00', kind: 'schedule' }],
      '2026-01-15',
      labels,
    )
    expect(areas).toEqual([{ x1: '10:00', x2: '13:30', kind: 'schedule' }])
  })

  it('clips intervals crossing midnight to the day', () => {
    const areas = outageAreas(
      [
        { start: '2026-01-14T22:00:00+02:00', end: '2026-01-15T02:00:00+02:00', kind: 'other' },
        { start: '2026-01-15T22:00:00+02:00', end: '2026-01-16T02:00:00+02:00', kind: 'schedule' },
      ],
      '2026-01-15',
      labels,
    )
    expect(areas).toEqual([
      { x1: '00:00', x2: '02:00', kind: 'other' },
      { x1: '22:00', x2: '23:55', kind: 'schedule' },
    ])
  })

  it('drops intervals of other days', () => {
    expect(
      outageAreas(
        [{ start: '2026-01-16T10:00:00+02:00', end: '2026-01-16T12:00:00+02:00', kind: 'schedule' }],
        '2026-01-15',
        labels,
      ),
    ).toEqual([])
  })

  it('draws tomorrow from what the cabinet published today', () => {
    // ЖЕ's schedule for 21.11.2026, read on the 20th: 04–06, 10–12, 16–18, 20–24.
    const areas = outageAreas(
      [
        { start: '2026-11-21T04:00:00+02:00', end: '2026-11-21T06:00:00+02:00', kind: 'schedule' },
        { start: '2026-11-21T10:00:00+02:00', end: '2026-11-21T12:00:00+02:00', kind: 'schedule' },
        { start: '2026-11-21T16:00:00+02:00', end: '2026-11-21T18:00:00+02:00', kind: 'schedule' },
        { start: '2026-11-21T20:00:00+02:00', end: '2026-11-22T00:00:00+02:00', kind: 'schedule' },
        { start: '2026-11-20T21:00:00+02:00', end: '2026-11-21T00:00:00+02:00', kind: 'schedule' },
      ],
      '2026-11-21',
      labels,
    )
    expect(areas.map((a) => `${a.x1}–${a.x2}`)).toEqual(['04:00–06:00', '10:00–12:00', '16:00–18:00', '20:00–23:55'])
  })
})

describe('outageKindAt', () => {
  const last = labels[labels.length - 1]
  const areas = [
    { x1: '10:00', x2: '12:00', kind: 'schedule' as const },
    { x1: '11:00', x2: '13:00', kind: 'other' as const },
    { x1: '20:00', x2: last, kind: 'schedule' as const },
  ]

  it('marks the buckets without light, the band end excluded', () => {
    expect(outageKindAt(areas, '09:55', last)).toBeNull()
    expect(outageKindAt(areas, '10:00', last)).toBe('schedule')
    expect(outageKindAt(areas, '11:30', last)).toBe('schedule')
    expect(outageKindAt(areas, '12:00', last)).toBe('other')
    expect(outageKindAt(areas, '13:00', last)).toBeNull()
  })

  it('keeps the last bucket of a band that runs to midnight', () => {
    expect(outageKindAt(areas, '23:50', last)).toBe('schedule')
    expect(outageKindAt(areas, last, last)).toBe('schedule')
  })
})
