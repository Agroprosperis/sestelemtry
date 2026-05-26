import { useEffect, useState } from 'react'
import { fetchDAMPrices, fetchEnergyFlowHourly, fetchTimeseries } from '../api'
import type { DAMPrice, EnergyFlowHourlyResponse, TimeseriesResponse } from '../types'
import { hourEconomics, type HourEconomicsRow, type HourFlows } from './compute'
import { rollHour, seedFromCostPerKwh, type EssState, ZERO_ESS_STATE } from './costBasis'
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
// We currently only consume the bucket for hour 0 (anchor for the
// cumulative residual calculation below), but keep the full 24-h
// window so an operator-side debugger / future "SOC vs computed
// residual" reconciliation chart can read the same response.
const SOC_METRIC_KEY = 'soc_percent'

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

    // Yesterday's window is needed only to pre-roll the cost-basis
    // state through the 24 hours preceding the page's calendar day,
    // so we end up at 00:00 today with a residual UAH/kWh that
    // reflects what was actually charged into the battery the day
    // before. When the request fails (network, 404 for new orgs)
    // the cost-basis pipeline falls back to the seed tariff.
    const yesterdayStart = new Date(dayStart.getTime())
    yesterdayStart.setDate(yesterdayStart.getDate() - 1)
    const yesterdayDate = formatLocalDate(yesterdayStart)

    const dayStartIso = dayStart.toISOString()
    const dayEndIso = dayEnd.toISOString()

    // SOC window is shifted one hour earlier than the day window so
    // each `aggregation=last` bucket [H-1, H) yields the value the
    // operator would have read at the start of hour H. The end
    // boundary lands at today 23:00 (i.e., dayEnd minus 1h), so the
    // last bucket covers 22:00–23:00 and feeds into hour 23.
    const socStart = new Date(dayStart.getTime() - 60 * 60 * 1000)
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
      // Yesterday's hourly flows (energy-flow-hourly endpoint).
      // Wrapped in `.catch(returns null)` so a 404 on a brand-new
      // org / collector outage degrades gracefully to seed-fallback
      // mode rather than failing the whole page.
      fetchEnergyFlowHourly(
        { organizationID: input.organizationID, date: yesterdayDate, tz: LOCAL_TZ },
        controller.signal,
      ).catch((err) => {
        if ((err as DOMException)?.name === 'AbortError') throw err
        return null
      }),
      fetchTimeseries(
        {
          organizationID: input.organizationID,
          metricKeys: PV_GRID_METRIC_KEYS as unknown as string[],
          from: yesterdayStart.toISOString(),
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
        { from: yesterdayDate, to: yesterdayDate, zone: DAM_ZONE },
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
          yFlowsResp,
          yDeltasResp,
          yDamResp,
        ]) => {
          const rows = assembleHourlyRows(
            flowsResp,
            deltasResp,
            damResp.prices,
            socResp,
            input.tariffs,
            yFlowsResp,
            yDeltasResp,
            yDamResp?.prices ?? null,
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
  yFlows: EnergyFlowHourlyResponse | null,
  yDeltas: TimeseriesResponse | null,
  yDamPrices: DAMPrice[] | null,
): Array<HourEconomicsRow | null> {
  const pvByHour = bucketBy(deltas.points.filter((p) => p.metric_key === 'accumulated_pv_energy_yield_kwh'))
  const importByHour = bucketBy(deltas.points.filter((p) => p.metric_key === 'accumulated_electricity_purchased_kwh'))
  const exportByHour = bucketBy(deltas.points.filter((p) => p.metric_key === 'accumulated_electricity_sold_kwh'))
  // SOC buckets are timestamped at the START of each [H-1, H)
  // window, so a sample at 23:00 yesterday represents "SOC observed
  // walking into hour 0 today". `bucketSocByHourStart` shifts the
  // hour index by +1 (mod 24) so the operator-facing hour is the
  // map's key.
  const socByHourStart = bucketSocByHourStart(
    socResp.points.filter((p) => p.metric_key === SOC_METRIC_KEY),
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
  const soc0Percent = socByHourStart.get(0)
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

  // Cost-basis third pass: pre-roll yesterday's 24 hours so we land
  // at 00:00 today with the actual UAH/kWh that survived overnight,
  // then roll today's 24 hours filling the four cost-basis fields
  // on each row. PV→ESS contributes 0 UAH (sun is free), Grid→ESS
  // contributes `gridToEss · importPrice` (real cash), and
  // discharges withdraw at the running average (WAC). The initial
  // state branches on which signals we have for yesterday: full
  // 48-hour roll when both flows and DAM are available; otherwise
  // a seed derived from `tariffs.seedEssCostUahPerKwh` × today's
  // hour-0 residual; otherwise an empty battery.
  let state: EssState = pickInitialEssState(
    yFlows,
    yDeltas,
    yDamPrices,
    tariffs,
    soc0Percent,
  )

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

// pickInitialEssState chooses where today's cost-basis pipeline
// starts. Pre-rolling yesterday is the highest-fidelity option;
// the seed-from-tariff path is the operator's manual fallback for
// fresh deployments / DAM outages; an empty battery is the
// last-ditch default when even the SOC anchor is missing. Kept as
// a pure helper so the lint rule that complains about double
// initialisation in the calling effect stays quiet.
//
// Exported for unit tests; production code calls it via
// `assembleHourlyRows`.
export function pickInitialEssState(
  yFlows: EnergyFlowHourlyResponse | null,
  yDeltas: TimeseriesResponse | null,
  yDamPrices: DAMPrice[] | null,
  tariffs: Tariffs,
  soc0Percent: number | undefined,
): EssState {
  if (yFlows && yDeltas && yDamPrices) {
    return preRollYesterday(yFlows, yDeltas, yDamPrices, tariffs)
  }
  if (soc0Percent !== undefined && Number.isFinite(soc0Percent) && soc0Percent > 0) {
    const dayStartResidual = (soc0Percent / 100) * tariffs.essCapacityKwh
    return seedFromCostPerKwh(dayStartResidual, tariffs.seedEssCostUahPerKwh)
  }
  return ZERO_ESS_STATE
}

// Exported for unit tests; production code reaches it via
// `pickInitialEssState`.
//
// preRollYesterday walks the previous day's 24-hour window through
// the same `rollHour` algorithm and returns the cost-basis state
// at midnight. Hours with missing RDN are SKIPPED entirely —
// pretending their import/export prices are 0 would log Grid→УЗЕ
// charges as free and corrupt the seed handed to today. A partly-
// missing yesterday still produces a useful seed when populated
// hours dominated the activity; if yesterday is entirely
// price-less, the seed degrades to `tariffs.seedEssCostUahPerKwh`
// applied to whatever residual `seedFromCostPerKwh` was given (we
// pass 0, so an empty battery — the cleanest default).
//
// Note: today's tariffs are reused for yesterday's recompute. If
// the operator changed e.g. distribution rate between the two
// days, today's KPI / table will reflect the new rate even for
// yesterday's charges. That trade-off is intentional — the
// dashboard never persists historical tariff snapshots, so the
// only honest alternative would be to ignore yesterday entirely.
export function preRollYesterday(
  yFlows: EnergyFlowHourlyResponse,
  yDeltas: TimeseriesResponse,
  yDamPrices: DAMPrice[],
  tariffs: Tariffs,
): EssState {
  const pvByHour = bucketBy(
    yDeltas.points.filter((p) => p.metric_key === 'accumulated_pv_energy_yield_kwh'),
  )
  const importByHour = bucketBy(
    yDeltas.points.filter((p) => p.metric_key === 'accumulated_electricity_purchased_kwh'),
  )
  const exportByHour = bucketBy(
    yDeltas.points.filter((p) => p.metric_key === 'accumulated_electricity_sold_kwh'),
  )
  const priceMap = buildPriceMap(yDamPrices)

  let state: EssState = seedFromCostPerKwh(0, tariffs.seedEssCostUahPerKwh)
  for (let h = 0; h < 24; h++) {
    const flowRow = yFlows.hours[h]
    if (!flowRow) continue
    const rdn = priceMap.has(h) ? (priceMap.get(h) as number) : null
    if (rdn === null) continue
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
    const economics = hourEconomics(rdn, flow, tariffs)
    const result = rollHour(
      state,
      flow,
      economics.importPriceUahPerKwh,
      economics.exportPriceUahPerKwh,
      tariffs.degradationUahPerKwh,
    )
    state = result.next
  }
  return state
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

// bucketSocByHourStart maps each SOC sample to the OPERATOR-FACING
// hour it represents the start of. The /api/v1/timeseries call uses
// `aggregation=last` over [H-1, H) buckets, so a point timestamped
// at H-1 is "the SOC observed at the start of hour H". We shift the
// source hour by +1 (mod 24) and keep the latest sample wins
// semantics that aggregation=last already provides server-side.
function bucketSocByHourStart(
  points: { metric_key: string; time: string; value: number }[],
): Map<number, number> {
  const out = new Map<number, number>()
  for (const point of points) {
    const t = new Date(point.time)
    if (Number.isNaN(t.getTime())) continue
    const sourceHour = t.getHours()
    const targetHour = (sourceHour + 1) % 24
    out.set(targetHour, point.value)
  }
  return out
}

// bucketBy folds a list of TimeseriesPoint entries into a hour →
// summed-delta map. The /api/v1/timeseries endpoint with
// bucket=1 hour & aggregation=delta returns one point per hour, so
// the fold is technically a sum-of-one in normal use; we keep the
// reduce form to handle the edge case where the API switches to
// emitting multiple sub-hour points.
function bucketBy(points: { time: string; value: number }[]): Map<number, number> {
  const out = new Map<number, number>()
  for (const point of points) {
    const t = new Date(point.time)
    if (Number.isNaN(t.getTime())) continue
    const hour = t.getHours()
    out.set(hour, (out.get(hour) ?? 0) + point.value)
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
