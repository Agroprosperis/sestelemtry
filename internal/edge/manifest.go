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
	// ManifestSeq grows strictly with every published document of the
	// site (ems_sl_write_cutover.md §4); the edge rejects one that is not
	// newer than the applied. 0 = an older cloud that does not number.
	ManifestSeq int64 `json:"manifest_seq,omitempty"`

	Mode Mode `json:"mode"`
	// WriteEnabled is informational: writing is the device's own flag,
	// so a manifest asking for it is rejected.
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
	// ManualCommands are the operator's confirmed desk blocks; while one
	// is in force the plan is not used.
	ManualCommands []ManualCommand `json:"manual_commands,omitempty"`
}

type ManifestLimits struct {
	EssChargeMaxKw    float64 `json:"ess_charge_max_kw,omitempty"`
	EssDischargeMaxKw float64 `json:"ess_discharge_max_kw,omitempty"`
}

type ManifestGridLimits struct {
	ImportLimitKw  float64 `json:"import_limit_kw,omitempty"`
	TargetImportKw float64 `json:"target_import_kw,omitempty"`
	PvRatedKw      float64 `json:"pv_rated_kw,omitempty"`
	// ExportCapKw is the site's PCC export ceiling
	// (active_consumer_export_power_cap.md; ЖЕ 850), the only source of
	// it on the edge. 0 = not set.
	ExportCapKw float64 `json:"export_cap_kw,omitempty"`
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

// ValidateForEdge rejects a manifest outright rather than obeying part
// of it. Writing is the device's own flag, so a manifest asking for it
// is refused, as is any mode beyond monitor|shadow.
func (m *Manifest) ValidateForEdge(siteID string) error {
	if m.SiteID != siteID {
		return fmt.Errorf("manifest: site_id %q does not match this edge (%q)", m.SiteID, siteID)
	}
	if m.WriteEnabled {
		return fmt.Errorf("manifest: write_enabled=true rejected — writing is switched on only on the device")
	}
	switch m.Mode {
	case ModeMonitor, ModeShadow:
	default:
		return fmt.Errorf("manifest: mode %q rejected — supported: monitor|shadow", m.Mode)
	}
	if m.ManifestID == "" {
		return fmt.Errorf("manifest: manifest_id is required")
	}
	if m.ManifestSeq < 0 {
		return fmt.Errorf("manifest: manifest_seq %d is negative", m.ManifestSeq)
	}
	if m.GridLimits.ExportCapKw < 0 || m.Limits.EssChargeMaxKw < 0 || m.Limits.EssDischargeMaxKw < 0 {
		return fmt.Errorf("manifest: negative limit")
	}
	if p := m.SocPolicy; p.MinEconomicPct < 0 || p.MaxEconomicPct > 100 ||
		(p.MaxEconomicPct > 0 && p.MinEconomicPct >= p.MaxEconomicPct) {
		return fmt.Errorf("manifest: SOC policy %v…%v%% out of range", p.MinEconomicPct, p.MaxEconomicPct)
	}
	return validateManualCommands(m.ManualCommands)
}

// AcceptAfter checks a validated manifest against the one applied: the
// number must grow, and while writing is on it must be there at all.
func (m *Manifest) AcceptAfter(appliedSeq int64, writing bool) error {
	if writing && m.ManifestSeq == 0 {
		return fmt.Errorf("manifest: unnumbered manifest rejected while writing is on")
	}
	if m.ManifestSeq > 0 && appliedSeq > 0 && m.ManifestSeq <= appliedSeq {
		return fmt.Errorf("manifest: manifest_seq %d is not newer than applied %d", m.ManifestSeq, appliedSeq)
	}
	return nil
}
