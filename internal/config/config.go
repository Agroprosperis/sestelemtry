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
	Host            string        `yaml:"host"`
	Port            int           `yaml:"port"`
	UnitID          int           `yaml:"unit_id"`
	ConnectTimeout  time.Duration `yaml:"connect_timeout"`
	RequestTimeout  time.Duration `yaml:"request_timeout"`
}

type Organization struct {
	ID           string           `yaml:"id"`
	Name         string           `yaml:"name"`
	SiteID       string           `yaml:"site_id"`
	DeviceID     string           `yaml:"device_id"`
	PollInterval time.Duration    `yaml:"poll_interval"`
	Modbus       Modbus           `yaml:"modbus"`
}

type RegisterAddressing struct {
	HoldingAddressBase int `yaml:"holding_address_base"`
}

type Root struct {
	DatabaseURL         string               `yaml:"database_url"`
	RegisterCatalog     string               `yaml:"register_catalog"`
	RegisterAddressing  RegisterAddressing  `yaml:"register_addressing"`
	ModbusRegisterMap   ModbusRegisterMap    `yaml:"modbus_register_map"`
	Organizations       []Organization       `yaml:"organizations"`
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
		if o.Modbus.Port == 0 {
			o.Modbus.Port = 502
		}
		if o.Modbus.UnitID == 0 {
			o.Modbus.UnitID = 99
		}
		if o.Modbus.UnitID < 0 || o.Modbus.UnitID > 255 {
			return nil, fmt.Errorf("config: org %q unit_id out of range", o.ID)
		}
		if o.Modbus.ConnectTimeout <= 0 {
			o.Modbus.ConnectTimeout = 5 * time.Second
		}
		if o.Modbus.RequestTimeout <= 0 {
			o.Modbus.RequestTimeout = 5 * time.Second
		}
	}
	return &c, nil
}

func (c *Root) validate() error {
	c.DatabaseURL = strings.TrimSpace(c.DatabaseURL)
	// database_url may be set later from DATABASE_URL in the process entrypoint.
	c.RegisterCatalog = strings.TrimSpace(c.RegisterCatalog)
	if c.RegisterCatalog == "" {
		return fmt.Errorf("config: register_catalog is required")
	}
	switch c.ModbusRegisterMap {
	case "", MapHolding, MapInput:
	default:
		return fmt.Errorf("config: modbus_register_map must be holding or input")
	}
	if len(c.Organizations) == 0 {
		return fmt.Errorf("config: at least one organization is required")
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
		if strings.TrimSpace(o.Modbus.Host) == "" {
			return fmt.Errorf("config: org %q modbus.host is required", id)
		}
	}
	return nil
}
