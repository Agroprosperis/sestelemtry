package decode

import (
	"testing"

	"github.com/nesh/sestelemetry/internal/registers"
)

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
