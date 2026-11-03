package pvplan

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"time"
)

// Source says where an organization's plan comes from. It is switched
// per organization so the in-house model can go live one station at a
// time, and each source keeps its own numbers: switching back loses
// nothing on either side.
type Source string

const (
	// SourceN8N reads the n8n flow, as the dashboard always has.
	SourceN8N Source = "n8n"
	// SourceModel computes the plan in-house from the station passport.
	SourceModel Source = "model"
)

// Valid reports whether s is one of the known sources.
func (s Source) Valid() bool { return s == SourceN8N || s == SourceModel }

// Orientation is one plane of panels: everything on it shares a tilt
// and an azimuth, so it sees one irradiance.
type Orientation struct {
	Idx     int     `json:"idx"`
	TiltDeg float64 `json:"tilt_deg"`
	// AzimuthDeg follows Open-Meteo: 0 = south, −90 = east, +90 = west,
	// ±180 = north.
	AzimuthDeg   float64 `json:"azimuth_deg"`
	ModulesCount int     `json:"modules_count"`
	ModulePowerW float64 `json:"module_power_w"`
	Enabled      bool    `json:"enabled"`
	Note         string  `json:"note,omitempty"`
}

// PdcKw is the plane's DC nameplate.
func (o Orientation) PdcKw() float64 {
	return float64(o.ModulesCount) * o.ModulePowerW / 1000
}

// Station is the passport the in-house plan is computed from, plus the
// switch that says whether the dashboard uses it.
type Station struct {
	OrganizationID string `json:"organization_id"`
	Source         Source `json:"source"`
	// Latitude / Longitude nil means "the organization's location in
	// config.yaml".
	Latitude     *float64 `json:"latitude"`
	Longitude    *float64 `json:"longitude"`
	PacLimitKw   float64  `json:"pac_limit_kw"`
	GammaPctPerC float64  `json:"gamma_pct_per_c"`
	// WholeElevatorFrom (YYYY-MM-DD) is the first day the planes
	// describe more than this site — Дубовʼязівський's de/de1 share the
	// whole elevator's planes until each site's own are entered. From
	// that day the plan is labelled as the elevator's and control does
	// not plan on it.
	WholeElevatorFrom   string        `json:"whole_elevator_from,omitempty"`
	Orientations        []Orientation `json:"orientations"`
	UpdatedAt           *time.Time    `json:"updated_at,omitempty"`
	UpdatedBy           string        `json:"updated_by,omitempty"`
	HistoryBackfilledAt *time.Time    `json:"history_backfilled_at,omitempty"`
}

// EnabledOrientations returns the planes that take part in the plan.
func (s Station) EnabledOrientations() []Orientation {
	out := make([]Orientation, 0, len(s.Orientations))
	for _, o := range s.Orientations {
		if o.Enabled && o.PdcKw() > 0 {
			out = append(out, o)
		}
	}
	return out
}

// PdcTotalKwp is the DC nameplate of the enabled planes.
func (s Station) PdcTotalKwp() float64 {
	total := 0.0
	for _, o := range s.EnabledOrientations() {
		total += o.PdcKw()
	}
	return total
}

// Modelled reports whether the passport is enough to compute a plan.
// The model runs for every such station whatever its source, so a
// station switched to the model has its plan ready.
func (s Station) Modelled() bool {
	return len(s.EnabledOrientations()) > 0 && s.PacLimitKw > 0
}

// CoversDay reports whether the plan for `day` (YYYY-MM-DD) describes
// exactly this site rather than the whole elevator.
func (s Station) CoversDay(day string) bool {
	return s.WholeElevatorFrom == "" || day < s.WholeElevatorFrom
}

// ErrInvalid matches (errors.Is) every Validate error: a passport the
// caller sent wrong, as opposed to one that failed to store.
var ErrInvalid = errors.New("pvplan: invalid passport")

// invalidError carries a Validate message, shown to the engineer as-is.
type invalidError struct{ msg string }

func (e invalidError) Error() string        { return e.msg }
func (e invalidError) Is(target error) bool { return target == ErrInvalid }

func invalid(format string, args ...any) error {
	return invalidError{msg: fmt.Sprintf(format, args...)}
}

// Validate rejects a passport its source can't plan from. Messages are
// shown to the engineer as-is.
func (s Station) Validate() error {
	if !s.Source.Valid() {
		return invalid("невідоме джерело плану %q", s.Source)
	}
	if _, ok := ElevatorCodeFor(s.OrganizationID); s.Source == SourceN8N && !ok {
		return invalid("для цієї станції немає прогнозу n8n — оберіть власну модель")
	}
	if (s.Latitude == nil) != (s.Longitude == nil) {
		return invalid("координати задаються парою: і широта, і довгота, або жодної")
	}
	if s.Latitude != nil && (math.IsNaN(*s.Latitude) || *s.Latitude < -90 || *s.Latitude > 90) {
		return invalid("широта має бути від −90 до 90")
	}
	if s.Longitude != nil && (math.IsNaN(*s.Longitude) || *s.Longitude < -180 || *s.Longitude > 180) {
		return invalid("довгота має бути від −180 до 180")
	}
	if len(s.Orientations) > 0 && !(s.PacLimitKw > 0) {
		return invalid("ліміт інвертора має бути більшим за 0 кВт")
	}
	if math.IsNaN(s.GammaPctPerC) || s.GammaPctPerC < -2 || s.GammaPctPerC > 0 {
		return invalid("температурний коефіцієнт має бути від −2 до 0 %%/°C")
	}
	if s.WholeElevatorFrom != "" {
		if _, err := time.Parse("2006-01-02", s.WholeElevatorFrom); err != nil {
			return invalid("дата «весь елеватор з» має бути у форматі РРРР-ММ-ДД")
		}
	}
	seen := make(map[int]bool, len(s.Orientations))
	for _, o := range s.Orientations {
		if o.Idx < 1 {
			return invalid("номер орієнтації має бути від 1")
		}
		if seen[o.Idx] {
			return invalid("орієнтація №%d повторюється", o.Idx)
		}
		seen[o.Idx] = true
		if math.IsNaN(o.TiltDeg) || o.TiltDeg < 0 || o.TiltDeg > 90 {
			return invalid("орієнтація №%d: нахил має бути від 0 до 90°", o.Idx)
		}
		if math.IsNaN(o.AzimuthDeg) || o.AzimuthDeg < -180 || o.AzimuthDeg > 180 {
			return invalid("орієнтація №%d: азимут має бути від −180 до 180°", o.Idx)
		}
		if o.ModulesCount < 1 {
			return invalid("орієнтація №%d: кількість модулів має бути від 1", o.Idx)
		}
		if !(o.ModulePowerW > 0) || o.ModulePowerW > 1000 {
			return invalid("орієнтація №%d: потужність модуля має бути від 1 до 1000 Вт", o.Idx)
		}
		if len([]rune(o.Note)) > 200 {
			return invalid("орієнтація №%d: примітка довша за 200 символів", o.Idx)
		}
	}
	if s.Source == SourceModel && !s.Modelled() {
		return invalid("для власного розрахунку потрібні ліміт інвертора і хоча б одна увімкнена орієнтація")
	}
	return nil
}

// Change is one edit in the passport journal. Orientation is the
// plane's index, or 0 for a station-level field.
type Change struct {
	Field       string `json:"field"`
	Orientation int    `json:"orientation,omitempty"`
	From        any    `json:"from,omitempty"`
	To          any    `json:"to,omitempty"`
}

// HistoryEntry is one save of the passport.
type HistoryEntry struct {
	At      time.Time `json:"at"`
	By      string    `json:"by,omitempty"`
	Changes []Change  `json:"changes"`
}

// Diff lists what changed between two passports, field by field.
func Diff(before, after Station) []Change {
	var out []Change
	add := func(field string, orientation int, from, to any) {
		out = append(out, Change{Field: field, Orientation: orientation, From: from, To: to})
	}
	if before.Source != after.Source {
		add("source", 0, string(before.Source), string(after.Source))
	}
	if !sameOptional(before.Latitude, after.Latitude) {
		add("latitude", 0, optionalValue(before.Latitude), optionalValue(after.Latitude))
	}
	if !sameOptional(before.Longitude, after.Longitude) {
		add("longitude", 0, optionalValue(before.Longitude), optionalValue(after.Longitude))
	}
	if before.PacLimitKw != after.PacLimitKw {
		add("pac_limit_kw", 0, before.PacLimitKw, after.PacLimitKw)
	}
	if before.GammaPctPerC != after.GammaPctPerC {
		add("gamma_pct_per_c", 0, before.GammaPctPerC, after.GammaPctPerC)
	}
	if before.WholeElevatorFrom != after.WholeElevatorFrom {
		add("whole_elevator_from", 0, before.WholeElevatorFrom, after.WholeElevatorFrom)
	}

	was := make(map[int]Orientation, len(before.Orientations))
	for _, o := range before.Orientations {
		was[o.Idx] = o
	}
	now := make(map[int]Orientation, len(after.Orientations))
	for _, o := range after.Orientations {
		now[o.Idx] = o
	}
	idxs := make([]int, 0, len(was)+len(now))
	for idx := range was {
		idxs = append(idxs, idx)
	}
	for idx := range now {
		if _, ok := was[idx]; !ok {
			idxs = append(idxs, idx)
		}
	}
	sort.Ints(idxs)
	for _, idx := range idxs {
		b, hadB := was[idx]
		a, hasA := now[idx]
		switch {
		case !hadB:
			add("orientation_added", idx, nil, a)
		case !hasA:
			add("orientation_removed", idx, b, nil)
		default:
			if b.TiltDeg != a.TiltDeg {
				add("tilt_deg", idx, b.TiltDeg, a.TiltDeg)
			}
			if b.AzimuthDeg != a.AzimuthDeg {
				add("azimuth_deg", idx, b.AzimuthDeg, a.AzimuthDeg)
			}
			if b.ModulesCount != a.ModulesCount {
				add("modules_count", idx, b.ModulesCount, a.ModulesCount)
			}
			if b.ModulePowerW != a.ModulePowerW {
				add("module_power_w", idx, b.ModulePowerW, a.ModulePowerW)
			}
			if b.Enabled != a.Enabled {
				add("enabled", idx, b.Enabled, a.Enabled)
			}
			if b.Note != a.Note {
				add("note", idx, b.Note, a.Note)
			}
		}
	}
	return out
}

func sameOptional(a, b *float64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func optionalValue(v *float64) any {
	if v == nil {
		return nil
	}
	return *v
}
