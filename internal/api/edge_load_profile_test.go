package api

import (
	"testing"
	"time"

	"github.com/nesh/sestelemetry/internal/storage"
)

func econDays(t *testing.T, loc *time.Location, days int, load func(day, hour int) float64) []storage.EconomicsHourlyRow {
	t.Helper()
	start := time.Date(2026, 9, 23, 0, 0, 0, 0, loc)
	var rows []storage.EconomicsHourlyRow
	for d := 0; d < days; d++ {
		for h := 0; h < 24; h++ {
			ts := start.AddDate(0, 0, d).Add(time.Duration(h) * time.Hour)
			rows = append(rows, storage.EconomicsHourlyRow{HourStart: ts.UTC(), LoadTotal: load(d, h)})
		}
	}
	return rows
}

func TestLoadProfileFromEconomicsMedianPerLocalHour(t *testing.T) {
	loc, _ := time.LoadLocation("Europe/Kyiv")
	// ze-like evening: the BESS covers ~270 kW that 40503 never sees.
	rows := econDays(t, loc, 9, func(d, h int) float64 {
		if h >= 20 {
			return 260 + float64(d) // median of 260..268 = 264
		}
		return 100 + float64(h)
	})
	got := loadProfileFromEconomics(rows, loc)
	if got == nil {
		t.Fatal("profile = nil, want 24 hours")
	}
	if got[21] != 264 {
		t.Fatalf("hour 21 = %v, want 264 (median over 9 days)", got[21])
	}
	if got[3] != 103 {
		t.Fatalf("hour 3 = %v, want 103 (local Kyiv hour, not UTC)", got[3])
	}
}

func TestLoadProfileFromEconomicsSkipsCounterGaps(t *testing.T) {
	loc, _ := time.LoadLocation("Europe/Kyiv")
	rows := econDays(t, loc, 8, func(d, h int) float64 {
		if d < 3 && h == 22 {
			return 0 // counter gap, not an idle plant
		}
		return 270
	})
	got := loadProfileFromEconomics(rows, loc)
	if got == nil || got[22] != 270 {
		t.Fatalf("hour 22 = %v, want 270 with gap hours skipped", got)
	}
}

func TestLoadProfileFromEconomicsFallsBackWhenThin(t *testing.T) {
	loc, _ := time.LoadLocation("Europe/Kyiv")
	few := econDays(t, loc, loadProfileMinDays-1, func(int, int) float64 { return 200 })
	if got := loadProfileFromEconomics(few, loc); got != nil {
		t.Fatalf("%d days must fall back, got %v", loadProfileMinDays-1, got)
	}
	holed := econDays(t, loc, 10, func(_, h int) float64 {
		if h == 4 {
			return 0
		}
		return 200
	})
	if got := loadProfileFromEconomics(holed, loc); got != nil {
		t.Fatal("an hour with no samples must fall back, not plan on zero load")
	}
	if got := loadProfileFromEconomics(nil, loc); got != nil {
		t.Fatal("no rows must fall back")
	}
}
