import type { OutageInterval } from '../../outages/outagesClient'
import { isOffgrid, type DaySupplyBucket } from './supply'

// OutageArea is one planned outage as a band on the day chart: x1/x2 are
// timeline labels ("HH:MM", 5-minute buckets).
export type OutageArea = { x1: string; x2: string; kind: OutageInterval['kind'] }

// OUTAGE_ROW_KEY carries the outage kind on a day-chart row inside one,
// for the tooltip.
export const OUTAGE_ROW_KEY = 'planned_outage_kind'

const KYIV_TZ = 'Europe/Kyiv'
const BUCKET_MIN = 5

const kyivParts = new Intl.DateTimeFormat('en-GB', {
  timeZone: KYIV_TZ,
  year: 'numeric',
  month: '2-digit',
  day: '2-digit',
  hour: '2-digit',
  minute: '2-digit',
  hourCycle: 'h23',
})

// kyivMoment splits an instant into its Kyiv calendar day (YYYY-MM-DD)
// and minutes since local midnight; null for an unparsable time.
function kyivMoment(iso: string): { day: string; minutes: number } | null {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return null
  const p: Record<string, string> = {}
  for (const part of kyivParts.formatToParts(d)) p[part.type] = part.value
  return {
    day: `${p.year}-${p.month}-${p.day}`,
    minutes: Number(p.hour) * 60 + Number(p.minute),
  }
}

function label(minutes: number): string {
  const m = Math.max(0, Math.min(24 * 60 - 1, minutes))
  return `${String(Math.floor(m / 60)).padStart(2, '0')}:${String(m % 60).padStart(2, '0')}`
}

// outageAreas maps the API's outage intervals onto the chart's timeline
// of the given Kyiv day (YYYY-MM-DD): the part of each interval inside
// the day, snapped outwards to the 5-minute labels the chart has.
// Intervals outside the day, or reaching no label, are dropped.
export function outageAreas(
  intervals: OutageInterval[] | null | undefined,
  day: string,
  timelineLabels: string[],
): OutageArea[] {
  if (!intervals || intervals.length === 0 || timelineLabels.length === 0) return []
  const first = timelineLabels[0]
  const last = timelineLabels[timelineLabels.length - 1]
  const out: OutageArea[] = []
  for (const iv of intervals) {
    const start = kyivMoment(iv.start)
    const end = kyivMoment(iv.end)
    if (!start || !end) continue
    if (start.day > day || end.day < day) continue
    // Clip to the day: an interval from the previous evening starts at
    // 00:00, one running past midnight ends at the last label.
    const startMin = start.day < day ? 0 : Math.floor(start.minutes / BUCKET_MIN) * BUCKET_MIN
    const endAtDayEnd = end.day > day
    const endMin = endAtDayEnd ? 24 * 60 : Math.ceil(end.minutes / BUCKET_MIN) * BUCKET_MIN
    if (endMin <= startMin) continue
    const startLabel = label(startMin)
    const endLabel = endAtDayEnd || endMin >= 24 * 60 ? last : label(endMin)
    const x1 = timelineLabels.find((l) => l >= startLabel) ?? first
    let x2 = last
    for (let i = timelineLabels.length - 1; i >= 0; i--) {
      if (timelineLabels[i] <= endLabel) {
        x2 = timelineLabels[i]
        break
      }
    }
    if (x1 >= x2) continue
    out.push({ x1, x2, kind: iv.kind })
  }
  return out
}

// offgridAreas are the stretches the site actually ran without the grid —
// on the generator, as an island or dark — read from its Encombi
// breakers, as timeline labels: x1 the first bucket without the grid, x2
// the first with it back, or the day's last label when it never came
// back. Changeover, unknown and not-yet buckets are not counted.
export function offgridAreas(
  supply: DaySupplyBucket[] | null | undefined,
  timelineLabels: string[],
): { x1: string; x2: string }[] {
  if (!supply || supply.length === 0 || timelineLabels.length === 0) return []
  const last = timelineLabels[timelineLabels.length - 1]
  const out: { x1: string; x2: string }[] = []
  let start: string | null = null
  for (const b of supply) {
    if (isOffgrid(b.mode)) {
      start ??= b.time
      continue
    }
    if (start !== null) {
      out.push({ x1: start, x2: b.time })
      start = null
    }
  }
  if (start !== null && start < last) out.push({ x1: start, x2: last })
  return out
}

// outageKindAt is the kind of the outage a timeline bucket falls in, the
// published schedule first; null outside every one. A band ends where
// the light is back, so its last label is not inside it — unless it is
// the day's last label, which a band only reaches running to midnight.
export function outageKindAt(areas: OutageArea[], time: string, lastLabel: string): OutageArea['kind'] | null {
  let kind: OutageArea['kind'] | null = null
  for (const a of areas) {
    if (time < a.x1 || (time >= a.x2 && !(a.x2 === lastLabel && time === lastLabel))) continue
    if (a.kind === 'schedule') return 'schedule'
    kind = a.kind
  }
  return kind
}
