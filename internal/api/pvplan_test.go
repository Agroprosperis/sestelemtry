package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	neturl "net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nesh/sestelemetry/internal/pvplan"
)

func pvPlanRequest(orgID, from, to, tz string) *http.Request {
	url := fmt.Sprintf(
		"/api/v1/pv-plan-summary?organization_id=%s&from=%s&to=%s&tz=%s",
		orgID, neturl.QueryEscape(from), neturl.QueryEscape(to), neturl.QueryEscape(tz),
	)
	return httptest.NewRequest(http.MethodGet, url, nil)
}

func decodePvPlan(t *testing.T, rec *httptest.ResponseRecorder) PvPlanSummaryResponse {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rec.Code, rec.Body.String())
	}
	var got PvPlanSummaryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return got
}

// The cache alone answers the request when every day in range is
// already stored, which is the steady state for a finished month.
func TestPvPlanSummarySumsCachedDays(t *testing.T) {
	store := &mockStore{
		pvPlanDays: []PvPlanDayTotal{
			{Day: time.Date(2024, 5, 1, 0, 0, 0, 0, time.UTC), PlannedKwh: 2000, FetchedAt: time.Now()},
			{Day: time.Date(2024, 5, 2, 0, 0, 0, 0, time.UTC), PlannedKwh: 1500, FetchedAt: time.Now()},
			// A recorded miss: asked, upstream had nothing. It must not
			// inflate coverage, and its zero must not lower the total.
			{Day: time.Date(2024, 5, 3, 0, 0, 0, 0, time.UTC), PlannedKwh: 0, FetchedAt: time.Now()},
		},
	}
	h := NewHandlers(store, "*")

	rec := httptest.NewRecorder()
	h.pvPlanSummary(rec, pvPlanRequest("ab",
		"2024-05-01T00:00:00+03:00", "2024-05-04T00:00:00+03:00", "Europe/Kyiv"))

	got := decodePvPlan(t, rec)
	if !got.Supported {
		t.Fatal("supported = false, want true for ab")
	}
	if got.PlannedKwh != 3500 {
		t.Fatalf("planned_kwh = %v, want 3500", got.PlannedKwh)
	}
	if got.DaysCovered != 2 || got.DaysExpected != 3 {
		t.Fatalf("coverage = %d/%d, want 2/3", got.DaysCovered, got.DaysExpected)
	}
	if got.FromDay != "2024-05-01" || got.ToDay != "2024-05-03" {
		t.Fatalf("day span = %s..%s, want 2024-05-01..2024-05-03", got.FromDay, got.ToDay)
	}
	// The store is queried by civil date in the request's timezone: a
	// Kyiv window starting at local midnight must not pull in the
	// previous UTC day.
	if store.pvPlanFromDay.Format("2006-01-02") != "2024-05-01" {
		t.Fatalf("queried from_day = %v, want 2024-05-01", store.pvPlanFromDay)
	}
}

// Days the cache is missing are fetched from the flow and written back,
// so the next request for the same period costs one indexed read.
func TestPvPlanSummaryFillsMissingDaysFromFlow(t *testing.T) {
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(`[{"hour_ending": 12, "orientation_idx": 1, "planned_kwh": 250}]`))
	}))
	defer srv.Close()

	store := &mockStore{}
	h := NewHandlers(store, "*")
	h.SetPvPlanClient(pvplan.NewClient(srv.URL, srv.Client()))

	rec := httptest.NewRecorder()
	h.pvPlanSummary(rec, pvPlanRequest("ab",
		"2024-05-01T00:00:00+03:00", "2024-05-03T00:00:00+03:00", "Europe/Kyiv"))

	got := decodePvPlan(t, rec)
	if got.PlannedKwh != 500 {
		t.Fatalf("planned_kwh = %v, want 500", got.PlannedKwh)
	}
	if got.DaysCovered != 2 || got.DaysExpected != 2 {
		t.Fatalf("coverage = %d/%d, want 2/2", got.DaysCovered, got.DaysExpected)
	}
	if n := calls.Load(); n != 2 {
		t.Fatalf("upstream calls = %d, want 2 (one per missing day)", n)
	}
	if len(store.pvPlanSaved) != 2 {
		t.Fatalf("saved %d days, want 2", len(store.pvPlanSaved))
	}
	if store.pvPlanSavedOrg != "ab" {
		t.Fatalf("saved org = %q, want ab", store.pvPlanSavedOrg)
	}
}

// A day whose fetch failed stays out of the cache so the next request
// retries it, rather than being recorded as "upstream has nothing".
func TestPvPlanSummaryLeavesFailedFetchesUncached(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusBadGateway)
	}))
	defer srv.Close()

	store := &mockStore{}
	h := NewHandlers(store, "*")
	h.SetPvPlanClient(pvplan.NewClient(srv.URL, srv.Client()))

	rec := httptest.NewRecorder()
	h.pvPlanSummary(rec, pvPlanRequest("ab",
		"2024-05-01T00:00:00+03:00", "2024-05-02T00:00:00+03:00", "Europe/Kyiv"))

	got := decodePvPlan(t, rec)
	if got.PlannedKwh != 0 || got.DaysCovered != 0 {
		t.Fatalf("planned_kwh = %v, days_covered = %d; want 0 and 0", got.PlannedKwh, got.DaysCovered)
	}
	if got.DaysExpected != 1 {
		t.Fatalf("days_expected = %d, want 1", got.DaysExpected)
	}
	if len(store.pvPlanSaved) != 0 {
		t.Fatalf("saved %d days, want none", len(store.pvPlanSaved))
	}
}

// An organization the flow has no site code for reports supported=false
// rather than a plan of zero, so the dashboard hides the comparison.
func TestPvPlanSummaryUnsupportedOrganization(t *testing.T) {
	store := &mockStore{}
	h := NewHandlers(store, "*")

	rec := httptest.NewRecorder()
	h.pvPlanSummary(rec, pvPlanRequest("demo-org",
		"2024-05-01T00:00:00Z", "2024-05-02T00:00:00Z", "UTC"))

	got := decodePvPlan(t, rec)
	if got.Supported {
		t.Fatal("supported = true, want false for demo-org")
	}
	if store.pvPlanOrg != "" {
		t.Fatalf("store was queried for %q; unsupported orgs must not hit the cache", store.pvPlanOrg)
	}
}

// The window's end is clamped to today: the flow forecasts ~2 weeks
// ahead, so counting the rest of the month would compare a full-period
// plan against elapsed-period actuals.
func TestPvPlanSummaryClampsWindowToToday(t *testing.T) {
	store := &mockStore{}
	h := NewHandlers(store, "*")

	future := time.Now().AddDate(1, 0, 0).UTC()
	rec := httptest.NewRecorder()
	h.pvPlanSummary(rec, pvPlanRequest("ab",
		future.Format(time.RFC3339), future.AddDate(0, 1, 0).Format(time.RFC3339), "UTC"))

	got := decodePvPlan(t, rec)
	if !got.Supported {
		t.Fatal("supported = false, want true")
	}
	if got.DaysExpected != 0 || got.PlannedKwh != 0 {
		t.Fatalf("days_expected = %d, planned_kwh = %v; a wholly future period has no plan to report",
			got.DaysExpected, got.PlannedKwh)
	}
}

// A month still in progress counts today but not tomorrow.
func TestPvPlanSummaryCountsElapsedDaysOfCurrentPeriod(t *testing.T) {
	store := &mockStore{}
	h := NewHandlers(store, "*")

	now := time.Now().UTC()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	monthEnd := monthStart.AddDate(0, 1, 0)
	rec := httptest.NewRecorder()
	h.pvPlanSummary(rec, pvPlanRequest("ab",
		monthStart.Format(time.RFC3339), monthEnd.Format(time.RFC3339), "UTC"))

	got := decodePvPlan(t, rec)
	if got.DaysExpected != now.Day() {
		t.Fatalf("days_expected = %d, want %d (days elapsed including today)", got.DaysExpected, now.Day())
	}
	if got.ToDay != now.Format("2006-01-02") {
		t.Fatalf("to_day = %s, want %s", got.ToDay, now.Format("2006-01-02"))
	}
}

// A cache read failure must not present a plan built from whichever
// days happened to be readable.
func TestPvPlanSummaryCacheErrorReportsNoCoverage(t *testing.T) {
	store := &mockStore{pvPlanErr: fmt.Errorf("relation pv_plan_daily does not exist")}
	h := NewHandlers(store, "*")

	rec := httptest.NewRecorder()
	h.pvPlanSummary(rec, pvPlanRequest("ab",
		"2024-05-01T00:00:00Z", "2024-05-04T00:00:00Z", "UTC"))

	got := decodePvPlan(t, rec)
	if got.PlannedKwh != 0 || got.DaysCovered != 0 {
		t.Fatalf("planned_kwh = %v, days_covered = %d; want zeros", got.PlannedKwh, got.DaysCovered)
	}
}

func TestPvPlanSummaryRejectsBadRequests(t *testing.T) {
	h := NewHandlers(&mockStore{}, "*")

	cases := map[string]*http.Request{
		"missing org":    pvPlanRequest("", "2024-05-01T00:00:00Z", "2024-05-02T00:00:00Z", "UTC"),
		"inverted range": pvPlanRequest("ab", "2024-05-02T00:00:00Z", "2024-05-01T00:00:00Z", "UTC"),
		"unknown tz":     pvPlanRequest("ab", "2024-05-01T00:00:00Z", "2024-05-02T00:00:00Z", "Mars/Olympus"),
		"missing range":  httptest.NewRequest(http.MethodGet, "/api/v1/pv-plan-summary?organization_id=ab", nil),
	}
	for name, req := range cases {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.pvPlanSummary(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body %q)", rec.Code, rec.Body.String())
			}
		})
	}
}
