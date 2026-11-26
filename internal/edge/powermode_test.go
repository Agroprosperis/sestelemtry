package edge

import (
	"context"
	"testing"
	"time"
)

// encombiTick is a fresh Encombi reading on top of the usual tick.
func encombiTick(bits uint16, busV, genKw float64) Tick {
	return controlTick(map[string]float64{
		keyEncombiDI: float64(bits), keyGenAlive: 1, keyGenPowerKw: genKw,
		keyBusVoltageA: busV, keyBusVoltageB: busV, keyBusVoltageC: busV,
	})
}

func TestClassifySupply(t *testing.T) {
	cases := []struct {
		name  string
		bits  uint16
		busV  float64
		alive float64
		want  string
	}{
		// 26.11.2026 13:15, from the IoT2050: T1, section, ESS, RN1, RN2, PQ.
		{"grid via T1", 0x01CE, 228, 1, SupplyGrid},
		// 15.11.2026 20:05:22: the grid back on T1.
		{"grid back", 0x014E, 228, 1, SupplyGrid},
		{"grid via T2", 1<<diT2 | 1<<diRN2 | 1<<diPQ, 228, 1, SupplyGrid},
		// 15.11.2026 19:55–20:00: island → no voltage → generator.
		{"island", 0x020C, 229, 1, SupplyIsland},
		{"island stopped, PQ", 0x010C, 0, 1, SupplyBlackout},
		{"RN3 up, breaker open", 0x050C, 0, 1, SupplyBlackout},
		{"generator on the bus", 0x052C, 231, 1, SupplyGenerator},
		{"generator breaker on a dead bus", 0x052C, 0, 1, SupplyBlackout},
		// Relays before the incomers: live grid behind open 1QF/2QF.
		{"relays without incomers", 1<<diRN1 | 1<<diRN2 | 1<<diVSG | 1<<diESS, 0, 1, SupplyBlackout},
		{"relays without incomers, live bus", 1<<diRN1 | 1<<diRN2 | 1<<diVSG | 1<<diESS, 230, 1, SupplyIsland},
		{"grid and generator", 0x01CE | 1<<diGen, 228, 1, SupplyTransition},
		{"meter offline", 0x01CE, 228, 0, SupplyUnknown},
	}
	for _, c := range cases {
		tk := encombiTick(c.bits, c.busV, 0)
		tk.Values[keyGenAlive] = c.alive
		if got := classifySupply(supplyInputsOf(tk), 180); got != c.want {
			t.Errorf("%s (0x%04X, %g V): %s, want %s", c.name, c.bits, c.busV, got, c.want)
		}
	}
	if got := classifySupply(supplyInputsOf(controlTick(nil)), 180); got != SupplyUnknown {
		t.Errorf("no Encombi reading: %s", got)
	}
}

func TestSwitchPosition(t *testing.T) {
	for bits, want := range map[uint16]string{
		0x01CE:                 SwitchPQ,
		0x020C:                 SwitchVSG,
		0x000C:                 SwitchOff,
		1<<diPQ | 1<<diVSG | 2: SwitchUnknown,
	} {
		if got := switchPosition(supplyInputsOf(encombiTick(bits, 230, 0))); got != want {
			t.Errorf("0x%04X: %s, want %s", bits, got, want)
		}
	}
	if got := switchPosition(supplyInputsOf(controlTick(nil))); got != SwitchUnknown {
		t.Errorf("no reading: %s", got)
	}
}

// §4.2: a new mode is taken once it holds 2 s; a shorter blink is not.
func TestSupplyTrackerDebounce(t *testing.T) {
	tr := newSupplyTracker(2 * time.Second)
	at := func(s float64) time.Time { return testTS.Add(time.Duration(s * float64(time.Second))) }
	if ch, _, _, _ := tr.observe(at(0), SupplyGrid, SwitchPQ); ch || tr.mode != SupplyUnknown {
		t.Fatalf("taken at once: %s", tr.mode)
	}
	if ch, _, prev, _ := tr.observe(at(2), SupplyGrid, SwitchPQ); !ch || tr.mode != SupplyGrid || prev != SupplyUnknown {
		t.Fatalf("after 2 s: %v %s %s", ch, tr.mode, prev)
	}
	tr.observe(at(3), SupplyBlackout, SwitchPQ)
	tr.observe(at(4.5), SupplyBlackout, SwitchPQ)
	if ch, _, _, _ := tr.observe(at(4.9), SupplyGrid, SwitchPQ); ch || tr.mode != SupplyGrid {
		t.Fatalf("a 1.9 s blink changed the mode: %s", tr.mode)
	}
	tr.observe(at(10), SupplyBlackout, SwitchVSG)
	ch, sw, _, prevSw := tr.observe(at(12), SupplyBlackout, SwitchVSG)
	if !ch || sw || tr.mode != SupplyBlackout || !tr.modeSince.Equal(at(10)) || prevSw != SwitchVSG {
		t.Fatalf("blackout: %v %v %s since %v", ch, sw, tr.mode, tr.modeSince)
	}
	st := tr.state(supplyInputs{})
	if !st.SwitchHeld(at(15), SwitchVSG, 5*time.Second) || st.SwitchHeld(at(14), SwitchVSG, 5*time.Second) {
		t.Fatal("switch held is counted from its change")
	}
}

func TestSupplyStatusOffGrid(t *testing.T) {
	cfg := zeCfg(t)
	tk := encombiTick(0x052C, 231, 300)
	tk.PVPowerKw, tk.ESSPowerKw = f64ptr(50), f64ptr(-100)
	st := SupplyState{Mode: SupplyGenerator, Switch: SwitchPQ, Fresh: true, Bits: 0x052C, GenPowerKw: f64ptr(300), BusVoltageV: f64ptr(231)}
	out := buildSupplyStatus(st, tk, ControllerEMS, cfg, 1720)
	if out.ModeLabel != "Генератор" || out.SwitchLabel != "PQ" || out.ControllerLabel != "EMS (запис увімкнено)" {
		t.Fatalf("labels: %+v", out)
	}
	if out.LoadKw == nil || *out.LoadKw != 250 || out.GenLoadPct == nil || *out.GenLoadPct != 41 || out.GenMinPct != 30 {
		t.Fatalf("load %v, generator %v %%", out.LoadKw, out.GenLoadPct)
	}
	// 50 % → 20 % of 1720 kWh at 200 kW net: 154.8 min.
	if out.AutonomyMin == nil || *out.AutonomyMin != 155 {
		t.Fatalf("autonomy %v", out.AutonomyMin)
	}
	if !out.Inputs["gen"] || !out.Inputs["rn3"] || out.Inputs["t1"] || out.DIBits == nil || *out.DIBits != 0x052C {
		t.Fatalf("inputs %+v", out.Inputs)
	}
	unknown := buildSupplyStatus(SupplyState{Mode: SupplyUnknown, Switch: SwitchUnknown}, controlTick(nil), ControllerEncombi, cfg, 1720)
	if unknown.Restriction == "" || unknown.DIBits != nil || unknown.SwitchLabel != "не читається" {
		t.Fatalf("unknown: %+v", unknown)
	}
}

// With encombi off nothing changes: no keys in the tick, no status line.
func TestObserveSupplyOffIsANoop(t *testing.T) {
	s := writeService(t, zeCfg(t))
	r := encombiReading{At: testTS, DIBits: 0x01CE, Alive: true, BusV: [3]float64{228, 230, 229}}
	s.lastEncombi.Store(&r)
	tk := controlTick(nil)
	s.observeSupply(context.Background(), testTS, &tk)
	if _, ok := tk.Values[keyEncombiDI]; ok || s.publishSupply(tk) != nil || s.buildHealth(testTS).Supply != nil {
		t.Fatal("encombi off still fed the tick or the status line")
	}
}

func TestObserveSupplyTakesTheMode(t *testing.T) {
	cfg := zeCfg(t)
	cfg.Encombi.Enabled, cfg.Encombi.Host = true, "10.28.40.103"
	s := writeService(t, cfg)
	s.supply = newSupplyTracker(cfg.Encombi.Debounce)
	for i := 0; i <= 3; i++ {
		now := testTS.Add(time.Duration(i) * time.Second)
		r := encombiReading{At: now, DIBits: 0x01CE, Alive: true, BusV: [3]float64{228, 230, 229}}
		s.lastEncombi.Store(&r)
		tk := controlTick(nil)
		s.observeSupply(context.Background(), now, &tk)
		if tk.Values[keyEncombiDI] != 0x01CE || tk.Values[keyBusVoltageB] != 230 {
			t.Fatalf("tick values %v", tk.Values)
		}
	}
	st := s.publishSupply(controlTick(nil))
	if st == nil || st.Mode != SupplyGrid || st.Controller != ControllerEncombi || s.buildHealth(testTS).Supply != st {
		t.Fatalf("status %+v", st)
	}
	// A reading older than stale_after is no reading: unknown after 2 s.
	for i := 4; i <= 12; i++ {
		tk := controlTick(nil)
		s.observeSupply(context.Background(), testTS.Add(time.Duration(i)*time.Second), &tk)
		if _, ok := tk.Values[keyEncombiDI]; ok && i > 8 {
			t.Fatalf("stale reading at %d s still in the tick", i)
		}
	}
	if s.supplyNow.Mode != SupplyUnknown {
		t.Fatalf("stale data: %s", s.supplyNow.Mode)
	}
}
