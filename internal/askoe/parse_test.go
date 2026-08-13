package askoe

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseHourlyImportSheet(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "aug2024_import.xls"))
	if err != nil {
		t.Fatal(err)
	}
	hours, err := parseHourlySheet(data)
	if err != nil {
		t.Fatal(err)
	}
	d := civilDay{2024, 8, 1}
	row, ok := hours[d]
	if !ok {
		t.Fatalf("missing 2024-08-01, have %d days", len(hours))
	}
	if row[0] != 240 {
		t.Errorf("hour 0 = %v, want 240", row[0])
	}
	var sum float64
	for _, v := range row {
		sum += v
	}
	if sum != 2310 {
		t.Errorf("day sum = %v, want 2310", sum)
	}
}

func TestParseWorkbooksCompleteAugust(t *testing.T) {
	files := mustTestdata(t)
	grid, warnings, err := ParseWorkbooks(files)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings: %v", warnings)
	}
	days := grid.CompleteDays()
	if len(days) != 31 {
		t.Fatalf("complete days = %d, want 31", len(days))
	}
	if days[0] != (civilDay{2024, 8, 1}) || days[30] != (civilDay{2024, 8, 31}) {
		t.Errorf("span %v .. %v", days[0], days[30])
	}
}

func TestBuildDaySamplesSpreadsHour(t *testing.T) {
	loc := time.UTC
	day := civilDay{2024, 8, 1}
	grid := HourGrid{
		Import: map[civilDay][24]float64{day: hourFilled(12)},
		Export: map[civilDay][24]float64{day: {}},
		PV:     map[civilDay][24]float64{day: hourFilled(24)},
	}
	samples := BuildDaySamples("ze", OrgHosts{}, loc, day, grid, Counters{})
	// 24h × 12 steps, but 24:00 is folded into 23:55 → 287 unique times × 6 metrics
	if len(samples) != 287*6 {
		t.Fatalf("len=%d", len(samples))
	}
	end := EndCounters(samples)
	if end.PV != 24*24 {
		t.Errorf("PV cum=%v want 576", end.PV)
	}
	if end.Import != 12*24 {
		t.Errorf("import cum=%v want 288", end.Import)
	}
	// First 5-min of hour 0: 24/12 = 2 kWh PV. Dashboard power = 2*12 = 24 kW = the hour's kWh.
	var firstPV float64
	for _, s := range samples {
		if s.MetricKey == pvMetric && s.Time.Equal(time.Date(2024, 8, 1, 0, 5, 0, 0, loc)) {
			firstPV = s.Value
			break
		}
	}
	if firstPV != 2 {
		t.Errorf("first 5-min PV counter = %v, want 2", firstPV)
	}

	at2355 := 0
	for _, s := range samples {
		if s.MetricKey == pvMetric && s.Time.Equal(time.Date(2024, 8, 1, 23, 55, 0, 0, loc)) {
			at2355++
		}
	}
	if at2355 != 1 {
		t.Errorf("PV samples at 23:55 = %d, want 1", at2355)
	}
}

func hourFilled(v float64) [24]float64 {
	var h [24]float64
	for i := range h {
		h[i] = v
	}
	return h
}

func mustTestdata(t *testing.T) []WorkbookFile {
	t.Helper()
	named := []struct{ file, as string }{
		{"aug2024_import.xls", "08 Погодинна А+ ЕЕ РУ-10 Серпень ЖЕ.xls"},
		{"aug2024_export.xls", "08 Погодинна А- ЕЕ РУ-10 Серпень ЖЕ.xls"},
		{"aug2024_pv.xls", "08 Погодинна А- ЕЕ СЕС Серпень ЖЕ.xls"},
	}
	out := make([]WorkbookFile, 0, 3)
	for _, n := range named {
		data, err := os.ReadFile(filepath.Join("testdata", n.file))
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, WorkbookFile{Name: n.as, Data: data})
	}
	return out
}
