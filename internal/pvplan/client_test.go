package pvplan

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// hour_ending 1..24 maps onto hour starts 0..23: the planner keys its
// PV curve by hour start, and an off-by-one here would shift the whole
// forecast an hour against the prices it's optimised against.
func TestAggregateHourlySumsOrientationsPerHour(t *testing.T) {
	rows := []map[string]any{
		{"hour_ending": 12.0, "orientation_idx": 1.0, "planned_kwh": 10.0},
		{"hour_ending": 12.0, "orientation_idx": 2.0, "planned_kwh": 5.0},
		{"hour_ending": 13.0, "orientation_idx": 1.0, "planned_kwh": 4.0},
		// Night hours contribute nothing and must not count as covered.
		{"hour_ending": 2.0, "orientation_idx": 1.0, "planned_kwh": 0.0},
		// Out-of-range and unparseable rows are skipped.
		{"hour_ending": 25.0, "orientation_idx": 1.0, "planned_kwh": 99.0},
		{"hour_ending": 14.0, "orientation_idx": 1.0, "planned_kwh": "not a number"},
	}
	got := AggregateHourly(rows)
	if len(got) != 2 {
		t.Fatalf("hours = %d (%v), want 2", len(got), got)
	}
	if got[11] != 15 {
		t.Fatalf("hour 11 = %v, want 15 (two orientations summed)", got[11])
	}
	if got[12] != 4 {
		t.Fatalf("hour 12 = %v, want 4", got[12])
	}
}

// The flow is not strict about types: numbers arrive as JSON numbers or
// as strings depending on the node that produced the row.
func TestAggregateHourlyAcceptsStringTypedNumbers(t *testing.T) {
	got := AggregateHourly([]map[string]any{
		{"hour_ending": "14", "orientation_idx": "1", "planned_kwh": " 33.5 "},
	})
	if got[13] != 33.5 {
		t.Fatalf("hour 13 = %v, want 33.5", got[13])
	}
}

// A repeated (hour_ending, orientation_idx) pair is the upstream feed
// re-publishing the same slot; counting both would double the day.
func TestAggregateHourlyDeduplicatesRepeatedSlots(t *testing.T) {
	got := AggregateHourly([]map[string]any{
		{"hour_ending": 12.0, "orientation_idx": 1.0, "planned_kwh": 10.0},
		{"hour_ending": 12.0, "orientation_idx": 1.0, "planned_kwh": 7.0},
	})
	if len(got) != 1 || got[11] != 7 {
		t.Fatalf("got %v, want {11: 7} (last write wins)", got)
	}
}

func TestDayTotalRequestsDayAndSums(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			{"hour_ending": 11, "orientation_idx": 1, "planned_kwh": 100},
			{"hour_ending": 12, "orientation_idx": 1, "planned_kwh": 120.5}
		]`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, srv.Client())
	day := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	kwh, ok, err := c.DayTotal(context.Background(), "AB", day)
	if err != nil {
		t.Fatalf("DayTotal: %v", err)
	}
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if kwh != 220.5 {
		t.Fatalf("kwh = %v, want 220.5", kwh)
	}
	if want := "elevator_code=AB&forecast_day=2026-08-01"; gotQuery != want {
		t.Fatalf("query = %q, want %q", gotQuery, want)
	}
}

// A day the flow knows nothing about is a normal answer, not an error:
// callers record the miss and report the day as uncovered.
func TestDayTotalReportsMissForDaysWithoutForecast(t *testing.T) {
	for name, body := range map[string]string{
		"empty array":  `[]`,
		"object body":  `{"message": "no data"}`,
		"zeroed hours": `[{"hour_ending": 2, "orientation_idx": 1, "planned_kwh": 0}]`,
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(body))
			}))
			defer srv.Close()

			c := NewClient(srv.URL, srv.Client())
			kwh, ok, err := c.DayTotal(context.Background(), "AB", time.Now())
			if err != nil {
				t.Fatalf("DayTotal: %v", err)
			}
			if ok {
				t.Fatal("ok = true, want false")
			}
			if kwh != 0 {
				t.Fatalf("kwh = %v, want 0", kwh)
			}
		})
	}
}

func TestDayTotalErrorsOnUpstreamFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusBadGateway)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, srv.Client())
	if _, _, err := c.DayTotal(context.Background(), "AB", time.Now()); err == nil {
		t.Fatal("expected an error for a 502 response")
	}
}

func TestElevatorCodeFor(t *testing.T) {
	if code, ok := ElevatorCodeFor("ab"); !ok || code != "AB" {
		t.Fatalf("ElevatorCodeFor(ab) = %q, %v; want AB, true", code, ok)
	}
	if _, ok := ElevatorCodeFor("demo-org"); ok {
		t.Fatal("demo-org must be unsupported")
	}
}
