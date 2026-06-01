// Package oree downloads and parses Day-Ahead Market (RDN) price files
// published by the Ukrainian "Operator of the Wholesale Electricity Market"
// (OREE) at https://www.oree.com.ua/.
//
// The published files are served as application/vnd.ms-excel (legacy OLE2
// BIFF8 .xls), despite the misleading "downloadxlsx" path component. This
// package contains a minimal BIFF8 reader scoped to the fixed DAM sheet
// layout of 25 rows × 6 columns and is deliberately self-contained to avoid
// pulling in a full xls library.
package oree

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/richardlehane/mscfb"
)

// DAMRow is one parsed hourly row from a DAM XLS sheet.
type DAMRow struct {
	Hour        int      // 1..24, hour ending
	Price       *float64 // UAH/MWh
	SaleVol     *float64 // MWh
	BuyVol      *float64 // MWh
	DeclSaleVol *float64 // MWh
	DeclBuyVol  *float64 // MWh
}

// BIFF record IDs we care about.
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

// ParseDAMSheet parses one OREE DAM .xls payload and returns up to 24 hourly rows.
func ParseDAMSheet(payload []byte) ([]DAMRow, error) {
	wb, err := extractWorkbookStream(payload)
	if err != nil {
		return nil, fmt.Errorf("oree: extract workbook: %w", err)
	}
	cells, err := decodeBIFF8(wb)
	if err != nil {
		return nil, fmt.Errorf("oree: decode biff8: %w", err)
	}
	return rowsFromCells(cells)
}

// extractWorkbookStream reads the "Workbook" stream out of the OLE2 compound file.
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
			buf, err := io.ReadAll(entry)
			if err != nil {
				return nil, err
			}
			return buf, nil
		}
	}
	return nil, errors.New("workbook stream not found")
}

type cellValue struct {
	str  string
	num  float64
	kind byte // 's' = string, 'n' = number
}

type sheetCells map[[2]int]cellValue

// decodeBIFF8 walks BIFF records and returns a row/col indexed map of cells
// for the first worksheet substream.
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
			// First BOF is the workbook globals; the second is the first worksheet.
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

// readSST stitches SST + CONTINUE records into the shared string table. It
// supports CONTINUE-fragment encoding flips: when a string straddles a record
// boundary, the encoding flag byte is repeated at the start of the next
// fragment and may switch between compressed (1-byte/char) and wide (UTF-16).
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

// fragReader reads sequentially across multiple fragments (SST body + each
// CONTINUE), tracking when a fragment boundary is crossed so that string
// payloads can re-read the leading 1-byte encoding flag per BIFF8 spec.
type fragReader struct {
	frags  [][]byte
	fIdx   int
	off    int
	atEdge bool // true right after we cross to a new fragment
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

// readString reads cch characters in either compressed (latin-1) or wide UTF-16
// encoding, supporting mid-string fragment boundaries with encoding flips.
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
			// One stray byte left at edge for a wide char — splice across edge.
			if !wide {
				// Compressed needs at least 1 byte and we have 0 — go around.
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
				// Encoding flipped mid-codeunit — extremely unlikely for this source;
				// salvage by reading 1 byte as latin-1 char and discarding the stray.
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

// readUnicodeString reads a 16-bit length-prefixed BIFF Unicode string used by
// LABEL records (without rich/far-east extensions for our purposes).
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

// decodeRK decodes a BIFF RK number (30-bit int or shifted 64-bit float).
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

// rowsFromCells maps the parsed sheet into 1..24 hourly rows. The OREE layout
// has the header in row 0 and hours 01:00..24:00 in rows 1..24, but we locate
// the header dynamically to be tolerant of leading metadata rows.
func rowsFromCells(cells sheetCells) ([]DAMRow, error) {
	maxRow := 0
	for k := range cells {
		if k[0] > maxRow {
			maxRow = k[0]
		}
	}

	headerRow := -1
	for r := 0; r <= maxRow; r++ {
		if c, ok := cells[[2]int{r, 0}]; ok && c.kind == 's' && containsHourHeader(c.str) {
			headerRow = r
			break
		}
	}
	if headerRow == -1 {
		return nil, errors.New("oree: header row with 'Година' not found")
	}

	// parseHourLabel already constrains hours to 1..24. We additionally
	// reject duplicate hours within a single sheet: two rows mapping to
	// the same hour would silently collapse on the (delivery_date, hour,
	// zone) upsert, leaving a gap. We deliberately do NOT require exactly
	// 24 rows — daylight-saving transition days legitimately have 23 or
	// 25 hours, and forcing 24 would make the collector store nothing for
	// the whole day.
	out := make([]DAMRow, 0, 24)
	seen := make(map[int]struct{}, 24)
	for r := headerRow + 1; r <= maxRow; r++ {
		hourCell, ok := cells[[2]int{r, 0}]
		if !ok || hourCell.kind != 's' {
			continue
		}
		hour, ok := parseHourLabel(hourCell.str)
		if !ok {
			continue
		}
		if _, dup := seen[hour]; dup {
			return nil, fmt.Errorf("oree: duplicate hour %d in sheet", hour)
		}
		seen[hour] = struct{}{}
		out = append(out, DAMRow{
			Hour:        hour,
			Price:       numAt(cells, r, 1),
			SaleVol:     numAt(cells, r, 2),
			BuyVol:      numAt(cells, r, 3),
			DeclSaleVol: numAt(cells, r, 4),
			DeclBuyVol:  numAt(cells, r, 5),
		})
	}
	if len(out) == 0 {
		return nil, errors.New("oree: no hourly rows decoded")
	}
	return out, nil
}

func containsHourHeader(s string) bool {
	return strings.Contains(strings.ToLower(strings.TrimSpace(s)), "годин")
}

// parseHourLabel maps "01:00".."24:00" → 1..24. Also tolerates "1:00" or "24".
func parseHourLabel(s string) (int, bool) {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, ":"); i > 0 {
		s = s[:i]
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 || n > 24 {
		return 0, false
	}
	return n, true
}

func numAt(cells sheetCells, row, col int) *float64 {
	c, ok := cells[[2]int{row, col}]
	if !ok {
		return nil
	}
	switch c.kind {
	case 'n':
		v := c.num
		return &v
	case 's':
		v, ok := parseUaNumber(c.str)
		if !ok {
			return nil
		}
		return &v
	}
	return nil
}

// parseUaNumber parses a Ukrainian-formatted number like "13 000,00" — comma
// decimal separator and arbitrary space-like thousand separators (U+0020,
// U+00A0 NBSP, U+2009 thin space, U+202F narrow NBSP).
func parseUaNumber(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" || s == "-" {
		return 0, false
	}
	cleaned := make([]rune, 0, len(s))
	for _, r := range s {
		switch r {
		case ' ', '\u00A0', '\u2009', '\u202F':
			continue
		case ',':
			cleaned = append(cleaned, '.')
		default:
			cleaned = append(cleaned, r)
		}
	}
	v, err := strconv.ParseFloat(string(cleaned), 64)
	if err != nil {
		return 0, false
	}
	return v, true
}
