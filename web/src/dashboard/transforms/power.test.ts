import { describe, expect, it } from 'vitest'
import type { TimeseriesPoint } from '../../types'
import { DAY_BUCKET_MINUTES } from '../timeline'
import { powerChartRows } from './power'

const DAY_BUCKETS = (24 * 60) / DAY_BUCKET_MINUTES

function bucketTime(anchor: Date, idx: number, offsetMinutes = 0): string {
  const d = new Date(anchor)
  d.setHours(0, idx * DAY_BUCKET_MINUTES + offsetMinutes, 0, 0)
  return d.toISOString()
}

describe('powerChartRows', () => {
  const anchor = new Date(2026, 3, 30)
  const nowAfterAnchor = new Date(2026, 4, 1, 12, 0, 0)
  const keys = ['active_ess_power_kw', 'grid_connected_active_power_kw', 'load_power_kw']

  it('produces a full day timeline with null values when no samples exist', () => {
    const rows = powerChartRows([], keys, anchor, nowAfterAnchor)
    expect(rows).toHaveLength(DAY_BUCKETS)
    expect(rows[0].active_ess_power_kw).toBeNull()
    expect(rows[12].grid_connected_active_power_kw).toBeNull()
    expect(rows[DAY_BUCKETS - 1].load_power_kw).toBeNull()
  })

  it('places samples in their 5-minute bucket and keeps gaps as null', () => {
    const points: TimeseriesPoint[] = [
      { time: bucketTime(anchor, 1), metric_key: 'load_power_kw', value: 1.5 },
      { time: bucketTime(anchor, 3), metric_key: 'load_power_kw', value: 2.5 },
    ]
    const rows = powerChartRows(points, keys, anchor, nowAfterAnchor)
    expect(rows[1].load_power_kw).toBe(1.5)
    expect(rows[2].load_power_kw).toBeNull()
    expect(rows[3].load_power_kw).toBe(2.5)
  })

  it('keeps the latest sample within a bucket (last semantics)', () => {
    const points: TimeseriesPoint[] = [
      { time: bucketTime(anchor, 5, 0), metric_key: 'active_ess_power_kw', value: 1 },
      { time: bucketTime(anchor, 5, 4), metric_key: 'active_ess_power_kw', value: 9 },
      { time: bucketTime(anchor, 5, 2), metric_key: 'active_ess_power_kw', value: 5 },
    ]
    const rows = powerChartRows(points, keys, anchor, nowAfterAnchor)
    expect(rows[5].active_ess_power_kw).toBe(9)
  })

  it('preserves negative values (charge/discharge sign)', () => {
    const points: TimeseriesPoint[] = [
      { time: bucketTime(anchor, 10), metric_key: 'active_ess_power_kw', value: -3.2 },
      { time: bucketTime(anchor, 11), metric_key: 'grid_connected_active_power_kw', value: -7 },
    ]
    const rows = powerChartRows(points, keys, anchor, nowAfterAnchor)
    expect(rows[10].active_ess_power_kw).toBe(-3.2)
    expect(rows[11].grid_connected_active_power_kw).toBe(-7)
  })

  it('ignores non-finite values and unknown metric keys', () => {
    const points: TimeseriesPoint[] = [
      { time: bucketTime(anchor, 1), metric_key: 'load_power_kw', value: Number.NaN },
      { time: bucketTime(anchor, 1), metric_key: 'active_pv_power_kw', value: 5 },
    ]
    const rows = powerChartRows(points, keys, anchor, nowAfterAnchor)
    expect(rows[1].load_power_kw).toBeNull()
    expect((rows[1] as Record<string, unknown>).active_pv_power_kw).toBeUndefined()
  })

  it('clears future buckets when the anchor is the current local day', () => {
    const todayAnchor = new Date(2026, 4, 1)
    const now = new Date(2026, 4, 1, 14, 30, 0)
    const currentIdx = (14 * 60 + 30) / DAY_BUCKET_MINUTES
    const points: TimeseriesPoint[] = [
      { time: bucketTime(todayAnchor, currentIdx - 1), metric_key: 'load_power_kw', value: 2 },
      { time: bucketTime(todayAnchor, currentIdx), metric_key: 'load_power_kw', value: 3 },
      { time: bucketTime(todayAnchor, currentIdx + 1), metric_key: 'load_power_kw', value: 4 },
    ]
    const rows = powerChartRows(points, keys, todayAnchor, now)
    expect(rows[currentIdx - 1].load_power_kw).toBe(2)
    expect(rows[currentIdx].load_power_kw).toBe(3)
    expect(rows[currentIdx + 1].load_power_kw).toBeNull()
    expect(rows[DAY_BUCKETS - 1].load_power_kw).toBeNull()
  })

  it('does not cut off any buckets when anchor is a past day', () => {
    const pastAnchor = new Date(2026, 3, 30)
    const now = new Date(2026, 4, 1, 8, 0, 0)
    const points: TimeseriesPoint[] = [
      { time: bucketTime(pastAnchor, DAY_BUCKETS - 1), metric_key: 'load_power_kw', value: 5 },
    ]
    const rows = powerChartRows(points, keys, pastAnchor, now)
    expect(rows[DAY_BUCKETS - 1].load_power_kw).toBe(5)
  })
})
