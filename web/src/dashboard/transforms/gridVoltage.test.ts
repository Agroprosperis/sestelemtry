import { describe, expect, it } from 'vitest'
import type { CurrentResponse } from '../../types'
import { formatGridVoltage, gridVoltageFrom } from './gridVoltage'

const NOW = Date.parse('2026-10-15T19:49:10Z')

function snap(org: string, values: Record<string, number>, time = '2026-10-15T19:49:01Z'): CurrentResponse {
  const metrics: CurrentResponse['metrics'] = {}
  for (const [k, v] of Object.entries(values)) metrics[k] = { metric_key: k, value: v, time }
  return { organization_id: org, metrics }
}

describe('gridVoltageFrom', () => {
  it('prefers the grid meter phases, wherever they come from', () => {
    // Жмеринка: the VM collector has no voltage, the edge shadow has both.
    const v = gridVoltageFrom(
      [
        snap('ze', {}),
        snap('ze-edge', {
          pv_grid_line_voltage_ab_v: 398,
          pv_grid_line_voltage_bc_v: 399,
          pv_grid_line_voltage_ca_v: 397,
          meter_phase_a_voltage_v: 227.4,
          meter_phase_b_voltage_v: 230.1,
          meter_phase_c_voltage_v: 229.2,
        }),
      ],
      NOW,
    )
    expect(v?.kind).toBe('phase')
    expect(formatGridVoltage(v!)).toBe('227/230/229 В')
  })

  it('falls back to line voltage, skipping inverters asleep at 0 V', () => {
    const v = gridVoltageFrom(
      [
        snap('x', {
          pv_grid_line_voltage_ab_v: 0,
          pv_grid_line_voltage_bc_v: 0,
          pv_grid_line_voltage_ca_v: 0,
          ess_grid_line_voltage_ab_v: 403,
          ess_grid_line_voltage_bc_v: 405,
          ess_grid_line_voltage_ca_v: 400,
        }),
      ],
      NOW,
    )
    expect(v?.kind).toBe('line')
    expect(formatGridVoltage(v!)).toBe('403/405/400 В')
  })

  it('shows nothing for a stale, partial or missing reading', () => {
    const stale = snap(
      'x',
      { meter_phase_a_voltage_v: 230, meter_phase_b_voltage_v: 231, meter_phase_c_voltage_v: 229 },
      '2026-10-15T19:30:00Z',
    )
    const partial = snap('x', { meter_phase_a_voltage_v: 230 })
    expect(gridVoltageFrom([stale, partial, null], NOW)).toBeNull()
  })
})
