package dispatch

import (
	"fmt"
	"testing"
)

// Worst case for the desk preview: 24 known hours, a manual goal on
// every one of them (24 lexicographic stages + overflow + economics).
func BenchmarkSimulateAllHoursManual(b *testing.B) {
	site := zeSite()
	n := 24
	m := Model{Loads: make([]*float64, n), Commands: make([]*Command, n), Cfg: defaultCfg()}
	in := Inputs{Site: site, StartKwh: 900, PV: make([]float64, n), Rdn: make([]*float64, n)}
	types := []CommandType{CmdCover, CmdSolar, CmdTarget, CmdCap, CmdExport, CmdFixed}
	for i := 0; i < n; i++ {
		m.Loads[i] = kw(150 + float64(i%5)*60)
		if i >= 8 && i <= 17 {
			in.PV[i] = 450
		}
		in.Rdn[i] = kw(2 + float64(i%7))
		t := types[i%len(types)]
		c := Command{Type: t, Value: 200, ID: fmt.Sprintf("b%d", i)}
		if t == CmdTarget {
			c.Value = 60
		}
		if t == CmdFixed {
			c.Direction = "discharge"
		}
		m.Commands[i] = &c
	}
	for i := 0; i < b.N; i++ {
		if _, err := Simulate(in, m); err != nil {
			b.Fatal(err)
		}
	}
}
