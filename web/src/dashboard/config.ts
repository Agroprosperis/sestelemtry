import type { DashboardConfig } from '../types'

export const KNOWN_ORGANIZATIONS = ['demo-org', 'pe']

export const DASHBOARD_REFRESH_MS = 1000

export const FALLBACK_DASHBOARD_CONFIG: DashboardConfig = {
  cards: [
    { key: 'pv_energy_yield_day_kwh', label: 'Виробіток за сьогодні', unit: 'kWh' },
    { key: 'total_energy_charged_kwh', label: 'Енергія, отримана ESS сьогодні', unit: 'kWh' },
    { key: 'total_energy_discharged_kwh', label: 'Енергія, витрачена ESS сьогодні', unit: 'kWh' },
    { key: 'load_power_kw', label: 'Потужність навантаження', unit: 'kW' },
    { key: 'active_pv_power_kw', label: 'Активна потужність PV', unit: 'kW' },
    { key: 'active_ess_power_kw', label: 'Активна потужність ESS', unit: 'kW' },
    { key: 'grid_connected_active_power_kw', label: 'Активна потужність мережі', unit: 'kW' },
    { key: 'soc_percent', label: 'SOC', unit: '%' },
    { key: 'accumulated_pv_energy_yield_kwh', label: 'Виробіток інвертора за сьогодні', unit: 'kWh' },
    { key: 'accumulated_electricity_purchased_kwh', label: 'Отримано з мережі за сьогодні', unit: 'kWh' },
    { key: 'accumulated_electricity_sold_kwh', label: 'Подано в мережу (накопичувально)', unit: 'kWh' },
    { key: 'accumulated_power_consumption_kwh', label: 'Споживання за сьогодні', unit: 'kWh' },
    { key: 'total_power_supply_from_grid_kwh', label: 'Загальне постачання з мережі', unit: 'kWh' },
  ],
  power_chart: [
    { key: 'active_pv_power_kw', label: 'Потужність PV', unit: 'kW' },
    { key: 'load_power_kw', label: 'Потужність навантаження', unit: 'kW' },
    { key: 'grid_connected_active_power_kw', label: 'Активна потужність мережі', unit: 'kW' },
  ],
  energy_chart: [
    { key: 'accumulated_electricity_purchased_kwh', label: 'З електромережі', unit: 'kWh' },
    { key: 'total_energy_discharged_kwh', label: 'Розряджання системи накопичення енергії', unit: 'kWh' },
    { key: 'accumulated_pv_energy_yield_kwh', label: 'Вироблено фотоелектричною установкою', unit: 'kWh' },
    { key: 'accumulated_electricity_sold_kwh', label: 'Подано в електромережу', unit: 'kWh' },
    { key: 'total_energy_charged_kwh', label: 'Заряджання системи накопичення енергії', unit: 'kWh' },
    { key: 'accumulated_power_consumption_kwh', label: 'Споживання приладами', unit: 'kWh' },
  ],
}
