import { useEffect, useState } from 'react'
import { fetchDAMPrices, fetchEnergyFlowHourly, fetchTimeseries } from '../api'
import type {
  DAMPrice,
  EnergyFlowHourlyResponse,
  EnergyFlowHourlyRow,
  TimeseriesResponse,
} from '../types'
import { hourEconomics, type HourEconomicsRow, type HourFlows } from './compute'
import { rollHour, ZERO_ESS_STATE, type EssState } from './costBasis'
import type { Tariffs } from './tariffs'

// DAM zone 2 is the unified UA grid; zone 1 is the historical
// Burshtyn island. Burshtyn priced separately until late 2022, but
// the daily-economics page targets the post-unification market only.
const DAM_ZONE = 2

// localKyivTz is the canonical timezone for the dashboard. The
// existing main dashboard uses the browser's resolved tz; for the
// economics page we want hard-coded Europe/Kyiv so the day boundary
// matches the operator's wallclock and the DAM hour numbering. The
// header still allows date selection in that zone.
const LOCAL_TZ = 'Europe/Kyiv'

// PV / grid accumulator metric_keys we hand to /api/v1/timeseries
// with `aggregation=delta&bucket=1 hour`. The deltas cover the
// SmartLogger-side flows that the new energy-flow-hourly endpoint
// doesn't already break out (it only returns the four ESS-specific
// directional flows + ESS counter deltas).
const PV_GRID_METRIC_KEYS = [
  'accumulated_pv_energy_yield_kwh',
  'accumulated_electricity_purchased_kwh',
  'accumulated_electricity_sold_kwh',
] as const

// soc_percent is a gauge (not an accumulator), so we pull it with
// `aggregation=last` over 1-hour buckets ending one hour BEFORE
// each target hour: the last raw sample observed in `[H-1, H)` is
// the closest the analyst has to "SOC at the start of hour H".
const SOC_METRIC_KEY = 'soc_percent'

// HISTORY_HOURS is the lookback window the cost-basis pipeline
// scans for the most recent ≤10% SOC drop. Two days back covers
// orgs that cycle the battery roughly daily — at the worst case
// (no cycling at all in 48 h) we fall back to ZERO_ESS_STATE,
// which is also the right answer when the battery has been idle.
const HISTORY_HOURS = 48

// SOC_RESET_THRESHOLD_PERCENT defines "deeply discharged": at or
// below this fraction of capacity the residual energy is small
// enough that we treat the bottom 10% as a free leftover and
// restart the WAC ledger from there. Hardcoded by design — the
// operator-facing tariff form intentionally does not expose it
// because operators don't tune this value day-to-day.
const SOC_RESET_THRESHOLD_PERCENT = 10

export type EconomicsData = {
  // Always 24-long. `null` means the hour wasn't recovered yet
  // (still loading) or the underlying telemetry had no rows for
  // the hour. The page treats both the same way: render an
  // empty-data placeholder rather than fabricating zeros.
  rows: Array<HourEconomicsRow | null>
  loading: boolean
  error: string | null
  // hoursMissingPrice is surfaced verbatim from the dailyTotals
  // computation so the header can show "ціни РДН частково
  // відсутні" without re-summing the rows in the UI layer.
  hoursMissingPrice: number
  // skipDiagnostics carries the bulk warning string from the
  // backend's IterateIntervals walk (collector outages, sentinel
  // hits, etc). null when the day was clean.
  skipDiagnostics: string | null
}

type Input = {
  organizationID: string
  // YYYY-MM-DD calendar day in LOCAL_TZ.
  date: string
  tariffs: Tariffs
}

// useEconomicsData fans out the three API calls in parallel and
// folds the responses into a 24-element HourEconomicsRow array.
// Returns the rows + loading/error so the page can show a skeleton
// or banner without sequencing mid-flight requests in JSX.
//
// We intentionally do not memoize across renders: the caller's
// (organizationID, date, tariffs) tuple is stable across keystrokes
// thanks to React's referential-equality on `tariffs` returned from
// `useState`, and the hook's effect dependency array only re-runs
// on real changes. This is simpler than wrapping in useMemo +
// useCallback and matches `useDashboardData`'s style.
export function useEconomicsData(input: Input): EconomicsData {
  const [data, setData] = useState<EconomicsData>(() => ({
    rows: Array.from({ length: 24 }, () => null),
    loading: true,
    error: null,
    hoursMissingPrice: 0,
    skipDiagnostics: null,
  }))

  useEffect(() => {
    if (!input.organizationID || !input.date) {
      setData({
        rows: Array.from({ length: 24 }, () => null),
        loading: false,
        error: null,
        hoursMissingPrice: 0,
        skipDiagnostics: null,
      })
      return
    }
    const controller = new AbortController()
    setData((prev) => ({ ...prev, loading: true, error: null }))

    const dayStart = new Date(`${input.date}T00:00:00`)
    const dayEnd = new Date(dayStart.getTime())
    dayEnd.setDate(dayEnd.getDate() + 1)

    // History window covers the HISTORY_HOURS hours preceding today's
    // 00:00. The cost-basis anchor search walks this window backward
    // looking for the last hour where SOC dropped to or below
    // SOC_RESET_THRESHOLD_PERCENT. We need the matching flows + DAM
    // for those hours so we can roll forward from the anchor.
    const yesterdayStart = new Date(dayStart.getTime())
    yesterdayStart.setDate(yesterdayStart.getDate() - 1)
    const yesterdayDate = formatLocalDate(yesterdayStart)

    const dayBeforeYesterdayStart = new Date(dayStart.getTime())
    dayBeforeYesterdayStart.setDate(dayBeforeYesterdayStart.getDate() - 2)
    const dayBeforeYesterdayDate = formatLocalDate(dayBeforeYesterdayStart)

    const historyStart = new Date(dayStart.getTime() - HISTORY_HOURS * 60 * 60 * 1000)

    const dayStartIso = dayStart.toISOString()
    const dayEndIso = dayEnd.toISOString()

    // SOC window is shifted one hour earlier than the day window so
    // each `aggregation=last` bucket [H-1, H) yields the value the
    // operator would have read at the start of hour H. We extend the
    // start by HISTORY_HOURS so the same response also covers all
    // start-of-hour SOC samples in the lookback window.
    const socStart = new Date(dayStart.getTime() - (HISTORY_HOURS + 1) * 60 * 60 * 1000)
    const socEnd = new Date(dayEnd.getTime() - 60 * 60 * 1000)

    Promise.all([
      fetchEnergyFlowHourly(
        { organizationID: input.organizationID, date: input.date, tz: LOCAL_TZ },
        controller.signal,
      ),
      fetchTimeseries(
        {
          organizationID: input.organizationID,
          metricKeys: PV_GRID_METRIC_KEYS as unknown as string[],
          from: dayStartIso,
          to: dayEndIso,
          bucket: '1 hour',
          tz: LOCAL_TZ,
          aggregation: 'delta',
        },
        controller.signal,
      ),
      fetchDAMPrices(
        { from: input.date, to: input.date, zone: DAM_ZONE },
        controller.signal,
      ),
      fetchTimeseries(
        {
          organizationID: input.organizationID,
          metricKeys: [SOC_METRIC_KEY],
          from: socStart.toISOString(),
          to: socEnd.toISOString(),
          bucket: '1 hour',
          tz: LOCAL_TZ,
          aggregation: 'last',
        },
        controller.signal,
      ),
      // History fetches: yesterday + day-before-yesterday flows,
      // 48-hour history deltas, and the matching 2-day DAM window.
      // All wrapped in `.catch(returns null)` so a 404 on a fresh
      // org / collector outage degrades gracefully — the cost-basis
      // search just returns ZERO_ESS_STATE if it can't find an
      // anchor.
      fetchEnergyFlowHourly(
        { organizationID: input.organizationID, date: yesterdayDate, tz: LOCAL_TZ },
        controller.signal,
      ).catch((err) => {
        if ((err as DOMException)?.name === 'AbortError') throw err
        return null
      }),
      fetchEnergyFlowHourly(
        { organizationID: input.organizationID, date: dayBeforeYesterdayDate, tz: LOCAL_TZ },
        controller.signal,
      ).catch((err) => {
        if ((err as DOMException)?.name === 'AbortError') throw err
        return null
      }),
      fetchTimeseries(
        {
          organizationID: input.organizationID,
          metricKeys: PV_GRID_METRIC_KEYS as unknown as string[],
          from: historyStart.toISOString(),
          to: dayStartIso,
          bucket: '1 hour',
          tz: LOCAL_TZ,
          aggregation: 'delta',
        },
        controller.signal,
      ).catch((err) => {
        if ((err as DOMException)?.name === 'AbortError') throw err
        return null
      }),
      fetchDAMPrices(
        { from: dayBeforeYesterdayDate, to: yesterdayDate, zone: DAM_ZONE },
        controller.signal,
      ).catch((err) => {
        if ((err as DOMException)?.name === 'AbortError') throw err
        return null
      }),
    ])
      .then(
        ([
          flowsResp,
          deltasResp,
          damResp,
          socResp,
          yesterdayFlowsResp,
          dayBeforeYesterdayFlowsResp,
          historyDeltasResp,
          historyDamResp,
        ]) => {
          const rows = assembleHourlyRows(
            flowsResp,
            deltasResp,
            damResp.prices,
            socResp,
            input.tariffs,
            dayStart,
            yesterdayFlowsResp,
            dayBeforeYesterdayFlowsResp,
            historyDeltasResp,
            historyDamResp?.prices ?? null,
            yesterdayDate,
            dayBeforeYesterdayDate,
          )
          const skipDiagnostics = collectSkipDiagnostics(flowsResp)
          const hoursMissingPrice = rows.reduce(
            (acc, row) => (row && row.rdnUahPerKwh === null ? acc + 1 : acc),
            0,
          )
          setData({
            rows,
            loading: false,
            error: null,
            hoursMissingPrice,
            skipDiagnostics,
          })
        },
      )
      .catch((err: unknown) => {
        if ((err as DOMException)?.name === 'AbortError') return
        const message =
          err instanceof Error
            ? err.message
            : typeof err === 'string'
              ? err
              : 'failed to load economics data'
        setData((prev) => ({ ...prev, loading: false, error: message }))
      })

    return () => controller.abort()
  }, [input.organizationID, input.date, input.tariffs])

  return data
}

// assembleHourlyRows joins the three responses into a 24-element
// array. The `flows` response is the spine (it's always 24 entries
// long, in chronological order); we layer the timeseries deltas and
// DAM prices on top by hour-of-day in LOCAL_TZ. Rows whose `flows`
// row is missing from the response (shouldn't happen with the new
// backend, but kept as a guard) end up null.
function assembleHourlyRows(
  flows: EnergyFlowHourlyResponse,
  deltas: TimeseriesResponse,
  damPrices: DAMPrice[],
  socResp: TimeseriesResponse,
  tariffs: Tariffs,
  dayStart: Date,
  yesterdayFlows: EnergyFlowHourlyResponse | null,
  dayBeforeYesterdayFlows: EnergyFlowHourlyResponse | null,
  historyDeltas: TimeseriesResponse | null,
  historyDamPrices: DAMPrice[] | null,
  yesterdayDate: string,
  dayBeforeYesterdayDate: string,
): Array<HourEconomicsRow | null> {
  const pvByHour = bucketByHourOfDay(
    deltas.points.filter((p) => p.metric_key === 'accumulated_pv_energy_yield_kwh'),
    dayStart,
  )
  const importByHour = bucketByHourOfDay(
    deltas.points.filter((p) => p.metric_key === 'accumulated_electricity_purchased_kwh'),
    dayStart,
  )
  const exportByHour = bucketByHourOfDay(
    deltas.points.filter((p) => p.metric_key === 'accumulated_electricity_sold_kwh'),
    dayStart,
  )
  // SOC buckets are timestamped at the START of each [H-1, H)
  // window, so a sample at 23:00 yesterday represents "SOC observed
  // walking into hour 0 today". `bucketSocByOffset` returns the SOC
  // value at any hour offset relative to today's 00:00 — offset 0
  // is today's hour 0, offset −48 is the first hour of the lookback
  // window.
  const socByOffset = bucketSocByOffset(
    socResp.points.filter((p) => p.metric_key === SOC_METRIC_KEY),
    dayStart,
  )

  const priceMap = buildPriceMap(damPrices)

  const out: Array<HourEconomicsRow | null> = []
  for (let h = 0; h < 24; h++) {
    const flowRow = flows.hours[h]
    if (!flowRow) {
      out.push(null)
      continue
    }
    const flow: HourFlows = {
      pv: pvByHour.get(h) ?? 0,
      gridImport: importByHour.get(h) ?? 0,
      gridExport: exportByHour.get(h) ?? 0,
      essCharged: flowRow.ess_charged_kwh,
      essDischarged: flowRow.ess_discharged_kwh,
      pvToEss: flowRow.pv_to_ess_kwh,
      gridToEss: flowRow.grid_to_ess_kwh,
      essToLoad: flowRow.ess_to_load_kwh,
      essToGrid: flowRow.ess_to_grid_kwh,
    }
    const rdn = priceMap.has(h) ? (priceMap.get(h) as number) : null
    const economics =
      rdn === null
        ? // Missing price → render the row's flows but mark the
          // economics as "no price". `dailyTotals` skips these
          // automatically via `rdnUahPerKwh === null`.
          hourEconomics(0, flow, tariffs)
        : hourEconomics(rdn, flow, tariffs)
    out.push({
      hour: h,
      hourStart: flowRow.from,
      rdnUahPerKwh: rdn,
      flow,
      economics,
      // Anchored on hour 0's SOC and rolled forward by net charge
      // flows below — placeholder until that second pass runs.
      essRemainingKwhStart: null,
      essCostBasisUahStart: null,
      essAvgCostUahPerKwhStart: null,
      essWithdrawnCostUah: null,
      essRealizedProfitUah: null,
      essCostBasisUahEnd: null,
      essAvgCostUahPerKwhEnd: null,
      essResidualKwhEnd: null,
    })
  }

  // Залишок УЗЕ second pass: anchor hour 0 from SOC[0] · ємність,
  // then for each subsequent hour roll the running residual forward
  // by the previous hour's net charge minus discharge:
  //   residual[h+1] = residual[h] + (PV→УЗЕ + Мережа→УЗЕ
  //                                 − УЗЕ→Споживання − УЗЕ→Мережа)[h]
  // Using cumulative flows instead of re-reading SOC for every hour
  // hides intra-hour gauge dropouts and keeps the table arithmetic
  // self-consistent (the operator can verify the running line by
  // adding/subtracting the four flow rows above it). When SOC[0] is
  // missing or any preceding hour has no flow data, the residual
  // is null from that point on — we don't fabricate a starting
  // value just to produce a number.
  const soc0Percent = socByOffset.get(0)
  let running: number | null =
    soc0Percent === undefined || !Number.isFinite(soc0Percent)
      ? null
      : (soc0Percent / 100) * tariffs.essCapacityKwh
  for (let h = 0; h < 24; h++) {
    const cur = out[h]
    if (cur !== null) {
      out[h] = { ...cur, essRemainingKwhStart: running }
    }
    if (h === 23) break
    if (running === null || cur === null) {
      running = null
    } else {
      running =
        running +
        cur.flow.pvToEss +
        cur.flow.gridToEss -
        cur.flow.essToLoad -
        cur.flow.essToGrid
    }
  }

  // Cost-basis third pass: scan the past HISTORY_HOURS hours for
  // the most recent ≤SOC_RESET_THRESHOLD_PERCENT moment, anchor
  // there with `state = (SOC × capacity, 0)` (the bottom 10% is
  // treated as a free leftover), and roll forward through the
  // remaining history hours so we land at today's 00:00 with a
  // calibrated WAC. Then the per-today-hour loop applies the same
  // rollHour algorithm to populate the four cost-basis fields on
  // each row. PV→ESS contributes 0 UAH, Grid→ESS contributes
  // `gridToEss · importPrice`, discharges withdraw at the running
  // average. Hours with missing RDN are skipped both inside the
  // pre-roll and inside today's loop — pretending their prices are
  // zero would log Grid→УЗЕ charges as free and corrupt downstream
  // realized profit numbers.
  const history = buildHistoryRecords(
    dayBeforeYesterdayFlows,
    yesterdayFlows,
    historyDeltas,
    historyDamPrices,
    socByOffset,
    dayStart,
    yesterdayDate,
    dayBeforeYesterdayDate,
  )
  let state: EssState = findAnchorAndPreRoll(history, soc0Percent, tariffs)

  for (let h = 0; h < 24; h++) {
    const cur = out[h]
    if (cur === null) continue
    // Skip null-RDN hours entirely instead of pretending the
    // import / export prices are 0: a Grid→УЗЕ flow at the
    // "free" price would dilute the WAC downward, and subsequent
    // priced-hour discharges would over-state realized profit. By
    // not advancing `state` we keep the cost-basis ledger calibrated
    // to whatever the last priced hour ended at; the kWh tracker
    // will drift from the inverter accumulator if there are real
    // flows in the missing-RDN window, but that's preferable to
    // fabricating prices.
    if (cur.rdnUahPerKwh === null) continue
    const result = rollHour(
      state,
      cur.flow,
      cur.economics.importPriceUahPerKwh,
      cur.economics.exportPriceUahPerKwh,
      tariffs.degradationUahPerKwh,
    )
    out[h] = {
      ...cur,
      essCostBasisUahStart: state.uah,
      essAvgCostUahPerKwhStart: result.avgCostStartUahPerKwh,
      essWithdrawnCostUah: result.withdrawnCostUah,
      essRealizedProfitUah: result.realizedProfitUah,
      essCostBasisUahEnd: result.next.uah,
      essAvgCostUahPerKwhEnd: result.avgCostEndUahPerKwh,
      essResidualKwhEnd: result.next.kwh,
    }
    state = result.next
  }
  return out
}

// HourHistoryRecord packages one hour of pre-today data into the
// shape `findAnchorAndPreRoll` consumes — the flow envelope (zero
// when the hour's flow data was missing), the RDN price for that
// hour (null when DAM was unpriced or the request failed), and the
// SOC at the START of the hour (null when the gauge sample was
// missing). The records are produced in chronological order, index
// 0 being the earliest hour in the lookback window and the last
// index being the hour ending one minute before today's 00:00.
type HourHistoryRecord = {
  flow: HourFlows
  rdnUahPerKwh: number | null
  socPercentStart: number | null
}

// ZERO_HOUR_FLOWS is the canonical "no activity" envelope used when
// a history hour's flow row was missing entirely (yesterday's API
// 404'd, or the hour fell outside the response). Frozen so passing
// it around doesn't risk accidental mutation through Object.assign.
const ZERO_HOUR_FLOWS: HourFlows = Object.freeze({
  pv: 0,
  gridImport: 0,
  gridExport: 0,
  essCharged: 0,
  essDischarged: 0,
  pvToEss: 0,
  gridToEss: 0,
  essToLoad: 0,
  essToGrid: 0,
})

// findAnchorAndPreRoll is the cost-basis pipeline's entry point. It
// scans `history` (chronological, length HISTORY_HOURS) right-to-
// left for the most recent hour where the start-of-hour SOC was at
// or below SOC_RESET_THRESHOLD_PERCENT — a "deep discharge" point.
// At that anchor the WAC ledger is reseeded with the residual
// energy at zero cost (the bottom slice of the battery is treated
// as free leftover), and `rollHour` is run forward through every
// later history hour so the returned state reflects what should
// land at today's 00:00.
//
// `todayHour0SocPercent` short-circuits the scan: if the operator
// is walking into today already drained, the cost-basis row should
// match — we return `(SOC × capacity, 0)` directly without rolling
// any prior history, since the relevant reset moment is RIGHT NOW.
//
// When no qualifying ≤10% SOC drop exists in the lookback window
// (battery cycled in a comfortable 20..80% range all 48 h), we fall
// back to using the EARLIEST history hour that still has a known
// SOC sample as a pseudo-anchor: seed with `(SOC × capacity, 0)` and
// pre-roll the rest of the window forward. This keeps yesterday's
// EOD cost basis from being silently reset to zero whenever the
// operator runs a "normal" cycling regime — which is exactly the
// bug the screenshots from 25.05 → 26.05 surfaced.
//
// Trade-off: the fallback treats whatever was inside the battery at
// the start of the 48 h window as free leftover, so a station that
// has been in steady-state >>48 h will systematically under-state
// the WAC of its carry-over. The longer-term fix is to persist EOD
// state on the backend; this client-side fallback is the simple
// "carry yesterday's number forward" patch.
//
// Returns ZERO_ESS_STATE only when there is no SOC sample in the
// entire history window — at that point there's nothing to anchor
// on and subsequent today-hour rolls will simply re-seed the bucket
// as charges arrive.
//
// Exported for unit tests; production code calls it via
// `assembleHourlyRows`.
export function findAnchorAndPreRoll(
  history: HourHistoryRecord[],
  todayHour0SocPercent: number | undefined,
  tariffs: Tariffs,
): EssState {
  if (
    todayHour0SocPercent !== undefined &&
    Number.isFinite(todayHour0SocPercent) &&
    todayHour0SocPercent <= SOC_RESET_THRESHOLD_PERCENT
  ) {
    const kwh = Math.max(0, (todayHour0SocPercent / 100) * tariffs.essCapacityKwh)
    return { kwh, uah: 0 }
  }

  let anchorIdx = -1
  for (let i = history.length - 1; i >= 0; i--) {
    const soc = history[i].socPercentStart
    if (soc !== null && soc <= SOC_RESET_THRESHOLD_PERCENT) {
      anchorIdx = i
      break
    }
  }
  // No deep-discharge anchor in the 48 h window. Fall back to the
  // earliest hour with a known SOC sample so yesterday's WAC keeps
  // flowing into today instead of being silently wiped. See the
  // doc comment above for the rationale and the steady-state caveat.
  if (anchorIdx < 0) {
    for (let i = 0; i < history.length; i++) {
      if (history[i].socPercentStart !== null) {
        anchorIdx = i
        break
      }
    }
  }
  if (anchorIdx < 0) return { ...ZERO_ESS_STATE }

  // Anchor SOC reading is guaranteed non-null by the loop above.
  const anchorSoc = history[anchorIdx].socPercentStart as number
  let state: EssState = {
    kwh: Math.max(0, (anchorSoc / 100) * tariffs.essCapacityKwh),
    uah: 0,
  }
  for (let i = anchorIdx + 1; i < history.length; i++) {
    const rec = history[i]
    if (rec.rdnUahPerKwh === null) continue
    const economics = hourEconomics(rec.rdnUahPerKwh, rec.flow, tariffs)
    const result = rollHour(
      state,
      rec.flow,
      economics.importPriceUahPerKwh,
      economics.exportPriceUahPerKwh,
      tariffs.degradationUahPerKwh,
    )
    state = result.next
  }
  return state
}

// buildHistoryRecords stitches the day-before-yesterday + yesterday
// flow responses, the 48-hour history-deltas response, the 2-day
// DAM response, and the SOC-by-offset map into a HISTORY_HOURS-long
// chronological array. Index 0 is the first hour of the
// day-before-yesterday in `LOCAL_TZ`; index 47 is the last hour of
// yesterday. Missing per-hour data degrades to zeros / nulls so the
// downstream anchor scan can decide hour-by-hour what to skip.
//
// The two date strings (`yesterdayDate`, `dayBeforeYesterdayDate`)
// must match the dates the outer `useEffect` used to call the
// energy-flow + DAM endpoints, so the DAM filter splits the 2-day
// response into the same buckets the API was queried for. The
// outer scope computes them with calendar arithmetic
// (`setDate(-1)` / `setDate(-2)`) which lines up with local
// midnight even across DST transitions; recomputing them here from
// `dayStart - 48h` (UTC math) would silently drift by 1 day on
// DST boundaries and drop the day-before-yesterday DAM rows.
export function buildHistoryRecords(
  dayBeforeYesterdayFlows: EnergyFlowHourlyResponse | null,
  yesterdayFlows: EnergyFlowHourlyResponse | null,
  historyDeltas: TimeseriesResponse | null,
  historyDamPrices: DAMPrice[] | null,
  socByOffset: Map<number, number>,
  dayStart: Date,
  yesterdayDate: string,
  dayBeforeYesterdayDate: string,
): HourHistoryRecord[] {
  const historyStart = new Date(dayStart.getTime() - HISTORY_HOURS * 60 * 60 * 1000)
  const pvByOffset = bucketByOffsetFromStart(
    historyDeltas?.points.filter((p) => p.metric_key === 'accumulated_pv_energy_yield_kwh') ?? [],
    historyStart,
    HISTORY_HOURS,
  )
  const importByOffset = bucketByOffsetFromStart(
    historyDeltas?.points.filter((p) => p.metric_key === 'accumulated_electricity_purchased_kwh') ?? [],
    historyStart,
    HISTORY_HOURS,
  )
  const exportByOffset = bucketByOffsetFromStart(
    historyDeltas?.points.filter((p) => p.metric_key === 'accumulated_electricity_sold_kwh') ?? [],
    historyStart,
    HISTORY_HOURS,
  )

  // Split the 2-day DAM response by `delivery_date`. The API
  // marshals Go's time.Time as ISO-8601 ("YYYY-MM-DDT00:00:00Z"),
  // so slicing the first 10 chars yields the calendar date in the
  // `delivery_date` column's timezone (UTC for the seed loader).
  // Tests pass plain "YYYY-MM-DD" strings directly, which slice
  // identity-preserves.
  const dy2Prices: DAMPrice[] = []
  const dy1Prices: DAMPrice[] = []
  if (historyDamPrices) {
    for (const p of historyDamPrices) {
      const dateKey = String(p.delivery_date).slice(0, 10)
      if (dateKey === dayBeforeYesterdayDate) dy2Prices.push(p)
      else if (dateKey === yesterdayDate) dy1Prices.push(p)
    }
  }
  const dy2PriceMap = buildPriceMap(dy2Prices)
  const dy1PriceMap = buildPriceMap(dy1Prices)

  const out: HourHistoryRecord[] = []
  for (let i = 0; i < HISTORY_HOURS; i++) {
    let flowRow: EnergyFlowHourlyRow | undefined
    let rdn: number | null
    if (i < 24) {
      flowRow = dayBeforeYesterdayFlows?.hours[i]
      rdn = dy2PriceMap.has(i) ? (dy2PriceMap.get(i) as number) : null
    } else {
      const yh = i - 24
      flowRow = yesterdayFlows?.hours[yh]
      rdn = dy1PriceMap.has(yh) ? (dy1PriceMap.get(yh) as number) : null
    }
    const flow: HourFlows = flowRow
      ? {
          pv: pvByOffset.get(i) ?? 0,
          gridImport: importByOffset.get(i) ?? 0,
          gridExport: exportByOffset.get(i) ?? 0,
          essCharged: flowRow.ess_charged_kwh,
          essDischarged: flowRow.ess_discharged_kwh,
          pvToEss: flowRow.pv_to_ess_kwh,
          gridToEss: flowRow.grid_to_ess_kwh,
          essToLoad: flowRow.ess_to_load_kwh,
          essToGrid: flowRow.ess_to_grid_kwh,
        }
      : { ...ZERO_HOUR_FLOWS }
    // History hour i (0..47) corresponds to SOC offset (i − 48)
    // relative to today's 00:00.
    const socOffset = i - HISTORY_HOURS
    const socRaw = socByOffset.get(socOffset)
    out.push({
      flow,
      rdnUahPerKwh: rdn,
      socPercentStart:
        socRaw !== undefined && Number.isFinite(socRaw) ? socRaw : null,
    })
  }
  return out
}

// buildPriceMap unpacks a DAM-prices response into a 0-indexed
// hour → UAH/kWh map. DAM hours are 1..24 (hour-ending) so we
// shift by 1 to align with the 0-indexed hour-of-day used
// everywhere else in the model. Same logic as the inline version
// the today path used to have; extracted so yesterday can reuse
// it without copy-pasting the filter.
function buildPriceMap(damPrices: DAMPrice[] | null | undefined): Map<number, number> {
  const out = new Map<number, number>()
  if (!damPrices) return out
  for (const p of damPrices) {
    if (p.zone !== DAM_ZONE) continue
    if (p.price_uah_per_mwh === null || p.price_uah_per_mwh === undefined) continue
    const idx = p.hour - 1
    if (idx < 0 || idx >= 24) continue
    out.set(idx, p.price_uah_per_mwh / 1000)
  }
  return out
}

// formatLocalDate turns a JS Date into the YYYY-MM-DD string the
// economics endpoints accept. We deliberately use `getFullYear()`
// + `getMonth()` etc. (browser-local) so the result matches the
// page's calendar-day input — using `toISOString()` would drift
// to UTC and shift by a day in negative tz offsets.
function formatLocalDate(d: Date): string {
  const yyyy = String(d.getFullYear()).padStart(4, '0')
  const mm = String(d.getMonth() + 1).padStart(2, '0')
  const dd = String(d.getDate()).padStart(2, '0')
  return `${yyyy}-${mm}-${dd}`
}

// bucketSocByOffset maps each SOC sample to the operator-facing
// hour OFFSET (relative to today's 00:00) it represents the start
// of. The /api/v1/timeseries call uses `aggregation=last` over
// [H-1, H) buckets, so a sample at time T represents "the SOC
// observed at the start of hour T+1h". The returned map's offset
// 0 is today's hour 0; offset −48 is the first history hour;
// offset 23 is today's hour 23.
//
// We use absolute hour offsets instead of `getHours() % 24`
// because the SOC fetch now spans 49 + 24 hours, which would
// collide on the same hour-of-day across days.
function bucketSocByOffset(
  points: { metric_key: string; time: string; value: number }[],
  dayStart: Date,
): Map<number, number> {
  // Each SOC sample at time T represents start-of-hour for T+1h,
  // so the "represented hour start" relative to dayStart is at
  // (T + 1h) − dayStart. Equivalently, offset from dayStart−1h is
  // (T − (dayStart − 1h)) / 1h.
  const baseMs = dayStart.getTime() - 60 * 60 * 1000
  const out = new Map<number, number>()
  for (const point of points) {
    const t = new Date(point.time)
    if (Number.isNaN(t.getTime())) continue
    const offset = Math.floor((t.getTime() - baseMs) / (60 * 60 * 1000))
    out.set(offset, point.value)
  }
  return out
}

// bucketByHourOfDay folds today's PV / grid delta points into a
// hour-of-day → summed-delta map indexed 0..23. The today fetch is
// scoped to [dayStart, dayEnd) so there's no cross-day collision —
// the absolute hour offset coincides with hour-of-day.
function bucketByHourOfDay(
  points: { time: string; value: number }[],
  dayStart: Date,
): Map<number, number> {
  const baseMs = dayStart.getTime()
  const out = new Map<number, number>()
  for (const point of points) {
    const t = new Date(point.time)
    if (Number.isNaN(t.getTime())) continue
    const offset = Math.floor((t.getTime() - baseMs) / (60 * 60 * 1000))
    if (offset < 0 || offset >= 24) continue
    out.set(offset, (out.get(offset) ?? 0) + point.value)
  }
  return out
}

// bucketByOffsetFromStart folds points into a hour-offset → summed-
// delta map where offset 0 is the first hour of the window. Used
// for the 48-hour history deltas; the today path uses
// `bucketByHourOfDay` because its window is single-day.
function bucketByOffsetFromStart(
  points: { time: string; value: number }[],
  windowStart: Date,
  windowHours: number,
): Map<number, number> {
  const baseMs = windowStart.getTime()
  const out = new Map<number, number>()
  for (const point of points) {
    const t = new Date(point.time)
    if (Number.isNaN(t.getTime())) continue
    const offset = Math.floor((t.getTime() - baseMs) / (60 * 60 * 1000))
    if (offset < 0 || offset >= windowHours) continue
    out.set(offset, (out.get(offset) ?? 0) + point.value)
  }
  return out
}

// collectSkipDiagnostics dedupes warnings the backend bundles on
// hour 0 (per the `computeEnergyFlowHourly` carrier convention) into
// a short human-readable string for the page header. Returns null
// when the response was clean so the UI can branch on truthiness.
function collectSkipDiagnostics(flows: EnergyFlowHourlyResponse): string | null {
  const hour0 = flows.hours[0]
  if (!hour0) return null
  const skipped = hour0.skipped_intervals
  const warnings = hour0.warnings ?? []
  if (skipped <= 0 && warnings.length === 0) return null
  const seen = new Set<string>()
  const dedupedWarnings = warnings.filter((w) => {
    if (seen.has(w)) return false
    seen.add(w)
    return true
  })
  const parts: string[] = []
  if (skipped > 0) {
    parts.push(`${skipped} інтервал(ів) пропущено алокатором`)
  }
  if (dedupedWarnings.length > 0) {
    parts.push(dedupedWarnings.slice(0, 3).join('; '))
  }
  return parts.length > 0 ? parts.join('. ') : null
}
