import type { DashboardConfig } from '../types'

export const KNOWN_ORGANIZATIONS = ['demo-org', 'pe', 'ze']

export const DASHBOARD_REFRESH_MS = 1000

// Earliest local-time instant whose cumulative-counter readings are
// considered reliable. Energy Summary computes period totals as
// `end - seed` over /current?at=... lookups; on periods that include
// dates before this floor the seed query returns the lifetime counter
// from a backfilled / faulty pre-deployment sample, which inflates the
// totals to nonsense (e.g. April 2026 once showed ~65 MWh consumption
// against ~20 MWh production). Both seed and end timestamps are clamped
// to be at-or-after this instant; if the whole period sits before it,
// totals are returned as zero.
export const MIN_RELIABLE_DATA_AT = new Date(2026, 3, 30)

export const FALLBACK_DASHBOARD_CONFIG: DashboardConfig = {
  cards: [
    { key: 'pv_energy_yield_day_kwh', label: 'Виробіток СЕС за день (PV energy yield of the day)', unit: 'kWh' },
    { key: 'power_supply_from_grid_day_kwh', label: 'Постачання з мережі за день (Power supply from grid today)', unit: 'kWh' },
    { key: 'energy_charged_day_kwh', label: 'Заряд УЗЕ за день (Current-day charge capacity)', unit: 'kWh' },
    { key: 'energy_discharged_day_kwh', label: 'Розряд УЗЕ за день (Energy discharged today)', unit: 'kWh' },
    { key: 'power_consumption_day_kwh', label: 'Споживання елеватора за день (Current day power consumption)', unit: 'kWh' },
    { key: 'electricity_sold_day_kwh', label: 'Експорт в мережу за день (Electricity sales volume of the day)', unit: 'kWh' },
    { key: 'electricity_purchased_day_kwh', label: 'Імпорт з мережі за день (Electricity purchased on the current day)', unit: 'kWh' },
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
