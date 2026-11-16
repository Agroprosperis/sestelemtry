import type { MetricKey } from './metrics'
import type { SupplyMode } from './transforms/supply'

const FALLBACK_COLOR = '#8b5cf6'

// Day-preset palette for the instantaneous-power areas (kW snapshots):
// PV is green, ESS blue, grid gray, load amber.
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

// The AI recommendation gets its own hue — green, blue, gray and amber are
// already spoken for by PV / ESS / grid / load, and the recommended ESS
// power sits right next to the blue actual-ESS line, so it has to be
// unmistakably a different series rather than a shade of the same one.
export const AI_PLAN_COLOR = '#db2777'

// The recommended consumption stays in the load family — a darker amber
// than the actual load line (#f59e0b), the same planned-vs-actual pairing
// the PV forecast uses with its darker green.
export const AI_PLAN_LOAD_COLOR = '#d97706'

// The generator is burnt orange, apart from the load's amber; the live
// diagram draws its flow in the same hue.
export const GEN_POWER_COLOR = '#c2410c'

// The supply modes: the grid and the generator keep their series colours,
// the island is the battery's blue. The day chart shades its hours
// without the grid in them.
export const SUPPLY_MODE_COLORS: Record<SupplyMode, string> = {
  grid: '#9ca3af',
  generator: GEN_POWER_COLOR,
  island: '#2563eb',
  blackout: '#dc2626',
  transition: '#a855f7',
  unknown: '#cbd5e1',
}
