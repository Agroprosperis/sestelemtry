import { describe, expect, it } from 'vitest'
import { flowsFromTotals, EMPTY_FLOWS } from './flows'

describe('flowsFromTotals', () => {
  it('empty totals collapse to all-zero flows', () => {
    const f = flowsFromTotals({}, null)
    expect(f.pvToLoadKwh).toBe(0)
    expect(f.pvToEssKwh).toBe(0)
    expect(f.pvToGridKwh).toBe(0)
    expect(f.gridToLoadKwh).toBe(0)
    expect(f.gridToEssKwh).toBe(0)
    expect(f.essToLoadKwh).toBe(0)
    expect(f.essToGridKwh).toBe(0)
  })

  // ESS discharge fully covers a load bigger than PV export. The
  // backend already split discharge into 80% load / 20% grid via
  // its allocation rule.
  it('uses the API flows field for ESS-side edges', () => {
    const f = flowsFromTotals(
      {
        accumulated_pv_energy_yield_kwh: 100,
        accumulated_electricity_sold_kwh: 20,
        accumulated_electricity_purchased_kwh: 30,
        total_energy_charged_kwh: 25,
        total_energy_discharged_kwh: 50,
      },
      {
        pv_to_ess_kwh: 20,
        grid_to_ess_kwh: 5,
        ess_to_load_kwh: 40,
        ess_to_grid_kwh: 10,
      },
    )
    expect(f.pvToEssKwh).toBe(20)
    expect(f.gridToEssKwh).toBe(5)
    expect(f.essToLoadKwh).toBe(40)
    expect(f.essToGridKwh).toBe(10)
    // PV → Grid = sold - ess_to_grid = 20 - 10 = 10
    expect(f.pvToGridKwh).toBe(10)
    // PV → Load = produced - export - pv_to_ess = 100 - 10 - 20 = 70
    expect(f.pvToLoadKwh).toBe(70)
    // Grid → Load = purchased - grid_to_ess = 30 - 5 = 25
    expect(f.gridToLoadKwh).toBe(25)
    // Load consumed = pvToLoad + gridToLoad + essToLoad = 70+25+40 = 135
    expect(f.loadConsumedKwh).toBe(135)
  })

  it('zeroes all ESS-side flows when the API returned no flows section', () => {
    const f = flowsFromTotals(
      {
        accumulated_pv_energy_yield_kwh: 100,
        accumulated_electricity_purchased_kwh: 50,
      },
      null,
    )
    // Without synthetic counters, all ESS-side flows should be zero
    // and the algebraic flows reflect "everything went to load".
    expect(f.pvToEssKwh).toBe(0)
    expect(f.gridToEssKwh).toBe(0)
    expect(f.essToLoadKwh).toBe(0)
    expect(f.essToGridKwh).toBe(0)
    expect(f.pvToLoadKwh).toBe(100)
    expect(f.gridToLoadKwh).toBe(50)
  })

  it('treats negative totals as zero', () => {
    const f = flowsFromTotals(
      { accumulated_pv_energy_yield_kwh: -100 },
      {
        pv_to_ess_kwh: -5,
        grid_to_ess_kwh: 0,
        ess_to_load_kwh: 0,
        ess_to_grid_kwh: 0,
      },
    )
    expect(f.pvProducedKwh).toBe(0)
    expect(f.pvToEssKwh).toBe(0)
  })

  it('exposes node throughput totals', () => {
    const f = flowsFromTotals(
      {
        accumulated_pv_energy_yield_kwh: 50,
        accumulated_electricity_purchased_kwh: 10,
        accumulated_electricity_sold_kwh: 5,
        total_energy_charged_kwh: 8,
        total_energy_discharged_kwh: 3,
      },
      null,
    )
    expect(f.pvProducedKwh).toBe(50)
    expect(f.gridImportKwh).toBe(10)
    expect(f.gridExportKwh).toBe(5)
    expect(f.essChargedKwh).toBe(8)
    expect(f.essDischargedKwh).toBe(3)
  })

  it('EMPTY_FLOWS matches an explicit empty call', () => {
    expect(EMPTY_FLOWS).toEqual(flowsFromTotals({}, null))
  })
})
