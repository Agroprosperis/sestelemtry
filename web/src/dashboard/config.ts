import type { DashboardConfig } from '../types'

export const KNOWN_ORGANIZATIONS = ['demo-org', 'pe', 'ze', 'ab', 'ke', 'pde', 'de', 'sm']

// ORGANIZATION_DISPLAY_NAMES maps an organization id to a
// human-readable Ukrainian site name shown next to selectors,
// header subtitles, and KPI labels. The backend's
// /api/v1/organizations response already carries a `name` field
// pulled from the deployment YAML, so this map is purely a
// frontend fallback for instances where the server response
// hasn't been refreshed yet (or the operator wants a different
// label inside the dashboard than the one logged in metrics).
//
// Keep this list short — anything that needs richer per-org
// metadata should live in the backend config so it survives a
// frontend redeploy.
export const ORGANIZATION_DISPLAY_NAMES: Record<string, string> = {
  pe: 'Радивилівський елеватор',
  ze: 'Жмеринський елеватор',
  ab: 'Агродар Бар',
  ke: 'Кролевецький елеватор',
  pde: 'Поділля елеватор',
  de: 'Дубовязівський елеватор',
  sm: 'Сорочанський мірошник',
}

// formatOrganizationLabel returns the user-facing name for an
// organization id. Prefers the explicit display map, falls back
// to the bare id so unknown / new orgs still render usefully.
export function formatOrganizationLabel(id: string): string {
  return ORGANIZATION_DISPLAY_NAMES[id] ?? id
}

export const DASHBOARD_REFRESH_MS = 1000

// Background re-fetch cadence for charts, summary cards, and period
// flow numbers. We deliberately poll these less often than /current
// (cards) because each tick triggers /timeseries (multiple metric
// keys × full period) and /energy-summary on the backend; once a
// minute is sufficient to keep the dashboard "live enough" without
// piling load on the API. The previous behaviour fetched these only
// on mount / preset change, which left an open browser tab showing
// midnight numbers until the operator manually reloaded.
export const DASHBOARD_CHART_REFRESH_MS = 60_000

// Fallback floor for a site not listed in ORGANIZATION_OPERATION_START.
// Energy Summary computes period totals as `end - seed` over
// /current?at=... lookups; reach back past the day a site was
// commissioned and the seed becomes a lifetime counter from a
// pre-deployment test reading, which inflates the totals to nonsense
// (e.g. April 2026 once showed ~65 MWh consumption against ~20 MWh
// production). This date predates no real site, so an unlisted org
// keeps the conservative behaviour it had before per-site floors
// existed: nothing before it is charted or totalled.
export const MIN_RELIABLE_DATA_AT = new Date(2026, 3, 30)

// ORGANIZATION_OPERATION_START is the day each site was commissioned —
// as far back as the FusionSolar archive import reaches, and therefore
// how far back the dashboard may chart and total. Months of imported
// archive stay invisible for any site whose entry is missing or too
// late, which is what MIN_RELIABLE_DATA_AT does to everyone by default.
//
// Local-time constructor on purpose: `new Date('2025-06-20')` is UTC
// midnight, which east of Greenwich is still 19 June locally and would
// let one pre-commissioning evening back into the first bucket.
//
// `scripts/data_gaps.sql` reports coverage against the same dates —
// keep the two in step when onboarding a site.
export const ORGANIZATION_OPERATION_START: Record<string, Date> = {
  ab: new Date(2025, 5, 20),
  ze: new Date(2025, 0, 29),
  ke: new Date(2025, 5, 30),
  pe: new Date(2026, 2, 30),
  pde: new Date(2025, 11, 24),
  sm: new Date(2026, 2, 30),
  de: new Date(2026, 3, 14),
}

// energyFloorFor returns the earliest local instant the dashboard may
// chart or total for one organization.
export function energyFloorFor(organizationID: string): Date {
  return ORGANIZATION_OPERATION_START[organizationID] ?? MIN_RELIABLE_DATA_AT
}

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
