package decode

import (
	"encoding/binary"
	"fmt"
	"math"

	"github.com/nesh/sestelemetry/internal/registers"
)

// Scaled decodes raw register bytes (ABCD_BE: big-endian over the Modbus register stream)
// and applies gain/offset.
func Scaled(dt registers.DataType, raw []byte, gain, offset float64) (float64, error) {
	if len(raw) < wordBytes(dt) {
		return 0, fmt.Errorf("decode: need %d bytes for %s, got %d", wordBytes(dt), dt, len(raw))
	}
	var v float64
	switch dt {
	case registers.DTUint16:
		v = float64(binary.BigEndian.Uint16(raw))
	case registers.DTUint32:
		v = float64(binary.BigEndian.Uint32(raw))
	case registers.DTInt32:
		v = float64(int32(binary.BigEndian.Uint32(raw)))
	case registers.DTInt64:
		v = float64(int64(binary.BigEndian.Uint64(raw)))
	case registers.DTUint64:
		u := binary.BigEndian.Uint64(raw)
		v = float64(u)
	default:
		return 0, fmt.Errorf("decode: unsupported data type %s", dt)
	}
	out := v*gain + offset
	if math.IsInf(out, 0) || math.IsNaN(out) {
		return 0, fmt.Errorf("decode: non-finite value")
	}
	return out, nil
}

func wordBytes(dt registers.DataType) int {
	switch dt {
	case registers.DTUint16:
		return 2
	case registers.DTUint32, registers.DTInt32:
		return 4
	case registers.DTInt64, registers.DTUint64:
		return 8
	default:
		return 0
	}
}
