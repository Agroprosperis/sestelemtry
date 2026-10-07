package dispatch

import (
	"math"
	"strings"
	"testing"
)

// Scenarios from the demo guide (ems-demo-guide.md §4, §7).

func kw(v float64) *float64 { return &v }

type scenario struct {
	loads    []float64
	pv       []float64
	rdn      []float64
	startPct float64
	cfg      Constraints
	commands map[int]Command // hour → command (ID = type, one block per type)
}

func (s scenario) run(t *testing.T) Result {
	t.Helper()
	site := zeSite()
	n := len(s.loads)
	m := Model{Loads: make([]*float64, n), Commands: make([]*Command, n), Cfg: s.cfg}
	in := Inputs{Site: site, StartKwh: site.CapacityKwh * s.startPct / 100, PV: make([]float64, n), Rdn: make([]*float64, n)}
	for i := 0; i < n; i++ {
		m.Loads[i] = kw(s.loads[i])
		if s.pv != nil {
			in.PV[i] = s.pv[i]
		}
		price := 5.0
		if s.rdn != nil {
			price = s.rdn[i]
		}
		in.Rdn[i] = kw(price)
	}
	for h, c := range s.commands {
		c := c
		if c.ID == "" {
			c.ID = string(c.Type)
		}
		m.Commands[h] = &c
	}
	res, err := Simulate(in, m)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func defaultCfg() Constraints {
	return Constraints{ReservePct: 20, GridCharge: true, EssSale: true, ImportCapKw: 1700, ExportCapKw: kw(800)}
}

func approx(t *testing.T, what string, got *float64, want float64) {
	t.Helper()
	if got == nil || math.Abs(*got-want) > 0.05 {
		t.Fatalf("%s = %v, want %v", what, deref(got), want)
	}
}

func deref(p *float64) any {
	if p == nil {
		return nil
	}
	return *p
}

// §4 table: load 200 kW, no PV, enough energy.
func TestGuideCoverVsFixedVsExport(t *testing.T) {
	cases := []struct {
		name            string
		cmd             Command
		discharge, expo float64
	}{
		{"cover 300", Command{Type: CmdCover, Value: 300}, 200, 0},
		{"fixed discharge 300", Command{Type: CmdFixed, Value: 300, Direction: "discharge"}, 300, 100},
		{"export 300", Command{Type: CmdExport, Value: 300}, 500, 300},
	}
	for _, c := range cases {
		res := scenario{loads: []float64{200}, startPct: 90, cfg: defaultCfg(), commands: map[int]Command{0: c.cmd}}.run(t)
		approx(t, c.name+" discharge", res.Hours[0].P, c.discharge)
		approx(t, c.name+" export", res.Hours[0].Export, c.expo)
		if len(res.Issues) != 0 {
			t.Fatalf("%s: unexpected issues %+v", c.name, res.Issues)
		}
	}
	// Load grows to 250: fixed 300 exports 50; export 300 needs 550.
	res := scenario{loads: []float64{250}, startPct: 90, cfg: defaultCfg(), commands: map[int]Command{0: {Type: CmdFixed, Value: 300, Direction: "discharge"}}}.run(t)
	approx(t, "fixed at 250 export", res.Hours[0].Export, 50)
	res = scenario{loads: []float64{250}, startPct: 90, cfg: defaultCfg(), commands: map[int]Command{0: {Type: CmdExport, Value: 300}}}.run(t)
	approx(t, "export 300 at 250 discharge", res.Hours[0].P, 550)
	// 100 kW PV at 200 kW load: export 300 needs 400 from the battery.
	res = scenario{loads: []float64{200}, pv: []float64{100}, startPct: 90, cfg: defaultCfg(), commands: map[int]Command{0: {Type: CmdExport, Value: 300}}}.run(t)
	approx(t, "export 300 with PV discharge", res.Hours[0].P, 400)
}

// §7: AUTO with an import cap of 150 kW at 400 kW load needs ≥ 250 kW.
func TestGuideImportCap(t *testing.T) {
	res := scenario{loads: []float64{400}, startPct: 80, cfg: defaultCfg(), commands: map[int]Command{0: {Type: CmdCap, Value: 150}}}.run(t)
	if *res.Hours[0].Import > 150.05 {
		t.Fatalf("import = %v, want ≤ 150", *res.Hours[0].Import)
	}
	if *res.Hours[0].P < 249.95 {
		t.Fatalf("discharge = %v, want ≥ 250", *res.Hours[0].P)
	}
}

// §7: charge to 80 % by 06:00 from 20 % needs ~1080 kWh at the input —
// more than 864 kW in one hour, so AUTO starts earlier.
func TestGuideTargetPrechargesEarlier(t *testing.T) {
	loads := []float64{100, 100, 100, 100, 100, 100}
	res := scenario{loads: loads, startPct: 20, cfg: defaultCfg(),
		commands: map[int]Command{5: {Type: CmdTarget, Value: 80}}}.run(t)
	if soc := *res.Hours[5].Soc; soc < 79.95 {
		t.Fatalf("SOC at 06:00 = %.2f, want 80", soc)
	}
	early := 0.0
	for i := 0; i < 5; i++ {
		early += math.Max(0, -*res.Hours[i].P)
	}
	if early < 100 {
		t.Fatalf("charge before 05:00 = %.1f kWh, want a pre-charge", early)
	}
	if len(res.Issues) != 0 {
		t.Fatalf("unexpected issues %+v", res.Issues)
	}
}

// §7: a full export ban keeps PCC export at zero even with PV surplus.
func TestGuideFullExportBan(t *testing.T) {
	cfg := defaultCfg()
	cfg.BlockExport = true
	res := scenario{loads: []float64{100, 100, 100}, pv: []float64{600, 600, 600}, rdn: []float64{10, 10, 10}, startPct: 85, cfg: cfg}.run(t)
	for i, h := range res.Hours {
		if *h.Export > 0.05 {
			t.Fatalf("hour %d exports %v with a full ban", i, *h.Export)
		}
	}
	if *res.Hours[2].Curtailed <= 0 {
		t.Fatal("a full battery must curtail PV under the ban")
	}
}

func TestGuideEssSaleOffLimitsFixedDischarge(t *testing.T) {
	cfg := defaultCfg()
	cfg.EssSale = false
	res := scenario{loads: []float64{200}, startPct: 90, cfg: cfg,
		commands: map[int]Command{0: {Type: CmdFixed, Value: 300, Direction: "discharge"}}}.run(t)
	approx(t, "discharge", res.Hours[0].P, 200)
	if len(res.Issues) != 1 || !strings.Contains(res.Issues[0].Text, "продаж енергії УЗЕ вимкнено") {
		t.Fatalf("issues = %+v, want the ESS-sale reason", res.Issues)
	}
}

func TestGuideGridChargeOffNeedsPV(t *testing.T) {
	cfg := defaultCfg()
	cfg.GridCharge = false
	res := scenario{loads: []float64{100}, startPct: 30, cfg: cfg,
		commands: map[int]Command{0: {Type: CmdFixed, Value: 300, Direction: "charge"}}}.run(t)
	approx(t, "charge", res.Hours[0].P, 0)
	if len(res.Issues) != 1 || !strings.Contains(res.Issues[0].Text, "заряд із мережі вимкнено") {
		t.Fatalf("issues = %+v, want the grid-charge reason", res.Issues)
	}
}

// §9: an earlier manual goal is not traded away for a later, pricier
// export — the earlier cover runs in full, the later export falls short.
func TestGuideEarlierCommandWins(t *testing.T) {
	cfg := defaultCfg()
	// 700 kWh above the 20 % reserve: the cover needs 600 kWh AC
	// (≈ 628 DC), the exports another 800 — enough for the cover only.
	start := 20 + 700.0/1720*100
	res := scenario{loads: []float64{300, 300, 0, 0}, rdn: []float64{5, 5, 15, 15}, startPct: start, cfg: cfg,
		commands: map[int]Command{
			0: {Type: CmdCover, Value: 300, ID: "cover"}, 1: {Type: CmdCover, Value: 300, ID: "cover"},
			2: {Type: CmdExport, Value: 400, ID: "export"}, 3: {Type: CmdExport, Value: 400, ID: "export"},
		}}.run(t)
	for i := 0; i < 2; i++ {
		if *res.Hours[i].P < 299.95 {
			t.Fatalf("hour %d cover = %v, want 300 (earlier goal first)", i, *res.Hours[i].P)
		}
	}
	short := false
	for _, is := range res.Issues {
		if is.Hour >= 2 && strings.Contains(is.Text, "експорт PCC") {
			short = true
		}
		if is.Hour < 2 {
			t.Fatalf("earlier hour %d has an issue: %s", is.Hour, is.Text)
		}
	}
	if !short {
		t.Fatalf("later export should fall short, issues %+v", res.Issues)
	}
}

func TestKnownHoursStopsAtLoadGapAndMissingPrice(t *testing.T) {
	site := zeSite()
	m := Model{Loads: []*float64{kw(100), kw(100), nil, kw(100)}, Commands: make([]*Command, 4), Cfg: defaultCfg()}
	in := Inputs{Site: site, StartKwh: 800, PV: make([]float64, 4), Rdn: []*float64{kw(5), kw(5), kw(5), kw(5)}}
	if got := KnownHours(in, m); got != 2 {
		t.Fatalf("known = %d, want 2 (gap at hour 2)", got)
	}
	m.Loads[2] = kw(100)
	in.Rdn[3] = nil
	if got := KnownHours(in, m); got != 3 {
		t.Fatalf("known = %d, want 3 (no RDN price at hour 3)", got)
	}
	res, err := Simulate(in, m)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Hours[3].Unknown || res.Hours[3].Before == nil || res.Hours[2].Unknown {
		t.Fatalf("hours = %+v, want hour 3 unknown with the SOC hand-off", res.Hours)
	}
}

func TestSplitFlow(t *testing.T) {
	parts := SplitFlow(-300, 500, 300)
	if len(parts) != 2 || parts[0].Key != "solar" || parts[0].Power != -200 || parts[1].Key != "grid" || parts[1].Power != -100 {
		t.Fatalf("charge split = %+v", parts)
	}
	parts = SplitFlow(500, 0, 200)
	if len(parts) != 2 || parts[0] != (FlowPart{"load", 200}) || parts[1] != (FlowPart{"export", 300}) {
		t.Fatalf("discharge split = %+v", parts)
	}
	if parts := SplitFlow(0, 100, 100); len(parts) != 0 {
		t.Fatalf("idle split = %+v", parts)
	}
}

func TestFmtMatchesUkLocale(t *testing.T) {
	for in, want := range map[float64]string{1700: "1\u00a0700", 864.25: "864,3", 12.04: "12", 0.05: "0,1", 1234567.89: "1\u00a0234\u00a0567,9", -3.5: "-3,5", 0: "0"} {
		if got := Fmt(in); got != want {
			t.Errorf("Fmt(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestValidate(t *testing.T) {
	site := zeSite()
	cfg := defaultCfg()
	ceiling := 850.0
	if msg := ValidateConstraints(site, cfg, &ceiling); msg != "" {
		t.Fatalf("default constraints: %s", msg)
	}
	bad := cfg
	bad.ExportCapKw = kw(900)
	if msg := ValidateConstraints(site, bad, &ceiling); !strings.Contains(msg, "850") {
		t.Fatalf("over-ceiling export cap: %q", msg)
	}
	noRegime := cfg
	noRegime.ExportCapKw = nil
	if msg := ValidateConstraints(site, noRegime, nil); !strings.Contains(msg, "ліміт експорту") {
		t.Fatalf("ESS sale without an export cap: %q", msg)
	}
	noRegime.EssSale = false
	if msg := ValidateConstraints(site, noRegime, nil); msg != "" {
		t.Fatalf("no regime, no sale: %q", msg)
	}
	if msg := ValidateCommand(site, cfg, Command{Type: CmdCover, Value: 900}); !strings.Contains(msg, "864") {
		t.Fatalf("cover above passport: %q", msg)
	}
	if msg := ValidateCommand(site, cfg, Command{Type: CmdTarget, Value: 15}); !strings.Contains(msg, "резервом") {
		t.Fatalf("target below reserve: %q", msg)
	}
	if msg := ValidateCommand(site, noRegime, Command{Type: CmdExport, Value: 100}); msg == "" {
		t.Fatal("export without an export cap must be rejected")
	}
	if msg := ValidateCommand(site, cfg, Command{Type: CmdFixed, Value: 100}); !strings.Contains(msg, "напрямок") {
		t.Fatalf("fixed without direction: %q", msg)
	}
}
