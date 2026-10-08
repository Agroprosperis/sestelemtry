package api

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/nesh/sestelemetry/internal/dispatch"
	"github.com/nesh/sestelemetry/internal/economics"
	"github.com/nesh/sestelemetry/internal/storage"
)

func dispatchTestEnv(t *testing.T) *dispatchEnv {
	t.Helper()
	loc, _ := time.LoadLocation("Europe/Kyiv")
	start := time.Date(2026, 10, 7, 18, 0, 0, 0, time.UTC)
	ceiling := 850.0
	env := &dispatchEnv{
		siteID: "ze", loc: loc, tz: "Europe/Kyiv", now: start.Add(17 * time.Minute), start: start,
		site: dispatch.Site{
			CapacityKwh: 1720, ChargeKw: 864, DischargeKw: 864, ImportKw: 1700, SocMinPct: 20, SocMaxPct: 90,
			Tariffs: economics.Tariffs{DistributionUahPerKwh: 2.75218, TransmissionUahPerKwh: 0.74291, ExportDiscount: 0.05, DegradationUahPerKwh: 0.6, RoundtripEfficiency: 0.913},
		},
		importSet: true, exportRegime: exportRegimeSelfProduction, exportCeiling: &ceiling,
	}
	env.params.PvRatedKw = 600
	env.defaults = dispatch.Constraints{ReservePct: 20, GridCharge: true, EssSale: true, ImportCapKw: 1700, ExportCapKw: copyKw(&ceiling)}
	env.inputs = dispatch.Inputs{Site: env.site, StartKwh: 860, PV: make([]float64, dispatchFutureHours), Rdn: make([]*float64, dispatchFutureHours)}
	for i := range env.inputs.Rdn {
		p := 5.0 + float64(i%6)
		env.inputs.Rdn[i] = &p
	}
	return env
}

func kwp(v float64) *float64 { return &v }

func TestDispatchStoredModelRoundTrip(t *testing.T) {
	start := time.Date(2026, 10, 7, 18, 0, 0, 0, time.UTC)
	m := dispatch.Model{Loads: make([]*float64, 24), Commands: make([]*dispatch.Command, 24), Cfg: dispatch.Constraints{ReservePct: 25}}
	m.Loads[0], m.Loads[1], m.Loads[2] = kwp(200), kwp(0), kwp(150)
	cover := dispatch.Command{Type: dispatch.CmdCover, Value: 300, ID: "b-cover"}
	for i := 0; i < 3; i++ {
		c := cover
		m.Commands[i] = &c
	}
	target := dispatch.Command{Type: dispatch.CmdTarget, Value: 80, ID: "b-target"}
	m.Commands[5] = &target

	sm := storedFromModel(m, start)
	if len(sm.Loads) != 3 || len(sm.Blocks) != 2 {
		t.Fatalf("stored = %+v, want 3 loads (0 kept) and 2 blocks", sm)
	}
	if !sm.Blocks[0].Until.Equal(start.Add(3 * time.Hour)) {
		t.Fatalf("cover block until = %v, want +3h", sm.Blocks[0].Until)
	}
	raw, _ := json.Marshal(sm)
	var back dispatchStoredModel
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	// Two hours later: the first two hours are gone, the cover block
	// keeps its last hour, the target sits at index 3.
	later := modelFromStored(back, start.Add(2*time.Hour), 24)
	if later.Loads[0] == nil || *later.Loads[0] != 150 || later.Loads[1] != nil {
		t.Fatalf("loads after 2 h = %v", later.Loads[:3])
	}
	if later.Commands[0] == nil || later.Commands[0].Type != dispatch.CmdCover || later.Commands[1] != nil {
		t.Fatalf("cover remainder = %+v %+v", later.Commands[0], later.Commands[1])
	}
	if later.Commands[3] == nil || later.Commands[3].Type != dispatch.CmdTarget {
		t.Fatalf("target = %+v, want at index 3", later.Commands[3])
	}
}

func TestDispatchDraftAlignsToServerHour(t *testing.T) {
	start := time.Date(2026, 10, 7, 18, 0, 0, 0, time.UTC)
	req := dispatchDraftRequest{StartHour: start.Add(-time.Hour), Loads: []*float64{kwp(1), kwp(2), kwp(3)}}
	m, err := req.model(start)
	if err != nil {
		t.Fatal(err)
	}
	if *m.Loads[0] != 2 || *m.Loads[1] != 3 || m.Loads[2] != nil {
		t.Fatalf("aligned loads = %v, want the elapsed hour dropped", m.Loads[:3])
	}
	if _, err := (dispatchDraftRequest{StartHour: start.Add(time.Hour)}).model(start); err == nil {
		t.Fatal("a draft from the future must be rejected")
	}
}

func TestSanitizeConstraintsFollowsEnvelope(t *testing.T) {
	env := dispatchTestEnv(t)
	cfg := sanitizeConstraints(env, dispatch.Constraints{ReservePct: 5, ImportCapKw: 5000, EssSale: true, ExportCapKw: kwp(1700)})
	if cfg.ReservePct != 20 || cfg.ImportCapKw != 1700 || *cfg.ExportCapKw != 850 {
		t.Fatalf("sanitized = %+v, want reserve 20, import 1700, export 850", cfg)
	}
	// A cap the operator cleared stays cleared: export off, not the ceiling.
	cfg = sanitizeConstraints(env, dispatch.Constraints{ReservePct: 30, ImportCapKw: 900, EssSale: true})
	if cfg.ExportCapKw != nil || cfg.EssSale {
		t.Fatalf("cleared cap: %+v, want no cap and ESS sale off", cfg)
	}
	env.exportCeiling = nil
	cfg = sanitizeConstraints(env, dispatch.Constraints{ReservePct: 30, ImportCapKw: 900, EssSale: true, ExportCapKw: kwp(400)})
	if cfg.ExportCapKw != nil || cfg.EssSale {
		t.Fatalf("no regime: %+v, want export cap cleared and ESS sale off", cfg)
	}
}

func TestExportCeilingByRegime(t *testing.T) {
	s := &EdgeSiteSettings{GridImportKw: 1700}
	if s.exportCeilingKw() != nil {
		t.Fatal("no regime must give no ceiling")
	}
	s.ExportRegime = exportRegimeSelfProduction
	if v := s.exportCeilingKw(); v == nil || *v != 850 {
		t.Fatalf("self production = %v, want 850", v)
	}
	s.ExportRegime = exportRegimeStorage
	s.ExportPccKw = 1200
	if v := s.exportCeilingKw(); v == nil || *v != 1200 {
		t.Fatalf("storage capped by PCC = %v, want 1200", v)
	}
	if err := (&EdgeSiteSettings{ExportRegime: "half"}).validate(); err == nil {
		t.Fatal("unknown regime must be rejected")
	}
}

func TestBuildDispatchManifest(t *testing.T) {
	env := dispatchTestEnv(t)
	m := dispatch.Model{Loads: make([]*float64, 24), Commands: make([]*dispatch.Command, 24), Cfg: env.defaults}
	for i := 0; i < 4; i++ {
		m.Loads[i] = kwp(250)
	}
	exp := dispatch.Command{Type: dispatch.CmdExport, Value: 300, ID: "b-exp"}
	m.Commands[1] = &exp
	res, err := dispatch.Simulate(env.inputs, m)
	if err != nil {
		t.Fatal(err)
	}
	doc, run := buildDispatchManifest(env, m, res, 3)
	if doc.Plan == nil || len(doc.Plan.Intervals) != 4 || len(run) != 4 {
		t.Fatalf("plan = %+v, want 4 known hours", doc.Plan)
	}
	if doc.Plan.Intervals[1].Action != "export" || doc.Plan.Intervals[1].EssKw < 549.9 {
		t.Fatalf("export hour = %+v, want ~550 kW export", doc.Plan.Intervals[1])
	}
	if !doc.ExportAllowed || doc.Source != "dispatch" || doc.SocPolicy.MinEconomicPct != 20 {
		t.Fatalf("manifest header = %+v", doc)
	}
	again, _ := buildDispatchManifest(env, m, res, 3)
	if again.ManifestID != doc.ManifestID {
		t.Fatal("same plan must hash to the same manifest id")
	}
	if run[1].Command == nil || run[1].ExportKw < 299.9 {
		t.Fatalf("run hour 1 = %+v", run[1])
	}

	// No known load → empty plan: the edge runs self-consumption.
	empty := dispatch.Model{Loads: make([]*float64, 24), Commands: make([]*dispatch.Command, 24), Cfg: env.defaults}
	res, _ = dispatch.Simulate(env.inputs, empty)
	doc, run = buildDispatchManifest(env, empty, res, 0)
	if doc.Plan != nil || len(run) != 0 {
		t.Fatalf("empty model published a plan: %+v", doc.Plan)
	}
}

func TestDispatchAction(t *testing.T) {
	cases := []struct {
		p, export, load, pv float64
		want                string
	}{
		{300, 100, 200, 0, "export"},
		{200, 0, 200, 0, "discharge"},
		{-150, 0, 100, 0, "charge"},
		{0.2, 0, 100, 0, "hold"},
	}
	for _, c := range cases {
		if got := dispatchAction(c.p, c.export, c.load, c.pv); got != c.want {
			t.Errorf("dispatchAction(%v) = %s, want %s", c, got, c.want)
		}
	}
}

func TestPlansInForcePicksTheRunBeforeEachHour(t *testing.T) {
	h0 := time.Date(2026, 10, 7, 10, 0, 0, 0, time.UTC)
	mk := func(start time.Time, runAt time.Time, ess ...float64) storage.DispatchRun {
		var hours []dispatchRunHour
		for i, v := range ess {
			hours = append(hours, dispatchRunHour{TS: start.Add(time.Duration(i) * time.Hour), EssKw: v})
		}
		raw, _ := json.Marshal(hours)
		return storage.DispatchRun{StartHour: start, RunAt: runAt, Hours: raw}
	}
	runs := []storage.DispatchRun{
		mk(h0, h0.Add(5*time.Minute), 100, 200, 300),                // 10:05 plan for 10..12
		mk(h0.Add(time.Hour), h0.Add(70*time.Minute), 222),          // 11:10 re-plan, covers 11 only
		mk(h0.Add(2*time.Hour), h0.Add(2*time.Hour-time.Minute), 9), // 11:59 plan from 12
	}
	got := plansInForce(runs, []time.Time{h0, h0.Add(time.Hour), h0.Add(2 * time.Hour), h0.Add(3 * time.Hour)})
	if _, ok := got[h0]; ok {
		t.Fatal("10:00 had no plan published before it started")
	}
	if got[h0.Add(time.Hour)].EssKw != 200 {
		t.Fatalf("11:00 = %v, want 200 (the 11:10 re-plan came after the hour started)", got[h0.Add(time.Hour)].EssKw)
	}
	if got[h0.Add(2*time.Hour)].EssKw != 9 {
		t.Fatalf("12:00 = %v, want 9 from the 11:59 run", got[h0.Add(2*time.Hour)].EssKw)
	}
	if _, ok := got[h0.Add(3*time.Hour)]; ok {
		t.Fatal("13:00 is past the newest run's horizon: no plan")
	}
}

func TestValidateModelNamesTheHour(t *testing.T) {
	env := dispatchTestEnv(t)
	m := dispatch.Model{Loads: make([]*float64, 24), Commands: make([]*dispatch.Command, 24), Cfg: env.defaults}
	bad := dispatch.Command{Type: dispatch.CmdCover, Value: 900, ID: "x"}
	m.Commands[2] = &bad
	msg := validateModel(env, m)
	if msg == "" || msg[:5] != "23:00" {
		t.Fatalf("message = %q, want it to start with the local hour 23:00", msg)
	}
}
