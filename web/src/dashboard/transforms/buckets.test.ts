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
    const rows = energyBucketDeltaRows(points, ['pv_energy_yield_day_kwh'], 'day', anchor)
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
    const rows = energyBucketDeltaRows(points, ['total_energy_charged_kwh'], 'day', anchor)
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
    const rows = energyBucketDeltaRows(points, ['pv_energy_yield_day_kwh'], 'day', anchor)
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
    const rows: EnergyRow[] = energyBucketDeltaRows(points, metricKeys, 'day', anchor)
    expect(rows[1].accumulated_power_consumption_kwh).toBe(-10)
  })

  it('returns full timeline of zeros when no matching points exist', () => {
    const rows = energyBucketDeltaRows([], ['pv_energy_yield_day_kwh'], 'day', anchor)
    expect(rows).toHaveLength(24)
    expect(rows.every((r) => r.pv_energy_yield_day_kwh === 0)).toBe(true)
  })

  it('produces 12 rows for the year preset', () => {
    const rows = energyBucketDeltaRows([], ['pv_energy_yield_day_kwh'], 'year', anchor)
    expect(rows).toHaveLength(12)
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
