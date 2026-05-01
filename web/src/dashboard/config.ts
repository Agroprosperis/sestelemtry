import type { DashboardConfig } from '../types'

export const KNOWN_ORGANIZATIONS = ['demo-org', 'pe']

export const DASHBOARD_REFRESH_MS = 1000

export const FALLBACK_DASHBOARD_CONFIG: DashboardConfig = {
  cards: [
    { key: 'pv_energy_yield_day_kwh', label: 'Виробіток за сьогодні (PV energy yield of the day)', unit: 'kWh' },
    { key: 'total_energy_charged_kwh', label: 'Енергія заряджання ESS — поточне значення лічильника (Total energy charged)', unit: 'kWh' },
    { key: 'total_energy_discharged_kwh', label: 'Енергія розряджання ESS — поточне значення лічильника (Total energy discharged)', unit: 'kWh' },
    { key: 'load_power_kw', label: 'Потужність навантаження (Load power)', unit: 'kW' },
    { key: 'active_pv_power_kw', label: 'Активна потужність PV (Active PV power)', unit: 'kW' },
    { key: 'active_ess_power_kw', label: 'Активна потужність ESS (Active ESS power)', unit: 'kW' },
    { key: 'grid_connected_active_power_kw', label: 'Активна потужність мережі (Grid-connected active power)', unit: 'kW' },
    { key: 'soc_percent', label: 'SOC', unit: '%' },
    { key: 'accumulated_pv_energy_yield_kwh', label: 'Виробіток інвертора — поточне значення лічильника (Accumulated PV energy yield)', unit: 'kWh' },
    { key: 'accumulated_electricity_purchased_kwh', label: 'Отримано з мережі — поточне значення лічильника (Accumulated electricity purchased)', unit: 'kWh' },
    { key: 'accumulated_electricity_sold_kwh', label: 'Подано в мережу — поточне значення лічильника (Accumulated electricity sold)', unit: 'kWh' },
    { key: 'accumulated_power_consumption_kwh', label: 'Споживання — поточне значення лічильника (Accumulated power consumption)', unit: 'kWh' },
    { key: 'total_power_supply_from_grid_kwh', label: 'Постачання з мережі — поточне значення лічильника (Total power supply from grid)', unit: 'kWh' },
  ],
  power_chart: [
    { key: 'active_pv_power_kw', label: 'Потужність PV (Active PV power)', unit: 'kW' },
    { key: 'load_power_kw', label: 'Потужність навантаження (Load power)', unit: 'kW' },
    { key: 'grid_connected_active_power_kw', label: 'Активна потужність мережі (Grid-connected active power)', unit: 'kW' },
  ],
  energy_chart: [
    { key: 'accumulated_electricity_purchased_kwh', label: 'З електромережі (Accumulated electricity purchased)', unit: 'kWh' },
    { key: 'total_energy_discharged_kwh', label: 'Розряджання системи накопичення енергії (Total energy discharged)', unit: 'kWh' },
    { key: 'accumulated_pv_energy_yield_kwh', label: 'Вироблено фотоелектричною установкою (Accumulated PV energy yield)', unit: 'kWh' },
    { key: 'accumulated_electricity_sold_kwh', label: 'Подано в електромережу (Accumulated electricity sold)', unit: 'kWh' },
    { key: 'total_energy_charged_kwh', label: 'Заряджання системи накопичення енергії (Total energy charged)', unit: 'kWh' },
    { key: 'accumulated_power_consumption_kwh', label: 'Споживання приладами (Accumulated power consumption)', unit: 'kWh' },
  ],
}
