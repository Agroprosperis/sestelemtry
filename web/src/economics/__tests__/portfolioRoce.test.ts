import { describe, expect, it } from 'vitest'
import type { EconomicsPortfolioSite } from '../../api'
import { portfolioRoce, siteRoce } from '../portfolio/roce'

function site(over: Partial<EconomicsPortfolioSite>): EconomicsPortfolioSite {
  return {
    id: 'x',
    name: 'x',
    has_data: true,
    effect_uah: 0,
    ebitda_uah: 0,
    schedule_reserve_uah: 0,
    bess_reserve_uah: 0,
    action_reserve_uah: 0,
    bess_data_ok: true,
    bess_anomalous_hours: 0,
    bess_anomalous_days: 0,
    pv_kwh: 0,
    load_kwh: 0,
    grid_import_kwh: 0,
    grid_export_kwh: 0,
    ess_net_uah: 0,
    ...over,
  }
}

describe('portfolio ROCE', () => {
  it('annualises the period EBITDA over the capital, like the ROCE card', () => {
    // Ten months of a year: ×12/10.
    expect(siteRoce(site({ ebitda_uah: 1_000_000, months_with_data: 10, capex_uah: 20_000_000 }))).toBeCloseTo(0.06)
    // One month.
    expect(siteRoce(site({ ebitda_uah: 100_000, months_with_data: 1, capex_uah: 24_000_000 }))).toBeCloseTo(0.05)
  })

  it('leaves objects without CAPEX or data out', () => {
    expect(siteRoce(site({ ebitda_uah: 1_000_000, months_with_data: 12, capex_uah: 0 }))).toBeNaN()
    expect(siteRoce(site({ has_data: false, capex_uah: 10_000_000 }))).toBeNaN()
    const total = portfolioRoce([
      site({ ebitda_uah: 1_200_000, months_with_data: 12, capex_uah: 10_000_000 }),
      site({ ebitda_uah: 300_000, months_with_data: 6, capex_uah: 20_000_000 }),
      site({ ebitda_uah: 5_000_000, months_with_data: 12, capex_uah: 0 }),
    ])
    // (1.2M + 0.6M) / 30M
    expect(total).toBeCloseTo(0.06)
    expect(portfolioRoce([site({ capex_uah: 0 })])).toBeNaN()
  })
})
