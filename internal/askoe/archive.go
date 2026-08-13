package askoe

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/bodgit/sevenzip"
)

const maxArchiveBytes = 32 << 20 // 32 MiB

// ExtractWorkbooks pulls every .xls out of a loose workbook, a zip, or a 7z.
func ExtractWorkbooks(filename string, payload []byte) ([]WorkbookFile, error) {
	if len(payload) == 0 {
		return nil, fmt.Errorf("empty upload")
	}
	if int64(len(payload)) > maxArchiveBytes {
		return nil, fmt.Errorf("upload exceeds %d bytes", maxArchiveBytes)
	}
	switch {
	case isZIP(payload):
		return fromZIP(payload)
	case is7z(payload):
		return from7z(payload)
	case isOLE(payload) || strings.EqualFold(filepath.Ext(filename), ".xls"):
		return []WorkbookFile{{Name: filename, Data: payload}}, nil
	default:
		return nil, fmt.Errorf("unsupported file %q (need .xls, .zip or .7z)", filename)
	}
}

func isZIP(b []byte) bool {
	return len(b) >= 4 && b[0] == 'P' && b[1] == 'K'
}

func is7z(b []byte) bool {
	return len(b) >= 6 && bytes.Equal(b[:6], []byte{0x37, 0x7A, 0xBC, 0xAF, 0x27, 0x1C})
}

func isOLE(b []byte) bool {
	return len(b) >= 8 && bytes.Equal(b[:8], []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1})
}

func fromZIP(payload []byte) ([]WorkbookFile, error) {
	r, err := zip.NewReader(bytes.NewReader(payload), int64(len(payload)))
	if err != nil {
		return nil, fmt.Errorf("zip: %w", err)
	}
	out := make([]WorkbookFile, 0)
	for _, f := range r.File {
		if f.FileInfo().IsDir() || !isXLSName(f.Name) {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f.Name, err)
		}
		data, err := io.ReadAll(io.LimitReader(rc, maxArchiveBytes))
		_ = rc.Close()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f.Name, err)
		}
		out = append(out, WorkbookFile{Name: f.Name, Data: data})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("zip contains no .xls workbooks")
	}
	return out, nil
}

func from7z(payload []byte) ([]WorkbookFile, error) {
	r, err := sevenzip.NewReader(bytes.NewReader(payload), int64(len(payload)))
	if err != nil {
		return nil, fmt.Errorf("7z: %w", err)
	}
	out := make([]WorkbookFile, 0)
	for _, f := range r.File {
		if f.FileInfo().IsDir() || !isXLSName(f.Name) {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f.Name, err)
		}
		data, err := io.ReadAll(io.LimitReader(rc, maxArchiveBytes))
		_ = rc.Close()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f.Name, err)
		}
		out = append(out, WorkbookFile{Name: f.Name, Data: data})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("7z contains no .xls workbooks")
	}
	return out, nil
}

func isXLSName(name string) bool {
	return strings.EqualFold(filepath.Ext(name), ".xls")
}
