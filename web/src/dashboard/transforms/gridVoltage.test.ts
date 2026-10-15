import { describe, expect, it } from 'vitest'
import type { CurrentResponse } from '../../types'
import { formatGridVoltage, gridVoltageFrom } from './gridVoltage'

const NOW = Date.parse('2026-10-15T19:49:10Z')

function snap(org: string, values: Record<string, number>, time = '2026-10-15T19:49:01Z'): CurrentResponse {
  const metrics: CurrentResponse['metrics'] = {}
  for (const [k, v] of Object.entries(values)) metrics[k] = { metric_key: k, value: v, time }
  return { organization_id: org, metrics }
}

const ZE_EDGE = {
  pv_grid_line_voltage_ab_v: 0,
  pv_grid_line_voltage_bc_v: 0,
  pv_grid_line_voltage_ca_v: 0,
  ess_grid_line_voltage_ab_v: 398,
  ess_grid_line_voltage_bc_v: 399,
  ess_grid_line_voltage_ca_v: 397,
  meter_phase_a_voltage_v: 228.6,
  meter_phase_b_voltage_v: 231.1,
  meter_phase_c_voltage_v: 229.7,
}

describe('gridVoltageFrom', () => {
  it('shows line voltage, so every site reads on the same 400 V scale', () => {
    const v = gridVoltageFrom(
      [snap('ab', { grid_line_voltage_ab_v: 390.6, grid_line_voltage_bc_v: 394, grid_line_voltage_ca_v: 394.5 })],
      NOW,
    )
    expect(formatGridVoltage(v!)).toBe('Uл 391/394/395 В')
  })

  it('takes the PCS side of a dual site while the PV inverters sleep at 0 V', () => {
    // Жмеринка: the VM collector has no voltage, the edge shadow has it.
    const v = gridVoltageFrom([snap('ze', {}), snap('ze-edge', ZE_EDGE)], NOW)
    expect(v?.kind).toBe('line')
    expect(formatGridVoltage(v!)).toBe('Uл 398/399/397 В')
  })

  it('falls back to the meter phases when no line reading is there', () => {
    const v = gridVoltageFrom(
      [snap('x', { meter_phase_a_voltage_v: 228.6, meter_phase_b_voltage_v: 231.1, meter_phase_c_voltage_v: 229.7 })],
      NOW,
    )
    expect(v?.kind).toBe('phase')
    expect(formatGridVoltage(v!)).toBe('Uф 229/231/230 В')
  })

  it('shows nothing for a stale, partial or missing reading', () => {
    const stale = snap(
      'x',
      { grid_line_voltage_ab_v: 400, grid_line_voltage_bc_v: 401, grid_line_voltage_ca_v: 399 },
      '2026-10-15T19:30:00Z',
    )
    const partial = snap('x', { meter_phase_a_voltage_v: 230 })
    expect(gridVoltageFrom([stale, partial, null], NOW)).toBeNull()
  })
})
