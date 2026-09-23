package api

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nesh/sestelemetry/internal/economics"
)

// portfolioBackend serves canned stored days per object so the portfolio
// handler can be driven end to end without a database.
type portfolioBackend struct {
	stubEconomicsBackend
	days map[string][]economics.DailyRecord
}

func (b portfolioBackend) LoadDailyRange(_ context.Context, orgID string, from, to time.Time) ([]economics.DailyRecord, error) {
	var out []economics.DailyRecord
	for _, d := range b.days[orgID] {
		if !d.Day.Before(from) && !d.Day.After(to) {
			out = append(out, d)
		}
	}
	return out, nil
}

// TestEconomicsPortfolioFold drives the concurrent per-object fan-out and
// checks the fold: the demo org is skipped, rows are ordered by reserve
// with empty objects last, and the totals / trend add up across objects.
func TestEconomicsPortfolioFold(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Kyiv")
	if err != nil {
		t.Skip("tzdata unavailable")
	}
	day := func(y int, m time.Month, d int, ebitda, pvToGrid, gridToLoad float64) economics.DailyRecord {
		return economics.DailyRecord{Day: time.Date(y, m, d, 0, 0, 0, 0, loc), IsFinal: true, Totals: economics.DailyTotals{
			Ebitda: ebitda, RevenueTotal: ebitda, RevenuePvSelf: ebitda, PV: 2 * ebitda,
			PVToGrid: pvToGrid, GridExport: pvToGrid, GridToLoad: gridToLoad, GridImport: gridToLoad,
			AvgImportPrice: 5, AvgExportPrice: 2, HoursWithData: 24,
		}}
	}
	h := NewHandlers(&mockStore{}, "*")
	h.SetOrganizations([]OrganizationInfo{{ID: demoOrgID}, {ID: "a"}, {ID: "b"}, {ID: "c"}})
	h.SetEconomicsService(economics.NewService(portfolioBackend{days: map[string][]economics.DailyRecord{
		demoOrgID: {day(2026, 3, 1, 1e9, 0, 0)},
		"a":       {day(2026, 3, 10, 1000, 100, 100)}, // schedule reserve 100 × (5 − 2) = 300
		"b":       {day(2026, 7, 1, 2000, 200, 300)},  // 200 × 3 = 600
	}}))

	get := func(query string) EconomicsPortfolioResponse {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/economics/portfolio?tz=Europe/Kyiv&"+query, nil)
		rec := httptest.NewRecorder()
		h.Router().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status %d (%s)", query, rec.Code, rec.Body.String())
		}
		var resp EconomicsPortfolioResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("%s: decode: %v", query, err)
		}
		return resp
	}
	ids := func(resp EconomicsPortfolioResponse) []string {
		out := make([]string, len(resp.Sites))
		for i, s := range resp.Sites {
			out[i] = s.ID
		}
		return out
	}

	year := get("period=2026")
	if got := ids(year); len(got) != 3 || got[0] != "b" || got[1] != "a" || got[2] != "c" {
		t.Fatalf("year sites = %v, want [b a c]", got)
	}
	if year.Sites[2].HasData {
		t.Fatalf("object without stored days reported has_data")
	}
	if year.Totals.EbitdaUah != 3000 || year.Totals.ScheduleReserveUah != 900 || year.MonthsWithData != 1 {
		t.Fatalf("year totals ebitda=%v schedule=%v months=%d, want 3000 / 900 / 1",
			year.Totals.EbitdaUah, year.Totals.ScheduleReserveUah, year.MonthsWithData)
	}
	if len(year.Trend) != 12 || year.Trend[2].Month != "2026-03" || year.Trend[2].EbitdaUah != 1000 || year.Trend[6].EbitdaUah != 2000 {
		t.Fatalf("trend = %+v, want 12 calendar months with Mar 1000 and Jul 2000", year.Trend)
	}

	month := get("month=2026-03")
	if got := ids(month); len(got) != 3 || got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Fatalf("month sites = %v, want [a b c]", got)
	}
	if month.Totals.EbitdaUah != 1000 || len(month.Trend) != 0 {
		t.Fatalf("month totals ebitda=%v trend=%d, want 1000 / 0", month.Totals.EbitdaUah, len(month.Trend))
	}
}

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
			ReasonCounts:   map[string]int{economics.AnomalyReasonPeakSpike: 2, economics.AnomalyReasonAfterGap: 1},
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
	if len(s.BessAnomalyReasons) != 2 || s.BessAnomalyReasons[0] != economics.AnomalyReasonAfterGap {
		t.Fatalf("BessAnomalyReasons = %v, want [after_gap peak_spike]", s.BessAnomalyReasons)
	}

	// Negative ESS reserve clamps to zero.
	neg := portfolioSiteFromTotals("ke", "Кролевецький", economics.MonthlyTotals{EssReserve: -500}, true)
	if neg.BessReserveUah != 0 {
		t.Fatalf("BessReserveUah = %v, want 0 (clamped)", neg.BessReserveUah)
	}
}
