import type { MetricKey } from './metrics'
import type { RangePreset } from './range'

const FALLBACK_COLOR = '#8b5cf6'

const DAY_COLORS: Partial<Record<MetricKey, string>> = {
  accumulated_electricity_purchased_kwh: '#9ca3af',
  total_energy_discharged_kwh: '#2563eb',
  accumulated_pv_energy_yield_kwh: '#22c55e',
  accumulated_electricity_sold_kwh: '#f97316',
  total_energy_charged_kwh: '#2563eb',
  accumulated_power_consumption_kwh: '#f59e0b',
}

const PERIOD_COLORS: Partial<Record<MetricKey, string>> = {
  accumulated_electricity_purchased_kwh: '#16a34a',
  total_energy_discharged_kwh: '#4ade80',
  accumulated_pv_energy_yield_kwh: '#86efac',
  accumulated_electricity_sold_kwh: '#f97316',
  total_energy_charged_kwh: '#fb923c',
  accumulated_power_consumption_kwh: '#fdba74',
}

const ENERGY_COLORS: Record<RangePreset, Partial<Record<MetricKey, string>>> = {
  day: DAY_COLORS,
  month: PERIOD_COLORS,
  year: PERIOD_COLORS,
}

export function energyColor(metricKey: string, preset: RangePreset): string {
  return ENERGY_COLORS[preset][metricKey as MetricKey] ?? FALLBACK_COLOR
}

// Day-preset palette for the instantaneous-power areas (kW snapshots). Mirrors
// the previous day energy palette so users carry visual continuity across
// chart redesigns: PV is green (matches accumulated_pv_energy_yield_kwh), ESS
// is blue (matches charge/discharge series), grid is gray (matches
// purchased), load is amber (matches accumulated_power_consumption_kwh).
const DAY_POWER_COLORS: Partial<Record<MetricKey, string>> = {
  active_pv_power_kw: '#22c55e',
  active_ess_power_kw: '#2563eb',
  grid_connected_active_power_kw: '#9ca3af',
  load_power_kw: '#f59e0b',
}

export function dayPowerColor(metricKey: string): string {
  return DAY_POWER_COLORS[metricKey as MetricKey] ?? FALLBACK_COLOR
}

// Forecast bars use a darker green than active_pv_power_kw (#22c55e) so the
// planned-vs-actual comparison reads at a glance: the line on top of the
// bars is the actual instantaneous PV output, the bars below are the
// hourly forecast envelope.
export const PV_FORECAST_COLOR = '#16a34a'
