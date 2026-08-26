import { useEffect, useState } from 'react'
import { fetchEconomicsDaily, type EconomicsHourApi } from '../api'
import type { HourEconomicsRow } from './compute'
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
}

type Input = {
  organizationID: string
  // YYYY-MM-DD calendar day in LOCAL_TZ.
  date: string
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
export function useEconomicsData(input: Input): EconomicsData {
  const [data, setData] = useState<EconomicsData>(() => ({
    rows: Array.from({ length: 24 }, () => null),
    loading: true,
    error: null,
    hoursMissingPrice: 0,
    skipDiagnostics: null,
    reconciled: false,
    qualityFlags: [],
  }))

  useEffect(() => {
    if (!input.organizationID || !input.date) {
      setData({
        rows: Array.from({ length: 24 }, () => null),
        loading: false,
        error: null,
        hoursMissingPrice: 0,
        skipDiagnostics: null,
        reconciled: false,
        qualityFlags: [],
      })
      return
    }
    const controller = new AbortController()
    setData((prev) => ({ ...prev, loading: true, error: null }))

    // The live today recompute can exceed the API write timeout; fail
    // the spinner instead of hanging until the browser gives up.
    const timeout = AbortSignal.timeout(20_000)
    const signal = AbortSignal.any([controller.signal, timeout])

    fetchEconomicsDaily(
      { organizationID: input.organizationID, date: input.date, tz: LOCAL_TZ },
      signal,
    )
      .then((resp) => {
        const rows = resp.hours.map((h) => (h ? mapHourRow(h) : null))
        setData({
          rows,
          loading: false,
          error: null,
          hoursMissingPrice: resp.hours_missing_price,
          skipDiagnostics: null,
          reconciled: resp.reconciled ?? false,
          qualityFlags: resp.quality_flags ?? [],
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
        setData((prev) => ({ ...prev, loading: false, error: message }))
      })

    return () => controller.abort()
  }, [input.organizationID, input.date, input.refreshKey])

  return data
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
