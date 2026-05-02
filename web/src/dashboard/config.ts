import type { DashboardConfig } from '../types'

export const KNOWN_ORGANIZATIONS = ['demo-org', 'pe']

export const DASHBOARD_REFRESH_MS = 1000

export const FALLBACK_DASHBOARD_CONFIG: DashboardConfig = {
  cards: [
    { key: 'pv_energy_yield_day_kwh', label: 'Виробіток СЕС за день (PV energy yield of the day)', unit: 'kWh' },
    { key: 'total_energy_charged_kwh', label: 'Загальна енергія заряду УЗЕ (Total energy charged)', unit: 'kWh' },
    { key: 'total_energy_discharged_kwh', label: 'Загальна енергія розряду УЗЕ (Total energy discharged)', unit: 'kWh' },
    { key: 'load_power_kw', label: 'Потужність навантаження (Load power)', unit: 'kW' },
    { key: 'active_pv_power_kw', label: 'Активна потужність СЕС (Active PV power)', unit: 'kW' },
    { key: 'active_ess_power_kw', label: 'Активна потужність УЗЕ (Active ESS power)', unit: 'kW' },
    { key: 'grid_connected_active_power_kw', label: 'Активна потужність у точці приєднання до мережі (Grid-connected active power)', unit: 'kW' },
    { key: 'soc_percent', label: 'Рівень заряду (SOC)', unit: '%' },
    { key: 'accumulated_pv_energy_yield_kwh', label: 'Накопичений виробіток СЕС (Accumulated PV energy yield)', unit: 'kWh' },
    { key: 'accumulated_electricity_purchased_kwh', label: 'Накопичене споживання з мережі (Accumulated electricity purchased)', unit: 'kWh' },
    { key: 'accumulated_electricity_sold_kwh', label: 'Накопичений відпуск у мережу (Accumulated electricity sold)', unit: 'kWh' },
    { key: 'accumulated_power_consumption_kwh', label: 'Накопичене споживання навантаження (Accumulated power consumption)', unit: 'kWh' },
    { key: 'total_power_supply_from_grid_kwh', label: 'Загальне постачання з мережі (Total power supply from grid)', unit: 'kWh' },
  ],
  power_chart: [
    { key: 'active_pv_power_kw', label: 'Активна потужність СЕС (Active PV power)', unit: 'kW' },
    { key: 'load_power_kw', label: 'Потужність навантаження (Load power)', unit: 'kW' },
    { key: 'grid_connected_active_power_kw', label: 'Активна потужність у точці приєднання до мережі (Grid-connected active power)', unit: 'kW' },
  ],
  energy_chart: [
    { key: 'accumulated_electricity_purchased_kwh', label: 'Накопичене споживання з мережі (Accumulated electricity purchased)', unit: 'kWh' },
    { key: 'total_energy_discharged_kwh', label: 'Загальна енергія розряду УЗЕ (Total energy discharged)', unit: 'kWh' },
    { key: 'accumulated_pv_energy_yield_kwh', label: 'Накопичений виробіток СЕС (Accumulated PV energy yield)', unit: 'kWh' },
    { key: 'accumulated_electricity_sold_kwh', label: 'Накопичений відпуск у мережу (Accumulated electricity sold)', unit: 'kWh' },
    { key: 'total_energy_charged_kwh', label: 'Загальна енергія заряду УЗЕ (Total energy charged)', unit: 'kWh' },
    { key: 'accumulated_power_consumption_kwh', label: 'Накопичене споживання навантаження (Accumulated power consumption)', unit: 'kWh' },
  ],
}
