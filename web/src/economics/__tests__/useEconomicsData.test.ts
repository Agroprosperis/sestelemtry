import { describe, expect, it } from 'vitest'
import { pickInitialEssState, preRollYesterday } from '../useEconomicsData'
import { ZERO_ESS_STATE } from '../costBasis'
import { DEFAULT_TARIFFS } from '../tariffs'
import type {
  DAMPrice,
  EnergyFlowHourlyResponse,
  EnergyFlowHourlyRow,
  TimeseriesResponse,
} from '../../types'

// makeFlowHour fabricates a single EnergyFlowHourlyRow at hour `h`.
// Hours that don't appear in the override list emit zero flow.
function makeFlowHour(h: number, overrides: Partial<EnergyFlowHourlyRow> = {}): EnergyFlowHourlyRow {
  return {
    hour: h,
    from: `2026-05-25T${String(h).padStart(2, '0')}:00:00+03:00`,
    to: `2026-05-25T${String(h + 1).padStart(2, '0')}:00:00+03:00`,
    pv_to_ess_kwh: 0,
    grid_to_ess_kwh: 0,
    ess_to_load_kwh: 0,
    ess_to_grid_kwh: 0,
    ess_charged_kwh: 0,
    ess_discharged_kwh: 0,
    skipped_intervals: 0,
    ...overrides,
  }
}

function makeFlowResponse(rows: Partial<Record<number, EnergyFlowHourlyRow>>): EnergyFlowHourlyResponse {
  return {
    organization_id: 'test',
    date: '2026-05-25',
    tz: 'Europe/Kyiv',
    hours: Array.from({ length: 24 }, (_, h) => rows[h] ?? makeFlowHour(h)),
  }
}

function makeEmptyDeltas(): TimeseriesResponse {
  return {
    organization_id: 'test',
    metric_keys: [],
    bucket: '1 hour',
    from: '2026-05-25T00:00:00+03:00',
    to: '2026-05-26T00:00:00+03:00',
    points: [],
  }
}

function makeDamPrices(prices: Record<number, number>): DAMPrice[] {
  // The frontend's buildPriceMap shifts hour-1 to 0-indexed, so we
  // emit DAM rows in the (1..24, hour-ending) convention the
  // upstream XLS uses.
  return Array.from({ length: 24 }, (_, idx) => ({
    delivery_date: '2026-05-25',
    hour: idx + 1,
    zone: 2,
    price_uah_per_mwh: idx in prices ? (prices[idx] as number) * 1000 : null,
  }))
}

describe('pickInitialEssState', () => {
  it('prefers SOC over preRollYesterday for the kWh leg', () => {
    // Yesterday's pre-roll would settle at kwh=100 (a 100 kWh
    // grid charge in hour 2 with no later activity). But SOC at
    // midnight reads 39.9% × 215 kWh = ~85.8 kWh — the actual
    // gauge. The cost-basis row should align with the gauge so
    // the displayed "Залишок УЗЕ" and "Собівартість УЗЕ" rows
    // can't disagree at hour 0.
    const tariffs = {
      ...DEFAULT_TARIFFS,
      distributionUahPerKwh: 0,
      transmissionUahPerKwh: 0,
      supplierMarginUahPerKwh: 0,
      otherFeesUahPerKwh: 0,
      degradationUahPerKwh: 0,
      seedEssCostUahPerKwh: 12,
      essCapacityKwh: 215,
    }
    const yFlows = makeFlowResponse({
      2: makeFlowHour(2, { grid_to_ess_kwh: 100, ess_charged_kwh: 100 }),
    })
    const yDam = makeDamPrices({ 2: 2 })
    const state = pickInitialEssState(
      yFlows,
      makeEmptyDeltas(),
      yDam,
      tariffs,
      39.9,
    )
    expect(state.kwh).toBeCloseTo((39.9 / 100) * 215, 6)
    expect(state.uah).toBeCloseTo((39.9 / 100) * 215 * 12, 6)
  })

  it('falls back to preRollYesterday when SOC is missing', () => {
    // Yesterday charges 100 kWh from grid in hour 2 → pre-roll
    // returns kwh=100. With seed=10 → uah=1000.
    const tariffs = {
      ...DEFAULT_TARIFFS,
      distributionUahPerKwh: 0,
      transmissionUahPerKwh: 0,
      supplierMarginUahPerKwh: 0,
      otherFeesUahPerKwh: 0,
      degradationUahPerKwh: 0,
      seedEssCostUahPerKwh: 10,
    }
    const yFlows = makeFlowResponse({
      2: makeFlowHour(2, { grid_to_ess_kwh: 100, ess_charged_kwh: 100 }),
    })
    const yDam = makeDamPrices({ 2: 2 })
    const state = pickInitialEssState(yFlows, makeEmptyDeltas(), yDam, tariffs, undefined)
    expect(state.kwh).toBeCloseTo(100, 6)
    expect(state.uah).toBeCloseTo(1000, 6)
  })

  it('preserves the kWh balance even when seed=0', () => {
    // SOC=25% × 200 kWh capacity = 50 kWh. Seed=0 → uah=0.
    const tariffs = {
      ...DEFAULT_TARIFFS,
      seedEssCostUahPerKwh: 0,
      essCapacityKwh: 200,
    }
    const state = pickInitialEssState(null, null, null, tariffs, 25)
    expect(state.kwh).toBeCloseTo(50, 6)
    expect(state.uah).toBeCloseTo(0, 6)
  })

  it('uses seedEssCostUahPerKwh × SOC when both signals are present', () => {
    const tariffs = { ...DEFAULT_TARIFFS, seedEssCostUahPerKwh: 1.5, essCapacityKwh: 200 }
    // SOC = 25% → residual 50 kWh; seed price 1.5 → uah = 75.
    const state = pickInitialEssState(null, null, null, tariffs, 25)
    expect(state.kwh).toBeCloseTo(50, 6)
    expect(state.uah).toBeCloseTo(75, 6)
  })

  it('returns ZERO_ESS_STATE when no yesterday and no SOC anchor', () => {
    const state = pickInitialEssState(null, null, null, DEFAULT_TARIFFS, undefined)
    expect(state).toEqual(ZERO_ESS_STATE)
  })

  it('returns ZERO_ESS_STATE when yesterday is partially missing and SOC is too', () => {
    // yFlows present but yDam absent → can't pre-roll. No SOC
    // either → seed × 0 = ZERO state.
    const yFlows = makeFlowResponse({
      2: makeFlowHour(2, { grid_to_ess_kwh: 100, ess_charged_kwh: 100 }),
    })
    const state = pickInitialEssState(yFlows, makeEmptyDeltas(), null, DEFAULT_TARIFFS, undefined)
    expect(state).toEqual(ZERO_ESS_STATE)
  })
})

describe('preRollYesterday null-RDN handling', () => {
  it('skips Grid→УЗЕ charges in null-RDN hours instead of logging them as free', () => {
    // Grid→УЗЕ 100 kWh in hour 2 with NO RDN price published. If
    // we naively rolled with importPrice=0, the next priced hour
    // would discharge the basis at avg=0 (free). Skipping keeps
    // state empty until a priced hour actually appears.
    const tariffs = {
      ...DEFAULT_TARIFFS,
      distributionUahPerKwh: 0,
      transmissionUahPerKwh: 0,
      supplierMarginUahPerKwh: 0,
      otherFeesUahPerKwh: 0,
      degradationUahPerKwh: 0,
    }
    const yFlows = makeFlowResponse({
      2: makeFlowHour(2, { grid_to_ess_kwh: 100, ess_charged_kwh: 100 }),
    })
    // No price for hour 2.
    const yDam = makeDamPrices({})
    const state = preRollYesterday(yFlows, makeEmptyDeltas(), yDam, tariffs)
    expect(state).toEqual(ZERO_ESS_STATE)
  })

  it('rolls only priced hours and ignores unpriced ones', () => {
    const tariffs = {
      ...DEFAULT_TARIFFS,
      distributionUahPerKwh: 0,
      transmissionUahPerKwh: 0,
      supplierMarginUahPerKwh: 0,
      otherFeesUahPerKwh: 0,
      degradationUahPerKwh: 0,
    }
    // Hour 2: 100 kWh grid charge @ RDN=2 (PRICED).
    // Hour 5: 50 kWh grid charge with NO RDN (skipped).
    // Hour 10: 50 kWh discharge to load @ RDN=8 (PRICED).
    // After the fix: state after hour 2 = (100, 200). Hour 5 is
    // skipped → state still (100, 200). Hour 10 discharges 50
    // → state = (50, 100).
    const yFlows = makeFlowResponse({
      2: makeFlowHour(2, { grid_to_ess_kwh: 100, ess_charged_kwh: 100 }),
      5: makeFlowHour(5, { grid_to_ess_kwh: 50, ess_charged_kwh: 50 }),
      10: makeFlowHour(10, { ess_to_load_kwh: 50, ess_discharged_kwh: 50 }),
    })
    const yDam = makeDamPrices({ 2: 2, 10: 8 })
    const state = preRollYesterday(yFlows, makeEmptyDeltas(), yDam, tariffs)
    expect(state.kwh).toBeCloseTo(50, 6)
    expect(state.uah).toBeCloseTo(100, 6)
  })
})
