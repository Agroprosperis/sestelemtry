package dispatch

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"os/exec"
	"reflect"
	"testing"

	"github.com/nesh/sestelemetry/internal/economics"
)

// Parity with the approved mockup: the same random models through the
// vendored JS (testdata/desk_lp.js) and through this package must give
// the same plan, SOC, PCC exchange and reasons.

type jsCase struct {
	Tariffs  map[string]any `json:"tariffs"`
	Passport map[string]any `json:"passport"`
	Model    jsModel        `json:"model"`
	PV       []float64      `json:"pv"`
	Price    []float64      `json:"price"`
	Start    float64        `json:"start"`
}

type jsModel struct {
	Loads    []*float64     `json:"loads"`
	Commands []*jsCommand   `json:"commands"`
	Cfg      map[string]any `json:"cfg"`
}

type jsCommand struct {
	S         string  `json:"s"`
	V         float64 `json:"v"`
	Direction string  `json:"direction"`
	ID        string  `json:"id"`
}

type jsHour struct {
	P         *float64 `json:"p"`
	Soc       *float64 `json:"soc"`
	Import    *float64 `json:"import"`
	Export    *float64 `json:"export"`
	Curtailed *float64 `json:"curtailed"`
	Unknown   bool     `json:"unknown"`
	Reasons   []string `json:"reasons"`
}

type jsResult struct {
	Known int      `json:"known"`
	Hours []jsHour `json:"hours"`
}

func zeSite() Site {
	return Site{
		CapacityKwh: 1720, ChargeKw: 864, DischargeKw: 864, ImportKw: 1700,
		SocMinPct: 10, SocMaxPct: 90,
		Tariffs: economics.Tariffs{
			DistributionUahPerKwh: 2.75218, TransmissionUahPerKwh: 0.74291,
			SupplierMarginMode: "abs", ExportDiscount: 0.05,
			DegradationUahPerKwh: 0.6, VatRate: 0.2, RoundtripEfficiency: 0.913,
		},
	}
}

func randomCase(r *rand.Rand, site Site) (Inputs, Model, jsCase) {
	hours := 6 + r.Intn(19)
	known := r.Intn(hours + 1)
	loads := make([]*float64, hours)
	for i := 0; i < hours; i++ {
		if i < known || (i > known && r.Intn(3) == 0) {
			v := math.Round(r.Float64()*600/10) * 10
			if r.Intn(8) == 0 {
				v = 0
			}
			loads[i] = &v
		}
	}
	pv := make([]float64, hours)
	price := make([]float64, hours)
	rdn := make([]*float64, hours)
	pvScale := r.Float64() * 650
	start := r.Intn(24)
	for i := 0; i < hours; i++ {
		h := (start + i) % 24
		if h >= 7 && h <= 18 {
			pv[i] = math.Round(pvScale*math.Sin(math.Pi*float64(h-6)/13)*10) / 10
		}
		price[i] = math.Round((0.01+r.Float64()*15)*100) / 100
		p := price[i]
		rdn[i] = &p
	}
	reserve := []float64{10, 20, 30, 50}[r.Intn(4)]
	cfg := Constraints{
		ReservePct:  reserve,
		GridCharge:  r.Intn(4) != 0,
		EssSale:     r.Intn(3) != 0,
		BlockExport: r.Intn(5) == 0,
		ImportCapKw: math.Round((100+r.Float64()*1600)/10) * 10,
	}
	var exportCap *float64
	if r.Intn(5) != 0 {
		v := math.Round(r.Float64()*850/10) * 10
		exportCap = &v
	}
	cfg.ExportCapKw = exportCap

	commands := make([]*Command, hours)
	jsCommands := make([]*jsCommand, hours)
	types := []CommandType{CmdCover, CmdSolar, CmdTarget, CmdCap, CmdExport, CmdFixed, CmdHold}
	for b := 0; b < r.Intn(4); b++ {
		from := r.Intn(hours)
		to := from + 1 + r.Intn(4)
		if to > hours {
			to = hours
		}
		t := types[r.Intn(len(types))]
		cmd := Command{Type: t, ID: fmt.Sprintf("block-%d-%d-%s", from, to, t)}
		switch t {
		case CmdCover, CmdSolar, CmdFixed:
			cmd.Value = math.Round(r.Float64()*864/10) * 10
		case CmdTarget:
			cmd.Value = reserve + math.Round(r.Float64()*(90-reserve)/5)*5
		case CmdCap:
			cmd.Value = math.Round(r.Float64()*1700/10) * 10
		case CmdExport:
			cmd.Value = math.Round(r.Float64()*850/10) * 10
		}
		if t == CmdFixed {
			cmd.Direction = []string{"charge", "discharge"}[r.Intn(2)]
		}
		for i := from; i < to; i++ {
			c := cmd
			commands[i] = &c
			jsCommands[i] = &jsCommand{S: string(c.Type), V: c.Value, Direction: c.Direction, ID: c.ID}
		}
	}
	startKwh := site.CapacityKwh * (math.Max(site.SocMinPct, reserve) + r.Float64()*(site.SocMaxPct-math.Max(site.SocMinPct, reserve))) / 100

	in := Inputs{Site: site, StartKwh: startKwh, PV: pv, Rdn: rdn}
	m := Model{Loads: loads, Commands: commands, Cfg: cfg}
	var jsExport any
	if exportCap != nil {
		jsExport = *exportCap
	}
	t := site.Tariffs
	c := jsCase{
		Tariffs: map[string]any{
			"distribution": t.DistributionUahPerKwh, "transmission": t.TransmissionUahPerKwh,
			"supplierMargin": t.SupplierMarginUahPerKwh, "supplierMode": "abs", "supplierPct": 0,
			"otherFees": t.OtherFeesUahPerKwh, "exportDiscount": t.ExportDiscount,
			"degradation": t.DegradationUahPerKwh, "includeVat": t.IncludeVat, "vatRate": t.VatRate,
			"roundtrip": t.RoundtripEfficiency,
		},
		Passport: map[string]any{
			"capacityKwh": site.CapacityKwh, "chargeKw": site.ChargeKw, "dischargeKw": site.DischargeKw,
			"importKw": site.ImportKw, "socMin": site.SocMinPct, "socMax": site.SocMaxPct,
		},
		Model: jsModel{Loads: loads, Commands: jsCommands, Cfg: map[string]any{
			"reserve": cfg.ReservePct, "grid": cfg.GridCharge, "export": cfg.EssSale,
			"blockExport": cfg.BlockExport, "importCap": cfg.ImportCapKw, "exportCap": jsExport,
		}},
		PV: pv, Price: price, Start: startKwh,
	}
	return in, m, c
}

func TestParityWithMockup(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed: parity with the mockup not checked")
	}
	r := rand.New(rand.NewSource(20261007))
	site := zeSite()
	const n = 60
	ins := make([]Inputs, n)
	models := make([]Model, n)
	cases := make([]jsCase, n)
	for i := range cases {
		ins[i], models[i], cases[i] = randomCase(r, site)
	}
	payload, _ := json.Marshal(map[string]any{"cases": cases})
	cmd := exec.Command(node, "testdata/desk_lp.js")
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("node harness: %v\n%s", err, stderr.String())
	}
	var js []jsResult
	if err := json.Unmarshal(out, &js); err != nil {
		t.Fatal(err)
	}
	const tol = 1e-6
	near := func(a, b *float64) bool {
		if a == nil || b == nil {
			return a == nil && b == nil
		}
		return math.Abs(*a-*b) <= tol*math.Max(1, math.Abs(*b))
	}
	compared, withReasons := 0, 0
	defer func() {
		if !t.Failed() && (compared < 200 || withReasons == 0) {
			t.Fatalf("parity is vacuous: %d known hours compared, %d with reasons", compared, withReasons)
		}
	}()
	for i := range cases {
		got, err := Simulate(ins[i], models[i])
		if err != nil {
			t.Fatalf("case %d: %v", i, err)
		}
		want := js[i]
		if got.KnownHours != want.Known {
			t.Fatalf("case %d: known hours %d, mockup %d", i, got.KnownHours, want.Known)
		}
		for h, w := range want.Hours {
			g := got.Hours[h]
			if g.Unknown != w.Unknown {
				t.Fatalf("case %d hour %d: unknown %v, mockup %v", i, h, g.Unknown, w.Unknown)
			}
			if w.Unknown {
				continue
			}
			compared++
			if len(w.Reasons) > 0 {
				withReasons++
			}
			if !near(g.P, w.P) || !near(g.Soc, w.Soc) || !near(g.Import, w.Import) || !near(g.Export, w.Export) || !near(g.Curtailed, w.Curtailed) {
				t.Fatalf("case %d hour %d: go p=%v soc=%v imp=%v exp=%v curt=%v; mockup p=%v soc=%v imp=%v exp=%v curt=%v",
					i, h, *g.P, *g.Soc, *g.Import, *g.Export, *g.Curtailed, *w.P, *w.Soc, *w.Import, *w.Export, *w.Curtailed)
			}
			if !reflect.DeepEqual(g.Reasons, nonNil(w.Reasons)) {
				t.Fatalf("case %d hour %d: reasons %q, mockup %q", i, h, g.Reasons, w.Reasons)
			}
		}
	}
}
