import type { UzePlanAction, UzePlanResponse } from '../../api'

// The day chart draws 288 five-minute buckets (12 per hour).
const BUCKETS_PER_HOUR = 12

// AiPlanBucket is the recommendation projected onto one chart bucket.
// `essKw` is present on every bucket of the hour so the line renders as a
// step. The plan holds that one power for the whole hour, so the SOC moves
// in a straight line from where the hour starts (the previous hour's end,
// or the plan's start of day) to the end-of-hour state the plan reports:
// `socPct` follows that line bucket by bucket, and the slope of each hour
// is the hour's power. When the start is unknown only the closing bucket
// carries the end-of-hour state.
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

  const socEnd = new Map<number, number>()
  for (const hour of plan.hours) {
    const h = Number(hour?.hour)
    const soc = Number(hour?.soc_pct)
    if (Number.isFinite(h) && h >= 0 && h <= 23 && Number.isFinite(soc)) socEnd.set(h, soc)
  }
  // soc_start_pct 0 means no start of day (a manifest plan carries none):
  // the modelled band never goes below 10 %.
  const dayStart = Number(plan.soc_start_pct)
  const socStartOf = (h: number): number | null =>
    h > 0 ? (socEnd.get(h - 1) ?? null) : Number.isFinite(dayStart) && dayStart > 0 ? dayStart : null

  for (const hour of plan.hours) {
    const h = Number(hour?.hour)
    if (!Number.isFinite(h) || h < 0 || h > 23) continue
    const essKw = Number(hour.recommended_ess_kw)
    if (!Number.isFinite(essKw)) continue
    const end = socEnd.get(h) ?? null
    const start = end == null ? null : socStartOf(h)
    const loadRaw = Number(hour.recommended_load_kw)
    const load = hour.recommended_load_kw != null && Number.isFinite(loadRaw) ? loadRaw : null
    const effectRaw = Number(hour.effect_uah)

    const base = h * BUCKETS_PER_HOUR
    for (let i = 0; i < BUCKETS_PER_HOUR; i++) {
      let socPct: number | null = null
      if (end != null && start != null) socPct = start + ((end - start) * i) / BUCKETS_PER_HOUR
      else if (end != null && i === BUCKETS_PER_HOUR - 1) socPct = end
      out.set(base + i, {
        essKw,
        socPct,
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
