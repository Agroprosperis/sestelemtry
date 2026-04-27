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

func TestScaledTooShort(t *testing.T) {
	_, err := Scaled(registers.DTUint64, []byte{0x00, 0x01}, 1, 0)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}
