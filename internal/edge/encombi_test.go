package edge

import (
	"context"
	"encoding/binary"
	"errors"
	"testing"
	"time"
)

// fakeEncombi answers FC04 from a register map and records the reads.
type fakeEncombi struct {
	regs  map[uint16]uint16
	reads []uint16
	err   error
}

func (f *fakeEncombi) ReadInput(_ context.Context, start, qty uint16) ([]byte, error) {
	f.reads = append(f.reads, start)
	if f.err != nil {
		return nil, f.err
	}
	b := make([]byte, int(qty)*2)
	for i := uint16(0); i < qty; i++ {
		binary.BigEndian.PutUint16(b[i*2:], f.regs[start+i])
	}
	return b, nil
}

func (f *fakeEncombi) Close() error { return nil }

func putU32(regs map[uint16]uint16, addr uint16, v uint32) {
	regs[addr], regs[addr+1] = uint16(v>>16), uint16(v)
}

func TestReadEncombi(t *testing.T) {
	regs := map[uint16]uint16{encombiAliveAddr: 1, encombiDIAddr: 0x052C}
	minus1500 := int32(-1500)
	putU32(regs, 0, uint32(minus1500))
	putU32(regs, 38, 2279)
	putU32(regs, 40, 2306)
	putU32(regs, 42, 2287)
	f := &fakeEncombi{regs: regs}
	r, err := readEncombi(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	if r.DIBits != 0x052C || r.GenPowerKw != -1.5 || !r.Alive || r.BusV != [3]float64{227.9, 230.6, 228.7} {
		t.Fatalf("reading %+v", r)
	}
	if len(f.reads) != 3 || f.reads[0] != 0 || f.reads[1] != 199 || f.reads[2] != 20500 {
		t.Fatalf("reads %v", f.reads)
	}
	v := r.values()
	if v[keyEncombiDI] != 0x052C || v[keyGenPowerKw] != -1.5 || v[keyGenAlive] != 1 || v[keyBusVoltageC] != 228.7 {
		t.Fatalf("values %v", v)
	}
	if _, err := readEncombi(context.Background(), &fakeEncombi{err: errors.New("timeout")}); err == nil {
		t.Fatal("a failed read passed")
	}
	if _, err := decodeEncombi(make([]byte, 10), make([]byte, 2), make([]byte, 2), testTS); err == nil {
		t.Fatal("a short frame passed")
	}
}

func TestEncombiConfig(t *testing.T) {
	cfg := zeCfg(t)
	e := cfg.Encombi
	if e.Enabled || e.Port != 502 || e.UnitID != 1 || e.PollInterval != time.Second || e.StaleAfter != 5*time.Second ||
		e.Debounce != 2*time.Second || e.BusLiveMinV != 180 {
		t.Fatalf("defaults %+v", e)
	}
	og := cfg.Offgrid
	if og.GeneratorRatedKw != 734 || og.GeneratorMinKw != 220 || og.GeneratorTargetKw != 480 ||
		og.IslandSocFloorPct != 20 || og.IslandPowerMaxKw != 576 {
		t.Fatalf("offgrid defaults %+v", og)
	}
	cfg.Encombi.Enabled = true
	if err := cfg.validate(); err == nil {
		t.Fatal("encombi enabled without a host")
	}
	cfg.Encombi.Host = "10.28.40.103"
	if err := cfg.validate(); err != nil {
		t.Fatal(err)
	}
	cfg.Encombi.StaleAfter = cfg.Encombi.PollInterval
	if err := cfg.validate(); err == nil {
		t.Fatal("stale_after not longer than the poll")
	}
}
