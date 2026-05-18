package api

import (
	"time"

	"github.com/nesh/sestelemetry/internal/energyflow"
)

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

// WeatherForecastHour is one hour of cached Open-Meteo forecast data
// for an organization. Numeric fields are pointers so missing values
// (e.g. radiation before sunrise on the first day of the model window)
// survive the JSON round-trip as nulls rather than being silently
// replaced with zero.
type WeatherForecastHour struct {
	Hour           time.Time `json:"hour"`
	Temperature2mC *float64  `json:"temperature_2m_c,omitempty"`
	CloudCoverPct  *float64  `json:"cloud_cover_pct,omitempty"`
	IsDay          *bool     `json:"is_day,omitempty"`
	ShortwaveWm2   *float64  `json:"shortwave_wm2,omitempty"`
	DirectWm2      *float64  `json:"direct_wm2,omitempty"`
	DiffuseWm2     *float64  `json:"diffuse_wm2,omitempty"`
	GtiInstantWm2  *float64  `json:"gti_instant_wm2,omitempty"`
	FetchedAt      time.Time `json:"fetched_at"`
}

// WeatherForecastDay is one daily summary cached for an organization.
type WeatherForecastDay struct {
	Day                   time.Time  `json:"day"`
	Sunrise               *time.Time `json:"sunrise,omitempty"`
	Sunset                *time.Time `json:"sunset,omitempty"`
	DaylightDurationS     *float64   `json:"daylight_duration_s,omitempty"`
	SunshineDurationS     *float64   `json:"sunshine_duration_s,omitempty"`
	ShortwaveRadiationSum *float64   `json:"shortwave_radiation_sum,omitempty"`
	FetchedAt             time.Time  `json:"fetched_at"`
}

// WeatherForecastResponse is the body of GET /api/v1/weather-forecast.
// `Hourly` and `Daily` are always non-nil arrays (empty when the
// collector hasn't populated this org / range yet) so clients can
// iterate without nil-checks. The dashboard treats an empty `Hourly`
// as "no cached forecast" and falls back to Open-Meteo directly.
type WeatherForecastResponse struct {
	OrganizationID string                `json:"organization_id"`
	From           time.Time             `json:"from"`
	To             time.Time             `json:"to"`
	Hourly         []WeatherForecastHour `json:"hourly"`
	Daily          []WeatherForecastDay  `json:"daily"`
}

// EnergySummaryAccumulators are the raw cumulative Modbus counters
// served by the EnergySummary store via `last(end) - last(seed)`.
// The four directional flow counters (pv_to_ess_kwh, grid_to_ess_kwh,
// ess_to_load_kwh, ess_to_grid_kwh) are NOT listed here — they are
// computed on the fly by the API handler and surface in the
// dedicated `flows` field of EnergySummaryResponse rather than in
// `totals`.
var EnergySummaryAccumulators = []string{
	"accumulated_pv_energy_yield_kwh",
	"accumulated_electricity_sold_kwh",
	"accumulated_electricity_purchased_kwh",
	"accumulated_power_consumption_kwh",
	"total_energy_charged_kwh",
	"total_energy_discharged_kwh",
}

// EnergyFlowTotals carries the four synthetic flow counters computed
// on the fly by the EnergySummary handler. The struct is the typed
// shape of the response's `flows` field — split out from the raw
// `totals` map so the dashboard can tell apart "we didn't run the
// allocator" (flows == nil) from "we ran it and the period has no
// usable data" (flows pointer with zero fields).
type EnergyFlowTotals struct {
	PVToESSKwh   float64 `json:"pv_to_ess_kwh"`
	GridToESSKwh float64 `json:"grid_to_ess_kwh"`
	ESSToLoadKwh float64 `json:"ess_to_load_kwh"`
	ESSToGridKwh float64 `json:"ess_to_grid_kwh"`
}

// EnergySummaryResponse returns the per-period accumulator deltas
// used by the dashboard's summary cards plus the optional
// directional flow totals. Each value in `Totals` is
// `last(value, time before to) - last(value, time before from)`,
// clamped to >= 0; counter rollbacks land as zero so the dashboard
// flags "no usable data" rather than inventing a number from
// corrupted samples. `Flows` is populated only when the caller
// requested at least one synthetic key and the window falls inside
// the on-the-fly compute budget (see maxEnergyFlowWindow).
type EnergySummaryResponse struct {
	OrganizationID string             `json:"organization_id"`
	From           time.Time          `json:"from"`
	To             time.Time          `json:"to"`
	Totals         map[string]float64 `json:"totals"`
	Flows          *EnergyFlowTotals  `json:"flows,omitempty"`
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

// EnergyFlowDevice describes one physical Modbus device's role
// assignment for the on-the-fly energy-flow compute. The API uses
// `Host` to match raw rows in telemetry_samples (whose `device_host`
// label was written by the collector's labelsForDevice) and `Role` to
// classify those rows into the energy-flow allocator's PV / ESS /
// single buckets. Empty Host means "no per-device label was written"
// — i.e. the legacy single-device YAML — and matches rows with no
// device_host label.
type EnergyFlowDevice struct {
	Host string
	Role string
}

// EnergyFlowOrg is the org-level metadata the on-the-fly summary
// needs beyond the public OrganizationInfo. It carries the
// per-device role mapping (so dual SmartLogger sites still merge PV
// and ESS rows correctly) and the ess_discharge_sign override that
// flips the sign convention on inverters reporting charge as
// positive. Populated by cmd/api/main.go from the same config the
// collector reads.
type EnergyFlowOrg struct {
	ID               string
	EssDischargeSign int
	Devices          []EnergyFlowDevice
}

// EnergyFlowRecomputeSourceMetrics is the set of source counter keys
// the on-the-fly compute reads from telemetry_samples to drive the
// allocation algorithm. Mirrors energyflow.{PVRequiredMetrics +
// ESSRequiredMetrics}; centralised here so the handler and the store
// both reference one list and the catalogue can extend in lockstep.
var EnergyFlowRecomputeSourceMetrics = []string{
	"accumulated_pv_energy_yield_kwh",
	"accumulated_electricity_purchased_kwh",
	"accumulated_electricity_sold_kwh",
	"total_energy_charged_kwh",
	"total_energy_discharged_kwh",
	"active_pv_power_kw",
	"active_ess_power_kw",
	"load_power_kw",
	"grid_connected_active_power_kw",
	"soc_percent",
}

// EnergyFlowSyntheticMetrics is the canonical list of synthetic flow
// metric_keys used by the API handler to detect when a caller has
// opted into the on-the-fly compute. Aliased to
// energyflow.SyntheticMetricKeys so there is a single source of
// truth across the api and energyflow packages.
var EnergyFlowSyntheticMetrics = energyflow.SyntheticMetricKeys

// EnergyFlowRawRow is one input row pulled from telemetry_samples by
// the recompute store helper. device_host is read from the labels
// JSONB and used by the handler to look up the device's role.
type EnergyFlowRawRow struct {
	Time       time.Time
	MetricKey  string
	Value      float64
	DeviceHost string
}

// EnergyFlowHourlyRow is one hour worth of synthetic flow totals
// produced by /api/v1/energy-flow-hourly. The handler runs the
// stateless `energyflow.Recompute()` over the hour's raw rows
// (same algorithm /api/v1/energy-summary uses for the daily
// totals), so the dashboard can compose 24 hourly economics
// rows that, summed back, exactly match the daily-card numbers.
//
// `From` and `To` are the absolute boundaries of the hour
// rendered in the request's timezone. `EssChargedKwh` and
// `EssDischargedKwh` are exposed because the dashboard needs
// them to derive `load[h]` via the energy-balance identity
// without spending another /timeseries call.
type EnergyFlowHourlyRow struct {
	Hour             int       `json:"hour"`
	From             time.Time `json:"from"`
	To               time.Time `json:"to"`
	PVToESSKwh       float64   `json:"pv_to_ess_kwh"`
	GridToESSKwh     float64   `json:"grid_to_ess_kwh"`
	ESSToLoadKwh     float64   `json:"ess_to_load_kwh"`
	ESSToGridKwh     float64   `json:"ess_to_grid_kwh"`
	EssChargedKwh    float64   `json:"ess_charged_kwh"`
	EssDischargedKwh float64   `json:"ess_discharged_kwh"`
	// SkippedIntervals counts how many sub-buckets within this
	// hour the allocator dropped (negative deltas, missing
	// accumulators, etc). > 0 means the four flow values for
	// this hour are partial — the dashboard uses this to
	// surface a per-hour "incomplete data" hint.
	SkippedIntervals int      `json:"skipped_intervals"`
	Warnings         []string `json:"warnings,omitempty"`
}

// EnergyFlowHourlyResponse is the body of GET /api/v1/energy-flow-hourly.
// `Hours` is always 24 entries (one per hour-of-day in the
// requested timezone). Hours with no underlying rows return
// zero flow values rather than being absent from the array
// so the dashboard can iterate without sparse-index handling.
type EnergyFlowHourlyResponse struct {
	OrganizationID string                `json:"organization_id"`
	Date           string                `json:"date"`
	Tz             string                `json:"tz"`
	Hours          []EnergyFlowHourlyRow `json:"hours"`
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
