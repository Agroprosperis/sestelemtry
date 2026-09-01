// Package edge implements the EMS edge controller that runs on the
// on-site Siemens IOT2050: SmartLogger polling, tick normalization,
// the SQLite black box, shadow control and the uplink to the central
// sestelemetry API.
//
// Spec: ems-spec docs/specs/ems_mvp_edge_shadow_spec.md (MVP-0..2).
package edge

import (
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Topology mirrors the site topologies from the energy-flow spec: one
// SmartLogger covering PV+ESS, or two boxes split by role (ze).
type Topology string

const (
	TopologySingle Topology = "single_smartlogger"
	TopologyDual   Topology = "dual_smartlogger"
)

// DeviceRole tells the poller which default register set a SmartLogger
// is responsible for.
type DeviceRole string

const (
	RoleAll DeviceRole = "all" // single topology: PV + ESS on one box
	RolePV  DeviceRole = "pv"
	RoleESS DeviceRole = "ess"
)

// Mode is the control mode. MVP supports only monitor/shadow; any
// auto_* mode is rejected at config load because the write path does
// not exist in this build (spec: write to 40378/40381 is forbidden
// until MVP-3 sign-off).
type Mode string

const (
	ModeMonitor Mode = "monitor"
	ModeShadow  Mode = "shadow"
)

// Device is one SmartLogger endpoint.
type Device struct {
	Role           DeviceRole    `yaml:"role"`
	Host           string        `yaml:"host"`
	Port           int           `yaml:"port"`
	UnitID         *int          `yaml:"unit_id"` // pointer: explicit 0 is a valid unit id
	ConnectTimeout time.Duration `yaml:"connect_timeout"`
	RequestTimeout time.Duration `yaml:"request_timeout"`
	// MetricKeys overrides the role's default register whitelist.
	MetricKeys []string `yaml:"metric_keys"`
}

// EffectiveUnitID resolves the pointer with the repo-wide default of 99
// (the unit id observed on the field per PCAP; the vendor doc says 0,
// so sites that need it set `unit_id: 0` explicitly).
func (d Device) EffectiveUnitID() int {
	if d.UnitID == nil {
		return 99
	}
	return *d.UnitID
}

type SmartLogger struct {
	Topology     Topology      `yaml:"topology"`
	PollInterval time.Duration `yaml:"poll_interval"`
	Devices      []Device      `yaml:"devices"`
}

type EdgeIdentity struct {
	EdgeID   string `yaml:"edge_id"`
	Platform string `yaml:"platform"`
}

type BlackboxConfig struct {
	Enabled         bool   `yaml:"enabled"`
	DBPath          string `yaml:"db_path"`
	RetentionDays   int    `yaml:"retention_days"`
	DiskCriticalPct int    `yaml:"disk_critical_pct"`
}

type UplinkConfig struct {
	Enabled           bool          `yaml:"enabled"`
	BaseURL           string        `yaml:"base_url"`
	BatchPath         string        `yaml:"batch_path"`
	HeartbeatPath     string        `yaml:"heartbeat_path"`
	ManifestPath      string        `yaml:"manifest_path"`
	BatchInterval     time.Duration `yaml:"batch_interval"`
	BatchMaxRecords   int           `yaml:"batch_max_records"`
	HeartbeatInterval time.Duration `yaml:"heartbeat_interval"`
	HTTPTimeout       time.Duration `yaml:"http_timeout"`
	// SiteTokenEnv names the environment variable holding the Bearer
	// token for this site. The token itself never appears in YAML.
	SiteTokenEnv string `yaml:"site_token_env"`
}

// Token reads the per-site Bearer token from the configured env var.
func (u UplinkConfig) Token() string {
	if u.SiteTokenEnv == "" {
		return ""
	}
	return strings.TrimSpace(os.Getenv(u.SiteTokenEnv))
}

type ManifestConfig struct {
	PollInterval time.Duration `yaml:"poll_interval"`
	CachePath    string        `yaml:"cache_path"`
}

type ControlConfig struct {
	Mode     Mode          `yaml:"mode"`
	Preset   string        `yaml:"preset"`
	Interval time.Duration `yaml:"interval"`
}

// LocalUIConfig is the on-device web console (spec ems_ui_edge_vs_cloud
// §2: стан + діагностика + emergency override; не повний планувальник).
// Enabled by default on :8081 — LAN-only by network design, no auth
// (mirrors the mockup; the device sits on the site OT network).
type LocalUIConfig struct {
	Enabled *bool  `yaml:"enabled"`
	Listen  string `yaml:"listen"`
}

// On reports whether the console should run (default true when the
// config omits the section).
func (l LocalUIConfig) On() bool { return l.Enabled == nil || *l.Enabled }

type GridLimits struct {
	ImportLimitKw  float64 `yaml:"import_limit_kw"`
	TargetImportKw float64 `yaml:"target_import_kw"`
}

type PVLimits struct {
	RatedKw float64 `yaml:"rated_kw"`
}

type BessLimits struct {
	RatedPowerKw      float64 `yaml:"rated_power_kw"`
	RatedCapacityKwh  float64 `yaml:"rated_capacity_kwh"`
	SocMinEconomicPct float64 `yaml:"soc_min_economic_pct"`
	SocMaxEconomicPct float64 `yaml:"soc_max_economic_pct"`
	// Site passport for the bess_inventory health check (diagnostics
	// spec §7.3): compared against SL 40398/40484/40488. Mismatch is a
	// warning, never a dispatch block. Zero = not configured, check
	// skipped. ze: 864 / 1720 / 8.
	PassportKw       float64 `yaml:"passport_kw"`
	PassportKwh      float64 `yaml:"passport_kwh"`
	PassportEssCount int     `yaml:"passport_ess_count"`
}

type Limits struct {
	Grid GridLimits `yaml:"grid"`
	PV   PVLimits   `yaml:"pv"`
	Bess BessLimits `yaml:"bess"`
}

// InverterDiagnostics configures the slow remapped-51xxx poll of the
// PV SmartLogger (diagnostics spec §6). DeviceAddresses are RS-485
// addresses on the PV SL (ze: 12…23, confirmed by Encombi PCAP) — NOT
// FusionSolar SNs and NOT "1..N because N inverters". An empty list
// disables the poll entirely (the fleet panel is then hidden; alarm
// words 50000…50005 are still read in the 1s ESS poll).
type InverterDiagnostics struct {
	DeviceAddresses []int          `yaml:"device_addresses"`
	PollInterval    time.Duration  `yaml:"poll_interval"`
	Labels          map[int]string `yaml:"labels"`
}

type DiagnosticsConfig struct {
	Inverters InverterDiagnostics `yaml:"inverters"`
}

// Config is the root of the edge YAML (config.edge.yaml).
type Config struct {
	SiteID          string         `yaml:"site_id"`
	Timezone        string         `yaml:"timezone"`
	RegisterCatalog string         `yaml:"register_catalog"`
	SmartLogger     SmartLogger    `yaml:"smartlogger"`
	Edge            EdgeIdentity   `yaml:"edge"`
	Blackbox        BlackboxConfig `yaml:"blackbox"`
	Uplink          UplinkConfig   `yaml:"uplink"`
	Manifest        ManifestConfig `yaml:"manifest"`
	Control         ControlConfig  `yaml:"control"`
	Limits          Limits         `yaml:"limits"`
	LocalUI         LocalUIConfig  `yaml:"local_ui"`
	Diagnostics     DiagnosticsConfig `yaml:"diagnostics"`

	// EssDischargeSign overrides the convention that raw
	// active_ess_power_kw > 0 means "discharging". Set to -1 for
	// firmwares that report charge as positive (ze). Allowed: 0
	// (= default 1), 1, -1. The normalizer applies it so the tick's
	// ess_power_kw is always + discharge / − charge per the spec.
	EssDischargeSign int `yaml:"ess_discharge_sign"`
}

// DefaultMetricKeys returns the per-role register whitelist from the
// MVP spec (§2.1 telemetry + §2.2 shadow reads). Overridable per device
// via `metric_keys`.
func DefaultMetricKeys(role DeviceRole) []string {
	pv := []string{
		"active_pv_power_kw",
		"load_power_kw",
		"grid_connected_active_power_kw",
		"accumulated_pv_energy_yield_kwh",
		"accumulated_electricity_purchased_kwh",
		"accumulated_electricity_sold_kwh",
		"accumulated_power_consumption_kwh",
	}
	ess := []string{
		"active_ess_power_kw",
		"soc_percent",
		"ess_charge_max_kw",
		"ess_discharge_max_kw",
		"pcs_shutdown",
		"total_energy_charged_kwh",
		"total_energy_discharged_kwh",
		// Shadow diagnostics (ems_edge_shadow_diagnostics.md §5/§7):
		// BESS array details + SL alarm words, same 1s FC3 poll.
		"reactive_ess_power_kvar",
		"ess_chargeable_kwh",
		"ess_dischargeable_kwh",
		"ess_soh_pct",
		"ess_soe_pct",
		"ess_rated_kw",
		"ess_rated_kwh",
		"ess_count",
		"pcs_count",
		"pcs_in_operation",
		"sl_alarm_1",
		"sl_alarm_2",
		"sl_alarm_3",
		"sl_alarm_4",
		"sl_alarm_5",
		"sl_alarm_6",
	}
	switch role {
	case RolePV:
		return pv
	case RoleESS:
		return ess
	default:
		return append(append([]string{}, pv...), ess...)
	}
}

// LoadConfig reads the edge YAML, expands ${ENV} in host/base_url
// fields, applies defaults and validates.
func LoadConfig(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Config
	if err := yaml.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	c.applyDefaults()
	if err := c.validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *Config) applyDefaults() {
	if strings.TrimSpace(c.Timezone) == "" {
		c.Timezone = "Europe/Kyiv"
	}
	if strings.TrimSpace(c.RegisterCatalog) == "" {
		c.RegisterCatalog = "registers/huawei_smartlogger.yaml"
	}

	sl := &c.SmartLogger
	if sl.Topology == "" {
		sl.Topology = TopologySingle
	}
	if sl.PollInterval <= 0 {
		sl.PollInterval = time.Second
	}
	for i := range sl.Devices {
		d := &sl.Devices[i]
		d.Host = os.ExpandEnv(strings.TrimSpace(d.Host))
		if d.Role == "" {
			d.Role = RoleAll
		}
		if d.Port == 0 {
			d.Port = 502
		}
		if d.ConnectTimeout <= 0 {
			d.ConnectTimeout = 5 * time.Second
		}
		if d.RequestTimeout <= 0 {
			d.RequestTimeout = 5 * time.Second
		}
	}

	if strings.TrimSpace(c.Edge.Platform) == "" {
		c.Edge.Platform = "siemens_iot2050"
	}

	if strings.TrimSpace(c.LocalUI.Listen) == "" {
		c.LocalUI.Listen = ":8081"
	}

	bb := &c.Blackbox
	if strings.TrimSpace(bb.DBPath) == "" {
		bb.DBPath = "/data/blackbox/blackbox.db"
	}
	if bb.RetentionDays <= 0 {
		bb.RetentionDays = 30
	}
	if bb.DiskCriticalPct <= 0 {
		bb.DiskCriticalPct = 95
	}

	u := &c.Uplink
	u.BaseURL = os.ExpandEnv(strings.TrimSpace(u.BaseURL))
	if u.BatchPath == "" {
		u.BatchPath = "/api/v1/edge/batch"
	}
	if u.HeartbeatPath == "" {
		u.HeartbeatPath = "/api/v1/edge/heartbeat"
	}
	if u.ManifestPath == "" {
		u.ManifestPath = "/api/v1/edge/manifest"
	}
	if u.BatchInterval <= 0 {
		u.BatchInterval = 30 * time.Second
	}
	if u.BatchMaxRecords <= 0 {
		u.BatchMaxRecords = 600
	}
	if u.HeartbeatInterval <= 0 {
		u.HeartbeatInterval = 30 * time.Second
	}
	if u.HTTPTimeout <= 0 {
		u.HTTPTimeout = 20 * time.Second
	}

	m := &c.Manifest
	if m.PollInterval <= 0 {
		m.PollInterval = time.Minute
	}
	if strings.TrimSpace(m.CachePath) == "" {
		m.CachePath = "/data/manifest/active_manifest.json"
	}

	ctl := &c.Control
	if ctl.Mode == "" {
		ctl.Mode = ModeShadow
	}
	if strings.TrimSpace(ctl.Preset) == "" {
		ctl.Preset = "self_consumption"
	}
	if ctl.Interval <= 0 {
		ctl.Interval = time.Second
	}

	if c.Limits.Bess.SocMinEconomicPct <= 0 {
		c.Limits.Bess.SocMinEconomicPct = 20
	}
	if c.Limits.Bess.SocMaxEconomicPct <= 0 {
		c.Limits.Bess.SocMaxEconomicPct = 90
	}

	if c.Diagnostics.Inverters.PollInterval <= 0 {
		c.Diagnostics.Inverters.PollInterval = 30 * time.Second
	}
}

func (c *Config) validate() error {
	if strings.TrimSpace(c.SiteID) == "" {
		return fmt.Errorf("edge config: site_id is required")
	}
	if _, err := time.LoadLocation(c.Timezone); err != nil {
		return fmt.Errorf("edge config: timezone: %w", err)
	}
	if strings.TrimSpace(c.Edge.EdgeID) == "" {
		return fmt.Errorf("edge config: edge.edge_id is required")
	}

	switch c.Control.Mode {
	case ModeMonitor, ModeShadow:
	default:
		// Hard gate: this build has no SmartLogger write path at all.
		return fmt.Errorf("edge config: control.mode must be monitor or shadow (write modes are not built until MVP-3), got %q", c.Control.Mode)
	}

	sl := c.SmartLogger
	switch sl.Topology {
	case TopologySingle:
		if len(sl.Devices) != 1 {
			return fmt.Errorf("edge config: single_smartlogger requires exactly 1 device, got %d", len(sl.Devices))
		}
		if sl.Devices[0].Role != RoleAll {
			return fmt.Errorf("edge config: single_smartlogger device role must be %q, got %q", RoleAll, sl.Devices[0].Role)
		}
	case TopologyDual:
		if len(sl.Devices) != 2 {
			return fmt.Errorf("edge config: dual_smartlogger requires exactly 2 devices, got %d", len(sl.Devices))
		}
		roles := map[DeviceRole]bool{}
		for _, d := range sl.Devices {
			roles[d.Role] = true
		}
		if !roles[RolePV] || !roles[RoleESS] {
			return fmt.Errorf("edge config: dual_smartlogger requires one pv and one ess device")
		}
	default:
		return fmt.Errorf("edge config: smartlogger.topology must be %q or %q", TopologySingle, TopologyDual)
	}
	for i, d := range sl.Devices {
		if d.Host == "" {
			return fmt.Errorf("edge config: smartlogger.devices[%d].host is required (env vars are expanded)", i)
		}
		uid := d.EffectiveUnitID()
		if uid < 0 || uid > 255 {
			return fmt.Errorf("edge config: smartlogger.devices[%d].unit_id out of range: %d", i, uid)
		}
	}

	if c.Uplink.Enabled {
		if c.Uplink.BaseURL == "" {
			return fmt.Errorf("edge config: uplink.base_url is required when uplink.enabled")
		}
		if c.Uplink.SiteTokenEnv == "" {
			return fmt.Errorf("edge config: uplink.site_token_env is required when uplink.enabled")
		}
	}

	b := c.Limits.Bess
	if b.SocMinEconomicPct >= b.SocMaxEconomicPct {
		return fmt.Errorf("edge config: limits.bess soc_min_economic_pct (%g) must be < soc_max_economic_pct (%g)",
			b.SocMinEconomicPct, b.SocMaxEconomicPct)
	}
	switch c.EssDischargeSign {
	case 0, 1, -1:
	default:
		return fmt.Errorf("edge config: ess_discharge_sign must be 1 or -1, got %d", c.EssDischargeSign)
	}
	for _, addr := range c.Diagnostics.Inverters.DeviceAddresses {
		if addr < 1 || addr > 247 {
			return fmt.Errorf("edge config: diagnostics.inverters.device_addresses: %d out of RS-485 range 1..247", addr)
		}
	}
	return nil
}

// EffectiveEssSign returns +1 or -1.
func (c *Config) EffectiveEssSign() float64 {
	if c.EssDischargeSign == -1 {
		return -1
	}
	return 1
}
