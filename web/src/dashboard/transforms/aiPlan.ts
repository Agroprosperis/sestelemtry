import type { UzePlanAction, UzePlanResponse } from '../../api'

// The day chart draws 288 five-minute buckets (12 per hour).
const BUCKETS_PER_HOUR = 12

// AiPlanBucket is the recommendation projected onto one chart bucket.
// `essKw` is present on every bucket of the hour so the line renders as a
// step; `socPct` only on the hour's last bucket, because the plan's SOC is
// the state at the END of the hour — the same hour-boundary convention the
// monthly cycle chart uses.
export type AiPlanBucket = {
  essKw: number
  socPct: number | null
  // loadKw is the recommended elevator consumption, stepped like essKw.
  // null when the hour has no telemetry to redistribute.
  loadKw: number | null
  action: UzePlanAction
  reasonText: string
  effectUah: number
}

// aiPlanBuckets expands the 24-hour recommendation into per-bucket values
// keyed by bucket index (0..287), so the chart can merge them onto the
// existing power rows by position.
//
// Returns an empty map when there is no usable plan, which is what makes
// the overlay degrade quietly: the day chart just draws without it.
export function aiPlanBuckets(plan: UzePlanResponse | null): Map<number, AiPlanBucket> {
  const out = new Map<number, AiPlanBucket>()
  if (!plan || !plan.available || !plan.hours?.length) return out

  for (const hour of plan.hours) {
    const h = Number(hour?.hour)
    if (!Number.isFinite(h) || h < 0 || h > 23) continue
    const essKw = Number(hour.recommended_ess_kw)
    if (!Number.isFinite(essKw)) continue
    const socRaw = Number(hour.soc_pct)
    const soc = Number.isFinite(socRaw) ? socRaw : null
    const loadRaw = Number(hour.recommended_load_kw)
    const load = hour.recommended_load_kw != null && Number.isFinite(loadRaw) ? loadRaw : null
    const effectRaw = Number(hour.effect_uah)

    const base = h * BUCKETS_PER_HOUR
    for (let i = 0; i < BUCKETS_PER_HOUR; i++) {
      out.set(base + i, {
        essKw,
        // SOC lands on the closing bucket only: anchoring it mid-hour
        // would draw the battery arriving at its end-of-hour state
        // before it had moved the energy to get there.
        socPct: i === BUCKETS_PER_HOUR - 1 ? soc : null,
        loadKw: load,
        action: hour.action,
        reasonText: hour.reason_text ?? '',
        effectUah: Number.isFinite(effectRaw) ? effectRaw : 0,
      })
    }
  }
  return out
}

// aiPlanHasDispatch reports whether the plan actually moves the battery.
// A day where the optimum is "do nothing" (no prices, dead telemetry)
// would otherwise add a flat zero line and a legend entry for nothing.
export function aiPlanHasDispatch(plan: UzePlanResponse | null): boolean {
  if (!plan || !plan.available) return false
  return (plan.hours ?? []).some(
    (h) => Number.isFinite(Number(h?.recommended_ess_kw)) && Math.abs(Number(h.recommended_ess_kw)) > 0.5,
  )
}

// aiPlanHasLoad reports whether the plan carries a consumption schedule
// worth drawing — at least one hour with a positive recommended load.
export function aiPlanHasLoad(plan: UzePlanResponse | null): boolean {
  if (!plan || !plan.available) return false
  return (plan.hours ?? []).some(
    (h) => h?.recommended_load_kw != null && Number(h.recommended_load_kw) > 0.5,
  )
}
