package api

import "time"

var DefaultDashboardMetrics = []string{
	"active_pv_power_kw",
	"active_ess_power_kw",
	"load_power_kw",
	"soc_percent",
	"grid_connected_active_power_kw",
	"pv_energy_yield_day_kwh",
	"total_energy_charged_kwh",
	"total_energy_discharged_kwh",
	"accumulated_pv_energy_yield_kwh",
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
		{Key: "pv_energy_yield_day_kwh", Label: "Виробіток за сьогодні", Unit: "kWh"},
		{Key: "total_energy_charged_kwh", Label: "Енергія, отримана ESS сьогодні", Unit: "kWh"},
		{Key: "total_energy_discharged_kwh", Label: "Енергія, витрачена ESS сьогодні", Unit: "kWh"},
		{Key: "load_power_kw", Label: "Потужність навантаження", Unit: "kW"},
		{Key: "active_pv_power_kw", Label: "Активна потужність PV", Unit: "kW"},
		{Key: "active_ess_power_kw", Label: "Активна потужність ESS", Unit: "kW"},
		{Key: "grid_connected_active_power_kw", Label: "Активна потужність мережі", Unit: "kW"},
		{Key: "soc_percent", Label: "SOC", Unit: "%"},
		{Key: "accumulated_pv_energy_yield_kwh", Label: "Виробіток інвертора за сьогодні", Unit: "kWh"},
		{Key: "accumulated_electricity_purchased_kwh", Label: "Отримано з мережі за сьогодні", Unit: "kWh"},
		{Key: "accumulated_electricity_sold_kwh", Label: "Подано в мережу (накопичувально)", Unit: "kWh"},
		{Key: "accumulated_power_consumption_kwh", Label: "Споживання за сьогодні", Unit: "kWh"},
		{Key: "total_power_supply_from_grid_kwh", Label: "Загальне постачання з мережі", Unit: "kWh"},
	},
	PowerChart: []DashboardMetric{
		{Key: "active_pv_power_kw", Label: "Потужність PV", Unit: "kW"},
		{Key: "load_power_kw", Label: "Потужність навантаження", Unit: "kW"},
		{Key: "grid_connected_active_power_kw", Label: "Активна потужність мережі", Unit: "kW"},
	},
	EnergyChart: []DashboardMetric{
		{Key: "pv_energy_yield_day_kwh", Label: "Виробіток за сьогодні", Unit: "kWh"},
		{Key: "total_energy_charged_kwh", Label: "Енергія, отримана ESS сьогодні", Unit: "kWh"},
		{Key: "total_energy_discharged_kwh", Label: "Енергія, витрачена ESS сьогодні", Unit: "kWh"},
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
