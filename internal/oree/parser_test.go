package oree

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestParseDAMSheet_RealFixture(t *testing.T) {
	path := filepath.Join("testdata", "dam_sample.xls")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	rows, err := ParseDAMSheet(b)
	if err != nil {
		t.Fatalf("ParseDAMSheet: %v", err)
	}
	if len(rows) != 24 {
		t.Fatalf("expected 24 rows, got %d", len(rows))
	}
	for i, r := range rows {
		if r.Hour != i+1 {
			t.Fatalf("row %d: expected hour %d, got %d", i, i+1, r.Hour)
		}
		if r.Price == nil || r.SaleVol == nil || r.BuyVol == nil ||
			r.DeclSaleVol == nil || r.DeclBuyVol == nil {
			t.Fatalf("row %d: unexpected nil column", i)
		}
	}

	first := rows[0]
	if !approxEq(*first.Price, 5600.00, 1e-6) {
		t.Fatalf("hour 1 price expected ~5600.00, got %v", *first.Price)
	}
	if !approxEq(*first.SaleVol, 3396.3, 1e-6) {
		t.Fatalf("hour 1 sale volume expected ~3396.3, got %v", *first.SaleVol)
	}

	peak := rows[19] // 20:00
	if !approxEq(*peak.Price, 14968.70, 1e-6) {
		t.Fatalf("hour 20 price expected ~14968.70, got %v", *peak.Price)
	}
}

func TestParseUaNumber(t *testing.T) {
	cases := []struct {
		in   string
		want float64
		ok   bool
	}{
		{"5 600,00", 5600, true},
		{"13 000,00", 13000, true},
		{"3 396,3", 3396.3, true},
		{"978,00", 978, true},
		{"-1,5", -1.5, true},
		{"0", 0, true},
		{"\u00A0\u20094 287,1\u202F", 4287.1, true},
		{"", 0, false},
		{"-", 0, false},
		{"abc", 0, false},
	}
	for _, c := range cases {
		got, ok := parseUaNumber(c.in)
		if ok != c.ok {
			t.Fatalf("parseUaNumber(%q) ok = %v, want %v", c.in, ok, c.ok)
		}
		if ok && !approxEq(got, c.want, 1e-9) {
			t.Fatalf("parseUaNumber(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestParseHourLabel(t *testing.T) {
	cases := []struct {
		in   string
		want int
		ok   bool
	}{
		{"01:00", 1, true},
		{"24:00", 24, true},
		{"7", 7, true},
		{"00:00", 0, false},
		{"25:00", 0, false},
		{"abc", 0, false},
	}
	for _, c := range cases {
		got, ok := parseHourLabel(c.in)
		if ok != c.ok || got != c.want {
			t.Fatalf("parseHourLabel(%q) = (%d,%v), want (%d,%v)", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestDecodeRK(t *testing.T) {
	cases := []struct {
		rk   int32
		want float64
	}{
		{0x00000002, 0.0},               // int 0
		{(40 << 2) | 0x02, 40.0},        // int 40
		{(150 << 2) | 0x03, 1.5},        // int 150 / 100
		{int32(uint32(0x40240000)), 10}, // float 10.0 (0x4024000000000000 >>32)
	}
	for _, c := range cases {
		got := decodeRK(c.rk)
		if !approxEq(got, c.want, 1e-9) {
			t.Fatalf("decodeRK(%#x) = %v, want %v", uint32(c.rk), got, c.want)
		}
	}
}

func approxEq(a, b, eps float64) bool { return math.Abs(a-b) <= eps }
