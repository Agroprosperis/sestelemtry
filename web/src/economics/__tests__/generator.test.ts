import { describe, expect, it } from 'vitest'
import type { EconomicsHourApi } from '../../api'
import { dailyTotals } from '../compute'
import { discrepancyFlags } from '../qualityFlags'
import { mapHourRow } from '../useEconomicsData'

function hour(h: number, fields: Partial<EconomicsHourApi> = {}): EconomicsHourApi {
  return {
    hour: h,
    hour_start: `2026-10-20T${String(h).padStart(2, '0')}:00:00+03:00`,
    rdn_uah_per_kwh: 2,
    pv_kwh: 0,
    grid_import_kwh: 0,
    grid_export_kwh: 0,
    ess_charged_kwh: 0,
    ess_discharged_kwh: 0,
    pv_to_ess_kwh: 0,
    grid_to_ess_kwh: 0,
    ess_to_load_kwh: 0,
    ess_to_grid_kwh: 0,
    load_kwh: 0,
    pv_to_load_kwh: 0,
    pv_to_grid_kwh: 0,
    grid_to_load_kwh: 0,
    import_price_uah_per_kwh: 2,
    export_price_uah_per_kwh: 2,
    baseline_cost_uah: 0,
    actual_cost_uah: 0,
    effect_uah: 0,
    ess_net_uah: 0,
    ess_remaining_kwh_start: null,
    ess_cost_basis_uah_start: null,
    ess_avg_cost_uah_per_kwh_start: null,
    ess_withdrawn_cost_uah: null,
    ess_realized_profit_uah: null,
    ess_cost_basis_uah_end: null,
    ess_avg_cost_uah_per_kwh_end: null,
    ess_residual_kwh_end: null,
    ...fields,
  }
}

// The day of the Go TestGenPerKwhDay as the server serves it: fuel at
// c = 15 грн/кВт·год, the generator at 02:00 (night charge), 12:00 (with
// PV) and 18:00 (30 min, then 30 min island), grid hours at 10:00 and 20:00.
function perKwhDay(): EconomicsHourApi[] {
  const hours = Array.from({ length: 24 }, (_, h) => hour(h))
  hours[2] = hour(2, {
    ess_charged_kwh: 50, load_kwh: 100, baseline_cost_uah: 1500, actual_cost_uah: 2250,
    effect_uah: -750, ess_net_uah: -750,
    gen_kwh: 150, minutes_generator: 60, gen_accounted: true, gen_to_load_kwh: 100, gen_to_ess_kwh: 50,
    load_offgrid_kwh: 100, gen_fuel_cost_uah: 2250, gen_fuel_l: 45, gen_fuel_method: 'per_kwh',
    gen_charge_cost_uah: 750,
  })
  hours[10] = hour(10, {
    pv_kwh: 50, grid_import_kwh: 10, ess_charged_kwh: 30, pv_to_ess_kwh: 30, load_kwh: 30,
    pv_to_load_kwh: 20, grid_to_load_kwh: 10, baseline_cost_uah: 60, actual_cost_uah: 20,
    effect_uah: 40, ess_net_uah: -60,
  })
  hours[12] = hour(12, {
    pv_kwh: 30, load_kwh: 100, pv_to_load_kwh: 30, baseline_cost_uah: 1500, actual_cost_uah: 1050,
    effect_uah: 450,
    gen_kwh: 70, minutes_generator: 60, gen_accounted: true, gen_to_load_kwh: 70, load_offgrid_kwh: 100,
    pv_to_load_offgrid_kwh: 30, gen_fuel_cost_uah: 1050, gen_fuel_l: 21, gen_fuel_method: 'per_kwh',
    pv_offgrid_value_uah: 450,
  })
  hours[18] = hour(18, {
    pv_kwh: 10, ess_discharged_kwh: 20, ess_to_load_kwh: 20, load_kwh: 70, pv_to_load_kwh: 10,
    baseline_cost_uah: 1050, actual_cost_uah: 600, effect_uah: 450, ess_net_uah: 300,
    gen_kwh: 40, minutes_generator: 30, minutes_island: 30, gen_accounted: true, gen_to_load_kwh: 40,
    load_offgrid_kwh: 70, pv_to_load_offgrid_kwh: 10, ess_to_load_offgrid_kwh: 20,
    gen_fuel_cost_uah: 600, gen_fuel_l: 12, gen_fuel_method: 'per_kwh',
    pv_offgrid_value_uah: 150, ess_offgrid_value_uah: 300,
  })
  hours[20] = hour(20, {
    grid_import_kwh: 15, ess_discharged_kwh: 10, ess_to_load_kwh: 10, load_kwh: 25,
    grid_to_load_kwh: 15, baseline_cost_uah: 50, actual_cost_uah: 30, effect_uah: 20, ess_net_uah: 20,
  })
  return hours
}

describe('generator economics in the browser', () => {
  it('adds the day up as the server does (Go TestGenPerKwhDay)', () => {
    const t = dailyTotals(perKwhDay().map(mapHourRow))
    expect(t.effect).toBeCloseTo(210, 9)
    expect(t.revenuePvSelf).toBeCloseTo(640, 9)
    expect(t.revenueEssSelf).toBeCloseTo(320, 9)
    expect(t.expenseGridCharge).toBe(0)
    expect(t.expenseGenCharge).toBeCloseTo(750, 9)
    expect(t.expenseTotal).toBeCloseTo(750, 9)
    expect(t.ebitda).toBeCloseTo(t.effect, 9)
    expect(t.genKwh).toBeCloseTo(260, 9)
    expect(t.genToLoad).toBeCloseTo(210, 9)
    expect(t.genToEss).toBeCloseTo(50, 9)
    expect(t.loadOffgrid).toBeCloseTo(270, 9)
    expect(t.genFuelCost).toBeCloseTo(3900, 9)
    expect(t.genFuelL).toBeCloseTo(78, 9)
  })

  it('keeps a day without generator data as before', () => {
    const rows = perKwhDay().map((h) => mapHourRow(hour(h.hour, {
      pv_kwh: h.pv_kwh, grid_import_kwh: h.grid_import_kwh, ess_to_load_kwh: h.ess_to_load_kwh,
      pv_to_load_kwh: h.pv_to_load_kwh, effect_uah: h.effect_uah,
    })))
    expect(rows.every((r) => r.gen === undefined)).toBe(true)
    const t = dailyTotals(rows)
    expect(t.revenuePvSelf).toBeCloseTo((20 + 30 + 10) * 2, 9)
    expect(t.revenueEssSelf).toBeCloseTo((20 + 10) * 2, 9)
    expect(t.expenseGenCharge).toBe(0)
    expect(t.genKwh).toBe(0)
  })

  it('keeps the generator energy of an hour not counted with it', () => {
    const row = mapHourRow(hour(5, { gen_kwh: 12, minutes_generator: 20 }))
    expect(row.gen?.accounted).toBe(false)
    const t = dailyTotals([row])
    expect(t.genKwh).toBe(12)
    expect(t.genFuelCost).toBe(0)
  })
})

describe('discrepancyFlags', () => {
  it('leaves the generator flags out of the reconciliation line', () => {
    expect(
      discrepancyFlags([
        'load_mismatch:0.0500',
        'gen_cost_missing',
        'supply_unknown:5',
        'offgrid_import:3',
        'import_lag:12',
      ]),
    ).toEqual(['load_mismatch:0.0500', 'import_lag:12'])
  })
})
