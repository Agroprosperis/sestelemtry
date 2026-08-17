import { renderHook, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { EconomicsMonthlyResponse } from '../../api'
import { formatMwh, formatShare } from '../monthly/format'
import { useEconomicsMonthlyData } from '../useEconomicsMonthlyData'

// The month rollup is computed server-side; the hook only fetches and
// surfaces it. We mock the api module so the hook is exercised alone.
vi.mock('../../api', () => ({
  fetchEconomicsMonthly: vi.fn(),
}))

import { fetchEconomicsMonthly } from '../../api'

const mockedFetch = vi.mocked(fetchEconomicsMonthly)

afterEach(() => {
  vi.clearAllMocks()
})

function emptyTotals(): EconomicsMonthlyResponse['totals'] {
  return {
    baseline_cost_uah: 0,
    actual_cost_uah: 0,
    effect_uah: 0,
    ess_net_uah: 0,
    load_kwh: 0,
    pv_kwh: 0,
    grid_import_kwh: 0,
    grid_export_kwh: 0,
    ess_charged_kwh: 0,
    ess_discharged_kwh: 0,
    pv_to_load_kwh: 0,
    pv_to_ess_kwh: 0,
    pv_to_grid_kwh: 0,
    grid_to_load_kwh: 0,
    grid_to_ess_kwh: 0,
    ess_to_load_kwh: 0,
    ess_to_grid_kwh: 0,
    avg_import_price_uah_per_kwh: 0,
    avg_export_price_uah_per_kwh: 0,
    rdn_avg_uah_per_kwh: 0,
    rdn_max_uah_per_kwh: 0,
    revenue_pv_export_uah: 0,
    revenue_pv_self_uah: 0,
    revenue_ess_export_uah: 0,
    revenue_ess_self_uah: 0,
    revenue_total_uah: 0,
    expense_grid_charge_uah: 0,
    expense_total_uah: 0,
    ebitda_uah: 0,
    pv_export_potential_uah: 0,
    pv_ess_export_potential_uah: 0,
    import_cost_uah: 0,
    ess_withdrawn_cost_uah: 0,
    ess_realized_profit_uah: 0,
    ess_degradation_cost_uah: 0,
    ess_avg_cost_basis_uah_per_kwh_eod: 0,
    ess_residual_kwh_eod: 0,
    ess_cost_basis_uah_eod: 0,
    equivalent_cycles: 0,
    days_with_data: 0,
    hours_with_data: 0,
    hours_missing_price: 0,
    flagged_days: 0,
    ess_fact_uah: 0,
    ess_optimum_uah: 0,
    ess_reserve_uah: 0,
    ess_captured_share: 0,
    ess_reserve_timing_uah: 0,
    ess_reserve_soc_uah: 0,
    ess_reserve_pv_uah: 0,
    ess_pv_missed_kwh: 0,
    best_day: { date: '', effect_uah: 0 },
    min_effect_day: { date: '', effect_uah: 0 },
    ess_data_quality: {
      data_ok: true,
      total_days: 0,
      anomalous_hours: 0,
      anomalous_days: 0,
      anomalous_dates: null,
      max_charge_kwh_per_interval: 0,
      max_discharge_kwh_per_interval: 0,
      power_limit_kwh_per_interval: 0,
      max_interval_power_kw: 0,
    },
  }
}

describe('useEconomicsMonthlyData', () => {
  it('returns the server month rollup', async () => {
    const resp: EconomicsMonthlyResponse = {
      organization_id: 'org1',
      month: '2026-06',
      tz: 'Europe/Kyiv',
      days_in_month: 1,
      totals: { ...emptyTotals(), effect_uah: 1234, equivalent_cycles: 1.5 },
      days: [],
      hourly_margin: [],
      uze_cycles: [],
    }
    mockedFetch.mockResolvedValue(resp)

    const { result } = renderHook(() =>
      useEconomicsMonthlyData({ organizationID: 'org1', month: '2026-06' }),
    )

    await waitFor(() => expect(result.current.loading).toBe(false))
    expect(result.current.error).toBeNull()
    expect(result.current.month?.totals.effect_uah).toBe(1234)
    expect(result.current.month?.totals.equivalent_cycles).toBe(1.5)
  })

  it('surfaces fetch errors', async () => {
    mockedFetch.mockRejectedValue(new Error('boom'))
    const { result } = renderHook(() =>
      useEconomicsMonthlyData({ organizationID: 'org1', month: '2026-06' }),
    )
    await waitFor(() => expect(result.current.loading).toBe(false))
    expect(result.current.error).toBe('boom')
  })

  it('stays idle with no org/month', () => {
    const { result } = renderHook(() =>
      useEconomicsMonthlyData({ organizationID: '', month: '' }),
    )
    expect(result.current.loading).toBe(false)
    expect(mockedFetch).not.toHaveBeenCalled()
  })
})

describe('monthly formatters', () => {
  it('formats kWh as MWh with one decimal', () => {
    expect(formatMwh(12340)).toBe('12,3 МВт·год')
  })

  it('guards share against a zero denominator', () => {
    expect(formatShare(5, 0)).toBe('—')
    expect(formatShare(25, 100)).toBe('25,0%')
  })
})
