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
		{Key: "load_power_kw", Label: "Load Power", Unit: "kW"},
		{Key: "active_pv_power_kw", Label: "Active PV Power", Unit: "kW"},
		{Key: "active_ess_power_kw", Label: "Active ESS Power", Unit: "kW"},
		{Key: "grid_connected_active_power_kw", Label: "Grid Active Power", Unit: "kW"},
		{Key: "soc_percent", Label: "SOC", Unit: "%"},
		{Key: "total_energy_charged_kwh", Label: "Total Energy Charged", Unit: "kWh"},
		{Key: "total_energy_discharged_kwh", Label: "Total Energy Discharged", Unit: "kWh"},
		{Key: "accumulated_pv_energy_yield_kwh", Label: "Inverter energy yield of current day", Unit: "kWh"},
		{Key: "accumulated_electricity_purchased_kwh", Label: "Current Day Supply From Grid", Unit: "kWh"},
		{Key: "accumulated_electricity_sold_kwh", Label: "Accumulated Grid Export", Unit: "kWh"},
		{Key: "accumulated_power_consumption_kwh", Label: "Current Day Consumption", Unit: "kWh"},
		{Key: "total_power_supply_from_grid_kwh", Label: "Total Supply From Grid", Unit: "kWh"},
	},
	PowerChart: []DashboardMetric{
		{Key: "active_pv_power_kw", Label: "PV Power", Unit: "kW"},
		{Key: "load_power_kw", Label: "Load Power", Unit: "kW"},
		{Key: "grid_connected_active_power_kw", Label: "Grid Active Power", Unit: "kW"},
	},
	EnergyChart: []DashboardMetric{
		{Key: "pv_energy_yield_day_kwh", Label: "PV Daily Yield", Unit: "kWh"},
		{Key: "total_energy_charged_kwh", Label: "Energy Charged", Unit: "kWh"},
		{Key: "total_energy_discharged_kwh", Label: "Energy Discharged", Unit: "kWh"},
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
