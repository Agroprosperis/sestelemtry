import { describe, expect, it } from 'vitest'
import { findAnchorAndPreRoll } from '../useEconomicsData'
import { ZERO_ESS_STATE } from '../costBasis'
import { DEFAULT_TARIFFS } from '../tariffs'
import type { HourFlows } from '../compute'

// Tariffs preset with all per-kWh additions zeroed out so RDN ==
// importPrice/exportPrice. Keeps the cost-basis arithmetic in the
// tests easy to follow without hourEconomics adding hidden
// distribution / transmission / VAT layers on top.
const FLAT_TARIFFS = {
  ...DEFAULT_TARIFFS,
  distributionUahPerKwh: 0,
  transmissionUahPerKwh: 0,
  supplierMarginUahPerKwh: 0,
  otherFeesUahPerKwh: 0,
  degradationUahPerKwh: 0,
  exportDiscount: 0,
  includeVat: false,
  essCapacityKwh: 200,
}

const ZERO_FLOW: HourFlows = {
  pv: 0,
  gridImport: 0,
  gridExport: 0,
  essCharged: 0,
  essDischarged: 0,
  pvToEss: 0,
  gridToEss: 0,
  essToLoad: 0,
  essToGrid: 0,
}

// HISTORY_HOURS in production is 48; the test helper reproduces
// that constant locally so a future bump in production code shows
// up as a clear arithmetic mismatch in the tests rather than a
// silent diff between two literals.
const HISTORY_HOURS = 48

// makeHistory builds a 48-element history array seeded with the
// given per-hour overrides. Hours not mentioned default to no
// activity, no RDN price, and no SOC sample. Indexes are
// chronological: 0 is the earliest hour of the lookback window
// (i.e. day-before-yesterday hour 0), 47 is the latest (yesterday
// hour 23).
type HourOverride = {
  flow?: Partial<HourFlows>
  rdnUahPerKwh?: number | null
  socPercentStart?: number | null
}

function makeHistory(overrides: Record<number, HourOverride>) {
  return Array.from({ length: HISTORY_HOURS }, (_, i) => {
    const o = overrides[i] ?? {}
    return {
      flow: { ...ZERO_FLOW, ...(o.flow ?? {}) },
      rdnUahPerKwh: o.rdnUahPerKwh === undefined ? null : o.rdnUahPerKwh,
      socPercentStart:
        o.socPercentStart === undefined ? null : o.socPercentStart,
    }
  })
}

// chargeFlow constructs a HourFlows envelope for "Grid→УЗЕ charge of
// `kwh` kWh" in a single line so test bodies stay focused on the
// scenario and not bookkeeping.
function chargeFlow(kwh: number): Partial<HourFlows> {
  return { gridToEss: kwh, essCharged: kwh }
}

describe('findAnchorAndPreRoll', () => {
  it('anchors at the most recent ≤10% SOC drop and rolls forward', () => {
    // Hour 30: SOC dipped to 8% → anchor here. State = (8% × 200, 0)
    //   = (16, 0).
    // Hour 31: 100 kWh grid charge @ RDN=2 → state = (116, 200).
    // Hours 32–47: no activity, no RDN samples (don't matter).
    const history = makeHistory({
      30: { socPercentStart: 8 },
      31: { flow: chargeFlow(100), rdnUahPerKwh: 2 },
    })
    const state = findAnchorAndPreRoll(history, undefined, FLAT_TARIFFS)
    expect(state.kwh).toBeCloseTo(116, 6)
    expect(state.uah).toBeCloseTo(200, 6)
  })

  it('seeds from earliest known SOC when no ≤10% anchor exists', () => {
    // Battery cycled in 35..60% the entire window — no deep
    // discharge to anchor on. The fallback grabs hour-0 SOC (50%)
    // as a pseudo-anchor with `kwh = 50% × 200 = 100, uah = 0` and
    // pre-rolls forward through the rest of the window. With no
    // charges or discharges or RDN samples populated below, the
    // state lands unchanged at the seed.
    const history = makeHistory({
      0: { socPercentStart: 50 },
      24: { socPercentStart: 35 },
      47: { socPercentStart: 60 },
    })
    const state = findAnchorAndPreRoll(history, undefined, FLAT_TARIFFS)
    expect(state.kwh).toBeCloseTo(100, 6)
    expect(state.uah).toBe(0)
  })

  it('rolls charges that occur after the pseudo-anchor seed', () => {
    // Same fallback path, but now hour 10 has a 100 kWh grid charge
    // at RDN=2. Seed = (50% × 200, 0) = (100, 0); after the charge
    // = (100 + 100, 0 + 200) = (200, 200). Confirms the fallback
    // doesn't short-circuit the pre-roll loop.
    const history = makeHistory({
      0: { socPercentStart: 50 },
      10: { flow: chargeFlow(100), rdnUahPerKwh: 2 },
    })
    const state = findAnchorAndPreRoll(history, undefined, FLAT_TARIFFS)
    expect(state.kwh).toBeCloseTo(200, 6)
    expect(state.uah).toBeCloseTo(200, 6)
  })

  it('returns ZERO_ESS_STATE when the window has no SOC samples at all', () => {
    // No SOC reading anywhere — neither a ≤10% anchor nor a fallback
    // seed exists. The truest "we have nothing to work with" branch:
    // subsequent today-hour rolls will re-seed the bucket as charges
    // arrive (matching the legacy behaviour for empty history).
    const history = makeHistory({})
    const state = findAnchorAndPreRoll(history, undefined, FLAT_TARIFFS)
    expect(state).toEqual(ZERO_ESS_STATE)
  })

  it('uses a mid-window SOC sample as fallback seed when earlier hours have no SOC', () => {
    // Only one SOC sample, sitting at offset 20 (mid-window) at 60%.
    // Fallback picks it as the pseudo-anchor — seed = 60% × 200 =
    // 120 kwh, 0 uah. A charge at offset 25 adds 50 kWh @ 4 UAH →
    // (120 + 50, 0 + 200). Earlier hours (0..19) are ignored
    // because we have no SOC reading to seed from there.
    const history = makeHistory({
      20: { socPercentStart: 60 },
      25: { flow: chargeFlow(50), rdnUahPerKwh: 4 },
    })
    const state = findAnchorAndPreRoll(history, undefined, FLAT_TARIFFS)
    expect(state.kwh).toBeCloseTo(170, 6)
    expect(state.uah).toBeCloseTo(200, 6)
  })

  it('uses today’s hour-0 SOC directly when it is itself ≤10%', () => {
    // History has a deeper drop (5%) at hour 30 — but today's hour
    // 0 SOC is 9%, which is the closer reset point. State = 9% × 200.
    const history = makeHistory({
      30: { socPercentStart: 5 },
      31: { flow: chargeFlow(100), rdnUahPerKwh: 2 },
    })
    const state = findAnchorAndPreRoll(history, 9, FLAT_TARIFFS)
    expect(state.kwh).toBeCloseTo(18, 6)
    expect(state.uah).toBe(0)
  })

  it('rolls all 47 hours when the anchor is at offset 0', () => {
    // Anchor at offset 0 (very start of the window), then a charge
    // at offset 10. Expect state to include that charge.
    const history = makeHistory({
      0: { socPercentStart: 7 },
      10: { flow: chargeFlow(50), rdnUahPerKwh: 4 },
    })
    const state = findAnchorAndPreRoll(history, undefined, FLAT_TARIFFS)
    // 7% × 200 = 14, plus 50 kWh @ 4 → 50 × 4 = 200 UAH.
    expect(state.kwh).toBeCloseTo(64, 6)
    expect(state.uah).toBeCloseTo(200, 6)
  })

  it('returns the anchor state directly when the anchor is the last hour', () => {
    // Anchor at offset 47 (latest history hour), nothing to roll.
    const history = makeHistory({
      47: { socPercentStart: 6 },
    })
    const state = findAnchorAndPreRoll(history, undefined, FLAT_TARIFFS)
    expect(state.kwh).toBeCloseTo(12, 6)
    expect(state.uah).toBe(0)
  })

  it('skips null-RDN hours after the anchor instead of treating them as free', () => {
    // Anchor at hour 30 (SOC 5% → kwh=10). Hour 31 has a 100 kWh
    // grid charge but NO RDN price — skipped. Hour 32 has 100 kWh
    // grid charge @ RDN=3 — applied. Final = (10 + 100, 0 + 300).
    const history = makeHistory({
      30: { socPercentStart: 5 },
      31: { flow: chargeFlow(100), rdnUahPerKwh: null },
      32: { flow: chargeFlow(100), rdnUahPerKwh: 3 },
    })
    const state = findAnchorAndPreRoll(history, undefined, FLAT_TARIFFS)
    // The skipped hour drifts the kwh tracker (it doesn't apply the
    // 100 kWh charge to the cost-basis ledger), so we end up with
    // 10 + 100 = 110 kWh, 300 UAH.
    expect(state.kwh).toBeCloseTo(110, 6)
    expect(state.uah).toBeCloseTo(300, 6)
  })

  it('picks the LATEST anchor when multiple ≤10% drops exist', () => {
    // Two qualifying drops: hour 5 (SOC=8%) and hour 30 (SOC=4%).
    // The scan returns the latest (30), not the lowest. Hour 6 has
    // a charge that should NOT be included.
    const history = makeHistory({
      5: { socPercentStart: 8 },
      6: { flow: chargeFlow(100), rdnUahPerKwh: 2 },
      30: { socPercentStart: 4 },
    })
    const state = findAnchorAndPreRoll(history, undefined, FLAT_TARIFFS)
    // Anchor at 30: kwh = 4% × 200 = 8, no charges after.
    expect(state.kwh).toBeCloseTo(8, 6)
    expect(state.uah).toBe(0)
  })
})
