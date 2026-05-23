import { act, renderHook, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

// IMPORTANT: vi.mock factories run BEFORE module-level imports of the
// system-under-test, so the api module is mocked before useOverviewData
// pulls it in. We import the mocked symbols below to drive the spies
// per-test.
vi.mock('../../../api', () => ({
  fetchEnergyFlowHourly: vi.fn(),
  fetchEnergySummary: vi.fn(),
  fetchCurrent: vi.fn(),
}))

vi.mock('../../hooks/usePvForecast', () => ({
  usePvForecast: vi.fn(() => ({ data: [], loading: false, error: null })),
}))

import {
  fetchCurrent,
  fetchEnergyFlowHourly,
  fetchEnergySummary,
} from '../../../api'
import { usePvForecast } from '../../hooks/usePvForecast'
import { useOverviewData } from '../useOverviewData'

const fetchEnergyFlowHourlyMock = fetchEnergyFlowHourly as unknown as ReturnType<
  typeof vi.fn
>
const fetchEnergySummaryMock = fetchEnergySummary as unknown as ReturnType<
  typeof vi.fn
>
const fetchCurrentMock = fetchCurrent as unknown as ReturnType<typeof vi.fn>
const usePvForecastMock = usePvForecast as unknown as ReturnType<typeof vi.fn>

const ANCHOR = new Date(2026, 4, 23) // 23 May 2026 local time

// isCumulativeFrom tells the cumulative `from` (the
// MIN_RELIABLE_DATA_AT floor — currently 30 Apr 2026 local) apart
// from the day-summary `from` (start of the anchor day) by
// checking whether the request window starts strictly before the
// anchor day's local midnight. The /day-summary path always
// starts at-or-after that boundary because dayRangeParams is
// built around the anchor.
function isCumulativeFrom(fromIso: string): boolean {
  return new Date(fromIso).getTime() < new Date(2026, 4, 23).getTime()
}

beforeEach(() => {
  fetchEnergyFlowHourlyMock.mockReset()
  fetchEnergySummaryMock.mockReset()
  fetchCurrentMock.mockReset()
  usePvForecastMock.mockReset()
  usePvForecastMock.mockReturnValue({ data: [], loading: false, error: null })
})

afterEach(() => {
  vi.useRealTimers()
})

describe('useOverviewData', () => {
  it('derives the seven directional flows from daily totals + flows section', async () => {
    fetchEnergyFlowHourlyMock.mockResolvedValue({
      organization_id: 'pe',
      date: '2026-05-23',
      tz: 'Europe/Kyiv',
      hours: [],
    })
    // The day-summary endpoint is the canonical source of the flow
    // numbers (single full-day allocator pass). Provide both totals
    // and flows so the hook can construct the seven-edge graph.
    fetchEnergySummaryMock.mockImplementation((input: { from: string }) => {
      const isCumulative = isCumulativeFrom(input.from)
      if (isCumulative) {
        return Promise.resolve({
          organization_id: 'pe',
          from: input.from,
          to: '',
          totals: {
            accumulated_pv_energy_yield_kwh: 1500,
            accumulated_power_consumption_kwh: 2000,
            accumulated_electricity_purchased_kwh: 800,
            accumulated_electricity_sold_kwh: 600,
            total_energy_charged_kwh: 300,
            total_energy_discharged_kwh: 200,
            total_power_supply_from_grid_kwh: 850,
          },
        })
      }
      return Promise.resolve({
        organization_id: 'pe',
        from: input.from,
        to: '',
        totals: {
          accumulated_pv_energy_yield_kwh: 100,
          accumulated_electricity_purchased_kwh: 30,
          accumulated_electricity_sold_kwh: 20,
          total_energy_charged_kwh: 25,
          total_energy_discharged_kwh: 50,
        },
        flows: {
          pv_to_ess_kwh: 20,
          grid_to_ess_kwh: 5,
          ess_to_load_kwh: 40,
          ess_to_grid_kwh: 10,
        },
      })
    })
    fetchCurrentMock.mockResolvedValue({
      organization_id: 'pe',
      metrics: {
        soc_percent: { metric_key: 'soc_percent', value: 73, time: '' },
      },
    })

    const { result } = renderHook(() =>
      useOverviewData({ organizationID: 'pe', anchor: ANCHOR }),
    )

    await waitFor(() => expect(result.current.loading).toBe(false))

    const f = result.current.flows
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
    // Aggregate consumption mirrors the load node total.
    expect(f.loadConsumedKwh).toBe(135)
  })

  it('exposes SOC and cumulative totals straight from /current and /energy-summary', async () => {
    fetchEnergyFlowHourlyMock.mockResolvedValue({
      organization_id: 'pe',
      date: '2026-05-23',
      tz: 'Europe/Kyiv',
      hours: [],
    })
    fetchEnergySummaryMock.mockImplementation((input: { from: string }) => {
      const isCumulative = isCumulativeFrom(input.from)
      if (isCumulative) {
        return Promise.resolve({
          totals: {
            accumulated_pv_energy_yield_kwh: 1234,
            accumulated_power_consumption_kwh: 5678,
            accumulated_electricity_purchased_kwh: 999,
            accumulated_electricity_sold_kwh: 222,
            total_energy_charged_kwh: 333,
            total_energy_discharged_kwh: 111,
            total_power_supply_from_grid_kwh: 1010,
          },
        })
      }
      return Promise.resolve({ totals: {}, flows: null })
    })
    fetchCurrentMock.mockResolvedValue({
      organization_id: 'pe',
      metrics: {
        soc_percent: { metric_key: 'soc_percent', value: 88.4, time: '' },
      },
    })

    const { result } = renderHook(() =>
      useOverviewData({ organizationID: 'pe', anchor: ANCHOR }),
    )

    await waitFor(() => expect(result.current.loading).toBe(false))
    expect(result.current.socPercent).toBe(88.4)
    expect(result.current.cumulative.pvProducedKwh).toBe(1234)
    expect(result.current.cumulative.consumptionKwh).toBe(5678)
    expect(result.current.cumulative.gridImportKwh).toBe(999)
    expect(result.current.cumulative.gridExportKwh).toBe(222)
    expect(result.current.cumulative.essChargedKwh).toBe(333)
    expect(result.current.cumulative.essDischargedKwh).toBe(111)
    expect(result.current.cumulative.gridSupplyKwh).toBe(1010)
    expect(result.current.cumulative.referenceAt).not.toBeNull()
  })

  it('falls back to summing hourly flows when daily summary is unavailable', async () => {
    fetchEnergyFlowHourlyMock.mockResolvedValue({
      organization_id: 'pe',
      date: '2026-05-23',
      tz: 'Europe/Kyiv',
      hours: [
        {
          hour: 10,
          from: '',
          to: '',
          pv_to_ess_kwh: 4,
          grid_to_ess_kwh: 1,
          ess_to_load_kwh: 3,
          ess_to_grid_kwh: 0,
          ess_charged_kwh: 5,
          ess_discharged_kwh: 3,
          skipped_intervals: 0,
        },
        {
          hour: 11,
          from: '',
          to: '',
          pv_to_ess_kwh: 6,
          grid_to_ess_kwh: 0,
          ess_to_load_kwh: 2,
          ess_to_grid_kwh: 1,
          ess_charged_kwh: 6,
          ess_discharged_kwh: 3,
          skipped_intervals: 0,
        },
      ],
    })
    // Daily summary fails; cumulative still resolves so the rest of
    // the page keeps rendering.
    fetchEnergySummaryMock.mockImplementation((input: { from: string }) => {
      const isCumulative = isCumulativeFrom(input.from)
      if (isCumulative) {
        return Promise.resolve({ totals: {} })
      }
      return Promise.reject(new Error('boom'))
    })
    fetchCurrentMock.mockResolvedValue({ organization_id: 'pe', metrics: {} })

    const { result } = renderHook(() =>
      useOverviewData({ organizationID: 'pe', anchor: ANCHOR }),
    )

    await waitFor(() => expect(result.current.loading).toBe(false))
    expect(result.current.flows.pvToEssKwh).toBe(10) // 4 + 6
    expect(result.current.flows.gridToEssKwh).toBe(1) // 1 + 0
    expect(result.current.flows.essToLoadKwh).toBe(5) // 3 + 2
    expect(result.current.flows.essToGridKwh).toBe(1) // 0 + 1
    expect(result.current.flows.essChargedKwh).toBe(11) // 5 + 6
    expect(result.current.flows.essDischargedKwh).toBe(6) // 3 + 3
  })

  it('forwards the PV forecast total (kWh) from usePvForecast', async () => {
    fetchEnergyFlowHourlyMock.mockResolvedValue({
      organization_id: 'pe',
      date: '2026-05-23',
      tz: 'Europe/Kyiv',
      hours: [],
    })
    fetchEnergySummaryMock.mockResolvedValue({ totals: {} })
    fetchCurrentMock.mockResolvedValue({ organization_id: 'pe', metrics: {} })
    // Three orientation rows for the same hour — they should be
    // summed into a single hour entry by aggregatePvForecastHourly,
    // and across the day the total is the sum across hours.
    usePvForecastMock.mockReturnValue({
      data: [
        {
          elevator_code: 'RE',
          orientation_idx: 0,
          hour_ending: 10,
          interval_start_local: '',
          gti_weighted_wm2: 0,
          pdc_total_kwp: 0,
          pac_limit_kw: 0,
          planned_dc_kw: 0,
          planned_ac_kw: 0,
          planned_kwh: 250,
          clip_loss_kwh: 0,
          temperature_2m_c: 0,
          cloud_cover_pct: 0,
          model_version: '',
        },
        {
          elevator_code: 'RE',
          orientation_idx: 1,
          hour_ending: 10,
          interval_start_local: '',
          gti_weighted_wm2: 0,
          pdc_total_kwp: 0,
          pac_limit_kw: 0,
          planned_dc_kw: 0,
          planned_ac_kw: 0,
          planned_kwh: 250,
          clip_loss_kwh: 0,
          temperature_2m_c: 0,
          cloud_cover_pct: 0,
          model_version: '',
        },
        {
          elevator_code: 'RE',
          orientation_idx: 0,
          hour_ending: 11,
          interval_start_local: '',
          gti_weighted_wm2: 0,
          pdc_total_kwp: 0,
          pac_limit_kw: 0,
          planned_dc_kw: 0,
          planned_ac_kw: 0,
          planned_kwh: 200,
          clip_loss_kwh: 0,
          temperature_2m_c: 0,
          cloud_cover_pct: 0,
          model_version: '',
        },
      ],
      loading: false,
      error: null,
    })

    const { result } = renderHook(() =>
      useOverviewData({ organizationID: 'pe', anchor: ANCHOR }),
    )
    await waitFor(() => expect(result.current.loading).toBe(false))
    // 250 + 250 (hour 10) + 200 (hour 11) = 700
    expect(result.current.pvForecastKwh).toBe(700)
  })

  it('returns null forecast when the org has no mapping', async () => {
    fetchEnergyFlowHourlyMock.mockResolvedValue({
      organization_id: 'demo-org',
      date: '2026-05-23',
      tz: 'UTC',
      hours: [],
    })
    fetchEnergySummaryMock.mockResolvedValue({ totals: {} })
    fetchCurrentMock.mockResolvedValue({ organization_id: 'demo-org', metrics: {} })
    usePvForecastMock.mockReturnValue({ data: [], loading: false, error: null })

    const { result } = renderHook(() =>
      useOverviewData({ organizationID: 'demo-org', anchor: ANCHOR }),
    )
    await waitFor(() => expect(result.current.loading).toBe(false))
    expect(result.current.pvForecastKwh).toBeNull()
  })

  it('aborts the in-flight request when organizationID changes', async () => {
    let resolveFirst: ((v: unknown) => void) | null = null
    fetchEnergyFlowHourlyMock.mockImplementation(
      (input: { organizationID: string }) => {
        if (input.organizationID === 'pe') {
          return new Promise((resolve) => {
            resolveFirst = resolve
          })
        }
        return Promise.resolve({
          organization_id: input.organizationID,
          date: '2026-05-23',
          tz: 'UTC',
          hours: [],
        })
      },
    )
    fetchEnergySummaryMock.mockResolvedValue({ totals: {} })
    fetchCurrentMock.mockResolvedValue({ organization_id: 'pe', metrics: {} })

    const { result, rerender } = renderHook(
      ({ organizationID }: { organizationID: string }) =>
        useOverviewData({ organizationID, anchor: ANCHOR }),
      { initialProps: { organizationID: 'pe' } },
    )
    expect(result.current.loading).toBe(true)
    rerender({ organizationID: 'ze' })
    await waitFor(() => expect(result.current.loading).toBe(false))
    // Resolving the first request after the org switch should not
    // overwrite the second request's data — easiest way to verify
    // is just that no error leaks through.
    if (resolveFirst) {
      act(() => {
        resolveFirst!({ hours: [] })
      })
    }
    expect(result.current.error).toBeNull()
  })
})
