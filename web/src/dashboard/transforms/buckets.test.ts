import { describe, expect, it } from 'vitest'
import { applyApplianceConsumptionRule, energyBucketDeltaRows, type EnergyRow } from './buckets'

describe('applyApplianceConsumptionRule', () => {
  it('replaces appliance consumption with formula result', () => {
    const deltas: Record<string, number> = {
      accumulated_electricity_purchased_kwh: 5,
      pv_energy_yield_day_kwh: 4,
      total_energy_discharged_kwh: 3,
      total_energy_charged_kwh: 2,
      accumulated_power_consumption_kwh: 0,
    }
    applyApplianceConsumptionRule(deltas)
    expect(deltas.accumulated_power_consumption_kwh).toBe(10)
  })

  it('clamps negative results to zero', () => {
    const deltas: Record<string, number> = {
      accumulated_electricity_purchased_kwh: 0,
      pv_energy_yield_day_kwh: 0,
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
    d.setHours(idx, 0, 0, 0)
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

  it('produces 24 rows for the day preset and fills empty buckets with zeros', () => {
    const points = contributionsAt(
      'pv_energy_yield_day_kwh',
      [
        { idx: 0, value: 0 },
        { idx: 1, value: 5 },
        { idx: 2, value: 7 },
      ],
      anchor,
    )
    const rows = energyBucketDeltaRows(points, ['pv_energy_yield_day_kwh'], 'day', anchor, nowAfterAnchor)
    expect(rows).toHaveLength(24)
    expect(rows[0].pv_energy_yield_day_kwh).toBe(0)
    expect(rows[1].pv_energy_yield_day_kwh).toBe(5)
    expect(rows[2].pv_energy_yield_day_kwh).toBe(7)
    expect(rows[5].pv_energy_yield_day_kwh).toBe(0)
    expect(rows[23].pv_energy_yield_day_kwh).toBe(0)
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
      'pv_energy_yield_day_kwh',
      [
        { idx: 1, value: -2 },
        { idx: 2, value: 4 },
      ],
      anchor,
    )
    const rows = energyBucketDeltaRows(points, ['pv_energy_yield_day_kwh'], 'day', anchor, nowAfterAnchor)
    expect(rows[1].pv_energy_yield_day_kwh).toBe(0)
    expect(rows[2].pv_energy_yield_day_kwh).toBe(4)
  })

  it('recomputes appliance consumption from formula', () => {
    const metricKeys = [
      'accumulated_electricity_purchased_kwh',
      'pv_energy_yield_day_kwh',
      'total_energy_discharged_kwh',
      'total_energy_charged_kwh',
      'accumulated_power_consumption_kwh',
    ]
    const points = [
      { time: bucketTime('day', anchor, 1), metric_key: 'accumulated_electricity_purchased_kwh', value: 5 },
      { time: bucketTime('day', anchor, 1), metric_key: 'pv_energy_yield_day_kwh', value: 4 },
      { time: bucketTime('day', anchor, 1), metric_key: 'total_energy_discharged_kwh', value: 3 },
      { time: bucketTime('day', anchor, 1), metric_key: 'total_energy_charged_kwh', value: 2 },
      { time: bucketTime('day', anchor, 1), metric_key: 'accumulated_power_consumption_kwh', value: 0 },
    ]
    const rows: EnergyRow[] = energyBucketDeltaRows(points, metricKeys, 'day', anchor, nowAfterAnchor)
    expect(rows[1].accumulated_power_consumption_kwh).toBe(-10)
  })

  it('returns full timeline of zeros when no matching points exist', () => {
    const rows = energyBucketDeltaRows([], ['pv_energy_yield_day_kwh'], 'day', anchor, nowAfterAnchor)
    expect(rows).toHaveLength(24)
    expect(rows.every((r) => r.pv_energy_yield_day_kwh === 0)).toBe(true)
  })

  it('produces 12 rows for the year preset', () => {
    const rows = energyBucketDeltaRows([], ['pv_energy_yield_day_kwh'], 'year', anchor)
    expect(rows).toHaveLength(12)
  })

  it('omits metric values for hours after now on the current day', () => {
    const todayAnchor = new Date(2026, 4, 1)
    const now = new Date(2026, 4, 1, 14, 30, 0)
    const points = contributionsAt(
      'pv_energy_yield_day_kwh',
      [
        { idx: 12, value: 3 },
        { idx: 13, value: 4 },
        { idx: 14, value: 1 },
      ],
      todayAnchor,
    )
    const rows = energyBucketDeltaRows(points, ['pv_energy_yield_day_kwh'], 'day', todayAnchor, now)
    expect(rows).toHaveLength(24)
    expect(rows[12].pv_energy_yield_day_kwh).toBe(3)
    expect(rows[13].pv_energy_yield_day_kwh).toBe(4)
    // Current hour is plotted with whatever data we have so far (partial).
    expect(rows[14].pv_energy_yield_day_kwh).toBe(1)
    // Future hours have no metric values so the line ends at the current hour
    // instead of dropping to zero.
    expect(rows[15].pv_energy_yield_day_kwh).toBeUndefined()
    expect(rows[23].pv_energy_yield_day_kwh).toBeUndefined()
    expect(rows[15].time).toBeDefined()
    expect(rows[23].time).toBeDefined()
  })

  it('keeps zero fill for past hours when anchor is the current day', () => {
    const todayAnchor = new Date(2026, 4, 1)
    const now = new Date(2026, 4, 1, 10, 0, 0)
    const rows = energyBucketDeltaRows([], ['pv_energy_yield_day_kwh'], 'day', todayAnchor, now)
    expect(rows[0].pv_energy_yield_day_kwh).toBe(0)
    expect(rows[9].pv_energy_yield_day_kwh).toBe(0)
    expect(rows[10].pv_energy_yield_day_kwh).toBe(0)
    expect(rows[11].pv_energy_yield_day_kwh).toBeUndefined()
  })

  it('does not omit any hours when anchor is a past day', () => {
    const pastAnchor = new Date(2026, 3, 30)
    const now = new Date(2026, 4, 1, 8, 0, 0)
    const rows = energyBucketDeltaRows([], ['pv_energy_yield_day_kwh'], 'day', pastAnchor, now)
    expect(rows.every((r) => r.pv_energy_yield_day_kwh === 0)).toBe(true)
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
