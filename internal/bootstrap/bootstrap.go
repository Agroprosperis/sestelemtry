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
	resolved, err := resolveCatalog(cfg)
	if err != nil {
		return nil, err
	}
	return &Runtime{
		Config:   cfg,
		Resolved: resolved,
	}, nil
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
