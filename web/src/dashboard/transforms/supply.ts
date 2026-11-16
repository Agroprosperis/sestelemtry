import type { TimeseriesPoint } from '../../types'
import { DAY_BUCKET_MINUTES, timelineBuckets } from '../timeline'

// SupplyMode is how a site is fed, by its Encombi's breakers, generator
// power and bus voltage (ems-spec generator handoff §4.1).
export type SupplyMode = 'grid' | 'generator' | 'island' | 'blackout' | 'transition' | 'unknown'

// The §4.1 thresholds: a running generator and a live bus.
export const GEN_RUNNING_KW = 5
const BUS_LIVE_V = 180

export const GEN_POWER_KEY = 'active_gen_power_kw'
export const GRID_BITS_KEY = 'grid_breakers_on_bits'
export const GEN_BITS_KEY = 'gen_breakers_on_bits'
export const BUS_VOLTAGE_KEY = 'gen_bus_voltage_a_v'

// SUPPLY_METRIC_KEYS are the readings the mode is read from.
export const SUPPLY_METRIC_KEYS = [GRID_BITS_KEY, GEN_BITS_KEY, GEN_POWER_KEY, BUS_VOLTAGE_KEY]

// SUPPLY_MODE_KEY carries a day-chart bucket's mode on its row, for the
// tooltip.
export const SUPPLY_MODE_KEY = 'supply_mode'

export function isOffgrid(mode: SupplyMode | null | undefined): boolean {
  return mode === 'generator' || mode === 'island' || mode === 'blackout'
}

// supplyModeOf applies the §4.1 table to one set of readings; a missing
// breaker reading makes the mode unknown.
export function supplyModeOf(r: {
  gridBits: number | null
  genBits: number | null
  genKw: number | null
  busV: number | null
}): SupplyMode {
  if (r.gridBits === null || r.genBits === null) return 'unknown'
  const gridOn = r.gridBits > 0
  const genOn = r.genBits > 0 || (r.genKw ?? 0) > GEN_RUNNING_KW
  if (gridOn && genOn) return 'transition'
  if (gridOn) return 'grid'
  if (genOn) return 'generator'
  return (r.busV ?? 0) > BUS_LIVE_V ? 'island' : 'blackout'
}

export const SUPPLY_MODE_LABELS: Record<SupplyMode, string> = {
  grid: 'Мережа',
  generator: 'Генератор',
  island: 'Острів (від УЗЕ)',
  blackout: 'Без живлення',
  transition: 'Перехід',
  unknown: 'Невідомо',
}

// DaySupplyBucket is one 5-minute bucket of the day's supply strip; mode
// is null for a bucket still ahead of now.
export type DaySupplyBucket = { t: number; time: string; mode: SupplyMode | null }

function slotOf(date: Date): number {
  const d = new Date(date)
  d.setMinutes(Math.floor(d.getMinutes() / DAY_BUCKET_MINUTES) * DAY_BUCKET_MINUTES, 0, 0)
  return d.getTime()
}

// daySupplyModes reads each 5-minute bucket's mode from its last breaker,
// power and voltage readings (aggregation=last). null for a site whose
// Encombi reported nothing that day, so the chart draws no strip.
export function daySupplyModes(points: TimeseriesPoint[], anchor: Date, now: Date = new Date()): DaySupplyBucket[] | null {
  const byKey = new Map<string, Map<number, number>>()
  for (const p of points) {
    if (!SUPPLY_METRIC_KEYS.includes(p.metric_key)) continue
    const t = new Date(p.time)
    if (!Number.isFinite(t.getTime()) || !Number.isFinite(p.value)) continue
    let inner = byKey.get(p.metric_key)
    if (!inner) {
      inner = new Map()
      byKey.set(p.metric_key, inner)
    }
    inner.set(slotOf(t), p.value)
  }
  if (!byKey.has(GRID_BITS_KEY) && !byKey.has(GEN_BITS_KEY)) return null
  const cutoff = slotOf(now)
  const read = (key: string, t: number) => byKey.get(key)?.get(t) ?? null
  return timelineBuckets('day', anchor).map(({ t, label }) => ({
    t,
    time: label,
    mode:
      t > cutoff
        ? null
        : supplyModeOf({
            gridBits: read(GRID_BITS_KEY, t),
            genBits: read(GEN_BITS_KEY, t),
            genKw: read(GEN_POWER_KEY, t),
            busV: read(BUS_VOLTAGE_KEY, t),
          }),
  }))
}

// SupplySegment is a run of buckets with one mode, x1..x2 being the
// chart's category labels of its first bucket and of the bucket after it.
export type SupplySegment = { x1: string; x2: string; mode: SupplyMode }

export function supplySegments(buckets: DaySupplyBucket[]): SupplySegment[] {
  const out: SupplySegment[] = []
  for (let i = 0; i < buckets.length; i++) {
    const mode = buckets[i].mode
    if (mode === null) continue
    let j = i
    while (j + 1 < buckets.length && buckets[j + 1].mode === mode) j++
    const end = j + 1 < buckets.length ? buckets[j + 1].time : buckets[j].time
    out.push({ x1: buckets[i].time, x2: end, mode })
    i = j
  }
  return out
}
