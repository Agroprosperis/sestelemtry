package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type ModbusRegisterMap string

const (
	MapHolding ModbusRegisterMap = "holding"
	MapInput   ModbusRegisterMap = "input"
)

type Modbus struct {
	Host                string        `yaml:"host"`
	Port                int           `yaml:"port"`
	UnitID              int           `yaml:"unit_id"`
	ConnectTimeout      time.Duration `yaml:"connect_timeout"`
	RequestTimeout      time.Duration `yaml:"request_timeout"`
	ReconnectBackoff    bool          `yaml:"reconnect_backoff"`
	MaxReconnectBackoff time.Duration `yaml:"max_reconnect_backoff"`
}

// ModbusDevice describes one Modbus endpoint that belongs to an
// organization. MetricKeys is an optional whitelist that scopes which
// catalog entries this physical device is responsible for; when empty,
// the device polls the full register catalog.
type ModbusDevice struct {
	Modbus     `yaml:",inline"`
	MetricKeys []string `yaml:"metric_keys"`
}

// Location is the geographic site of an organization. Consumed by the
// API server to expose coordinates to clients (e.g. the dashboard's
// weather widget). Optional: organizations without a `location` block
// are returned with `Location == nil` and any UI feature that needs a
// position simply hides itself.
type Location struct {
	Latitude  float64 `yaml:"latitude"`
	Longitude float64 `yaml:"longitude"`
	// City is a short human-readable label (e.g. "Жмеринка") shown
	// alongside the data. Optional; empty values are passed through
	// to the client unchanged.
	City string `yaml:"city"`
}

// InventoryExpected holds the site-config passport values used to
// compare against SmartLogger plant-inventory Modbus reads. Pointer
// fields are optional: only non-nil values are checked for
// INVENTORY_MISMATCH. Leave the whole block unset to skip comparison.
type InventoryExpected struct {
	PVRatedKw   *float64 `yaml:"pv_rated_kw"`
	ESSRatedKw  *float64 `yaml:"ess_rated_kw"`
	ESSRatedKwh *float64 `yaml:"ess_rated_kwh"`
	ESSCount    *float64 `yaml:"ess_count"`
}

type Organization struct {
	ID            string         `yaml:"id"`
	Name          string         `yaml:"name"`
	SiteID        string         `yaml:"site_id"`
	DeviceID      string         `yaml:"device_id"`
	Location      *Location      `yaml:"location,omitempty"`
	PollInterval  time.Duration  `yaml:"poll_interval"`
	Modbus        Modbus         `yaml:"modbus"`
	ModbusDevices []ModbusDevice `yaml:"modbus_devices"`

	// Inventory is the expected plant passport (rated PV/ESS, cabinet
	// count) used by the rare inventory poll for mismatch alerts.
	Inventory *InventoryExpected `yaml:"inventory,omitempty"`

	// EssDischargeSign overrides the convention that
	// `active_ess_power_kw > 0` means "ESS discharging". Set to -1
	// for inverters that report charge as positive ESS power.
	// Allowed values: 0 (= default 1), 1, -1. The default never
	// rejects a sample; it only flips the energy-flow allocator's
	// instantaneous-power sign sanity check, since the cumulative
	// energy_charged / energy_discharged accumulators are sign-
	// invariant.
	EssDischargeSign int `yaml:"ess_discharge_sign"`

	// EssMaxPowerKw is the physical charge/discharge power ceiling of
	// the ESS in kW. When > 0, the energy-flow allocator rejects any
	// interval whose implied average ESS power exceeds this bound — a
	// guard against cumulative-counter steps (device re-base, firmware
	// resync, corrupted high reading) that would otherwise dump a huge
	// fake charge/discharge into a single hour. 0 (default) disables the
	// guard. Set a value slightly above the ESS rated power.
	EssMaxPowerKw float64 `yaml:"ess_max_power_kw"`
}

// Devices returns the effective list of Modbus endpoints for this
// organization. Orgs with the legacy single `modbus:` block are wrapped
// into a one-element slice so the collector can treat all configs
// uniformly. The returned slice is never empty when validation passed.
func (o *Organization) Devices() []ModbusDevice {
	if len(o.ModbusDevices) > 0 {
		return o.ModbusDevices
	}
	return []ModbusDevice{{Modbus: o.Modbus}}
}

type RegisterAddressing struct {
	HoldingAddressBase int `yaml:"holding_address_base"`
}

// OREERetry tunes how many times the dam-collector retries an OREE download.
type OREERetry struct {
	Attempts int           `yaml:"attempts"`
	Backoff  time.Duration `yaml:"backoff"`
}

// OREE configures the optional dam-collector service that ingests Day-Ahead
// Market prices from https://www.oree.com.ua/ once per day.
type OREE struct {
	Enabled            bool   `yaml:"enabled"`
	BaseURL            string `yaml:"base_url"`
	Zone               int    `yaml:"zone"`
	RunAt              string `yaml:"run_at"`
	Timezone           string `yaml:"timezone"`
	DeliveryOffsetDays int    `yaml:"delivery_offset_days"`
	// BackfillDays asks the collector to also re-check this many recent
	// past delivery dates on startup and after each daily run, fetching
	// any day that has fewer than 24 stored hours. This self-heals days
	// the scheduled run missed (failed OREE publication window, daemon
	// downtime) without operator intervention. Unset defaults to 7; a
	// value <= 0 disables backfill.
	BackfillDays int           `yaml:"backfill_days"`
	HTTPTimeout  time.Duration `yaml:"http_timeout"`
	UserAgent    string        `yaml:"user_agent"`
	Retry        OREERetry     `yaml:"retry"`
}

// Weather configures the optional weather-collector service that
// caches Open-Meteo forecasts per organization in TimescaleDB so the
// API can serve them without hitting Open-Meteo on every request and
// future backend models (economic, PV) have a stable source of truth.
//
// The collector polls every `Interval` and upserts (organization, hour)
// rows. Elapsed hours/days are frozen (never overwritten), so past days
// keep the forecast as it stood; `PastDays` re-pulls recent history to
// backfill gaps without clobbering frozen rows.
type Weather struct {
	Enabled     bool          `yaml:"enabled"`
	BaseURL     string        `yaml:"base_url"`
	Interval    time.Duration `yaml:"interval"`
	HTTPTimeout time.Duration `yaml:"http_timeout"`
	UserAgent   string        `yaml:"user_agent"`
	Retry       OREERetry     `yaml:"retry"`
	// PastDays asks Open-Meteo for this many days of recent past data
	// in addition to today + the forecast horizon. It lets the
	// collector backfill freeze-on-past rows for days/orgs it missed
	// (e.g. a newly added org or a window where the collector was down)
	// instead of leaving a permanent gap that the dashboard can never
	// fill — the direct Open-Meteo fallback has no access to the past.
	PastDays int `yaml:"past_days"`
}

// Economics configures the optional economics-recompute service that
// recomputes and persists hourly/daily economics on a schedule, so the
// dashboard always reads a warm cache and the heavy compute runs
// unattended instead of being triggered by hand from the browser.
//
// All inputs are read from the local database (telemetry samples, DAM
// prices, tariffs, and any already-imported canonical KPIs) — the
// service never calls FusionSolar or any other external API.
//
// Behaviour: nightly at RunAt it recomputes the last FinalizeDays days
// (which are now final, so the dashboard serves them from cache
// forever); every TodayInterval it recomputes the current, still-open
// day so the dashboard's "today" stays fresh without a live recompute
// on each read.
type Economics struct {
	Enabled        bool          `yaml:"enabled"`
	RunAt          string        `yaml:"run_at"`
	Timezone       string        `yaml:"timezone"`
	FinalizeDays   int           `yaml:"finalize_days"`
	TodayInterval  time.Duration `yaml:"today_interval"`
	MaxConcurrency int           `yaml:"max_concurrency"`
}

type Root struct {
	DatabaseURL        string             `yaml:"database_url"`
	RegisterCatalog    string             `yaml:"register_catalog"`
	RegisterAddressing RegisterAddressing `yaml:"register_addressing"`
	ModbusRegisterMap  ModbusRegisterMap  `yaml:"modbus_register_map"`
	Organizations      []Organization     `yaml:"organizations"`
	OREE               OREE               `yaml:"oree"`
	Weather            Weather            `yaml:"weather"`
	Economics          Economics          `yaml:"economics"`
}

// Load reads YAML config from path and applies defaults.
func Load(path string) (*Root, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Root
	if err := yaml.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	if err := c.validate(); err != nil {
		return nil, err
	}
	if c.ModbusRegisterMap == "" {
		c.ModbusRegisterMap = MapHolding
	}
	for i := range c.Organizations {
		o := &c.Organizations[i]
		if o.PollInterval <= 0 {
			o.PollInterval = 15 * time.Second
		}
		if len(o.ModbusDevices) > 0 {
			for j := range o.ModbusDevices {
				if err := applyModbusDefaults(&o.ModbusDevices[j].Modbus); err != nil {
					return nil, fmt.Errorf("config: org %q modbus_devices[%d]: %w", o.ID, j, err)
				}
			}
		} else {
			if err := applyModbusDefaults(&o.Modbus); err != nil {
				return nil, fmt.Errorf("config: org %q: %w", o.ID, err)
			}
		}
	}
	// Defaults are always applied (harmless and keep the structs sane for
	// any code that reads them), but strict validation only runs when the
	// service is enabled. A modbus-only deployment shouldn't fail to load
	// just because it carries a stale/invalid oree: or weather: block.
	c.applyOREEDefaults()
	if c.OREE.Enabled {
		if err := c.validateOREE(); err != nil {
			return nil, err
		}
	}
	c.applyWeatherDefaults()
	if c.Weather.Enabled {
		if err := c.validateWeather(); err != nil {
			return nil, err
		}
	}
	c.applyEconomicsDefaults()
	if c.Economics.Enabled {
		if err := c.validateEconomics(); err != nil {
			return nil, err
		}
	}
	return &c, nil
}

// applyModbusDefaults fills in port/unit_id/timeouts on a Modbus block
// in place. It also rejects unit_id values outside the protocol's 0..255
// range; everything else is normalized to a sensible default.
func applyModbusDefaults(m *Modbus) error {
	if m.Port == 0 {
		m.Port = 502
	}
	if m.UnitID == 0 {
		m.UnitID = 99
	}
	if m.UnitID < 0 || m.UnitID > 255 {
		return fmt.Errorf("unit_id out of range: %d", m.UnitID)
	}
	if m.ConnectTimeout <= 0 {
		m.ConnectTimeout = 5 * time.Second
	}
	if m.RequestTimeout <= 0 {
		m.RequestTimeout = 5 * time.Second
	}
	if m.ReconnectBackoff && m.MaxReconnectBackoff <= 0 {
		m.MaxReconnectBackoff = 2 * time.Minute
	}
	return nil
}

func (c *Root) applyOREEDefaults() {
	o := &c.OREE
	if o.BaseURL == "" {
		o.BaseURL = "https://www.oree.com.ua"
	}
	if o.Zone == 0 {
		o.Zone = 2
	}
	if strings.TrimSpace(o.RunAt) == "" {
		o.RunAt = "15:30"
	}
	if strings.TrimSpace(o.Timezone) == "" {
		o.Timezone = "Europe/Kyiv"
	}
	if o.BackfillDays == 0 {
		o.BackfillDays = 7
	}
	if o.HTTPTimeout <= 0 {
		o.HTTPTimeout = 30 * time.Second
	}
	if o.UserAgent == "" {
		o.UserAgent = "sestelemetry-dam/1.0"
	}
	if o.Retry.Attempts <= 0 {
		o.Retry.Attempts = 5
	}
	if o.Retry.Backoff <= 0 {
		o.Retry.Backoff = 5 * time.Minute
	}
}

func (c *Root) applyWeatherDefaults() {
	w := &c.Weather
	if w.BaseURL == "" {
		w.BaseURL = "https://api.open-meteo.com/v1/forecast"
	}
	if w.Interval <= 0 {
		w.Interval = time.Hour
	}
	if w.HTTPTimeout <= 0 {
		w.HTTPTimeout = 30 * time.Second
	}
	if w.UserAgent == "" {
		w.UserAgent = "sestelemetry-weather/1.0"
	}
	if w.Retry.Attempts <= 0 {
		w.Retry.Attempts = 3
	}
	if w.Retry.Backoff <= 0 {
		w.Retry.Backoff = 5 * time.Second
	}
	if w.PastDays <= 0 {
		w.PastDays = 7
	}
}

func (c *Root) applyEconomicsDefaults() {
	e := &c.Economics
	if strings.TrimSpace(e.RunAt) == "" {
		e.RunAt = "03:00"
	}
	if strings.TrimSpace(e.Timezone) == "" {
		e.Timezone = "Europe/Kyiv"
	}
	if e.FinalizeDays <= 0 {
		e.FinalizeDays = 3
	}
	if e.TodayInterval <= 0 {
		e.TodayInterval = time.Hour
	}
	if e.MaxConcurrency <= 0 {
		e.MaxConcurrency = 2
	}
}

func (c *Root) validateEconomics() error {
	e := &c.Economics
	if _, _, err := ParseRunAt(e.RunAt); err != nil {
		return fmt.Errorf("config: economics.run_at: %w", err)
	}
	if _, err := time.LoadLocation(e.Timezone); err != nil {
		return fmt.Errorf("config: economics.timezone: %w", err)
	}
	if e.FinalizeDays < 1 || e.FinalizeDays > 90 {
		return fmt.Errorf("config: economics.finalize_days must be in [1..90], got %d", e.FinalizeDays)
	}
	if e.TodayInterval < 5*time.Minute {
		return fmt.Errorf("config: economics.today_interval must be >= 5m, got %s", e.TodayInterval)
	}
	if e.TodayInterval > 24*time.Hour {
		return fmt.Errorf("config: economics.today_interval must be <= 24h, got %s", e.TodayInterval)
	}
	if e.MaxConcurrency < 1 || e.MaxConcurrency > 16 {
		return fmt.Errorf("config: economics.max_concurrency must be in [1..16], got %d", e.MaxConcurrency)
	}
	return nil
}

func (c *Root) validateWeather() error {
	w := &c.Weather
	if w.Interval < time.Minute {
		return fmt.Errorf("config: weather.interval must be >= 1m, got %s", w.Interval)
	}
	if w.Interval > 24*time.Hour {
		return fmt.Errorf("config: weather.interval must be <= 24h, got %s", w.Interval)
	}
	// Open-Meteo caps past_days at 92 on the /v1/forecast endpoint.
	if w.PastDays > 92 {
		return fmt.Errorf("config: weather.past_days must be <= 92, got %d", w.PastDays)
	}
	return nil
}

func (c *Root) validateOREE() error {
	o := &c.OREE
	if o.Zone < 1 || o.Zone > 99 {
		return fmt.Errorf("config: oree.zone must be in [1..99]")
	}
	if _, _, err := ParseRunAt(o.RunAt); err != nil {
		return fmt.Errorf("config: oree.run_at: %w", err)
	}
	if _, err := time.LoadLocation(o.Timezone); err != nil {
		return fmt.Errorf("config: oree.timezone: %w", err)
	}
	if o.DeliveryOffsetDays < -30 || o.DeliveryOffsetDays > 7 {
		return fmt.Errorf("config: oree.delivery_offset_days must be in [-30..7]")
	}
	if o.BackfillDays > 60 {
		return fmt.Errorf("config: oree.backfill_days must be <= 60")
	}
	return nil
}

// ParseRunAt validates and splits an "HH:MM" daily-run-at expression.
func ParseRunAt(s string) (hour, minute int, err error) {
	parts := strings.Split(strings.TrimSpace(s), ":")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("expected HH:MM, got %q", s)
	}
	h, err1 := parseTwoDigit(parts[0])
	m, err2 := parseTwoDigit(parts[1])
	if err1 != nil || err2 != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, 0, fmt.Errorf("invalid HH:MM %q", s)
	}
	return h, m, nil
}

func parseTwoDigit(s string) (int, error) {
	s = strings.TrimSpace(s)
	if len(s) == 0 || len(s) > 2 {
		return 0, fmt.Errorf("not a 1-2 digit number")
	}
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("non-digit")
		}
		n = n*10 + int(r-'0')
	}
	return n, nil
}

func (c *Root) validate() error {
	c.DatabaseURL = strings.TrimSpace(c.DatabaseURL)
	// database_url may be set later from DATABASE_URL in the process entrypoint.
	c.RegisterCatalog = strings.TrimSpace(c.RegisterCatalog)
	switch c.ModbusRegisterMap {
	case "", MapHolding, MapInput:
	default:
		return fmt.Errorf("config: modbus_register_map must be holding or input")
	}
	seen := map[string]struct{}{}
	for _, o := range c.Organizations {
		id := strings.TrimSpace(o.ID)
		if id == "" {
			return fmt.Errorf("config: organization id is required")
		}
		if _, ok := seen[id]; ok {
			return fmt.Errorf("config: duplicate organization id %q", id)
		}
		seen[id] = struct{}{}
		hasLegacy := strings.TrimSpace(o.Modbus.Host) != ""
		hasDevices := len(o.ModbusDevices) > 0
		if !hasLegacy && !hasDevices {
			return fmt.Errorf("config: org %q requires modbus.host or modbus_devices", id)
		}
		if hasLegacy && hasDevices {
			return fmt.Errorf("config: org %q cannot set both modbus and modbus_devices", id)
		}
		for j, d := range o.ModbusDevices {
			if strings.TrimSpace(d.Host) == "" {
				return fmt.Errorf("config: org %q modbus_devices[%d].host is required", id, j)
			}
		}
		if o.Location != nil {
			if o.Location.Latitude < -90 || o.Location.Latitude > 90 {
				return fmt.Errorf("config: org %q location.latitude out of range [-90,90]: %g", id, o.Location.Latitude)
			}
			if o.Location.Longitude < -180 || o.Location.Longitude > 180 {
				return fmt.Errorf("config: org %q location.longitude out of range [-180,180]: %g", id, o.Location.Longitude)
			}
		}
		switch o.EssDischargeSign {
		case 0, 1, -1:
		default:
			return fmt.Errorf("config: org %q ess_discharge_sign must be 1 or -1, got %d", id, o.EssDischargeSign)
		}
		if o.EssMaxPowerKw < 0 {
			return fmt.Errorf("config: org %q ess_max_power_kw must be >= 0, got %g", id, o.EssMaxPowerKw)
		}
	}
	return nil
}

// RequireModbus returns an error if the config lacks the fields the modbus
// collector requires. Other services (dam-collector, api) skip this check.
func (c *Root) RequireModbus() error {
	if c.RegisterCatalog == "" {
		return fmt.Errorf("config: register_catalog is required")
	}
	if len(c.Organizations) == 0 {
		return fmt.Errorf("config: at least one organization is required")
	}
	return nil
}
