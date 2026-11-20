import { cleanup, render, screen, within } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'
import type { EconomicsPortfolioResponse, EconomicsPortfolioSite } from '../../api'
import { formatPercent } from '../monthly/format'
import { EconomicsPortfolioView } from '../portfolio/EconomicsPortfolioView'

afterEach(cleanup)

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
    ess_captured_share: 0,
    ...over,
  }
}

describe('portfolio «Реалізовано УЗЕ»', () => {
  it("shows the object's УЗЕ fact over its optimum, as its month does", () => {
    // The elevator's ess_net counts its day with the generator as the
    // elevator's, the fact counts each battery: the share comes from the API.
    const elevator = site({ id: 'de-all', ess_net_uah: -17_598, bess_reserve_uah: 78_122, ess_captured_share: -0.374 })
    const portfolio: EconomicsPortfolioResponse = {
      scope: 'month',
      label: '2026-11',
      tz: 'Europe/Kyiv',
      months_with_data: 1,
      sites: [elevator],
      totals: site({ id: '__total__', name: 'Портфель' }),
      trend: [],
    }
    render(
      <EconomicsPortfolioView
        data={{ portfolio, loading: false, error: null }}
        month="2026-11"
        period="2026"
        from="2026-01"
        to="2026-11"
        scope="month"
        onScopeChange={() => {}}
      />,
    )
    const row = screen.getByRole('row', { name: /Дубов'язівський елеватор/ })
    expect(within(row).getByText(formatPercent(-0.374))).toBeTruthy()
  })
})
