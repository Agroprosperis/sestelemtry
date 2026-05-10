package api

import "time"

var DefaultDashboardMetrics = []string{
	"active_pv_power_kw",
	"active_ess_power_kw",
	"load_power_kw",
	"soc_percent",
	"grid_connected_active_power_kw",
	"pv_energy_yield_day_kwh",
	"power_supply_from_grid_day_kwh",
	"energy_charged_day_kwh",
	"energy_discharged_day_kwh",
	"power_consumption_day_kwh",
	"electricity_sold_day_kwh",
	"electricity_purchased_day_kwh",
	"accumulated_pv_energy_yield_kwh",
	"total_energy_charged_kwh",
	"total_energy_discharged_kwh",
	"accumulated_electricity_purchased_kwh",
	"accumulated_electricity_sold_kwh",
	"accumulated_power_consumption_kwh",
	"total_power_supply_from_grid_kwh",
}

type DashboardMetric struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Unit  string `json:"unit"`
}

type DashboardConfig struct {
	Cards       []DashboardMetric `json:"cards"`
	PowerChart  []DashboardMetric `json:"power_chart"`
	EnergyChart []DashboardMetric `json:"energy_chart"`
}

var DefaultDashboardConfig = DashboardConfig{
	Cards: []DashboardMetric{
		{Key: "pv_energy_yield_day_kwh", Label: "Виробіток СЕС за день (PV energy yield of the day)", Unit: "kWh"},
		{Key: "power_supply_from_grid_day_kwh", Label: "Постачання з мережі за день (Power supply from grid today)", Unit: "kWh"},
		{Key: "energy_charged_day_kwh", Label: "Заряд УЗЕ за день (Current-day charge capacity)", Unit: "kWh"},
		{Key: "energy_discharged_day_kwh", Label: "Розряд УЗЕ за день (Energy discharged today)", Unit: "kWh"},
		{Key: "power_consumption_day_kwh", Label: "Споживання елеватора за день (Current day power consumption)", Unit: "kWh"},
		{Key: "electricity_sold_day_kwh", Label: "Експорт в мережу за день (Electricity sales volume of the day)", Unit: "kWh"},
		{Key: "electricity_purchased_day_kwh", Label: "Імпорт з мережі за день (Electricity purchased on the current day)", Unit: "kWh"},
		{Key: "total_energy_charged_kwh", Label: "Загальна енергія заряду УЗЕ (Total energy charged)", Unit: "kWh"},
		{Key: "total_energy_discharged_kwh", Label: "Загальна енергія розряду УЗЕ (Total energy discharged)", Unit: "kWh"},
		{Key: "load_power_kw", Label: "Потужність навантаження (Load power)", Unit: "kW"},
		{Key: "active_pv_power_kw", Label: "Активна потужність СЕС (Active PV power)", Unit: "kW"},
		{Key: "active_ess_power_kw", Label: "Активна потужність УЗЕ (Active ESS power)", Unit: "kW"},
		{Key: "grid_connected_active_power_kw", Label: "Активна потужність у точці приєднання до мережі (Grid-connected active power)", Unit: "kW"},
		{Key: "soc_percent", Label: "Рівень заряду (SOC)", Unit: "%"},
		{Key: "accumulated_pv_energy_yield_kwh", Label: "Накопичений виробіток СЕС (Accumulated PV energy yield)", Unit: "kWh"},
		{Key: "accumulated_electricity_purchased_kwh", Label: "Накопичене споживання з мережі (Accumulated electricity purchased)", Unit: "kWh"},
		{Key: "accumulated_electricity_sold_kwh", Label: "Накопичений відпуск у мережу (Accumulated electricity sold)", Unit: "kWh"},
		{Key: "accumulated_power_consumption_kwh", Label: "Накопичене споживання навантаження (Accumulated power consumption)", Unit: "kWh"},
		{Key: "total_power_supply_from_grid_kwh", Label: "Загальне постачання з мережі (Total power supply from grid)", Unit: "kWh"},
	},
	PowerChart: []DashboardMetric{
		{Key: "active_pv_power_kw", Label: "Активна потужність СЕС (Active PV power)", Unit: "kW"},
		{Key: "load_power_kw", Label: "Потужність навантаження (Load power)", Unit: "kW"},
		{Key: "grid_connected_active_power_kw", Label: "Активна потужність у точці приєднання до мережі (Grid-connected active power)", Unit: "kW"},
	},
	EnergyChart: []DashboardMetric{
		{Key: "accumulated_electricity_purchased_kwh", Label: "Накопичене споживання з мережі (Accumulated electricity purchased)", Unit: "kWh"},
		{Key: "total_energy_discharged_kwh", Label: "Загальна енергія розряду УЗЕ (Total energy discharged)", Unit: "kWh"},
		{Key: "accumulated_pv_energy_yield_kwh", Label: "Накопичений виробіток СЕС (Accumulated PV energy yield)", Unit: "kWh"},
		{Key: "accumulated_electricity_sold_kwh", Label: "Накопичений відпуск у мережу (Accumulated electricity sold)", Unit: "kWh"},
		{Key: "total_energy_charged_kwh", Label: "Загальна енергія заряду УЗЕ (Total energy charged)", Unit: "kWh"},
		{Key: "accumulated_power_consumption_kwh", Label: "Накопичене споживання навантаження (Accumulated power consumption)", Unit: "kWh"},
	},
}

type CurrentMetric struct {
	MetricKey string            `json:"metric_key"`
	Value     float64           `json:"value"`
	Time      time.Time         `json:"time"`
	Labels    map[string]string `json:"labels,omitempty"`
}

type CurrentResponse struct {
	OrganizationID string                   `json:"organization_id"`
	Metrics        map[string]CurrentMetric `json:"metrics"`
}

type TimeseriesPoint struct {
	Time      time.Time `json:"time"`
	MetricKey string    `json:"metric_key"`
	Value     float64   `json:"value"`
}

type TimeseriesResponse struct {
	OrganizationID string            `json:"organization_id"`
	MetricKeys     []string          `json:"metric_keys"`
	Bucket         string            `json:"bucket"`
	From           time.Time         `json:"from"`
	To             time.Time         `json:"to"`
	Points         []TimeseriesPoint `json:"points"`
}

// DAMPrice is one hourly Day-Ahead Market record exposed via the API.
// Numeric fields are pointers because the source XLS may omit cells.
type DAMPrice struct {
	DeliveryDate              time.Time `json:"delivery_date"`
	Hour                      int       `json:"hour"`
	Zone                      int       `json:"zone"`
	PriceUAHPerMWh            *float64  `json:"price_uah_per_mwh,omitempty"`
	SaleVolumeMWh             *float64  `json:"sale_volume_mwh,omitempty"`
	PurchaseVolumeMWh         *float64  `json:"purchase_volume_mwh,omitempty"`
	DeclaredSaleVolumeMWh     *float64  `json:"declared_sale_volume_mwh,omitempty"`
	DeclaredPurchaseVolumeMWh *float64  `json:"declared_purchase_volume_mwh,omitempty"`
}

type DAMPricesResponse struct {
	Zone   int        `json:"zone"`
	From   time.Time  `json:"from"`
	To     time.Time  `json:"to"`
	Prices []DAMPrice `json:"prices"`
}

// EnergySummaryAccumulators are the cumulative counters used by the
// monthly/yearly summary cards and by the energy-flow Sankey diagram.
// The first six entries back `energySummaryFromTotals` on the
// frontend; the four `*_to_*_kwh` entries are synthetic counters
// produced by the collector's energyflow package and consumed by
// `flowsFromTotals` in [web/src/dashboard/transforms/flows.ts]. They
// share the same `last(end) - last(seed)` lookup as the catalog
// metrics — synthetic counters are written cumulatively, so the
// existing summary SQL works for them without a schema change.
var EnergySummaryAccumulators = []string{
	"accumulated_pv_energy_yield_kwh",
	"accumulated_electricity_sold_kwh",
	"accumulated_electricity_purchased_kwh",
	"accumulated_power_consumption_kwh",
	"total_energy_charged_kwh",
	"total_energy_discharged_kwh",
	"pv_to_ess_kwh",
	"grid_to_ess_kwh",
	"ess_to_load_kwh",
	"ess_to_grid_kwh",
}

// EnergySummaryResponse returns the per-period accumulator deltas used
// by the dashboard's summary cards. Each value is `last(value, time
// before to) - last(value, time before from)`, clamped to >= 0. When
// the counter is genuinely accumulative this matches what an operator
// would expect ("show me how much X grew this month"); when an
// inverter glitch drops the register mid-period the result clamps to
// zero, which is the explicit signal we want — the dashboard refuses
// to invent numbers from corrupted data.
type EnergySummaryResponse struct {
	OrganizationID string             `json:"organization_id"`
	From           time.Time          `json:"from"`
	To             time.Time          `json:"to"`
	Totals         map[string]float64 `json:"totals"`
}

// LocationInfo is the geographic site of an organization, exposed via
// /api/v1/organizations. Mirrors `config.Location` minus its YAML tags.
// Empty `City` is preserved as an empty string (not omitted) so the
// JSON shape stays predictable for clients.
type LocationInfo struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	City      string  `json:"city"`
}

// OrganizationInfo is the public, dashboard-safe view of an
// organization. Modbus connection details are deliberately *not*
// included — the API only exposes information the UI needs to render
// (id, display name, optional location).
type OrganizationInfo struct {
	ID       string        `json:"id"`
	Name     string        `json:"name,omitempty"`
	Location *LocationInfo `json:"location,omitempty"`
}

// OrganizationsResponse is the body of GET /api/v1/organizations.
// Wrapped in an object (rather than a bare array) so we can add
// pagination / metadata fields later without breaking clients.
type OrganizationsResponse struct {
	Organizations []OrganizationInfo `json:"organizations"`
}

// SampleRow is one raw `telemetry_samples` record exposed by
// /api/v1/samples. The endpoint streams these rows as CSV so the
// dashboard's "Експорт даних → Сирі дані" path can hand the analyst
// per-poll values rather than the bucketed aggregates the regular
// timeseries endpoint produces.
//
// `Labels` is the deserialized JSONB column. Empty maps are emitted
// as nil so the CSV writer can render an empty cell instead of "{}",
// which matches what users expect when the inverter returns no extra
// dimensions for a sample.
type SampleRow struct {
	Time      time.Time
	MetricKey string
	Value     float64
	Labels    map[string]string
}
