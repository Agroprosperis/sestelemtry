import { describe, expect, it } from 'vitest'
import { DAY_BUCKET_MINUTES } from '../timeline'
import { applyApplianceConsumptionRule, energyBucketDeltaRows, type EnergyRow } from './buckets'

const DAY_BUCKETS = (24 * 60) / DAY_BUCKET_MINUTES

describe('applyApplianceConsumptionRule', () => {
  it('replaces zero appliance consumption with formula result', () => {
    const deltas: Record<string, number> = {
      accumulated_electricity_purchased_kwh: 5,
      accumulated_pv_energy_yield_kwh: 4,
      total_energy_discharged_kwh: 3,
      total_energy_charged_kwh: 2,
      accumulated_power_consumption_kwh: 0,
    }
    applyApplianceConsumptionRule(deltas)
    expect(deltas.accumulated_power_consumption_kwh).toBe(10)
  })

  it('keeps the device-reported value when it is non-zero', () => {
    const deltas: Record<string, number> = {
      accumulated_electricity_purchased_kwh: 5,
      accumulated_pv_energy_yield_kwh: 4,
      total_energy_discharged_kwh: 3,
      total_energy_charged_kwh: 2,
      accumulated_power_consumption_kwh: 7,
    }
    applyApplianceConsumptionRule(deltas)
    expect(deltas.accumulated_power_consumption_kwh).toBe(7)
  })

  it('clamps negative formula results to zero', () => {
    const deltas: Record<string, number> = {
      accumulated_electricity_purchased_kwh: 0,
      accumulated_pv_energy_yield_kwh: 0,
      total_energy_discharged_kwh: 0,
      total_energy_charged_kwh: 50,
      accumulated_power_consumption_kwh: 0,
    }
    applyApplianceConsumptionRule(deltas)
    expect(deltas.accumulated_power_consumption_kwh).toBe(0)
  })

  it('is a no-op when key is absent', () => {
    const deltas: Record<string, number> = { other: 1 }
    applyApplianceConsumptionRule(deltas)
    expect(deltas).toEqual({ other: 1 })
  })
})

function bucketTime(preset: 'day' | 'month' | 'year', anchor: Date, idx: number): string {
  const d = new Date(anchor)
  if (preset === 'day') {
    d.setHours(0, idx * DAY_BUCKET_MINUTES, 0, 0)
  } else if (preset === 'month') {
    d.setDate(idx + 1)
    d.setHours(0, 0, 0, 0)
  } else {
    d.setMonth(idx, 1)
    d.setHours(0, 0, 0, 0)
  }
  return d.toISOString()
}

function contributionsAt(
  metricKey: string,
  samples: { idx: number; value: number }[],
  anchor: Date,
): { time: string; metric_key: string; value: number }[] {
  return samples.map(({ idx, value }) => ({
    time: bucketTime('day', anchor, idx),
    metric_key: metricKey,
    value,
  }))
}

describe('energyBucketDeltaRows', () => {
  const anchor = new Date(2026, 3, 30)
  // Pin "now" to a day strictly after the anchor so the future-hour cutoff is
  // disabled and tests stay deterministic regardless of the wall clock.
  const nowAfterAnchor = new Date(2026, 4, 1, 12, 0, 0)

  it('produces 288 rows for the day preset and fills empty buckets with zeros', () => {
    const points = contributionsAt(
      'accumulated_pv_energy_yield_kwh',
      [
        { idx: 0, value: 0 },
        { idx: 1, value: 5 },
        { idx: 2, value: 7 },
      ],
      anchor,
    )
    const rows = energyBucketDeltaRows(points, ['accumulated_pv_energy_yield_kwh'], 'day', anchor, nowAfterAnchor)
    expect(rows).toHaveLength(DAY_BUCKETS)
    expect(rows[0].accumulated_pv_energy_yield_kwh).toBe(0)
    expect(rows[1].accumulated_pv_energy_yield_kwh).toBe(5)
    expect(rows[2].accumulated_pv_energy_yield_kwh).toBe(7)
    expect(rows[5].accumulated_pv_energy_yield_kwh).toBe(0)
    expect(rows[DAY_BUCKETS - 1].accumulated_pv_energy_yield_kwh).toBe(0)
  })

  it('applies sign direction for sink metrics', () => {
    const points = contributionsAt(
      'total_energy_charged_kwh',
      [
        { idx: 1, value: 1 },
        { idx: 2, value: 3 },
      ],
      anchor,
    )
    const rows = energyBucketDeltaRows(points, ['total_energy_charged_kwh'], 'day', anchor, nowAfterAnchor)
    expect(rows[1].total_energy_charged_kwh).toBe(-1)
    expect(rows[2].total_energy_charged_kwh).toBe(-3)
  })

  it('clamps negative bucket values to zero', () => {
    const points = contributionsAt(
      'accumulated_pv_energy_yield_kwh',
      [
        { idx: 1, value: -2 },
        { idx: 2, value: 4 },
      ],
      anchor,
    )
    const rows = energyBucketDeltaRows(points, ['accumulated_pv_energy_yield_kwh'], 'day', anchor, nowAfterAnchor)
    expect(rows[1].accumulated_pv_energy_yield_kwh).toBe(0)
    expect(rows[2].accumulated_pv_energy_yield_kwh).toBe(4)
  })

  it('keeps the device-reported consumption when it is non-zero', () => {
    const metricKeys = [
      'accumulated_electricity_purchased_kwh',
      'accumulated_pv_energy_yield_kwh',
      'total_energy_discharged_kwh',
      'total_energy_charged_kwh',
      'accumulated_power_consumption_kwh',
    ]
    const points = [
      { time: bucketTime('day', anchor, 1), metric_key: 'accumulated_electricity_purchased_kwh', value: 5 },
      { time: bucketTime('day', anchor, 1), metric_key: 'accumulated_pv_energy_yield_kwh', value: 4 },
      { time: bucketTime('day', anchor, 1), metric_key: 'total_energy_discharged_kwh', value: 3 },
      { time: bucketTime('day', anchor, 1), metric_key: 'total_energy_charged_kwh', value: 2 },
      { time: bucketTime('day', anchor, 1), metric_key: 'accumulated_power_consumption_kwh', value: 7 },
    ]
    const rows: EnergyRow[] = energyBucketDeltaRows(points, metricKeys, 'day', anchor, nowAfterAnchor)
    // Sink direction is -1, so a non-zero device-reported delta of 7 shows
    // up as -7 on the y-axis (no algebraic override).
    expect(rows[1].accumulated_power_consumption_kwh).toBe(-7)
  })

  it('falls back to algebraic formula when device counter reports zero', () => {
    const metricKeys = [
      'accumulated_electricity_purchased_kwh',
      'accumulated_pv_energy_yield_kwh',
      'total_energy_discharged_kwh',
      'total_energy_charged_kwh',
      'accumulated_power_consumption_kwh',
    ]
    const points = [
      { time: bucketTime('day', anchor, 1), metric_key: 'accumulated_electricity_purchased_kwh', value: 5 },
      { time: bucketTime('day', anchor, 1), metric_key: 'accumulated_pv_energy_yield_kwh', value: 4 },
      { time: bucketTime('day', anchor, 1), metric_key: 'total_energy_discharged_kwh', value: 3 },
      { time: bucketTime('day', anchor, 1), metric_key: 'total_energy_charged_kwh', value: 2 },
      { time: bucketTime('day', anchor, 1), metric_key: 'accumulated_power_consumption_kwh', value: 0 },
    ]
    const rows: EnergyRow[] = energyBucketDeltaRows(points, metricKeys, 'day', anchor, nowAfterAnchor)
    // 5 + 4 + 3 - 2 = 10, applied as a sink (-10) when the raw counter is 0.
    expect(rows[1].accumulated_power_consumption_kwh).toBe(-10)
  })

  it('returns full timeline of zeros when no matching points exist', () => {
    const rows = energyBucketDeltaRows([], ['accumulated_pv_energy_yield_kwh'], 'day', anchor, nowAfterAnchor)
    expect(rows).toHaveLength(DAY_BUCKETS)
    expect(rows.every((r) => r.accumulated_pv_energy_yield_kwh === 0)).toBe(true)
  })

  it('produces 12 rows for the year preset', () => {
    const rows = energyBucketDeltaRows([], ['accumulated_pv_energy_yield_kwh'], 'year', anchor)
    expect(rows).toHaveLength(12)
  })

  it('omits metric values for buckets after now on the current day', () => {
    const todayAnchor = new Date(2026, 4, 1)
    // 14:30 falls on bucket index 174 (14 * 12 + 6) at 5-minute resolution.
    const now = new Date(2026, 4, 1, 14, 30, 0)
    const currentIdx = (14 * 60 + 30) / DAY_BUCKET_MINUTES
    const points = contributionsAt(
      'accumulated_pv_energy_yield_kwh',
      [
        { idx: currentIdx - 2, value: 3 },
        { idx: currentIdx - 1, value: 4 },
        { idx: currentIdx, value: 1 },
      ],
      todayAnchor,
    )
    const rows = energyBucketDeltaRows(points, ['accumulated_pv_energy_yield_kwh'], 'day', todayAnchor, now)
    expect(rows).toHaveLength(DAY_BUCKETS)
    expect(rows[currentIdx - 2].accumulated_pv_energy_yield_kwh).toBe(3)
    expect(rows[currentIdx - 1].accumulated_pv_energy_yield_kwh).toBe(4)
    // Current bucket is plotted with whatever data we have so far (partial).
    expect(rows[currentIdx].accumulated_pv_energy_yield_kwh).toBe(1)
    // Future buckets have no metric values so the line ends at the current
    // bucket instead of dropping to zero.
    expect(rows[currentIdx + 1].accumulated_pv_energy_yield_kwh).toBeUndefined()
    expect(rows[DAY_BUCKETS - 1].accumulated_pv_energy_yield_kwh).toBeUndefined()
    expect(rows[currentIdx + 1].time).toBeDefined()
    expect(rows[DAY_BUCKETS - 1].time).toBeDefined()
  })

  it('keeps zero fill for past buckets when anchor is the current day', () => {
    const todayAnchor = new Date(2026, 4, 1)
    const now = new Date(2026, 4, 1, 10, 0, 0)
    const currentIdx = (10 * 60) / DAY_BUCKET_MINUTES
    const rows = energyBucketDeltaRows([], ['accumulated_pv_energy_yield_kwh'], 'day', todayAnchor, now)
    expect(rows[0].accumulated_pv_energy_yield_kwh).toBe(0)
    expect(rows[currentIdx - 1].accumulated_pv_energy_yield_kwh).toBe(0)
    expect(rows[currentIdx].accumulated_pv_energy_yield_kwh).toBe(0)
    expect(rows[currentIdx + 1].accumulated_pv_energy_yield_kwh).toBeUndefined()
  })

  it('does not omit any buckets when anchor is a past day', () => {
    const pastAnchor = new Date(2026, 3, 30)
    const now = new Date(2026, 4, 1, 8, 0, 0)
    const rows = energyBucketDeltaRows([], ['accumulated_pv_energy_yield_kwh'], 'day', pastAnchor, now)
    expect(rows.every((r) => r.accumulated_pv_energy_yield_kwh === 0)).toBe(true)
  })

  it('sums daily contributions into months for the year preset', () => {
    const yearAnchor = new Date(2026, 0, 1)
    const points = [
      { time: new Date(2026, 3, 1, 0, 0, 0).toISOString(), metric_key: 'accumulated_electricity_purchased_kwh', value: 50 },
      { time: new Date(2026, 3, 30, 0, 0, 0).toISOString(), metric_key: 'accumulated_electricity_purchased_kwh', value: 100 },
      { time: new Date(2026, 4, 1, 0, 0, 0).toISOString(), metric_key: 'accumulated_electricity_purchased_kwh', value: 25 },
      { time: new Date(2026, 4, 31, 0, 0, 0).toISOString(), metric_key: 'accumulated_electricity_purchased_kwh', value: 75 },
    ]
    const rows = energyBucketDeltaRows(points, ['accumulated_electricity_purchased_kwh'], 'year', yearAnchor)
    expect(rows).toHaveLength(12)
    expect(rows[0].accumulated_electricity_purchased_kwh).toBe(0)
    expect(rows[3].accumulated_electricity_purchased_kwh).toBe(150)
    expect(rows[4].accumulated_electricity_purchased_kwh).toBe(100)
  })
})
