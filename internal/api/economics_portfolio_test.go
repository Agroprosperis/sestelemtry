package api

import (
	"math"
	"testing"

	"github.com/nesh/sestelemetry/internal/economics"
)

// TestScheduleReserveUah checks the work-schedule (elevator) reserve:
// shiftable = min(pv_to_grid, grid_to_load), valued at max(0, import−export).
func TestScheduleReserveUah(t *testing.T) {
	cases := []struct {
		name string
		in   economics.MonthlyTotals
		want float64
	}{
		{
			name: "shiftable limited by grid_to_load",
			in:   economics.MonthlyTotals{AvgImportPrice: 5, AvgExportPrice: 2, PVToGrid: 100, GridToLoad: 40},
			want: 40 * 3,
		},
		{
			name: "negative gap clamps to zero",
			in:   economics.MonthlyTotals{AvgImportPrice: 2, AvgExportPrice: 5, PVToGrid: 100, GridToLoad: 40},
			want: 0,
		},
		{
			name: "no exportable surplus",
			in:   economics.MonthlyTotals{AvgImportPrice: 5, AvgExportPrice: 2, PVToGrid: 0, GridToLoad: 40},
			want: 0,
		},
	}
	for _, c := range cases {
		if got := scheduleReserveUah(c.in); math.Abs(got-c.want) > 1e-9 {
			t.Errorf("%s: scheduleReserveUah = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestPortfolioSiteFromTotals verifies the per-site row: bess reserve is
// clamped at zero, action = schedule + bess, and the data-quality flags
// flow through.
func TestPortfolioSiteFromTotals(t *testing.T) {
	tot := economics.MonthlyTotals{
		Effect: 1000, Ebitda: 1100,
		AvgImportPrice: 5, AvgExportPrice: 2, PVToGrid: 100, GridToLoad: 40, // sched = 120
		EssReserve: 800,
		EssDataQuality: economics.DataQuality{
			DataOK: false, AnomalousHours: 3, AnomalousDays: 2,
			AnomalousDates: []string{"2026-07-03", "2026-07-11"},
		},
	}
	s := portfolioSiteFromTotals("ze", "Жмеринський", tot, true)
	if math.Abs(s.ScheduleReserveUah-120) > 1e-9 {
		t.Fatalf("ScheduleReserveUah = %v, want 120", s.ScheduleReserveUah)
	}
	if s.BessReserveUah != 800 {
		t.Fatalf("BessReserveUah = %v, want 800", s.BessReserveUah)
	}
	if math.Abs(s.ActionReserveUah-920) > 1e-9 {
		t.Fatalf("ActionReserveUah = %v, want 920", s.ActionReserveUah)
	}
	if s.BessDataOk || s.BessAnomalousHours != 3 || s.BessAnomalousDays != 2 {
		t.Fatalf("data quality flags not propagated: ok=%v hours=%d days=%d", s.BessDataOk, s.BessAnomalousHours, s.BessAnomalousDays)
	}
	if len(s.BessAnomalousDates) != 2 || s.BessAnomalousDates[0] != "2026-07-03" {
		t.Fatalf("BessAnomalousDates = %v, want [2026-07-03 2026-07-11]", s.BessAnomalousDates)
	}

	// Negative ESS reserve clamps to zero.
	neg := portfolioSiteFromTotals("ke", "Кролевецький", economics.MonthlyTotals{EssReserve: -500}, true)
	if neg.BessReserveUah != 0 {
		t.Fatalf("BessReserveUah = %v, want 0 (clamped)", neg.BessReserveUah)
	}
}
