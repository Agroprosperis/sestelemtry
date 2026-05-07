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

type Organization struct {
	ID            string         `yaml:"id"`
	Name          string         `yaml:"name"`
	SiteID        string         `yaml:"site_id"`
	DeviceID      string         `yaml:"device_id"`
	PollInterval  time.Duration  `yaml:"poll_interval"`
	Modbus        Modbus         `yaml:"modbus"`
	ModbusDevices []ModbusDevice `yaml:"modbus_devices"`
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
	Enabled            bool          `yaml:"enabled"`
	BaseURL            string        `yaml:"base_url"`
	Zone               int           `yaml:"zone"`
	RunAt              string        `yaml:"run_at"`
	Timezone           string        `yaml:"timezone"`
	DeliveryOffsetDays int           `yaml:"delivery_offset_days"`
	HTTPTimeout        time.Duration `yaml:"http_timeout"`
	UserAgent          string        `yaml:"user_agent"`
	Retry              OREERetry     `yaml:"retry"`
}

type Root struct {
	DatabaseURL        string             `yaml:"database_url"`
	RegisterCatalog    string             `yaml:"register_catalog"`
	RegisterAddressing RegisterAddressing `yaml:"register_addressing"`
	ModbusRegisterMap  ModbusRegisterMap  `yaml:"modbus_register_map"`
	Organizations      []Organization     `yaml:"organizations"`
	OREE               OREE               `yaml:"oree"`
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
	c.applyOREEDefaults()
	if err := c.validateOREE(); err != nil {
		return nil, err
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
