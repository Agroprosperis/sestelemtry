package edge

import (
	"fmt"
	"time"
)

// ManifestSchemaLite identifies the simplified pre-v1 manifest the MVP
// exchanges. The full signed v1 bundle (ems_manifest_schema.md) arrives
// with MVP-3.
const ManifestSchemaLite = "lite-1"

// FallbackPreset is what the engine runs when no valid manifest is
// available (never received one, or valid_until passed): max own
// consumption, no arbitrage, no grid charge (spec: self_consumption_safe).
const FallbackPreset = "self_consumption_safe"

const (
	PresetSelfConsumption     = "self_consumption"
	PresetSelfConsumptionSafe = "self_consumption_safe"
	PresetEconomicArbitrage   = "economic_arbitrage"
)

// Manifest is the cloud→edge control contract ("manifest-lite"): mode,
// preset, limits, SOC policy and an optional dispatch plan. Served by
// GET /api/v1/edge/manifest and cached on disk by the edge.
type Manifest struct {
	SchemaVersion string    `json:"schema_version"`
	ManifestID    string    `json:"manifest_id"`
	SiteID        string    `json:"site_id"`
	IssuedAt      time.Time `json:"issued_at"`
	ValidFrom     time.Time `json:"valid_from"`
	ValidUntil    time.Time `json:"valid_until"`

	Mode         Mode   `json:"mode"`
	WriteEnabled bool   `json:"write_enabled"`
	Preset       string `json:"preset"`
	// ExportAllowed lets the plan discharge past the local deficit
	// (BESS arbitrage sells at the export tariff). Absent/false in old
	// manifests → the legacy no-export clamp stays in force.
	ExportAllowed bool `json:"export_allowed,omitempty"`

	Limits     ManifestLimits     `json:"limits"`
	GridLimits ManifestGridLimits `json:"grid_limits"`
	SocPolicy  SocPolicy          `json:"soc_policy"`

	Plan *Plan `json:"plan,omitempty"`
}

type ManifestLimits struct {
	EssChargeMaxKw    float64 `json:"ess_charge_max_kw,omitempty"`
	EssDischargeMaxKw float64 `json:"ess_discharge_max_kw,omitempty"`
}

type ManifestGridLimits struct {
	ImportLimitKw  float64 `json:"import_limit_kw,omitempty"`
	TargetImportKw float64 `json:"target_import_kw,omitempty"`
	PvRatedKw      float64 `json:"pv_rated_kw,omitempty"`
}

type SocPolicy struct {
	MinEconomicPct float64 `json:"min_economic_pct,omitempty"`
	MaxEconomicPct float64 `json:"max_economic_pct,omitempty"`
	ReservePeakPct float64 `json:"reserve_peak_pct,omitempty"`
}

// Plan is the level-A dispatch trajectory from the cloud planner.
type Plan struct {
	Granularity string         `json:"granularity"` // e.g. "1h", "5m"
	LoadSource  string         `json:"load_source,omitempty"`
	Intervals   []PlanInterval `json:"intervals"`
}

type PlanInterval struct {
	TS           time.Time `json:"ts"`
	EssKw        float64   `json:"ess_kw"` // + discharge / − charge
	SocTargetPct float64   `json:"soc_target_pct,omitempty"`
	Action       string    `json:"action,omitempty"`
	PriceUah     float64   `json:"rdn_uah_per_kwh,omitempty"`
}

// IntervalDuration parses the plan granularity, defaulting to 1h.
func (p *Plan) IntervalDuration() time.Duration {
	if p == nil || p.Granularity == "" {
		return time.Hour
	}
	d, err := time.ParseDuration(p.Granularity)
	if err != nil || d <= 0 {
		return time.Hour
	}
	return d
}

// IntervalAt returns the plan interval containing t, or nil.
func (p *Plan) IntervalAt(t time.Time) *PlanInterval {
	if p == nil || len(p.Intervals) == 0 {
		return nil
	}
	d := p.IntervalDuration()
	for i := range p.Intervals {
		iv := &p.Intervals[i]
		if !t.Before(iv.TS) && t.Before(iv.TS.Add(d)) {
			return iv
		}
	}
	return nil
}

// ActiveAt reports whether the manifest's validity window contains t.
func (m *Manifest) ActiveAt(t time.Time) bool {
	if m == nil {
		return false
	}
	if !m.ValidFrom.IsZero() && t.Before(m.ValidFrom) {
		return false
	}
	if !m.ValidUntil.IsZero() && t.After(m.ValidUntil) {
		return false
	}
	return true
}

// ValidateForEdge is the MVP hard gate: this build has no SmartLogger
// write path, so any manifest requesting writes or an auto mode is
// rejected outright rather than partially obeyed.
func (m *Manifest) ValidateForEdge(siteID string) error {
	if m.SiteID != siteID {
		return fmt.Errorf("manifest: site_id %q does not match this edge (%q)", m.SiteID, siteID)
	}
	if m.WriteEnabled {
		return fmt.Errorf("manifest: write_enabled=true rejected — MVP build has no write path (MVP-3 gate)")
	}
	switch m.Mode {
	case ModeMonitor, ModeShadow:
	default:
		return fmt.Errorf("manifest: mode %q rejected — MVP supports monitor|shadow only", m.Mode)
	}
	if m.ManifestID == "" {
		return fmt.Errorf("manifest: manifest_id is required")
	}
	return nil
}
