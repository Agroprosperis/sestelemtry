import { describe, expect, it } from 'vitest'
import { toChartRows } from './chart'

describe('toChartRows', () => {
  it('groups points by time and keeps metric columns', () => {
    const rows = toChartRows(
      [
        { time: '2026-04-26T10:00:00Z', metric_key: 'a', value: 1 },
        { time: '2026-04-26T10:00:00Z', metric_key: 'b', value: 2 },
        { time: '2026-04-26T11:00:00Z', metric_key: 'a', value: 3 },
      ],
      ['a', 'b'],
    )

    expect(rows.length).toBe(2)
    expect(rows[0].a).toBe(1)
    expect(rows[0].b).toBe(2)
    expect(rows[1].a).toBe(3)
    expect(rows[1].b).toBeNull()
  })
})
