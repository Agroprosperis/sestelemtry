import { describe, expect, it } from 'vitest'
import {
  liveAllocationFromCurrent,
  NO_DATA_ALLOCATION,
  type LiveAllocation,
} from './liveAllocation'
import type { CurrentResponse } from '../../types'

function buildCurrent(
  metrics: Record<string, number>,
  time = '2026-05-10T12:00:00Z',
): CurrentResponse {
  const out: CurrentResponse = { organization_id: 'demo', metrics: {} }
  for (const [k, v] of Object.entries(metrics)) {
    out.metrics[k] = { metric_key: k, value: v, time }
  }
  return out
}

describe('liveAllocationFromCurrent', () => {
  it('returns no_data when current is null', () => {
    expect(liveAllocationFromCurrent(null)).toEqual(NO_DATA_ALLOCATION)
  })

  it('returns no_data when none of the four power metrics are present', () => {
    const out = liveAllocationFromCurrent(buildCurrent({ soc_percent: 50 }))
    expect(out.status).toBe('no_data')
  })

  it('PV-only daytime: PV covers load, surplus exports', () => {
    const out = liveAllocationFromCurrent(
      buildCurrent({
        active_pv_power_kw: 50,
        load_power_kw: 30,
        grid_connected_active_power_kw: -20,
        active_ess_power_kw: 0,
      }),
    )
    expect(out.pvToLoadKw).toBe(30)
    expect(out.pvToGridKw).toBe(20)
    expect(out.pvToEssKw).toBe(0)
    expect(out.gridToLoadKw).toBe(0)
    expect(out.gridToEssKw).toBe(0)
    expect(out.essToLoadKw).toBe(0)
    expect(out.essToGridKw).toBe(0)
    expect(out.pvState).toBe('generating')
    expect(out.gridState).toBe('exporting')
    expect(out.essState).toBe('idle')
    expect(out.netExportKw).toBe(20)
    expect(out.status).toBe('normal')
  })

  it('PV charges battery while load is also drawn from PV', () => {
    const out = liveAllocationFromCurrent(
      buildCurrent({
        active_pv_power_kw: 40,
        load_power_kw: 10,
        grid_connected_active_power_kw: 0,
        active_ess_power_kw: -30,
      }),
    )
    expect(out.pvToLoadKw).toBe(10)
    expect(out.pvToEssKw).toBe(30)
    expect(out.pvToGridKw).toBe(0)
    expect(out.essState).toBe('charging')
    expect(out.gridState).toBe('idle')
  })

  it('ESS discharges to cover load when PV is offline', () => {
    const out = liveAllocationFromCurrent(
      buildCurrent({
        active_pv_power_kw: 0,
        load_power_kw: 8,
        grid_connected_active_power_kw: 0,
        active_ess_power_kw: 8,
      }),
    )
    expect(out.essToLoadKw).toBe(8)
    expect(out.essToGridKw).toBe(0)
    expect(out.essState).toBe('discharging')
    expect(out.pvState).toBe('idle')
  })

  it('ESS overshoots load — surplus exports', () => {
    const out = liveAllocationFromCurrent(
      buildCurrent({
        active_pv_power_kw: 0,
        load_power_kw: 5,
        grid_connected_active_power_kw: -3,
        active_ess_power_kw: 8,
      }),
    )
    expect(out.essToLoadKw).toBe(5)
    expect(out.essToGridKw).toBe(3)
    expect(out.gridState).toBe('exporting')
  })

  it('grid imports to cover load when PV+ESS are not enough', () => {
    const out = liveAllocationFromCurrent(
      buildCurrent({
        active_pv_power_kw: 0,
        load_power_kw: 12,
        grid_connected_active_power_kw: 12,
        active_ess_power_kw: 0,
      }),
    )
    expect(out.gridToLoadKw).toBe(12)
    expect(out.gridToEssKw).toBe(0)
    expect(out.gridState).toBe('importing')
  })

  it('grid charges battery when PV cannot cover the requested charge', () => {
    const out = liveAllocationFromCurrent(
      buildCurrent({
        active_pv_power_kw: 0,
        load_power_kw: 0,
        grid_connected_active_power_kw: 5,
        active_ess_power_kw: -5,
      }),
    )
    expect(out.gridToLoadKw).toBe(0)
    expect(out.gridToEssKw).toBe(5)
    expect(out.essState).toBe('charging')
    expect(out.gridState).toBe('importing')
  })

  it('night idle: everything within IDLE_KW', () => {
    const out = liveAllocationFromCurrent(
      buildCurrent({
        active_pv_power_kw: 0,
        load_power_kw: 0,
        grid_connected_active_power_kw: 0.01,
        active_ess_power_kw: -0.02,
      }),
    )
    expect(out.pvState).toBe('idle')
    expect(out.loadState).toBe('idle')
    expect(out.gridState).toBe('idle')
    expect(out.essState).toBe('idle')
  })

  it('respects ess_discharge_sign = -1 (firmware reports flipped sign)', () => {
    // Same wire reading "+5" as charging/discharging depending on
    // the firmware convention. With ess_discharge_sign = -1 the
    // "+5" should mean charging.
    const positive: LiveAllocation = liveAllocationFromCurrent(
      buildCurrent({
        active_pv_power_kw: 0,
        load_power_kw: 0,
        grid_connected_active_power_kw: 5,
        active_ess_power_kw: 5,
      }),
      -1,
    )
    expect(positive.essState).toBe('charging')
    expect(positive.gridToEssKw).toBe(5)
  })

  it('exposes the freshest reading time as observedAt', () => {
    const out = liveAllocationFromCurrent(
      buildCurrent(
        {
          active_pv_power_kw: 1,
          load_power_kw: 1,
          grid_connected_active_power_kw: 0,
          active_ess_power_kw: 0,
        },
        '2026-05-10T12:34:56Z',
      ),
    )
    expect(out.observedAt?.toISOString()).toBe('2026-05-10T12:34:56.000Z')
  })

  it('clamps a small negative load to 0 (sensor noise)', () => {
    const out = liveAllocationFromCurrent(
      buildCurrent({
        active_pv_power_kw: 1,
        load_power_kw: -0.1,
        grid_connected_active_power_kw: -1,
        active_ess_power_kw: 0,
      }),
    )
    expect(out.loadKw).toBe(0)
    expect(out.pvToLoadKw).toBe(0)
    expect(out.pvToGridKw).toBe(1)
  })
})
