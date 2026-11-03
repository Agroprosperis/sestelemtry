package pvplan

import (
	"errors"
	"math"
	"strings"
	"testing"
)

// The seed is the n8n orientation table without its duplicates: 29
// planes across the 7 elevator codes, plus DE's 5 copied onto de1. Its
// nameplates must add up to the flow's pdc_total_kwp per site, which
// is what proves no plane was dropped or doubled.
func TestSeedStationsMatchTheN8nTable(t *testing.T) {
	stations, err := SeedStations()
	if err != nil {
		t.Fatalf("SeedStations: %v", err)
	}
	want := map[string]struct {
		planes int
		kwp    float64
	}{
		"ab":  {7, 461.97},
		"ke":  {2, 531},
		"pe":  {6, 442.8},
		"ze":  {5, 605.12},
		"de":  {5, 605.12},
		"de1": {5, 605.12},
		"sm":  {3, 508.4},
		"pde": {1, 312.48},
	}
	if len(stations) != len(want) {
		t.Fatalf("seeded %d stations, want %d", len(stations), len(want))
	}
	for _, st := range stations {
		w, ok := want[st.OrganizationID]
		if !ok {
			t.Fatalf("unexpected seeded station %q", st.OrganizationID)
		}
		if len(st.Orientations) != w.planes {
			t.Errorf("%s: %d planes, want %d", st.OrganizationID, len(st.Orientations), w.planes)
		}
		if got := st.PdcTotalKwp(); math.Abs(got-w.kwp) > 0.001 {
			t.Errorf("%s: %.3f kWp, want %.3f", st.OrganizationID, got, w.kwp)
		}
		// Switching is the operator's call: seeding must not move any
		// station off n8n.
		if st.Source != SourceN8N {
			t.Errorf("%s: seeded on %q, want n8n", st.OrganizationID, st.Source)
		}
		// The duplicates that put the flow on the equator had no
		// coordinates; none may survive.
		if st.Latitude == nil || st.Longitude == nil || math.Abs(*st.Latitude) < 1 {
			t.Errorf("%s: coordinates %v/%v, want the site's", st.OrganizationID, st.Latitude, st.Longitude)
		}
		if !st.Modelled() {
			t.Errorf("%s: seed is not enough to run the model", st.OrganizationID)
		}
	}
}

func TestSeedFlagsDubovyazivskyAsTheWholeElevator(t *testing.T) {
	stations, err := SeedStations()
	if err != nil {
		t.Fatalf("SeedStations: %v", err)
	}
	for _, st := range stations {
		wholeFrom := ""
		if st.OrganizationID == "de" || st.OrganizationID == "de1" {
			wholeFrom = "2026-09-24"
		}
		if st.WholeElevatorFrom != wholeFrom {
			t.Errorf("%s: whole_elevator_from = %q, want %q", st.OrganizationID, st.WholeElevatorFrom, wholeFrom)
		}
	}
}

func TestCoversDay(t *testing.T) {
	st := Station{WholeElevatorFrom: "2026-09-24"}
	if !st.CoversDay("2026-09-23") || st.CoversDay("2026-09-24") || st.CoversDay("2026-10-01") {
		t.Fatal("the site is covered up to the day before whole_elevator_from only")
	}
	if !(Station{}).CoversDay("2026-10-01") {
		t.Fatal("a station without the date always covers itself")
	}
}

func TestValidateRejectsWhatTheModelCantRunOn(t *testing.T) {
	lat := 49.0
	good := pdeStation()
	cases := map[string]func(*Station){
		"unknown source":         func(s *Station) { s.Source = "excel" },
		"model without planes":   func(s *Station) { s.Orientations = nil },
		"half a coordinate pair": func(s *Station) { s.Latitude = &lat },
		"tilt past vertical":     func(s *Station) { s.Orientations[0].TiltDeg = 95 },
		"azimuth out of range":   func(s *Station) { s.Orientations[0].AzimuthDeg = 200 },
		"no modules":             func(s *Station) { s.Orientations[0].ModulesCount = 0 },
		"positive gamma":         func(s *Station) { s.GammaPctPerC = 0.3 },
		"duplicate plane":        func(s *Station) { s.Orientations = append(s.Orientations, s.Orientations[0]) },
		"bad whole-elevator day": func(s *Station) { s.WholeElevatorFrom = "24.09.2026" },
		"planes without a limit": func(s *Station) { s.PacLimitKw = 0 },
	}
	for name, spoil := range cases {
		t.Run(name, func(t *testing.T) {
			st := good
			st.Orientations = append([]Orientation(nil), good.Orientations...)
			spoil(&st)
			if err := st.Validate(); err == nil {
				t.Fatal("expected a validation error")
			}
		})
	}
	if err := good.Validate(); err != nil {
		t.Fatalf("a complete passport must pass: %v", err)
	}
}

// The n8n source doesn't need planes: an empty passport keeps a station
// the flow forecasts on the flow. A station the flow has no code for
// can't be on it at all.
func TestValidateTheN8NSourceNeedsOnlyAFlowCode(t *testing.T) {
	if err := (Station{OrganizationID: "ab", Source: SourceN8N}).Validate(); err != nil {
		t.Fatalf("empty n8n passport for ab: %v", err)
	}
	err := (Station{OrganizationID: "demo-org", Source: SourceN8N}).Validate()
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("n8n for demo-org: err = %v, want ErrInvalid", err)
	}
}

// Callers tell a bad passport from a failed save by ErrInvalid; the
// text stays the engineer-facing message.
func TestValidateErrorsMatchErrInvalid(t *testing.T) {
	st := pdeStation()
	st.Orientations[0].ModulesCount = 0
	err := st.Validate()
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
	if err.Error() != "орієнтація №1: кількість модулів має бути від 1" {
		t.Fatalf("message = %q", err.Error())
	}
}

func TestDiffListsEveryEdit(t *testing.T) {
	before := pdeStation()
	before.Source = SourceN8N
	after := pdeStation()
	after.PacLimitKw = 260
	after.Orientations = []Orientation{
		{Idx: 1, TiltDeg: 25, AzimuthDeg: -13.3, ModulesCount: 504, ModulePowerW: 620, Enabled: true},
		{Idx: 2, TiltDeg: 10, AzimuthDeg: 90, ModulesCount: 20, ModulePowerW: 620, Enabled: true},
	}
	got := map[string]Change{}
	for _, c := range Diff(before, after) {
		got[c.Field] = c
	}
	for _, field := range []string{"source", "pac_limit_kw", "tilt_deg", "orientation_added"} {
		if _, ok := got[field]; !ok {
			t.Errorf("missing %s in %v", field, got)
		}
	}
	if c := got["source"]; c.From != "n8n" || c.To != "model" {
		t.Errorf("source change = %+v, want n8n → model", c)
	}
	if c := got["orientation_added"]; c.Orientation != 2 {
		t.Errorf("added plane = %d, want 2", c.Orientation)
	}
	if len(Diff(after, after)) != 0 {
		t.Error("an unchanged passport must journal nothing")
	}
}

func TestValidateMessagesAreUkrainian(t *testing.T) {
	st := pdeStation()
	st.Orientations[0].TiltDeg = 95
	err := st.Validate()
	if err == nil || !strings.Contains(err.Error(), "нахил") {
		t.Fatalf("err = %v, want the engineer-facing message", err)
	}
}
