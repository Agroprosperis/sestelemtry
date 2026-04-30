import { describe, expect, it } from 'vitest'
import { metricDeltas } from './deltas'

describe('metricDeltas', () => {
  it('returns last - first for each requested key', () => {
    const points = [
      { time: '2026-04-30T00:00:00Z', metric_key: 'a', value: 10 },
      { time: '2026-04-30T01:00:00Z', metric_key: 'a', value: 12 },
      { time: '2026-04-30T02:00:00Z', metric_key: 'a', value: 17 },
      { time: '2026-04-30T00:00:00Z', metric_key: 'b', value: 100 },
      { time: '2026-04-30T03:00:00Z', metric_key: 'b', value: 130 },
    ]
    const out = metricDeltas(points, new Set(['a', 'b']))
    expect(out.a).toBe(7)
    expect(out.b).toBe(30)
  })

  it('ignores points whose key is not requested', () => {
    const points = [
      { time: '2026-04-30T00:00:00Z', metric_key: 'a', value: 0 },
      { time: '2026-04-30T01:00:00Z', metric_key: 'a', value: 5 },
      { time: '2026-04-30T01:00:00Z', metric_key: 'other', value: 99 },
    ]
    const out = metricDeltas(points, new Set(['a']))
    expect(out).toEqual({ a: 5 })
  })

  it('skips non-finite values and timestamps', () => {
    const points = [
      { time: 'not-a-date', metric_key: 'a', value: 1 },
      { time: '2026-04-30T00:00:00Z', metric_key: 'a', value: Number.NaN },
      { time: '2026-04-30T01:00:00Z', metric_key: 'a', value: 4 },
      { time: '2026-04-30T02:00:00Z', metric_key: 'a', value: 9 },
    ]
    const out = metricDeltas(points, new Set(['a']))
    expect(out.a).toBe(5)
  })

  it('handles unsorted input by picking earliest and latest timestamps', () => {
    const points = [
      { time: '2026-04-30T05:00:00Z', metric_key: 'a', value: 50 },
      { time: '2026-04-30T01:00:00Z', metric_key: 'a', value: 10 },
      { time: '2026-04-30T03:00:00Z', metric_key: 'a', value: 30 },
    ]
    const out = metricDeltas(points, new Set(['a']))
    expect(out.a).toBe(40)
  })

  it('returns empty object when no points match', () => {
    expect(metricDeltas([], new Set(['a']))).toEqual({})
  })
})
