package api

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nesh/sestelemetry/internal/energyflow"
)

func TestEnergyFlowHourly_RequiresOrganizationID(t *testing.T) {
	h := NewHandlers(&mockStore{}, "*")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/energy-flow-hourly?date=2026-05-09", nil)
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400 got %d", rec.Code)
	}
}

func TestEnergyFlowHourly_RequiresDate(t *testing.T) {
	h := NewHandlers(&mockStore{}, "*")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/energy-flow-hourly?organization_id=demo", nil)
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400 got %d", rec.Code)
	}
}

func TestEnergyFlowHourly_RejectsBadDate(t *testing.T) {
	h := NewHandlers(&mockStore{}, "*")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/energy-flow-hourly?organization_id=demo&date=09-05-2026", nil)
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400 got %d", rec.Code)
	}
}

func TestEnergyFlowHourly_RejectsBadTimezone(t *testing.T) {
	h := NewHandlers(&mockStore{}, "*")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/energy-flow-hourly?organization_id=demo&date=2026-05-09&tz=Mars/Olympus", nil)
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400 got %d", rec.Code)
	}
}

// TestEnergyFlowHourly_Always24Hours verifies the response always
// contains 24 entries, even when no underlying rows back the day.
// The dashboard relies on this invariant to render its hourly chart
// without sparse-index handling.
func TestEnergyFlowHourly_Always24Hours(t *testing.T) {
	store := &mockStore{flowSources: nil}
	h := NewHandlers(store, "*")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/energy-flow-hourly?organization_id=demo&date=2026-05-09&tz=UTC", nil)
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 got %d, body=%s", rec.Code, rec.Body.String())
	}
	var body EnergyFlowHourlyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body.Hours) != 24 {
		t.Fatalf("hours count = %d, want 24", len(body.Hours))
	}
	for h, row := range body.Hours {
		if row.Hour != h {
			t.Fatalf("row[%d].Hour = %d", h, row.Hour)
		}
		if row.From.Hour() != h {
			t.Fatalf("row[%d].From hour = %d, want %d", h, row.From.Hour(), h)
		}
		if !row.To.Equal(row.From.Add(time.Hour)) {
			t.Fatalf("row[%d] To-From != 1h", h)
		}
		if row.PVToESSKwh != 0 || row.GridToESSKwh != 0 || row.ESSToLoadKwh != 0 || row.ESSToGridKwh != 0 {
			t.Fatalf("row[%d] expected zero flows, got %+v", h, row)
		}
	}
	if body.Date != "2026-05-09" {
		t.Fatalf("date = %q, want 2026-05-09", body.Date)
	}
	if body.Tz != "UTC" {
		t.Fatalf("tz = %q, want UTC", body.Tz)
	}
}

// TestEnergyFlowHourly_HourlySumsMatchDailyRecompute is the key
// invariant the financial-economics page depends on: summing the 24
// hourly results we return must equal a single Recompute over the
// same day-window. If the per-hour split ever drifts from the
// daily allocator (different bucket boundaries, sign normalization,
// etc.) the dashboard's hourly view would silently disagree with the
// "Перетік за день" card.
func TestEnergyFlowHourly_HourlySumsMatchDailyRecompute(t *testing.T) {
	dayStart := time.Date(2026, 5, 9, 0, 0, 0, 0, time.UTC)
	rows := syntheticDayRows(dayStart)

	rawRows := make([]EnergyFlowRawRow, 0, len(rows))
	for _, r := range rows {
		rawRows = append(rawRows, r)
	}

	store := &mockStore{flowSources: rawRows}
	h := NewHandlers(store, "*")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/energy-flow-hourly?organization_id=demo&date=2026-05-09&tz=UTC", nil)
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 got %d, body=%s", rec.Code, rec.Body.String())
	}
	var body EnergyFlowHourlyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	var sumPVtoESS, sumGridToESS, sumESStoLoad, sumESStoGrid float64
	for _, row := range body.Hours {
		sumPVtoESS += row.PVToESSKwh
		sumGridToESS += row.GridToESSKwh
		sumESStoLoad += row.ESSToLoadKwh
		sumESStoGrid += row.ESSToGridKwh
	}

	rawSamples := buildRawSamples(rawRows, EnergyFlowOrg{})
	daily := energyflow.Recompute(rawSamples, energyflow.Options{
		AllocationWindowSeconds: 60,
		MaxGapSeconds:           0,
	})

	const tol = 1e-6
	if !floatNearAPI(sumPVtoESS, daily.Totals[energyflow.MetricPVToESSKwh], tol) {
		t.Fatalf("pv_to_ess sum = %g, daily = %g", sumPVtoESS, daily.Totals[energyflow.MetricPVToESSKwh])
	}
	if !floatNearAPI(sumGridToESS, daily.Totals[energyflow.MetricGridToESSKwh], tol) {
		t.Fatalf("grid_to_ess sum = %g, daily = %g", sumGridToESS, daily.Totals[energyflow.MetricGridToESSKwh])
	}
	if !floatNearAPI(sumESStoLoad, daily.Totals[energyflow.MetricESSToLoadKwh], tol) {
		t.Fatalf("ess_to_load sum = %g, daily = %g", sumESStoLoad, daily.Totals[energyflow.MetricESSToLoadKwh])
	}
	if !floatNearAPI(sumESStoGrid, daily.Totals[energyflow.MetricESSToGridKwh], tol) {
		t.Fatalf("ess_to_grid sum = %g, daily = %g", sumESStoGrid, daily.Totals[energyflow.MetricESSToGridKwh])
	}
}

// TestEnergyFlowHourly_TimezonePartitionsCorrectly drives the
// handler with a localised day. Rows scheduled at "every hour on
// the hour" in Europe/Kyiv must each land in their own hour slot
// regardless of UTC offset jitter; the response should put 09:30
// Kyiv content in slot Hour=9 (not Hour=6 if we'd treated the row's
// UTC time as authoritative).
func TestEnergyFlowHourly_TimezonePartitionsCorrectly(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Kyiv")
	if err != nil {
		t.Fatalf("load Kyiv: %v", err)
	}
	// One single-SmartLogger snapshot per minute for 60 minutes
	// of 09:00–09:59 Kyiv local time. The ESS charge counter
	// ticks up by 1 kWh/minute so Recompute attributes ~59 kWh
	// to grid_to_ess (no PV produced). We deliberately stop
	// before 10:00 so every Allocate `curr` timestamp falls in
	// hour 9 — the right-closed bucketing convention would
	// otherwise spill the last interval's `curr=10:00` into
	// hour 10, which is correct but not what this test exercises.
	t0Kyiv := time.Date(2026, 5, 9, 9, 0, 0, 0, loc)
	rows := make([]EnergyFlowRawRow, 0, 60)
	for i := 0; i <= 59; i++ {
		ts := t0Kyiv.Add(time.Duration(i) * time.Minute)
		base := []EnergyFlowRawRow{
			{Time: ts, MetricKey: energyflow.SrcAccumulatedPVYieldKwh, Value: 1000},
			{Time: ts, MetricKey: energyflow.SrcAccumulatedPurchasedKwh, Value: 500 + float64(i)},
			{Time: ts, MetricKey: energyflow.SrcAccumulatedSoldKwh, Value: 0},
			{Time: ts, MetricKey: energyflow.SrcTotalEssChargedKwh, Value: 200 + float64(i)},
			{Time: ts, MetricKey: energyflow.SrcTotalEssDischargedKwh, Value: 0},
		}
		rows = append(rows, base...)
	}

	store := &mockStore{flowSources: rows}
	h := NewHandlers(store, "*")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/energy-flow-hourly?organization_id=demo&date=2026-05-09&tz=Europe/Kyiv", nil)
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 got %d, body=%s", rec.Code, rec.Body.String())
	}
	var body EnergyFlowHourlyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !strings.HasPrefix(body.Tz, "Europe/Kyiv") {
		t.Fatalf("tz = %q, want Europe/Kyiv", body.Tz)
	}
	for h, row := range body.Hours {
		got := row.GridToESSKwh
		if h == 9 {
			if got < 55 || got > 60 {
				t.Fatalf("hour 9 grid_to_ess = %g, want ~59", got)
			}
		} else if got != 0 {
			t.Fatalf("hour %d grid_to_ess = %g, want 0 (rows belong to hour 9)", h, got)
		}
	}
}

// syntheticDayRows produces a deterministic set of single-SmartLogger
// rows covering the full 24h of `dayStart` in 5-minute steps. PV
// generation peaks at noon, grid imports during the morning load
// ramp, ESS charges around midday from the PV surplus, and discharges
// in the evening. The exact numbers don't matter for the equivalence
// test — what matters is that the 5-minute polling cadence matches
// real telemetry density and that values vary by hour so the per-hour
// split can be checked against the daily run.
func syntheticDayRows(dayStart time.Time) []EnergyFlowRawRow {
	pv := 0.0
	purchased := 100.0
	sold := 0.0
	charged := 50.0
	discharged := 30.0
	rows := make([]EnergyFlowRawRow, 0, 24*12*5)
	for minute := 0; minute <= 24*60; minute += 5 {
		ts := dayStart.Add(time.Duration(minute) * time.Minute)
		// Hour-of-day shape: PV is a smooth bell from sunrise (06:00)
		// to sunset (20:00), peaking at noon. Grid import follows a
		// morning + evening load shape. Battery charges from the
		// midday surplus and discharges in the evening.
		h := float64(minute) / 60.0
		pvRate := 0.0
		if h > 6 && h < 20 {
			pvRate = 6 * math.Sin(math.Pi*(h-6)/14)
		}
		loadRate := 1.5 + 0.4*math.Sin(2*math.Pi*(h-7)/24)
		chargeRate := 0.0
		dischargeRate := 0.0
		surplus := pvRate - loadRate
		if surplus > 0 && h < 17 {
			chargeRate = math.Min(surplus, 3.0)
		} else if h >= 18 && h < 22 {
			dischargeRate = 2.0
		}
		gridImportRate := loadRate - pvRate + chargeRate
		if gridImportRate < 0 {
			gridImportRate = 0
		}
		gridExportRate := pvRate - loadRate - chargeRate
		if gridExportRate < 0 {
			gridExportRate = 0
		}
		dt := 5.0 / 60.0
		pv += pvRate * dt
		purchased += gridImportRate * dt
		sold += gridExportRate * dt
		charged += chargeRate * dt
		discharged += dischargeRate * dt
		rows = append(rows, EnergyFlowRawRow{Time: ts, MetricKey: energyflow.SrcAccumulatedPVYieldKwh, Value: pv})
		rows = append(rows, EnergyFlowRawRow{Time: ts, MetricKey: energyflow.SrcAccumulatedPurchasedKwh, Value: purchased})
		rows = append(rows, EnergyFlowRawRow{Time: ts, MetricKey: energyflow.SrcAccumulatedSoldKwh, Value: sold})
		rows = append(rows, EnergyFlowRawRow{Time: ts, MetricKey: energyflow.SrcTotalEssChargedKwh, Value: charged})
		rows = append(rows, EnergyFlowRawRow{Time: ts, MetricKey: energyflow.SrcTotalEssDischargedKwh, Value: discharged})
	}
	return rows
}

func floatNearAPI(a, b, tol float64) bool {
	if math.IsNaN(a) || math.IsNaN(b) {
		return false
	}
	d := a - b
	if d < 0 {
		d = -d
	}
	return d <= tol
}

// pin context import for older toolchains where unused-imports errors
// would otherwise surface; the test calls h.Router() which threads
// r.Context() through to the store's mock methods.
var _ = context.TODO
