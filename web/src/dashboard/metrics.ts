export const ALL_METRIC_KEYS = [
  'pv_energy_yield_day_kwh',
  'total_energy_charged_kwh',
  'total_energy_discharged_kwh',
  'load_power_kw',
  'active_pv_power_kw',
  'active_ess_power_kw',
  'grid_connected_active_power_kw',
  'soc_percent',
  'accumulated_pv_energy_yield_kwh',
  'accumulated_electricity_purchased_kwh',
  'accumulated_electricity_sold_kwh',
  'accumulated_power_consumption_kwh',
  'total_power_supply_from_grid_kwh',
] as const

export type MetricKey = (typeof ALL_METRIC_KEYS)[number]

export const APPLIANCE_CONSUMPTION_METRIC: MetricKey = 'accumulated_power_consumption_kwh'

export const PERIOD_ENERGY_METRIC_KEYS = new Set<MetricKey>([
  'total_energy_charged_kwh',
  'total_energy_discharged_kwh',
])

export const DAY_ENERGY_METRIC_KEYS = new Set<MetricKey>([
  'accumulated_pv_energy_yield_kwh',
  'accumulated_power_consumption_kwh',
  'accumulated_electricity_purchased_kwh',
])

export const DAY_ENERGY_METRIC_KEYS_LIST: MetricKey[] = Array.from(DAY_ENERGY_METRIC_KEYS)

export const SOURCE_ENERGY_METRIC_KEYS: MetricKey[] = [
  'accumulated_electricity_purchased_kwh',
  'total_energy_discharged_kwh',
  'accumulated_pv_energy_yield_kwh',
]

export const SINK_ENERGY_METRIC_KEYS: MetricKey[] = [
  'accumulated_electricity_sold_kwh',
  'total_energy_charged_kwh',
  'accumulated_power_consumption_kwh',
]

export const ENERGY_TREND_METRIC_DIRECTIONS: Partial<Record<MetricKey, 1 | -1>> = {
  accumulated_electricity_purchased_kwh: 1,
  total_energy_discharged_kwh: 1,
  accumulated_pv_energy_yield_kwh: 1,
  accumulated_electricity_sold_kwh: -1,
  total_energy_charged_kwh: -1,
  accumulated_power_consumption_kwh: -1,
}

// Day-preset power lines (instantaneous kW snapshots, aggregation=last). The
// list is intentionally local to the frontend so it can evolve independently
// of the bigger DashboardConfig.PowerChart shipped from the backend.
export const DAY_POWER_METRIC_KEYS: MetricKey[] = [
  'active_ess_power_kw',
  'grid_connected_active_power_kw',
  'load_power_kw',
]

export const DAY_POWER_METRIC_LABELS: Partial<Record<MetricKey, string>> = {
  active_ess_power_kw: 'Потужність УЗЕ (заряд/розряд)',
  grid_connected_active_power_kw: 'Потужність у точці приєднання',
  load_power_kw: 'Потужність навантаження',
}
