import { describe, expect, it } from 'vitest'
import { outageAreas } from './outages'

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
})
