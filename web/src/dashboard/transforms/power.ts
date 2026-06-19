import type { TimeseriesPoint } from '../../types'
import { DAY_BUCKET_MINUTES, timelineBuckets } from '../timeline'

export type PowerChartRow = { time: string } & Record<string, string | number | null>

// Telemetry occasionally emits absurd power spikes (sensor glitches, unit
// mismatches upstream). Anything beyond this magnitude is clamped to 0 so a
// single bad sample doesn't blow up the y-axis scale of the day chart.
const DAY_POWER_ANOMALY_THRESHOLD_KW = 2000

// LOAD_KEY and the three sources we sum to derive it. Kept separate from
// the caller-provided `keys` list so a downstream rename of the metric
// only needs to flip these constants in one place.
const LOAD_KEY = 'load_power_kw'
const PV_KEY = 'active_pv_power_kw'
const GRID_KEY = 'grid_connected_active_power_kw'
const ESS_KEY = 'active_ess_power_kw'

// Cumulative-counter metric_keys used to RECONSTRUCT instantaneous power for
// days that only have archive data (the FusionSolar importer writes these
// kWh counters but no `*_power_kw` snapshots). Each 5-minute bucket carries
// the server's MAX-MIN delta (already clamped >= 0); average power over the
// interval is delta(kWh) / (5/60 h) = delta * 12.
const FALLBACK_PV_YIELD_KEY = 'accumulated_pv_energy_yield_kwh'
const FALLBACK_GRID_IMPORT_KEY = 'accumulated_electricity_purchased_kwh'
const FALLBACK_GRID_EXPORT_KEY = 'accumulated_electricity_sold_kwh'
const FALLBACK_ESS_CHARGE_KEY = 'total_energy_charged_kwh'
const FALLBACK_ESS_DISCHARGE_KEY = 'total_energy_discharged_kwh'

// kWh accumulated over one DAY_BUCKET_MINUTES interval, expressed as average
// power: 1 kWh in 5 min == 12 kW.
const KW_PER_KWH_5MIN = 60 / DAY_BUCKET_MINUTES

type DerivedPower = { pv: number; grid: number; ess: number }

// derivedPowerByBucket folds per-bucket cumulative-counter deltas into the
// three directional power values, keyed by the same 5-minute slot the
// instantaneous samples use. Grid is net import (import - export, + = import)
// and ESS is net discharge (discharge - charge, + = discharge), matching the
// live-metric sign convention the load derivation and tooltip already assume.
function derivedPowerByBucket(
  fallbackPoints: TimeseriesPoint[],
): Map<string, DerivedPower> {
  type Deltas = {
    pvYield: number
    gridImport: number
    gridExport: number
    essCharge: number
    essDischarge: number
  }
  const deltas = new Map<string, Deltas>()
  for (const p of fallbackPoints) {
    const t = new Date(p.time)
    const ts = t.getTime()
    if (!Number.isFinite(ts) || !Number.isFinite(p.value)) continue
    const slot = bucketKey(t)
    const cur =
      deltas.get(slot) ??
      { pvYield: 0, gridImport: 0, gridExport: 0, essCharge: 0, essDischarge: 0 }
    switch (p.metric_key) {
      case FALLBACK_PV_YIELD_KEY:
        cur.pvYield += p.value
        break
      case FALLBACK_GRID_IMPORT_KEY:
        cur.gridImport += p.value
        break
      case FALLBACK_GRID_EXPORT_KEY:
        cur.gridExport += p.value
        break
      case FALLBACK_ESS_CHARGE_KEY:
        cur.essCharge += p.value
        break
      case FALLBACK_ESS_DISCHARGE_KEY:
        cur.essDischarge += p.value
        break
      default:
        continue
    }
    deltas.set(slot, cur)
  }
  const out = new Map<string, DerivedPower>()
  for (const [slot, d] of deltas) {
    out.set(slot, {
      pv: d.pvYield * KW_PER_KWH_5MIN,
      grid: (d.gridImport - d.gridExport) * KW_PER_KWH_5MIN,
      ess: (d.essDischarge - d.essCharge) * KW_PER_KWH_5MIN,
    })
  }
  return out
}

// clampAnomaly drops absurd power magnitudes (sensor glitches / counter
// resets that produce a huge single-bucket delta) to 0 so one bad sample
// can't blow up the y-axis scale.
function clampAnomaly(value: number): number {
  return Math.abs(value) > DAY_POWER_ANOMALY_THRESHOLD_KW ? 0 : value
}

// powerChartRows aligns instantaneous power samples (kW) to the day-preset
// 5-minute timeline. For each (bucket, metric) it keeps the sample with the
// latest `time` (semantics matches the server `aggregation=last`). Empty
// buckets stay `null` so the Recharts <Line> draws a gap instead of dropping
// to zero. Buckets with a start time strictly greater than the current
// 5-minute bucket on the anchor day are also returned with `null` values so
// the chart's lines do not extend into the future.
//
// `load_power_kw` is a special case: even when present in `keys`, its
// raw samples (Modbus 40503) are ignored and the row value is derived
// from PV + Grid + ESS via the bus-balance identity. The
// SmartLogger's 40503 register tracks only the inverter's "Backup
// load" branch, so it consistently undercounts site-wide consumption
// during normal grid-tied operation; the derived value is closer to
// reality.
//
// We negate the sum so the chart renders sources (PV/Grid import/ESS
// discharge) above zero and the load sink below zero — that mirrors
// the physical bus balance ("what flows in equals what flows out")
// and lets analysts read self-sufficiency at a glance:
//   load_power_kw = -(active_pv_power_kw
//                     + grid_connected_active_power_kw
//                     + active_ess_power_kw)
// where Grid > 0 means import and ESS > 0 means discharge. The
// tooltip flips the sign back for display so users still see a
// positive consumption number.
//
// If any of the three inputs is null in a bucket the load stays null
// (gap) — partial sums would be misleading.
//
// `fallbackPoints` are per-bucket cumulative-counter deltas (kWh). When a
// bucket has no instantaneous power sample (typical for archive-only days,
// where the FusionSolar importer wrote only kWh counters), the PV/Grid/ESS
// value is reconstructed from these deltas (delta * 12 == avg kW). The
// fallback is applied per bucket and per metric, so a day that mixes live
// and archive segments renders one continuous line. Instantaneous samples
// always take precedence when present.
export function powerChartRows(
  points: TimeseriesPoint[],
  keys: string[],
  anchor: Date,
  now: Date = new Date(),
  fallbackPoints: TimeseriesPoint[] = [],
): PowerChartRow[] {
  const lastByKey = new Map<string, { value: number; time: number }>()
  for (const p of points) {
    if (!keys.includes(p.metric_key)) continue
    if (p.metric_key === LOAD_KEY) continue
    const t = new Date(p.time)
    const ts = t.getTime()
    if (!Number.isFinite(ts) || !Number.isFinite(p.value)) continue
    const slot = bucketKey(t)
    const composite = `${p.metric_key}@${slot}`
    const existing = lastByKey.get(composite)
    if (!existing || ts > existing.time) {
      lastByKey.set(composite, { value: p.value, time: ts })
    }
  }
  const derived = derivedPowerByBucket(fallbackPoints)
  const derivedKeyOf: Record<string, keyof DerivedPower> = {
    [PV_KEY]: 'pv',
    [GRID_KEY]: 'grid',
    [ESS_KEY]: 'ess',
  }
  const cutoff = futureDayCutoff(anchor, now)
  const timeline = timelineBuckets('day', anchor)
  const wantsLoad = keys.includes(LOAD_KEY)
  return timeline.map(({ t, label }) => {
    const row: PowerChartRow = { time: label }
    const slot = bucketKey(new Date(t))
    const isFuture = cutoff !== null && t > cutoff
    for (const key of keys) {
      if (key === LOAD_KEY) continue
      if (isFuture) {
        row[key] = null
        continue
      }
      const hit = lastByKey.get(`${key}@${slot}`)
      if (hit) {
        row[key] = clampAnomaly(hit.value)
        continue
      }
      const fallbackKey = derivedKeyOf[key]
      const slotDerived = fallbackKey ? derived.get(slot) : undefined
      if (slotDerived && fallbackKey) {
        row[key] = clampAnomaly(slotDerived[fallbackKey])
      } else {
        row[key] = null
      }
    }
    if (wantsLoad) {
      if (isFuture) {
        row[LOAD_KEY] = null
      } else {
        const pv = row[PV_KEY]
        const grid = row[GRID_KEY]
        const ess = row[ESS_KEY]
        if (
          typeof pv === 'number' &&
          typeof grid === 'number' &&
          typeof ess === 'number'
        ) {
          row[LOAD_KEY] = -(pv + grid + ess)
        } else {
          row[LOAD_KEY] = null
        }
      }
    }
    return row
  })
}

function bucketKey(date: Date): string {
  const minute = Math.floor(date.getMinutes() / DAY_BUCKET_MINUTES) * DAY_BUCKET_MINUTES
  return `${date.getFullYear()}-${date.getMonth()}-${date.getDate()}-${date.getHours()}-${minute}`
}

// futureDayCutoff mirrors the energy-bucket transform: when the anchor is the
// local current day, return the start time of the bucket containing `now`.
// Buckets with a start strictly after this are considered "future" and get
// null values so the lines end at the latest known sample.
function futureDayCutoff(anchor: Date, now: Date): number | null {
  const anchorDay = new Date(anchor)
  anchorDay.setHours(0, 0, 0, 0)
  const today = new Date(now)
  today.setHours(0, 0, 0, 0)
  if (anchorDay.getTime() !== today.getTime()) return null
  const currentBucketStart = new Date(now)
  const minute = Math.floor(currentBucketStart.getMinutes() / DAY_BUCKET_MINUTES) * DAY_BUCKET_MINUTES
  currentBucketStart.setMinutes(minute, 0, 0)
  return currentBucketStart.getTime()
}
