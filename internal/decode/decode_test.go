package decode

import (
	"encoding/binary"
	"math"
	"testing"

	"github.com/nesh/sestelemetry/internal/registers"
)

func float32Bytes(f float32) []byte {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, math.Float32bits(f))
	return b
}

func TestScaledFloat32(t *testing.T) {
	// Encombi POWER_P_DG_TOTAL on Кролевець, 2026-11-15 18:30.
	v, err := Scaled(registers.DTFloat32, float32Bytes(277.1), 1, 0)
	if err != nil {
		t.Fatalf("Scaled returned error: %v", err)
	}
	if v != 277.1 {
		t.Fatalf("FLOAT32 277.1 decoded as %v, want the shortest decimal 277.1", v)
	}
	v, err = Scaled(registers.DTFloat32, float32Bytes(-103.25), 1, 0)
	if err != nil || v != -103.25 {
		t.Fatalf("FLOAT32 -103.25 decoded as %v, %v", v, err)
	}
}

func TestIsInvalidFloat32AndBitfield(t *testing.T) {
	for _, f := range []float32{float32(math.NaN()), float32(math.Inf(1)), float32(math.Inf(-1))} {
		if !IsInvalid(registers.DTFloat32, float32Bytes(f)) {
			t.Errorf("FLOAT32 %v must be no value", f)
		}
	}
	if IsInvalid(registers.DTFloat32, float32Bytes(0)) {
		t.Error("FLOAT32 0 is a reading")
	}
	allOnes := []byte{0xff, 0xff}
	bits := registers.Entry{MetricKey: "gen_breakers_on_bits", DataType: registers.DTUint16, Bitfield: true}
	if IsInvalidFor(bits, allOnes) {
		t.Error("bit field 0xFFFF is a reading: all sixteen breakers closed")
	}
	plain := registers.Entry{MetricKey: "gen_meter_alive", DataType: registers.DTUint16}
	if !IsInvalidFor(plain, allOnes) {
		t.Error("plain UINT16 0xFFFF must stay no value")
	}
}

func TestScaledInt32(t *testing.T) {
	// INT32(-1120) => -1.12 with gain=0.001
	v, err := Scaled(registers.DTInt32, []byte{0xff, 0xff, 0xfb, 0xa0}, 0.001, 0)
	if err != nil {
		t.Fatalf("Scaled returned error: %v", err)
	}
	if v != -1.12 {
		t.Fatalf("unexpected value: %v", v)
	}
}

func TestIsInvalid(t *testing.T) {
	cases := []struct {
		dt   registers.DataType
		raw  []byte
		want bool
	}{
		// Кролевець, grid meter not answering: 2 147 483.647 kW.
		{registers.DTInt32, []byte{0x7f, 0xff, 0xff, 0xff}, true},
		{registers.DTInt32, []byte{0x80, 0x00, 0x00, 0x00}, true},
		{registers.DTInt32, []byte{0xff, 0xff, 0xfb, 0xa0}, false}, // -1.12 kW
		{registers.DTUint16, []byte{0xff, 0xff}, true},
		{registers.DTUint16, []byte{0x0f, 0xbe}, false}, // 402.2 V
		{registers.DTUint32, []byte{0xff, 0xff, 0xff, 0xff}, true},
		{registers.DTInt64, []byte{0x7f, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}, true},
		{registers.DTInt64, []byte{0, 0, 0, 0, 0, 0x22, 0x9f, 0x50}, false},
		{registers.DTUint64, []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}, true},
		{registers.DTUint64, []byte{0xff, 0xff}, false},
	}
	for _, c := range cases {
		if got := IsInvalid(c.dt, c.raw); got != c.want {
			t.Errorf("IsInvalid(%s, % x) = %v, want %v", c.dt, c.raw, got, c.want)
		}
	}
}

func TestScaledTooShort(t *testing.T) {
	_, err := Scaled(registers.DTUint64, []byte{0x00, 0x01}, 1, 0)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}
