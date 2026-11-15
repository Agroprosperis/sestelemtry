package decode

import (
	"encoding/binary"
	"fmt"
	"math"
	"strconv"

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
	case registers.DTFloat32:
		v = float32Value(binary.BigEndian.Uint32(raw))
	default:
		return 0, fmt.Errorf("decode: unsupported data type %s", dt)
	}
	out := v*gain + offset
	if math.IsInf(out, 0) || math.IsNaN(out) {
		return 0, fmt.Errorf("decode: non-finite value")
	}
	return out, nil
}

// float32Value widens an IEEE 754 single to the shortest float64 that
// prints the same, so a device's 277.1 is stored as 277.1 rather than
// as 277.1000061035156.
func float32Value(bits uint32) float64 {
	f := math.Float32frombits(bits)
	v, err := strconv.ParseFloat(strconv.FormatFloat(float64(f), 'g', -1, 32), 64)
	if err != nil {
		return float64(f)
	}
	return v
}

// IsInvalid reports whether raw holds the value Huawei devices return
// for a reading they do not have (a meter or inverter that is not
// answering the SmartLogger): all ones for unsigned types, the maximum
// (or minimum) for signed ones. Scaled, these read as 2 147 483 kW and
// the like, never as a measurement. A FLOAT32 has no such sentinel;
// NaN and ±Inf are its "no value".
func IsInvalid(dt registers.DataType, raw []byte) bool {
	if len(raw) < wordBytes(dt) {
		return false
	}
	switch dt {
	case registers.DTUint16:
		return binary.BigEndian.Uint16(raw) == math.MaxUint16
	case registers.DTUint32:
		return binary.BigEndian.Uint32(raw) == math.MaxUint32
	case registers.DTInt32:
		v := int32(binary.BigEndian.Uint32(raw))
		return v == math.MaxInt32 || v == math.MinInt32
	case registers.DTUint64:
		return binary.BigEndian.Uint64(raw) == math.MaxUint64
	case registers.DTInt64:
		v := int64(binary.BigEndian.Uint64(raw))
		return v == math.MaxInt64 || v == math.MinInt64
	case registers.DTFloat32:
		f := float64(math.Float32frombits(binary.BigEndian.Uint32(raw)))
		return math.IsNaN(f) || math.IsInf(f, 0)
	}
	return false
}

// IsInvalidFor is IsInvalid for a catalog entry. A bit field has no
// sentinel: all ones is a reading like any other.
func IsInvalidFor(e registers.Entry, raw []byte) bool {
	return !e.Bitfield && IsInvalid(e.DataType, raw)
}

func wordBytes(dt registers.DataType) int {
	switch dt {
	case registers.DTUint16:
		return 2
	case registers.DTUint32, registers.DTInt32, registers.DTFloat32:
		return 4
	case registers.DTInt64, registers.DTUint64:
		return 8
	default:
		return 0
	}
}
