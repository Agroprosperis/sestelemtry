import type { PvForecastPoint } from '../../types'

// PvForecastHourlyRow describes one hour of forecast generation aggregated
// across all panel orientations. `hour` is 0..23 and represents the hour
// start in local Kyiv time (so `hour=0` covers 00:00–01:00, mirroring n8n's
// `hour_ending=1`). `plannedKw` is the average AC power in kW for that hour
// — equal to `planned_kwh` since each interval is exactly 1 hour, so it can
// be plotted directly on the day chart's kW axis.
export type PvForecastHourlyRow = {
  hour: number
  plannedKw: number
}

export type ElevatorCode = 'JE' | 'RE' | 'PE' | 'AB' | 'KE' | 'DE' | 'SE'

// elevatorCodeFor maps the dashboard's organization ID to the n8n flow's
// elevator code. Elevators that we don't have a forecast for return null,
// which signals callers to skip the network request entirely.
export function elevatorCodeFor(organizationID: string): ElevatorCode | null {
  if (organizationID === 'ze') return 'JE'
  if (organizationID === 'pe') return 'RE'
  if (organizationID === 'pde') return 'PE'
  if (organizationID === 'ab') return 'AB'
  if (organizationID === 'ke') return 'KE'
  if (organizationID === 'de') return 'DE'
  if (organizationID === 'se') return 'SE'
  return null
}

function pad(n: number): string {
  return n < 10 ? `0${n}` : String(n)
}

// forecastDayFromAnchor renders the anchor date as YYYY-MM-DD in the
// browser's local TZ. For Ukrainian users this is Kyiv local, which matches
// the n8n flow's `hour_ending` semantics. For users in other zones the day
// boundary may be off by ±1, but that's the same tradeoff the rest of the
// dashboard makes (see `toDateOnly` in `useDashboardData`).
export function forecastDayFromAnchor(anchor: Date): string {
  return `${anchor.getFullYear()}-${pad(anchor.getMonth() + 1)}-${pad(anchor.getDate())}`
}

// aggregatePvForecastHourly sums planned_kwh across orientations for each
// hour of the forecast day. Duplicate (hour_ending, orientation_idx) entries
// from the upstream feed are deduplicated — the last record wins, mirroring
// the previous PvForecastChart behavior. Returns a sparse list (only hours
// that have at least one orientation contributing a finite, positive value).
export function aggregatePvForecastHourly(
  points: PvForecastPoint[],
): PvForecastHourlyRow[] {
  // hourEnding -> orientation_idx -> planned_kwh. The inner Map.set
  // overwrites duplicate orientations within an hour bucket.
  const byHour = new Map<number, Map<number, number>>()
  for (const p of points) {
    if (!p) continue
    const hourEnding = Number(p.hour_ending)
    if (!Number.isFinite(hourEnding) || hourEnding < 1 || hourEnding > 24) continue
    const value = Number(p.planned_kwh)
    if (!Number.isFinite(value)) continue
    let inner = byHour.get(hourEnding)
    if (!inner) {
      inner = new Map<number, number>()
      byHour.set(hourEnding, inner)
    }
    inner.set(Number(p.orientation_idx), value)
  }

  const out: PvForecastHourlyRow[] = []
  for (const [hourEnding, byOrientation] of byHour) {
    let sum = 0
    for (const v of byOrientation.values()) sum += v
    if (sum <= 0) continue
    out.push({ hour: hourEnding - 1, plannedKw: sum })
  }
  out.sort((a, b) => a.hour - b.hour)
  return out
}
