import type { PvForecastResponse } from '../../api'

// PvForecastHourlyRow describes one hour of the day's planned generation,
// summed across the station's panel orientations. `hour` is 0..23 and is
// the hour start in local Kyiv time (so `hour=0` covers 00:00–01:00).
// `plannedKw` is the average AC power in kW for that hour — equal to the
// hour's planned kWh since each interval is exactly 1 hour, so it can be
// plotted directly on the day chart's kW axis.
export type PvForecastHourlyRow = {
  hour: number
  plannedKw: number
}

export const PV_FORECAST_WHOLE_ELEVATOR_LABEL = 'весь елеватор'
export const PV_FORECAST_WHOLE_ELEVATOR_NOTE =
  'Прогноз СЕС — на весь елеватор (Т1 і Т2 разом): окремого прогнозу для цього вводу ще немає, тож план завищений.'

// pvForecastLabel is the forecast series name, marked when the
// forecast is the whole elevator's.
export function pvForecastLabel(wholeElevator: boolean): string {
  return wholeElevator ? `Прогноз СЕС (${PV_FORECAST_WHOLE_ELEVATOR_LABEL})` : 'Прогноз СЕС'
}

// pvPlanCaption labels a period plan line the same way.
export function pvPlanCaption(wholeElevator = false): string {
  return wholeElevator ? `План (${PV_FORECAST_WHOLE_ELEVATOR_LABEL})` : 'План'
}

function pad(n: number): string {
  return n < 10 ? `0${n}` : String(n)
}

// forecastDayFromAnchor renders the anchor date as YYYY-MM-DD in the
// browser's local TZ. For Ukrainian users this is Kyiv local, which is
// the day the plan's hours are counted in. For users in other zones the
// day boundary may be off by ±1, but that's the same tradeoff the rest
// of the dashboard makes (see `toDateOnly` in `useDashboardData`).
export function forecastDayFromAnchor(anchor: Date): string {
  return `${anchor.getFullYear()}-${pad(anchor.getMonth() + 1)}-${pad(anchor.getDate())}`
}

// pvForecastRows turns the API's hours into chart rows, ordered by
// hour, dropping hours that are dark or not a usable number.
export function pvForecastRows(
  hours: PvForecastResponse['hours'] | null | undefined,
): PvForecastHourlyRow[] {
  const out: PvForecastHourlyRow[] = []
  for (const h of hours ?? []) {
    const hour = Number(h?.hour)
    const kwh = Number(h?.planned_kwh)
    if (!Number.isInteger(hour) || hour < 0 || hour > 23) continue
    if (!Number.isFinite(kwh) || kwh <= 0) continue
    out.push({ hour, plannedKw: kwh })
  }
  out.sort((a, b) => a.hour - b.hour)
  return out
}
