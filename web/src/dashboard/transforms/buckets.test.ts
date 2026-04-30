import { describe, expect, it } from 'vitest'
import { applyApplianceConsumptionRule, energyBucketDeltaRows } from './buckets'

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

describe('energyBucketDeltaRows', () => {
  it('produces per-bucket deltas with sign direction applied', () => {
    const points = [
      { time: '2026-04-30T00:00:00Z', metric_key: 'pv_energy_yield_day_kwh', value: 0 },
      { time: '2026-04-30T01:00:00Z', metric_key: 'pv_energy_yield_day_kwh', value: 5 },
      { time: '2026-04-30T02:00:00Z', metric_key: 'pv_energy_yield_day_kwh', value: 12 },
      { time: '2026-04-30T00:00:00Z', metric_key: 'total_energy_charged_kwh', value: 0 },
      { time: '2026-04-30T01:00:00Z', metric_key: 'total_energy_charged_kwh', value: 1 },
      { time: '2026-04-30T02:00:00Z', metric_key: 'total_energy_charged_kwh', value: 4 },
    ]
    const rows = energyBucketDeltaRows(
      points,
      ['pv_energy_yield_day_kwh', 'total_energy_charged_kwh'],
      'day',
    )
    expect(rows).toHaveLength(3)
    expect(rows[1].pv_energy_yield_day_kwh).toBe(5)
    expect(rows[2].pv_energy_yield_day_kwh).toBe(7)
    expect(rows[1].total_energy_charged_kwh).toBe(-1)
    expect(rows[2].total_energy_charged_kwh).toBe(-3)
  })

  it('clamps negative deltas (counter resets) to zero', () => {
    const points = [
      { time: '2026-04-30T00:00:00Z', metric_key: 'pv_energy_yield_day_kwh', value: 10 },
      { time: '2026-04-30T01:00:00Z', metric_key: 'pv_energy_yield_day_kwh', value: 2 },
      { time: '2026-04-30T02:00:00Z', metric_key: 'pv_energy_yield_day_kwh', value: 6 },
    ]
    const rows = energyBucketDeltaRows(points, ['pv_energy_yield_day_kwh'], 'day')
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
      ...metricKeys.map((k) => ({ time: '2026-04-30T00:00:00Z', metric_key: k, value: 0 })),
      { time: '2026-04-30T01:00:00Z', metric_key: 'accumulated_electricity_purchased_kwh', value: 5 },
      { time: '2026-04-30T01:00:00Z', metric_key: 'pv_energy_yield_day_kwh', value: 4 },
      { time: '2026-04-30T01:00:00Z', metric_key: 'total_energy_discharged_kwh', value: 3 },
      { time: '2026-04-30T01:00:00Z', metric_key: 'total_energy_charged_kwh', value: 2 },
      { time: '2026-04-30T01:00:00Z', metric_key: 'accumulated_power_consumption_kwh', value: 0 },
    ]
    const rows = energyBucketDeltaRows(points, metricKeys, 'day')
    expect(rows[1].accumulated_power_consumption_kwh).toBe(-10)
  })

  it('returns empty array when no matching points', () => {
    expect(energyBucketDeltaRows([], ['x'], 'day')).toEqual([])
  })
})
