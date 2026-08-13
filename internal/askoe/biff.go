package askoe

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"unicode/utf16"

	"github.com/richardlehane/mscfb"
)

const (
	recBOF      = 0x0809
	recEOF      = 0x000A
	recSST      = 0x00FC
	recCONTINUE = 0x003C
	recLabelSST = 0x00FD
	recRK       = 0x027E
	recMULRK    = 0x00BD
	recNUMBER   = 0x0203
	recLABEL    = 0x0204
)

type biffRecord struct {
	id      uint16
	payload []byte
}

type cellValue struct {
	str  string
	num  float64
	kind byte // 's' = string, 'n' = number
}

type sheetCells map[[2]int]cellValue

func decodeXLS(payload []byte) (sheetCells, error) {
	wb, err := extractWorkbookStream(payload)
	if err != nil {
		return nil, err
	}
	return decodeBIFF8(wb)
}

func extractWorkbookStream(payload []byte) ([]byte, error) {
	rdr, err := mscfb.New(bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	for {
		entry, err := rdr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if entry.Name == "Workbook" || entry.Name == "Book" {
			return io.ReadAll(entry)
		}
	}
	return nil, errors.New("workbook stream not found")
}

func decodeBIFF8(wb []byte) (sheetCells, error) {
	records := make([]biffRecord, 0, 256)
	for i := 0; i+4 <= len(wb); {
		id := binary.LittleEndian.Uint16(wb[i:])
		ln := int(binary.LittleEndian.Uint16(wb[i+2:]))
		end := i + 4 + ln
		if end > len(wb) {
			return nil, fmt.Errorf("truncated record id=%#x len=%d at %d", id, ln, i)
		}
		records = append(records, biffRecord{id: id, payload: wb[i+4 : end]})
		i = end
	}

	sst, err := readSST(records)
	if err != nil {
		return nil, err
	}

	cells := make(sheetCells)
	bofs := 0
	inSheet := false
	for _, r := range records {
		switch r.id {
		case recBOF:
			bofs++
			inSheet = bofs >= 2
		case recEOF:
			if inSheet {
				return cells, nil
			}
		case recLabelSST:
			if !inSheet || len(r.payload) < 10 {
				continue
			}
			row := int(binary.LittleEndian.Uint16(r.payload[0:]))
			col := int(binary.LittleEndian.Uint16(r.payload[2:]))
			isst := int(binary.LittleEndian.Uint32(r.payload[6:]))
			if isst >= 0 && isst < len(sst) {
				cells[[2]int{row, col}] = cellValue{kind: 's', str: sst[isst]}
			}
		case recRK:
			if !inSheet || len(r.payload) < 10 {
				continue
			}
			row := int(binary.LittleEndian.Uint16(r.payload[0:]))
			col := int(binary.LittleEndian.Uint16(r.payload[2:]))
			rk := int32(binary.LittleEndian.Uint32(r.payload[6:]))
			cells[[2]int{row, col}] = cellValue{kind: 'n', num: decodeRK(rk)}
		case recMULRK:
			if !inSheet || len(r.payload) < 6 {
				continue
			}
			row := int(binary.LittleEndian.Uint16(r.payload[0:]))
			firstCol := int(binary.LittleEndian.Uint16(r.payload[2:]))
			body := r.payload[4 : len(r.payload)-2]
			for k := 0; k+6 <= len(body); k += 6 {
				rk := int32(binary.LittleEndian.Uint32(body[k+2:]))
				cells[[2]int{row, firstCol + k/6}] = cellValue{kind: 'n', num: decodeRK(rk)}
			}
		case recNUMBER:
			if !inSheet || len(r.payload) < 14 {
				continue
			}
			row := int(binary.LittleEndian.Uint16(r.payload[0:]))
			col := int(binary.LittleEndian.Uint16(r.payload[2:]))
			bits := binary.LittleEndian.Uint64(r.payload[6:])
			cells[[2]int{row, col}] = cellValue{kind: 'n', num: math.Float64frombits(bits)}
		case recLABEL:
			if !inSheet || len(r.payload) < 8 {
				continue
			}
			row := int(binary.LittleEndian.Uint16(r.payload[0:]))
			col := int(binary.LittleEndian.Uint16(r.payload[2:]))
			s, _ := readUnicodeString(r.payload[6:])
			cells[[2]int{row, col}] = cellValue{kind: 's', str: s}
		}
	}
	return cells, nil
}

func readSST(records []biffRecord) ([]string, error) {
	idx := -1
	for i, r := range records {
		if r.id == recSST {
			idx = i
			break
		}
	}
	if idx == -1 {
		return nil, nil
	}
	first := records[idx].payload
	if len(first) < 8 {
		return nil, errors.New("sst: header too short")
	}
	totalStrs := int(binary.LittleEndian.Uint32(first[4:]))
	frags := [][]byte{first[8:]}
	for j := idx + 1; j < len(records); j++ {
		if records[j].id != recCONTINUE {
			break
		}
		frags = append(frags, records[j].payload)
	}
	r := &fragReader{frags: frags}
	out := make([]string, 0, totalStrs)
	for k := 0; k < totalStrs; k++ {
		hdr, err := r.read(3)
		if err != nil {
			return nil, fmt.Errorf("sst[%d] header: %w", k, err)
		}
		cch := int(binary.LittleEndian.Uint16(hdr[0:]))
		flags := hdr[2]
		wide := flags&0x01 != 0
		hasFarExt := flags&0x04 != 0
		hasRich := flags&0x08 != 0
		var rt int
		if hasRich {
			b, err := r.read(2)
			if err != nil {
				return nil, err
			}
			rt = int(binary.LittleEndian.Uint16(b))
		}
		var sz int
		if hasFarExt {
			b, err := r.read(4)
			if err != nil {
				return nil, err
			}
			sz = int(binary.LittleEndian.Uint32(b))
		}
		s, err := r.readString(cch, wide)
		if err != nil {
			return nil, fmt.Errorf("sst[%d] body: %w", k, err)
		}
		if hasRich {
			if _, err := r.read(rt * 4); err != nil {
				return nil, err
			}
		}
		if hasFarExt {
			if _, err := r.read(sz); err != nil {
				return nil, err
			}
		}
		out = append(out, s)
	}
	return out, nil
}

type fragReader struct {
	frags  [][]byte
	fIdx   int
	off    int
	atEdge bool
}

func (r *fragReader) advanceIfEmpty() {
	for r.fIdx < len(r.frags) && r.off >= len(r.frags[r.fIdx]) {
		r.fIdx++
		r.off = 0
		r.atEdge = true
	}
}

func (r *fragReader) read(n int) ([]byte, error) {
	if n == 0 {
		return nil, nil
	}
	out := make([]byte, 0, n)
	for len(out) < n {
		r.advanceIfEmpty()
		if r.fIdx >= len(r.frags) {
			return nil, io.ErrUnexpectedEOF
		}
		cur := r.frags[r.fIdx]
		take := len(cur) - r.off
		if take > n-len(out) {
			take = n - len(out)
		}
		out = append(out, cur[r.off:r.off+take]...)
		r.off += take
	}
	return out, nil
}

func (r *fragReader) readString(cch int, wide bool) (string, error) {
	var sb strings.Builder
	remaining := cch
	for remaining > 0 {
		r.advanceIfEmpty()
		if r.fIdx >= len(r.frags) {
			return "", io.ErrUnexpectedEOF
		}
		if r.atEdge {
			r.atEdge = false
			fb, err := r.read(1)
			if err != nil {
				return "", err
			}
			wide = fb[0]&0x01 != 0
			r.advanceIfEmpty()
			if r.fIdx >= len(r.frags) {
				return "", io.ErrUnexpectedEOF
			}
		}
		cur := r.frags[r.fIdx]
		avail := len(cur) - r.off
		charBytes := 1
		if wide {
			charBytes = 2
		}
		can := avail / charBytes
		if can == 0 {
			if !wide {
				r.fIdx++
				r.off = 0
				r.atEdge = true
				continue
			}
			lo := cur[r.off]
			r.off++
			r.fIdx++
			r.off = 0
			r.atEdge = true
			fb, err := r.read(1)
			if err != nil {
				return "", err
			}
			wide = fb[0]&0x01 != 0
			r.advanceIfEmpty()
			if !wide {
				b, err := r.read(1)
				if err != nil {
					return "", err
				}
				sb.WriteRune(rune(b[0]))
				remaining--
				continue
			}
			hi, err := r.read(1)
			if err != nil {
				return "", err
			}
			sb.WriteRune(rune(uint16(lo) | uint16(hi[0])<<8))
			remaining--
			continue
		}
		if can > remaining {
			can = remaining
		}
		chunk := cur[r.off : r.off+can*charBytes]
		r.off += can * charBytes
		if wide {
			u := make([]uint16, can)
			for i := 0; i < can; i++ {
				u[i] = binary.LittleEndian.Uint16(chunk[i*2:])
			}
			sb.WriteString(string(utf16.Decode(u)))
		} else {
			for _, b := range chunk {
				sb.WriteRune(rune(b))
			}
		}
		remaining -= can
	}
	return sb.String(), nil
}

func readUnicodeString(p []byte) (string, int) {
	if len(p) < 3 {
		return "", 0
	}
	cch := int(binary.LittleEndian.Uint16(p[0:]))
	flags := p[2]
	wide := flags&0x01 != 0
	pos := 3
	if wide {
		need := cch * 2
		if pos+need > len(p) {
			return "", 0
		}
		u := make([]uint16, cch)
		for i := 0; i < cch; i++ {
			u[i] = binary.LittleEndian.Uint16(p[pos+i*2:])
		}
		return string(utf16.Decode(u)), pos + need
	}
	if pos+cch > len(p) {
		return "", 0
	}
	return string(p[pos : pos+cch]), pos + cch
}

func decodeRK(rk int32) float64 {
	isInt := rk&0x02 != 0
	div100 := rk&0x01 != 0
	var v float64
	if isInt {
		v = float64(rk >> 2)
	} else {
		bits := uint64(uint32(rk)&0xFFFFFFFC) << 32
		v = math.Float64frombits(bits)
	}
	if div100 {
		v /= 100
	}
	return v
}
