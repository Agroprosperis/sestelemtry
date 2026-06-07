import { renderHook, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { EconomicsDailyResponse, EconomicsHourApi } from '../../api'
import { useEconomicsData } from '../useEconomicsData'

// useEconomicsData now makes a single call to /economics/daily and maps
// the flat wire shape into the nested HourEconomicsRow the charts/table
// consume. We mock the api module so the hook is exercised in isolation.
vi.mock('../../api', () => ({
  fetchEconomicsDaily: vi.fn(),
}))

import { fetchEconomicsDaily } from '../../api'

const mockedFetch = vi.mocked(fetchEconomicsDaily)

afterEach(() => {
  vi.clearAllMocks()
})

function hour(partial: Partial<EconomicsHourApi> & { hour: number }): EconomicsHourApi {
  return {
    hour: partial.hour,
    hour_start: `2026-04-01T${String(partial.hour).padStart(2, '0')}:00:00+03:00`,
    rdn_uah_per_kwh: null,
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
    import_price_uah_per_kwh: 0,
    export_price_uah_per_kwh: 0,
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
    ...partial,
  }
}

describe('useEconomicsData', () => {
  it('maps the server response into HourEconomicsRow[]', async () => {
    const hours: Array<EconomicsHourApi | null> = Array.from({ length: 24 }, (_, h) =>
      h === 0 ? null : hour({ hour: h }),
    )
    hours[1] = hour({
      hour: 1,
      rdn_uah_per_kwh: 2,
      pv_kwh: 10,
      grid_import_kwh: 5,
      grid_export_kwh: 2,
      load_kwh: 13,
      pv_to_load_kwh: 8,
      pv_to_grid_kwh: 2,
      grid_to_load_kwh: 5,
      import_price_uah_per_kwh: 2,
      export_price_uah_per_kwh: 2,
      baseline_cost_uah: 26,
      ess_remaining_kwh_start: 100,
    })
    const resp: EconomicsDailyResponse = {
      organization_id: 'org1',
      date: '2026-04-01',
      tz: 'Europe/Kyiv',
      is_final: true,
      hours_missing_price: 22,
      hours,
    }
    mockedFetch.mockResolvedValue(resp)

    const { result } = renderHook(() =>
      useEconomicsData({ organizationID: 'org1', date: '2026-04-01' }),
    )

    await waitFor(() => expect(result.current.loading).toBe(false))
    expect(result.current.error).toBeNull()
    expect(result.current.hoursMissingPrice).toBe(22)
    expect(result.current.rows[0]).toBeNull()
    const row1 = result.current.rows[1]
    expect(row1).not.toBeNull()
    expect(row1?.rdnUahPerKwh).toBe(2)
    expect(row1?.flow.pv).toBe(10)
    expect(row1?.economics.load).toBe(13)
    expect(row1?.economics.pvToLoad).toBe(8)
    expect(row1?.essRemainingKwhStart).toBe(100)
  })

  it('surfaces fetch errors', async () => {
    mockedFetch.mockRejectedValue(new Error('boom'))
    const { result } = renderHook(() =>
      useEconomicsData({ organizationID: 'org1', date: '2026-04-01' }),
    )
    await waitFor(() => expect(result.current.loading).toBe(false))
    expect(result.current.error).toBe('boom')
  })

  it('stays idle with no org/date', () => {
    const { result } = renderHook(() => useEconomicsData({ organizationID: '', date: '' }))
    expect(result.current.loading).toBe(false)
    expect(mockedFetch).not.toHaveBeenCalled()
  })
})
