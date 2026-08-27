import { act, renderHook, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import * as api from '../../api'
import type { RangePreset } from '../range'
import { useDashboardData } from './useDashboardData'

// The dashboard's own start-date floor hides anything before this day,
// so the anchor has to sit after it for the hook to fetch at all.
const ANCHOR = new Date(2026, 6, 15)

// A day's worth of allocator output, and a month's worth of cached
// rollup. Kept clearly apart in magnitude so a test can tell which
// pipeline's answer ended up on screen.
const DAY_FLOWS: api.EnergyFlowTotals = {
  pv_to_ess_kwh: 282,
  grid_to_ess_kwh: 4,
  ess_to_load_kwh: 158,
  ess_to_grid_kwh: 0,
}

const DAY_TOTALS = {
  accumulated_pv_energy_yield_kwh: 417,
  accumulated_electricity_purchased_kwh: 12,
  accumulated_electricity_sold_kwh: 42,
  total_energy_charged_kwh: 286,
  total_energy_discharged_kwh: 158,
}

const MONTH_FLOWS: api.EnergyFlowTotals = {
  pv_to_ess_kwh: 8000,
  grid_to_ess_kwh: 300,
  ess_to_load_kwh: 7600,
  ess_to_grid_kwh: 120,
}

const MONTH_TOTALS = {
  accumulated_pv_energy_yield_kwh: 60000,
  accumulated_electricity_purchased_kwh: 900,
  accumulated_electricity_sold_kwh: 5000,
  total_energy_charged_kwh: 8300,
  total_energy_discharged_kwh: 7720,
}

// Anything wider than the API's day-sized allocator budget is served
// from the persisted per-day rollup instead.
const MAX_ALLOCATOR_WINDOW_MS = 36 * 60 * 60 * 1000

// deferred hands back a promise plus the switch that settles it, so a
// test can hold one pipeline mid-flight while the period changes.
function deferred<T>() {
  let resolve: (value: T) => void = () => {}
  const promise = new Promise<T>((r) => {
    resolve = r
  })
  return { promise, resolve }
}

function summaryResponse(
  totals: Record<string, number>,
  flows: api.EnergyFlowTotals | null,
  meta?: api.EnergyFlowMeta,
): api.EnergySummaryResponse {
  return {
    organization_id: 'ab',
    from: ANCHOR.toISOString(),
    to: ANCHOR.toISOString(),
    totals,
    flows,
    flows_meta: meta ?? null,
  }
}

beforeEach(() => {
  vi.spyOn(api, 'fetchDashboardConfig').mockResolvedValue({
    cards: [],
    power_chart: [],
    energy_chart: [{ key: 'accumulated_pv_energy_yield_kwh', label: 'PV', unit: 'kWh' }],
  })
  vi.spyOn(api, 'fetchCurrent').mockResolvedValue({
    organization_id: 'ab',
    metrics: {},
  })
  vi.spyOn(api, 'fetchTimeseries').mockImplementation(async (input) => ({
    organization_id: input.organizationID,
    metric_keys: input.metricKeys,
    bucket: input.bucket,
    from: input.from,
    to: input.to,
    points: [],
  }))
  vi.spyOn(api, 'fetchDAMPrices').mockResolvedValue({
    zone: 2,
    from: '2026-07-15',
    to: '2026-07-15',
    prices: [],
  })
  vi.spyOn(api, 'fetchPvForecast').mockResolvedValue([])
  vi.spyOn(api, 'fetchPvPlanSummary').mockResolvedValue({
    organization_id: 'ab',
    supported: false,
    planned_kwh: 0,
    days_covered: 0,
    days_expected: 0,
  })
})

afterEach(() => {
  vi.restoreAllMocks()
})

describe('useDashboardData period flows', () => {
  it('publishes the allocator result for the day preset', async () => {
    vi.spyOn(api, 'fetchEnergySummary').mockResolvedValue(
      summaryResponse(DAY_TOTALS, DAY_FLOWS, { source: 'allocator' }),
    )
    const { result } = renderHook(() =>
      useDashboardData({ organizationID: 'ab', preset: 'day', anchor: ANCHOR }),
    )
    await waitFor(() => expect(result.current.flowsLoaded).toBe(true))
    expect(result.current.energyFlows.pvToEssKwh).toBe(282)
    expect(result.current.flowsGap).toBeNull()
  })

  it('publishes the cached per-day rollup for the month preset', async () => {
    vi.spyOn(api, 'fetchEnergySummary').mockResolvedValue(
      summaryResponse(MONTH_TOTALS, MONTH_FLOWS, {
        source: 'daily_cache',
        days_covered: 31,
        days_expected: 31,
      }),
    )
    const { result } = renderHook(() =>
      useDashboardData({ organizationID: 'ab', preset: 'month', anchor: ANCHOR }),
    )
    await waitFor(() => expect(result.current.flowsLoaded).toBe(true))
    expect(result.current.energyFlows.pvToEssKwh).toBe(8000)
    expect(result.current.energyFlows.pvProducedKwh).toBe(60000)
    expect(result.current.flowsGap).toBeNull()
  })

  // A month whose cache is missing days sums short. The numbers are
  // real, so they stay on screen, but the card has to be able to say
  // the period isn't fully covered.
  it('reports the shortfall when the cache misses days', async () => {
    vi.spyOn(api, 'fetchEnergySummary').mockResolvedValue(
      summaryResponse(MONTH_TOTALS, MONTH_FLOWS, {
        source: 'daily_cache',
        days_covered: 24,
        days_expected: 31,
      }),
    )
    const { result } = renderHook(() =>
      useDashboardData({ organizationID: 'ab', preset: 'month', anchor: ANCHOR }),
    )
    await waitFor(() => expect(result.current.flowsLoaded).toBe(true))
    expect(result.current.flowsGap).toEqual({ covered: 24, expected: 31 })
  })

  // The current period is short by exactly one day for most of every
  // morning, until the economics daemon's hourly pass reaches today.
  // Flagging that would put a banner on screen daily over a few kWh.
  it('stays quiet when only the in-progress day is missing', async () => {
    vi.spyOn(api, 'fetchEnergySummary').mockResolvedValue(
      summaryResponse(MONTH_TOTALS, MONTH_FLOWS, {
        source: 'daily_cache',
        days_covered: 26,
        days_expected: 27,
      }),
    )
    const { result } = renderHook(() =>
      useDashboardData({ organizationID: 'ab', preset: 'month', anchor: ANCHOR }),
    )
    await waitFor(() => expect(result.current.flowsLoaded).toBe(true))
    expect(result.current.flowsGap).toBeNull()
  })

  // The day allocator takes 5–15 s on a busy day. Switching to month
  // mid-flight used to let that answer land afterwards and repopulate
  // the card, so the day's kWh sat under a "Підсумок за місяць" header
  // and looked like a card that refused to update.
  it('drops an allocator answer that arrives after the period changed', async () => {
    const pending = deferred<api.EnergySummaryResponse>()
    vi.spyOn(api, 'fetchEnergySummary').mockImplementation(async (input) => {
      const span = new Date(input.to).getTime() - new Date(input.from).getTime()
      // Hold the day request open; let the month request through.
      if (span <= MAX_ALLOCATOR_WINDOW_MS) return pending.promise
      return summaryResponse(MONTH_TOTALS, MONTH_FLOWS, {
        source: 'daily_cache',
        days_covered: 31,
        days_expected: 31,
      })
    })

    const { result, rerender } = renderHook(
      ({ preset }: { preset: RangePreset }) =>
        useDashboardData({ organizationID: 'ab', preset, anchor: ANCHOR }),
      { initialProps: { preset: 'day' as RangePreset } },
    )
    await waitFor(() => expect(api.fetchEnergySummary).toHaveBeenCalled())

    rerender({ preset: 'month' })
    await waitFor(() => expect(result.current.energyFlows.pvToEssKwh).toBe(8000))

    // Release the abandoned day request only once the month's numbers
    // are on screen, so "the stale answer lands last" is the ordering
    // under test rather than a race the mocks happen to win.
    await act(async () => {
      pending.resolve(summaryResponse(DAY_TOTALS, DAY_FLOWS, { source: 'allocator' }))
      await new Promise((resolve) => setTimeout(resolve, 0))
    })
    expect(result.current.energyFlows.pvToEssKwh).toBe(8000)
    expect(result.current.energyFlows.pvProducedKwh).toBe(60000)
  })
})

function planResponse(
  plannedKwh: number,
  daysCovered: number,
  daysExpected: number,
): api.PvPlanSummaryResponse {
  return {
    organization_id: 'ab',
    supported: true,
    planned_kwh: plannedKwh,
    days_covered: daysCovered,
    days_expected: daysExpected,
  }
}

describe('useDashboardData period plan', () => {
  beforeEach(() => {
    vi.spyOn(api, 'fetchEnergySummary').mockResolvedValue(
      summaryResponse(MONTH_TOTALS, MONTH_FLOWS, {
        source: 'daily_cache',
        days_covered: 31,
        days_expected: 31,
      }),
    )
  })

  it('publishes the period plan for the month preset', async () => {
    vi.spyOn(api, 'fetchPvPlanSummary').mockResolvedValue(planResponse(72000, 31, 31))
    const { result } = renderHook(() =>
      useDashboardData({ organizationID: 'ab', preset: 'month', anchor: ANCHOR }),
    )
    await waitFor(() => expect(result.current.pvForecastTotal).toBe(72000))
    expect(result.current.pvForecastLoading).toBe(false)
    expect(result.current.pvForecastCoverage).toBeNull()
  })

  // The forecast flow only keeps history back to its own deployment, so
  // an early period compares actuals against a plan for part of it.
  it('reports the shortfall when the plan misses days', async () => {
    vi.spyOn(api, 'fetchPvPlanSummary').mockResolvedValue(planResponse(40000, 18, 31))
    const { result } = renderHook(() =>
      useDashboardData({ organizationID: 'ab', preset: 'month', anchor: ANCHOR }),
    )
    await waitFor(() => expect(result.current.pvForecastTotal).toBe(40000))
    expect(result.current.pvForecastCoverage).toEqual({ covered: 18, expected: 31 })
  })

  it('hides the comparison for an organization the forecast flow does not cover', async () => {
    vi.spyOn(api, 'fetchPvPlanSummary').mockResolvedValue({
      organization_id: 'demo-org',
      supported: false,
      planned_kwh: 0,
      days_covered: 0,
      days_expected: 30,
    })
    const { result } = renderHook(() =>
      useDashboardData({ organizationID: 'demo-org', preset: 'month', anchor: ANCHOR }),
    )
    await waitFor(() => expect(result.current.pvForecastLoading).toBe(false))
    expect(result.current.pvForecastTotal).toBeNull()
    expect(result.current.pvForecastCoverage).toBeNull()
  })

  // A failed plan request hides the comparison; it says nothing about
  // the actuals sitting next to it, so the error banner stays clear.
  it('keeps the dashboard error channel clean when the plan fails', async () => {
    vi.spyOn(api, 'fetchPvPlanSummary').mockRejectedValue(new Error('upstream down'))
    const { result } = renderHook(() =>
      useDashboardData({ organizationID: 'ab', preset: 'month', anchor: ANCHOR }),
    )
    await waitFor(() => expect(result.current.pvForecastLoading).toBe(false))
    expect(result.current.pvForecastTotal).toBeNull()
    expect(result.current.error).toBeNull()
  })

  // The day preset already holds the hourly forecast it plots, so it
  // must sum that locally rather than paying for the period endpoint.
  it('sums the hourly forecast on the day preset without asking the server', async () => {
    vi.spyOn(api, 'fetchEnergySummary').mockResolvedValue(
      summaryResponse(DAY_TOTALS, DAY_FLOWS, { source: 'allocator' }),
    )
    vi.spyOn(api, 'fetchPvForecast').mockResolvedValue([
      { hour_ending: 11, orientation_idx: 1, planned_kwh: 120 },
      { hour_ending: 12, orientation_idx: 1, planned_kwh: 180 },
    ] as never)
    // usePvForecast caches per (site, day) for five minutes, so this
    // needs a day the earlier tests in this file haven't already
    // cached an empty forecast for.
    const { result } = renderHook(() =>
      useDashboardData({
        organizationID: 'ab',
        preset: 'day',
        anchor: new Date(2026, 6, 16),
      }),
    )
    await waitFor(() => expect(result.current.pvForecastTotal).toBe(300))
    expect(api.fetchPvPlanSummary).not.toHaveBeenCalled()
  })

  // Same race the flows guard against: the plan for a cold year takes
  // seconds to fill upstream, and that answer must not land under a
  // header the operator has already changed away from.
  it('drops a plan answer that arrives after the period changed', async () => {
    const pending = deferred<api.PvPlanSummaryResponse>()
    vi.spyOn(api, 'fetchPvPlanSummary').mockImplementation(async (input) => {
      const span = new Date(input.to).getTime() - new Date(input.from).getTime()
      // Hold the month request open; answer the (wider) year request.
      if (span < 60 * 24 * 60 * 60 * 1000) return pending.promise
      return planResponse(600000, 365, 365)
    })

    const { result, rerender } = renderHook(
      ({ preset }: { preset: RangePreset }) =>
        useDashboardData({ organizationID: 'ab', preset, anchor: ANCHOR }),
      { initialProps: { preset: 'month' as RangePreset } },
    )
    await waitFor(() => expect(api.fetchPvPlanSummary).toHaveBeenCalled())

    rerender({ preset: 'year' })
    await waitFor(() => expect(result.current.pvForecastTotal).toBe(600000))

    await act(async () => {
      pending.resolve(planResponse(72000, 31, 31))
      await new Promise((resolve) => setTimeout(resolve, 0))
    })
    expect(result.current.pvForecastTotal).toBe(600000)
  })
})
