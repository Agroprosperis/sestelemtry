package api

import "time"

var DefaultDashboardMetrics = []string{
	"active_pv_power_kw",
	"active_ess_power_kw",
	"load_power_kw",
	"soc_percent",
	"grid_connected_active_power_kw",
	"pv_energy_yield_day_kwh",
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
	DeliveryDate                time.Time `json:"delivery_date"`
	Hour                        int       `json:"hour"`
	Zone                        int       `json:"zone"`
	PriceUAHPerMWh              *float64  `json:"price_uah_per_mwh,omitempty"`
	SaleVolumeMWh               *float64  `json:"sale_volume_mwh,omitempty"`
	PurchaseVolumeMWh           *float64  `json:"purchase_volume_mwh,omitempty"`
	DeclaredSaleVolumeMWh       *float64  `json:"declared_sale_volume_mwh,omitempty"`
	DeclaredPurchaseVolumeMWh   *float64  `json:"declared_purchase_volume_mwh,omitempty"`
}

type DAMPricesResponse struct {
	Zone   int        `json:"zone"`
	From   time.Time  `json:"from"`
	To     time.Time  `json:"to"`
	Prices []DAMPrice `json:"prices"`
}
