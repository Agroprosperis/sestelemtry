package edge

import (
	"fmt"
	"math"
	"time"
)

// Data quality per the MVP spec §6.
const (
	QualityOK        = "ok"
	QualityStale     = "stale"
	QualityEstimated = "estimated"
	QualityFault     = "fault"
)

// Tick is one normalized telemetry snapshot (spec §6). Pointer fields
// are nil when the backing register was not read this tick (e.g. the
// ESS SmartLogger of a dual site is down). Values carries every decoded
// metric_key with its raw catalog-scaled value so the cloud ingest can
// store the exact same rows the VM collector would have written.
type Tick struct {
	SiteID          string    `json:"site_id"`
	TS              time.Time `json:"ts"`
	SourceTimestamp time.Time `json:"source_timestamp"`
	EdgeReceivedAt  time.Time `json:"edge_received_at"`
	Topology        string    `json:"topology"`

	PVPowerKw         *float64 `json:"pv_power_kw,omitempty"`
	ESSPowerKw        *float64 `json:"ess_power_kw,omitempty"` // + discharge / − charge
	GridPowerKw       *float64 `json:"grid_power_kw,omitempty"`
	LoadPowerKw       *float64 `json:"load_power_kw,omitempty"`
	SocPercent        *float64 `json:"soc_percent,omitempty"`
	ESSChargeMaxKw    *float64 `json:"ess_charge_max_kw,omitempty"`
	ESSDischargeMaxKw *float64 `json:"ess_discharge_max_kw,omitempty"`
	PCSShutdown       *bool    `json:"pcs_shutdown,omitempty"`

	DataQuality string             `json:"data_quality"`
	Values      map[string]float64 `json:"values,omitempty"`
}

// reading is one successful (or failed) poll of a single SmartLogger.
type reading struct {
	role   DeviceRole
	host   string
	at     time.Time
	values map[string]float64
	err    error
}

// Normalizer merges per-device readings into 1-second ticks and
// classifies data quality:
//
//	ok        - every expected role reported within staleAfter
//	stale     - some role's last data is older than staleAfter
//	estimated - ok, but load_power_kw was derived from the node balance
//	fault     - a role has never reported or is older than faultAfter
type Normalizer struct {
	siteID     string
	topology   Topology
	essSign    float64
	expected   []DeviceRole
	staleAfter time.Duration
	faultAfter time.Duration

	last map[DeviceRole]reading
}

func NewNormalizer(cfg *Config) *Normalizer {
	expected := make([]DeviceRole, 0, len(cfg.SmartLogger.Devices))
	for _, d := range cfg.SmartLogger.Devices {
		expected = append(expected, d.Role)
	}
	poll := cfg.SmartLogger.PollInterval
	stale := 3 * poll
	if stale < 5*time.Second {
		stale = 5 * time.Second
	}
	return &Normalizer{
		siteID:     cfg.SiteID,
		topology:   cfg.SmartLogger.Topology,
		essSign:    cfg.EffectiveEssSign(),
		expected:   expected,
		staleAfter: stale,
		faultAfter: 30 * time.Second,
		last:       map[DeviceRole]reading{},
	}
}

// Observe records the latest reading of a device role. Failed polls
// (r.err != nil) do not overwrite the last good values; freshness decay
// alone downgrades quality.
func (n *Normalizer) Observe(r reading) {
	if r.err != nil {
		return
	}
	n.last[r.role] = r
}

// BuildTick merges the freshest values per role into one Tick at `now`.
func (n *Normalizer) BuildTick(now time.Time) Tick {
	values := map[string]float64{}
	quality := QualityOK
	for _, role := range n.expected {
		r, ok := n.last[role]
		if !ok || now.Sub(r.at) > n.faultAfter {
			quality = QualityFault
			continue
		}
		if now.Sub(r.at) > n.staleAfter && quality != QualityFault {
			quality = QualityStale
		}
		for k, v := range r.values {
			values[k] = v
		}
	}
	return buildTickFromValues(n.siteID, n.topology, n.essSign, now, values, quality)
}

// buildTickFromValues maps raw metric_key values into the normalized
// tick fields. Shared by the live normalizer and the replay harness so
// both paths produce byte-identical decisions for the same inputs.
func buildTickFromValues(
	siteID string,
	topology Topology,
	essSign float64,
	ts time.Time,
	values map[string]float64,
	quality string,
) Tick {
	t := Tick{
		SiteID:          siteID,
		TS:              ts.UTC(),
		SourceTimestamp: ts.UTC(),
		EdgeReceivedAt:  time.Now().UTC(),
		Topology:        string(topology),
		Values:          values,
	}

	if v, ok := values["active_pv_power_kw"]; ok {
		t.PVPowerKw = f64ptr(v)
	}
	if v, ok := values["active_ess_power_kw"]; ok {
		t.ESSPowerKw = f64ptr(v * essSign)
	}
	if v, ok := values["grid_connected_active_power_kw"]; ok {
		t.GridPowerKw = f64ptr(v)
	}
	if v, ok := values["load_power_kw"]; ok {
		t.LoadPowerKw = f64ptr(v)
	}
	if v, ok := values["soc_percent"]; ok {
		t.SocPercent = f64ptr(v)
	}
	if v, ok := values["ess_charge_max_kw"]; ok {
		t.ESSChargeMaxKw = f64ptr(v)
	}
	if v, ok := values["ess_discharge_max_kw"]; ok {
		t.ESSDischargeMaxKw = f64ptr(v)
	}
	if v, ok := values["pcs_shutdown"]; ok {
		b := v != 0
		t.PCSShutdown = &b
	}

	// Node balance fallback (spec §5.4): load ≈ pv + ess + grid with
	// ess + discharge and grid + import both feeding the load.
	if t.LoadPowerKw == nil && t.PVPowerKw != nil && t.ESSPowerKw != nil && t.GridPowerKw != nil {
		load := *t.PVPowerKw + *t.ESSPowerKw + *t.GridPowerKw
		if load < 0 {
			load = 0
		}
		t.LoadPowerKw = f64ptr(round3(load))
		if quality == QualityOK {
			quality = QualityEstimated
		}
	}

	t.DataQuality = quality
	return t
}

func f64ptr(v float64) *float64 { return &v }

func round3(v float64) float64 { return math.Round(v*1000) / 1000 }

// slAlarmWordKeys are the metric keys of the six SmartLogger alarm
// words (registers 50000…50005, Issue 52 Alarm 1…6), in order.
var slAlarmWordKeys = [6]string{
	"sl_alarm_1", "sl_alarm_2", "sl_alarm_3",
	"sl_alarm_4", "sl_alarm_5", "sl_alarm_6",
}

// SLAlarmWords extracts the six raw alarm words from the tick. The
// second return is false when none of the words were polled at all
// (old catalog, replay CSV without the columns) — absence must not be
// confused with "all clear", but it must not fake an alarm either.
func (t Tick) SLAlarmWords() ([6]uint16, bool) {
	var words [6]uint16
	any := false
	for i, k := range slAlarmWordKeys {
		if v, ok := t.Values[k]; ok {
			words[i] = uint16(v)
			any = true
		}
	}
	return words, any
}

// SLAlarmActive reports whether any polled alarm word is non-zero.
func (t Tick) SLAlarmActive() bool {
	words, ok := t.SLAlarmWords()
	if !ok {
		return false
	}
	for _, w := range words {
		if w != 0 {
			return true
		}
	}
	return false
}

// slAlarmHex renders the words for events and the health snapshot:
// "0x0" for zero, "0x0010"-style for non-zero (mirrors the spec's UI
// example `A2=0x0010`).
func slAlarmHex(words [6]uint16) [6]string {
	var out [6]string
	for i, w := range words {
		if w == 0 {
			out[i] = "0x0"
		} else {
			out[i] = fmt.Sprintf("0x%04x", w)
		}
	}
	return out
}
