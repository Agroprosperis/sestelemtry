package bootstrap

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/nesh/sestelemetry/internal/config"
	"github.com/nesh/sestelemetry/internal/energyflow"
	"github.com/nesh/sestelemetry/internal/registers"
)

type Runtime struct {
	Config   *config.Root
	Resolved []registers.ResolvedEntry
	// DeviceCatalogs holds the register maps devices bring along,
	// keyed by their modbus_devices[].register_catalog path.
	DeviceCatalogs map[string]DeviceCatalog
}

// DeviceCatalog is a register map one kind of device brings along (see
// config.ModbusDevice.RegisterCatalog), resolved with its own addressing.
type DeviceCatalog struct {
	Device   registers.Device
	Resolved []registers.ResolvedEntry
}

// CatalogFor returns the entries a device polls from: its own catalog
// when it names one, the main catalog otherwise.
func (r *Runtime) CatalogFor(dev config.ModbusDevice) []registers.ResolvedEntry {
	if dev.OwnCatalog() {
		return r.DeviceCatalogs[dev.RegisterCatalog].Resolved
	}
	return r.Resolved
}

func Load(configPath string) (*Runtime, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	if err := cfg.RequireModbus(); err != nil {
		return nil, err
	}
	resolved, err := resolveCatalog(cfg)
	if err != nil {
		return nil, err
	}
	deviceCatalogs, err := LoadDeviceCatalogs(cfg)
	if err != nil {
		return nil, err
	}
	rt := &Runtime{
		Config:         cfg,
		Resolved:       resolved,
		DeviceCatalogs: deviceCatalogs,
	}
	if err := validateDeviceMetricKeys(rt); err != nil {
		return nil, err
	}
	return rt, nil
}

// LoadDeviceCatalogs loads every register_catalog a device names, once
// per path. Each is resolved with its own addressing: the top-level
// register_addressing override belongs to the main catalog. On error the
// map still holds the catalogs that did load, so a service that only
// classifies devices can carry on without the broken one.
func LoadDeviceCatalogs(cfg *config.Root) (map[string]DeviceCatalog, error) {
	out := map[string]DeviceCatalog{}
	if cfg == nil {
		return out, nil
	}
	var errs []error
	for _, org := range cfg.Organizations {
		for j, dev := range org.ModbusDevices {
			path := dev.RegisterCatalog
			if path == "" {
				continue
			}
			if _, done := out[path]; done {
				continue
			}
			cat, err := registers.Load(path)
			if err == nil {
				var resolved []registers.ResolvedEntry
				if resolved, err = cat.Resolve(0); err == nil {
					out[path] = DeviceCatalog{Device: cat.Device, Resolved: resolved}
					continue
				}
			}
			errs = append(errs, fmt.Errorf("bootstrap: org=%q modbus_devices[%d] register_catalog %s: %w", org.ID, j, path, err))
		}
	}
	return out, errors.Join(errs...)
}

// DeviceRole classifies a device for the energy-flow allocator by the
// keys it polls: its metric_keys whitelist, else its own catalog, else
// the whole main catalog (a single SmartLogger). A device whose own
// catalog did not load feeds nothing.
func DeviceRole(dev config.ModbusDevice, catalogs map[string]DeviceCatalog) energyflow.Role {
	if len(dev.MetricKeys) > 0 {
		return energyflow.DetectRole(dev.MetricKeys)
	}
	if !dev.OwnCatalog() {
		return energyflow.RoleSingle
	}
	cat, ok := catalogs[dev.RegisterCatalog]
	if !ok {
		return energyflow.RoleNone
	}
	polled, _ := registers.Subset(cat.Resolved, nil)
	if len(polled) == 0 {
		return energyflow.RoleNone
	}
	keys := make([]string, 0, len(polled))
	for _, e := range polled {
		keys = append(keys, e.MetricKey)
	}
	return energyflow.DetectRole(keys)
}

// validateDeviceMetricKeys ensures every per-device metric_keys whitelist
// references a key that exists in the catalog the device reads. Catching
// typos here gives a fast, explicit startup error with the offending
// org+host instead of silent "no samples" failures during the first poll.
func validateDeviceMetricKeys(rt *Runtime) error {
	if rt == nil || rt.Config == nil {
		return nil
	}
	for _, org := range rt.Config.Organizations {
		for j, dev := range org.ModbusDevices {
			if len(dev.MetricKeys) == 0 {
				continue
			}
			if _, err := registers.Subset(rt.CatalogFor(dev), dev.MetricKeys); err != nil {
				return fmt.Errorf("bootstrap: org=%q modbus_devices[%d] host=%s: %w", org.ID, j, dev.Host, err)
			}
		}
	}
	return nil
}

func ApplyDatabaseURLEnv(cfg *config.Root) {
	if cfg == nil {
		return
	}
	if v := strings.TrimSpace(os.Getenv("DATABASE_URL")); v != "" {
		cfg.DatabaseURL = v
	}
}

func resolveCatalog(cfg *config.Root) ([]registers.ResolvedEntry, error) {
	cat, err := registers.Load(cfg.RegisterCatalog)
	if err != nil {
		return nil, fmt.Errorf("register_catalog: %w", err)
	}
	base := cat.Addressing.HoldingAddressBase
	if cfg.RegisterAddressing.HoldingAddressBase != 0 {
		base = cfg.RegisterAddressing.HoldingAddressBase
	}
	resolved, err := cat.Resolve(base)
	if err != nil {
		return nil, fmt.Errorf("resolve_registers: %w", err)
	}
	return resolved, nil
}
