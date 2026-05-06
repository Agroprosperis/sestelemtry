package bootstrap

import (
	"fmt"
	"os"
	"strings"

	"github.com/nesh/sestelemetry/internal/config"
	"github.com/nesh/sestelemetry/internal/registers"
)

type Runtime struct {
	Config   *config.Root
	Resolved []registers.ResolvedEntry
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
	if err := validateDeviceMetricKeys(cfg, resolved); err != nil {
		return nil, err
	}
	return &Runtime{
		Config:   cfg,
		Resolved: resolved,
	}, nil
}

// validateDeviceMetricKeys ensures every per-device metric_keys whitelist
// references a key that exists in the loaded catalog. Catching typos here
// gives a fast, explicit startup error with the offending org+host instead
// of silent "no samples" failures during the first poll.
func validateDeviceMetricKeys(cfg *config.Root, resolved []registers.ResolvedEntry) error {
	if cfg == nil {
		return nil
	}
	for _, org := range cfg.Organizations {
		for j, dev := range org.ModbusDevices {
			if len(dev.MetricKeys) == 0 {
				continue
			}
			if _, err := registers.Subset(resolved, dev.MetricKeys); err != nil {
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
