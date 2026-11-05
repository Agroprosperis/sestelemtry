import { useEffect, useMemo, useState } from 'react'
import { fetchEconomicsDaily, type EconomicsHourApi } from '../api'
import type { HourEconomicsRow } from './compute'
import type { Currency } from './currency'
import type { Tariffs } from './tariffs'

// localKyivTz is the canonical timezone for the economics page: the
// day boundary and DAM hour numbering are always Europe/Kyiv,
// regardless of the operator's browser zone.
const LOCAL_TZ = 'Europe/Kyiv'

export type EconomicsData = {
  // Always 24-long. `null` means the hour had no flow data (or the
  // page is still loading). The page renders an empty-data
  // placeholder rather than fabricating zeros.
  rows: Array<HourEconomicsRow | null>
  loading: boolean
  error: string | null
  // hoursMissingPrice mirrors the server's count of priced-less hours
  // so the header can show "ціни РДН частково відсутні".
  hoursMissingPrice: number
  // skipDiagnostics is retained for shape compatibility; the
  // server-side pipeline no longer surfaces per-day allocator
  // warnings here, so it's always null.
  skipDiagnostics: string | null
  // reconciled is true when the day's flows were scaled to the canonical
  // FusionSolar daily KPIs; qualityFlags carries the reconciliation
  // diagnostics (e.g. "load_mismatch:0.07").
  reconciled: boolean
  qualityFlags: string[]
  // isFinal is true once the day has fully elapsed; computedAt (RFC3339)
  // is when the server last computed it, null while nothing has loaded.
  isFinal: boolean
  computedAt: string | null
  // fxRate is the day's UAH per unit the money was converted at (null in
  // UAH); fxCarried marks a day that borrowed the nearest rate,
  // fxProvisional today's rate before 12:00, which may still move.
  fxRate: number | null
  fxCarried: boolean
  fxProvisional: boolean
}

type Input = {
  organizationID: string
  // YYYY-MM-DD calendar day in LOCAL_TZ.
  date: string
  // currency the money comes back in; UAH when omitted.
  currency?: Currency
  // tariffs is retained for caller compatibility but no longer drives
  // computation — the server resolves the date-versioned tariff
  // schedule and computes economics itself.
  tariffs?: Tariffs
  // refreshKey re-fires the fetch without changing inputs (e.g. after
  // a DAM-price refresh or a recompute). `undefined` → 0.
  refreshKey?: number
}

// useEconomicsData reads the server-computed economics for one day from
// the single /economics/daily endpoint. All computation now lives in
// the Go service (internal/economics); this hook only maps the wire
// shape into the existing HourEconomicsRow[] the charts/table consume.
type DayValues = Omit<EconomicsData, 'loading' | 'error'>

const EMPTY_DAY: DayValues = {
  rows: Array.from({ length: 24 }, () => null),
  hoursMissingPrice: 0,
  skipDiagnostics: null,
  reconciled: false,
  qualityFlags: [],
  isFinal: false,
  computedAt: null,
  fxRate: null,
  fxCarried: false,
  fxProvisional: false,
}

export function useEconomicsData(input: Input): EconomicsData {
  const active = Boolean(input.organizationID && input.date)
  const currency = input.currency ?? 'UAH'
  const key = JSON.stringify([input.organizationID, input.date, currency, input.refreshKey ?? 0])
  // The last answer and the request it settled; while the current key
  // has no answer it is loading and the previous day stays on screen.
  const [answer, setAnswer] = useState<{ key: string; day: DayValues; error: string | null } | null>(null)

  useEffect(() => {
    if (!active) return
    const controller = new AbortController()

    // The live today recompute can exceed the API write timeout; fail
    // the spinner instead of hanging until the browser gives up.
    const timeout = AbortSignal.timeout(20_000)
    const signal = AbortSignal.any([controller.signal, timeout])

    fetchEconomicsDaily(
      { organizationID: input.organizationID, date: input.date, tz: LOCAL_TZ, currency },
      signal,
    )
      .then((resp) => {
        setAnswer({
          key,
          day: {
            rows: resp.hours.map((h) => (h ? mapHourRow(h) : null)),
            hoursMissingPrice: resp.hours_missing_price,
            skipDiagnostics: null,
            reconciled: resp.reconciled ?? false,
            qualityFlags: resp.quality_flags ?? [],
            isFinal: resp.is_final,
            computedAt: resp.computed_at ?? null,
            fxRate: resp.fx_rate && resp.fx_rate > 0 ? resp.fx_rate : null,
            fxCarried: resp.fx_carried ?? false,
            fxProvisional: resp.fx_provisional ?? false,
          },
          error: null,
        })
      })
      .catch((err: unknown) => {
        if (controller.signal.aborted) return
        const name = (err as DOMException)?.name
        const message =
          name === 'TimeoutError' || name === 'AbortError'
            ? 'Денний розрахунок не відповів за 20 с. Оновіть сторінку або відкрийте вчорашній день.'
            : err instanceof Error
              ? err.message
              : typeof err === 'string'
                ? err
                : 'failed to load economics data'
        setAnswer((prev) => ({ key, day: prev?.day ?? EMPTY_DAY, error: message }))
      })

    return () => controller.abort()
  }, [active, key, input.organizationID, input.date, currency])

  return useMemo(() => {
    if (!active) return { ...EMPTY_DAY, loading: false, error: null }
    const loading = answer?.key !== key
    return { ...(answer?.day ?? EMPTY_DAY), loading, error: loading ? null : (answer?.error ?? null) }
  }, [active, answer, key])
}

// fetchUahDayRows reads one day in UAH whatever the page shows: the Excel
// export stays in the stored currency.
export async function fetchUahDayRows(organizationID: string, date: string): Promise<Array<HourEconomicsRow | null>> {
  const resp = await fetchEconomicsDaily({ organizationID, date, tz: LOCAL_TZ })
  return resp.hours.map((h) => (h ? mapHourRow(h) : null))
}

// mapHourRow converts the flat snake_case wire shape into the nested
// HourEconomicsRow the dashboard components already consume.
function mapHourRow(h: EconomicsHourApi): HourEconomicsRow {
  return {
    hour: h.hour,
    hourStart: h.hour_start,
    rdnUahPerKwh: h.rdn_uah_per_kwh,
    flow: {
      pv: h.pv_kwh,
      gridImport: h.grid_import_kwh,
      gridExport: h.grid_export_kwh,
      essCharged: h.ess_charged_kwh,
      essDischarged: h.ess_discharged_kwh,
      pvToEss: h.pv_to_ess_kwh,
      gridToEss: h.grid_to_ess_kwh,
      essToLoad: h.ess_to_load_kwh,
      essToGrid: h.ess_to_grid_kwh,
    },
    economics: {
      load: h.load_kwh,
      pvToLoad: h.pv_to_load_kwh,
      pvToGrid: h.pv_to_grid_kwh,
      gridToLoad: h.grid_to_load_kwh,
      importPriceUahPerKwh: h.import_price_uah_per_kwh,
      exportPriceUahPerKwh: h.export_price_uah_per_kwh,
      baselineCost: h.baseline_cost_uah,
      actualCost: h.actual_cost_uah,
      effect: h.effect_uah,
      essNet: h.ess_net_uah,
    },
    essRemainingKwhStart: h.ess_remaining_kwh_start,
    essCostBasisUahStart: h.ess_cost_basis_uah_start,
    essAvgCostUahPerKwhStart: h.ess_avg_cost_uah_per_kwh_start,
    essWithdrawnCostUah: h.ess_withdrawn_cost_uah,
    essRealizedProfitUah: h.ess_realized_profit_uah,
    essCostBasisUahEnd: h.ess_cost_basis_uah_end,
    essAvgCostUahPerKwhEnd: h.ess_avg_cost_uah_per_kwh_end,
    essResidualKwhEnd: h.ess_residual_kwh_end,
  }
}
