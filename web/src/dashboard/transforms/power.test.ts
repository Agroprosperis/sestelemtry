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
  const keys = [
    'active_pv_power_kw',
    'active_ess_power_kw',
    'grid_connected_active_power_kw',
    'load_power_kw',
  ]
  const inputKeys = [
    'active_pv_power_kw',
    'active_ess_power_kw',
    'grid_connected_active_power_kw',
  ]

  it('produces a full day timeline with null values when no samples exist', () => {
    const rows = powerChartRows([], keys, anchor, nowAfterAnchor)
    expect(rows).toHaveLength(DAY_BUCKETS)
    expect(rows[0].active_ess_power_kw).toBeNull()
    expect(rows[12].grid_connected_active_power_kw).toBeNull()
    expect(rows[DAY_BUCKETS - 1].load_power_kw).toBeNull()
  })

  it('places samples in their 5-minute bucket and keeps gaps as null', () => {
    const points: TimeseriesPoint[] = [
      { time: bucketTime(anchor, 1), metric_key: 'active_pv_power_kw', value: 1.5 },
      { time: bucketTime(anchor, 3), metric_key: 'active_pv_power_kw', value: 2.5 },
    ]
    const rows = powerChartRows(points, inputKeys, anchor, nowAfterAnchor)
    expect(rows[1].active_pv_power_kw).toBe(1.5)
    expect(rows[2].active_pv_power_kw).toBeNull()
    expect(rows[3].active_pv_power_kw).toBe(2.5)
  })

  it('keeps the latest sample within a bucket (last semantics)', () => {
    const points: TimeseriesPoint[] = [
      { time: bucketTime(anchor, 5, 0), metric_key: 'active_ess_power_kw', value: 1 },
      { time: bucketTime(anchor, 5, 4), metric_key: 'active_ess_power_kw', value: 9 },
      { time: bucketTime(anchor, 5, 2), metric_key: 'active_ess_power_kw', value: 5 },
    ]
    const rows = powerChartRows(points, inputKeys, anchor, nowAfterAnchor)
    expect(rows[5].active_ess_power_kw).toBe(9)
  })

  it('preserves negative values (charge/discharge sign)', () => {
    const points: TimeseriesPoint[] = [
      { time: bucketTime(anchor, 10), metric_key: 'active_ess_power_kw', value: -3.2 },
      { time: bucketTime(anchor, 11), metric_key: 'grid_connected_active_power_kw', value: -7 },
    ]
    const rows = powerChartRows(points, inputKeys, anchor, nowAfterAnchor)
    expect(rows[10].active_ess_power_kw).toBe(-3.2)
    expect(rows[11].grid_connected_active_power_kw).toBe(-7)
  })

  it('ignores non-finite values and unknown metric keys', () => {
    const points: TimeseriesPoint[] = [
      { time: bucketTime(anchor, 1), metric_key: 'active_ess_power_kw', value: Number.NaN },
      { time: bucketTime(anchor, 1), metric_key: 'soc_percent', value: 75 },
    ]
    const rows = powerChartRows(points, inputKeys, anchor, nowAfterAnchor)
    expect(rows[1].active_ess_power_kw).toBeNull()
    expect((rows[1] as Record<string, unknown>).soc_percent).toBeUndefined()
  })

  it('clears future buckets when the anchor is the current local day', () => {
    const todayAnchor = new Date(2026, 4, 1)
    const now = new Date(2026, 4, 1, 14, 30, 0)
    const currentIdx = (14 * 60 + 30) / DAY_BUCKET_MINUTES
    const points: TimeseriesPoint[] = [
      { time: bucketTime(todayAnchor, currentIdx - 1), metric_key: 'active_pv_power_kw', value: 2 },
      { time: bucketTime(todayAnchor, currentIdx), metric_key: 'active_pv_power_kw', value: 3 },
      { time: bucketTime(todayAnchor, currentIdx + 1), metric_key: 'active_pv_power_kw', value: 4 },
    ]
    const rows = powerChartRows(points, inputKeys, todayAnchor, now)
    expect(rows[currentIdx - 1].active_pv_power_kw).toBe(2)
    expect(rows[currentIdx].active_pv_power_kw).toBe(3)
    expect(rows[currentIdx + 1].active_pv_power_kw).toBeNull()
    expect(rows[DAY_BUCKETS - 1].active_pv_power_kw).toBeNull()
  })

  it('clamps anomalous spikes (|value| > 2000 kW) to 0 and keeps boundary values', () => {
    const points: TimeseriesPoint[] = [
      { time: bucketTime(anchor, 1), metric_key: 'active_pv_power_kw', value: 5000 },
      { time: bucketTime(anchor, 2), metric_key: 'active_ess_power_kw', value: -3500 },
      { time: bucketTime(anchor, 3), metric_key: 'grid_connected_active_power_kw', value: 2000 },
      { time: bucketTime(anchor, 4), metric_key: 'grid_connected_active_power_kw', value: -2000 },
    ]
    const rows = powerChartRows(points, inputKeys, anchor, nowAfterAnchor)
    expect(rows[1].active_pv_power_kw).toBe(0)
    expect(rows[2].active_ess_power_kw).toBe(0)
    expect(rows[3].grid_connected_active_power_kw).toBe(2000)
    expect(rows[4].grid_connected_active_power_kw).toBe(-2000)
  })

  it('does not cut off any buckets when anchor is a past day', () => {
    const pastAnchor = new Date(2026, 3, 30)
    const now = new Date(2026, 4, 1, 8, 0, 0)
    const points: TimeseriesPoint[] = [
      { time: bucketTime(pastAnchor, DAY_BUCKETS - 1), metric_key: 'active_pv_power_kw', value: 5 },
    ]
    const rows = powerChartRows(points, inputKeys, pastAnchor, now)
    expect(rows[DAY_BUCKETS - 1].active_pv_power_kw).toBe(5)
  })

  describe('derived load_power_kw', () => {
    it('computes load = -(pv + grid + ess) so the sink renders below zero', () => {
      const points: TimeseriesPoint[] = [
        { time: bucketTime(anchor, 5), metric_key: 'active_pv_power_kw', value: 8 },
        { time: bucketTime(anchor, 5), metric_key: 'grid_connected_active_power_kw', value: 3 },
        { time: bucketTime(anchor, 5), metric_key: 'active_ess_power_kw', value: 1 },
      ]
      const rows = powerChartRows(points, keys, anchor, nowAfterAnchor)
      expect(rows[5].load_power_kw).toBe(-12)
    })

    it('handles negative grid (export) and negative ess (charge) algebraically', () => {
      const points: TimeseriesPoint[] = [
        { time: bucketTime(anchor, 5), metric_key: 'active_pv_power_kw', value: 10 },
        { time: bucketTime(anchor, 5), metric_key: 'grid_connected_active_power_kw', value: -4 },
        { time: bucketTime(anchor, 5), metric_key: 'active_ess_power_kw', value: -3 },
      ]
      const rows = powerChartRows(points, keys, anchor, nowAfterAnchor)
      expect(rows[5].load_power_kw).toBe(-3)
    })

    it('returns null load when any of pv/grid/ess is missing in the bucket', () => {
      const points: TimeseriesPoint[] = [
        { time: bucketTime(anchor, 5), metric_key: 'active_pv_power_kw', value: 8 },
        { time: bucketTime(anchor, 5), metric_key: 'grid_connected_active_power_kw', value: 3 },
      ]
      const rows = powerChartRows(points, keys, anchor, nowAfterAnchor)
      expect(rows[5].load_power_kw).toBeNull()
    })

    it('ignores any raw load_power_kw samples in the input', () => {
      const points: TimeseriesPoint[] = [
        { time: bucketTime(anchor, 5), metric_key: 'active_pv_power_kw', value: 5 },
        { time: bucketTime(anchor, 5), metric_key: 'grid_connected_active_power_kw', value: 2 },
        { time: bucketTime(anchor, 5), metric_key: 'active_ess_power_kw', value: 0 },
        { time: bucketTime(anchor, 5), metric_key: 'load_power_kw', value: 999 },
      ]
      const rows = powerChartRows(points, keys, anchor, nowAfterAnchor)
      expect(rows[5].load_power_kw).toBe(-7)
    })

    it('keeps load null in future buckets even when inputs would otherwise be there', () => {
      const todayAnchor = new Date(2026, 4, 1)
      const now = new Date(2026, 4, 1, 14, 30, 0)
      const futureIdx = ((14 * 60 + 30) / DAY_BUCKET_MINUTES) + 5
      const points: TimeseriesPoint[] = [
        { time: bucketTime(todayAnchor, futureIdx), metric_key: 'active_pv_power_kw', value: 1 },
        { time: bucketTime(todayAnchor, futureIdx), metric_key: 'grid_connected_active_power_kw', value: 1 },
        { time: bucketTime(todayAnchor, futureIdx), metric_key: 'active_ess_power_kw', value: 1 },
      ]
      const rows = powerChartRows(points, keys, todayAnchor, now)
      expect(rows[futureIdx].load_power_kw).toBeNull()
    })
  })
})
