// manifestPlan adapts the manifest-lite plan.intervals into the
// UzePlanResponse shape the dashboard's EnergyChart already knows how
// to project onto the day chart (per-hour ESS step + end-of-hour SOC).
// Reusing that pipeline means the «план і факт» overlay costs zero new
// chart code.

import type { UzePlanAction, UzePlanHour, UzePlanResponse } from '../api'
import type { ManifestPayload } from './controlClient'

function sameLocalDay(a: Date, b: Date): boolean {
  return (
    a.getFullYear() === b.getFullYear() &&
    a.getMonth() === b.getMonth() &&
    a.getDate() === b.getDate()
  )
}

function actionFor(essKw: number): UzePlanAction {
  if (essKw > 0.5) return 'discharge'
  if (essKw < -0.5) return 'charge'
  return 'hold'
}

// manifestPlanToUzePlan projects the manifest intervals that fall on
// `anchor`'s local day. Returns null when the manifest has no plan for
// that day, which makes the overlay degrade quietly.
export function manifestPlanToUzePlan(
  payload: ManifestPayload | undefined,
  anchor: Date,
): UzePlanResponse | null {
  const intervals = payload?.plan?.intervals
  if (!payload || !intervals || intervals.length === 0) return null

  const hours: UzePlanHour[] = []
  for (const iv of intervals) {
    const d = new Date(iv.ts)
    if (Number.isNaN(d.getTime()) || !sameLocalDay(d, anchor)) continue
    const soc = iv.soc_target_pct
    hours.push({
      hour: d.getHours(),
      recommended_ess_kw: iv.ess_kw,
      soc_pct: soc != null && Number.isFinite(soc) ? soc : Number.NaN,
      ess_to_load_kwh: 0,
      ess_to_grid_kwh: 0,
      pv_to_ess_kwh: 0,
      grid_to_ess_kwh: 0,
      effect_uah: 0,
      action: actionFor(iv.ess_kw),
      reason_code: 'manifest_plan',
      reason_text:
        `План manifest ${payload.manifest_id}` +
        (iv.rdn_uah_per_kwh ? ` · РДН ${iv.rdn_uah_per_kwh.toFixed(2)} грн/кВт·год` : ''),
      recommended_load_kw: null,
      rdn_uah_per_kwh: iv.rdn_uah_per_kwh ?? null,
    })
  }
  if (hours.length === 0) return null

  return {
    organization_id: payload.site_id,
    date: anchor.toISOString().slice(0, 10),
    tz: '',
    available: true,
    soc_start_pct: 0,
    capacity_kwh: 0,
    power_kw: 0,
    hours,
    totals: {
      optimum_uah: 0,
      fact_uah: 0,
      reserve_uah: 0,
      captured_share: 0,
      charge_pv_kwh: 0,
      charge_grid_kwh: 0,
      discharge_kwh: 0,
      export_val_uah: 0,
      load_val_uah: 0,
      charge_pv_cost_uah: 0,
      grid_cost_uah: 0,
      degradation_uah: 0,
    },
  }
}

// appliedAnnotation converts the manifest applied_at moment into the
// day chart's 5-minute bucket label ("HH:MM") — but only when it falls
// on the anchor day, otherwise the marker would pin to a wrong bucket.
export function appliedAnnotation(
  appliedAt: string | undefined,
  anchor: Date,
): { time: string; label: string } | undefined {
  if (!appliedAt) return undefined
  const d = new Date(appliedAt)
  if (Number.isNaN(d.getTime()) || !sameLocalDay(d, anchor)) return undefined
  const minutes = Math.floor(d.getMinutes() / 5) * 5
  const hh = String(d.getHours()).padStart(2, '0')
  const mm = String(minutes).padStart(2, '0')
  return { time: `${hh}:${mm}`, label: 'manifest applied' }
}
