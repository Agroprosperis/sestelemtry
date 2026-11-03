package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nesh/sestelemetry/internal/pvplan"
)

// These tests pin the per-station source switch: whatever reads a plan
// — the day chart, the month/year card, control — follows the station's
// passport, and switching it never mixes the two sources' numbers.

func modelStation(org string) pvplan.Station {
	lat, lon := 49.04, 27.74
	return pvplan.Station{
		OrganizationID: org,
		Source:         pvplan.SourceModel,
		Latitude:       &lat,
		Longitude:      &lon,
		PacLimitKw:     450,
		GammaPctPerC:   -0.29,
		Orientations: []pvplan.Orientation{
			{Idx: 1, TiltDeg: 30, AzimuthDeg: 0, ModulesCount: 549, ModulePowerW: 590, Enabled: true},
		},
	}
}

func pvForecastRequest(org, date string) *http.Request {
	return httptest.NewRequest(http.MethodGet,
		"/api/v1/pv-forecast?organization_id="+org+"&date="+date+"&tz=Europe/Kyiv", nil)
}

func decodePvForecast(t *testing.T, rec *httptest.ResponseRecorder) PvForecastResponse {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rec.Code, rec.Body.String())
	}
	var got PvForecastResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return got
}

// On the model, the day chart plots the day-ahead plan — the one
// plan-vs-actual compares against — not the freshest revision control
// uses.
func TestPvForecastServesTheModelsDayAheadPlan(t *testing.T) {
	store := &mockStore{
		pvStations: map[string]pvplan.Station{"ab": modelStation("ab")},
		pvHours: map[string]map[int]float64{
			"ab|2026-11-03|day_ahead": {11: 150, 10: 100, 3: 0},
			"ab|2026-11-03|latest":    {10: 999},
		},
	}
	h := NewHandlers(store, "*")

	rec := httptest.NewRecorder()
	h.pvForecast(rec, pvForecastRequest("ab", "2026-11-03"))
	got := decodePvForecast(t, rec)

	if !got.Supported || got.Source != "model" {
		t.Fatalf("supported=%v source=%q, want the model", got.Supported, got.Source)
	}
	if len(got.Hours) != 2 || got.Hours[0].Hour != 10 || got.Hours[1].Hour != 11 {
		t.Fatalf("hours = %+v, want 10 and 11 in order, the dark hour dropped", got.Hours)
	}
	if got.PlannedKwh != 250 {
		t.Fatalf("planned = %v, want the day-ahead 250", got.PlannedKwh)
	}
	if got.ComputedAt == nil {
		t.Fatal("computed_at must say when the plan was made")
	}
}

// A station still on n8n is read from the flow through the server-side
// cache: a dashboard poll doesn't call the flow again.
func TestPvForecastReadsTheFlowForAStationOnN8N(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(`[{"hour_ending": 12, "orientation_idx": 1, "planned_kwh": 120},
			{"hour_ending": 12, "orientation_idx": 2, "planned_kwh": 30}]`))
	}))
	defer srv.Close()

	onN8N := modelStation("ab")
	onN8N.Source = pvplan.SourceN8N
	store := &mockStore{
		pvStations: map[string]pvplan.Station{"ab": onN8N},
		// The model's plan is there, ready for a switch — and must not
		// leak into the n8n answer.
		pvHours: map[string]map[int]float64{"ab|2026-11-03|day_ahead": {11: 999}},
	}
	h := NewHandlers(store, "*")
	h.SetPvPlanClient(pvplan.NewClient(srv.URL, srv.Client()))

	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		h.pvForecast(rec, pvForecastRequest("ab", "2026-11-03"))
		got := decodePvForecast(t, rec)
		if got.Source != "n8n" || len(got.Hours) != 1 || got.Hours[0].Hour != 11 || got.Hours[0].PlannedKwh != 150 {
			t.Fatalf("response %d = %+v, want the flow's hour 11 = 150", i, got)
		}
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("flow called %d times, want once", n)
	}
}

// The n8n cache drops what no TTL can serve any more when it writes, so
// a long-running API doesn't keep every day anyone ever opened.
func TestN8NCacheDropsExpiredDaysOnWrite(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"hour_ending": 12, "orientation_idx": 1, "planned_kwh": 100}]`))
	}))
	defer srv.Close()
	h := NewHandlers(&mockStore{}, "*")
	h.SetPvPlanClient(pvplan.NewClient(srv.URL, srv.Client()))
	h.pvN8N.m = map[string]cachedPvPlan{
		"ab|2026-01-01": {byHour: map[int]float64{11: 1}, at: time.Now().Add(-25 * time.Hour)},
		"ab|2026-11-02": {byHour: map[int]float64{11: 2}, at: time.Now()},
	}

	if _, err := h.n8nDayHours(context.Background(), "ab", "AB", time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC), time.UTC); err != nil {
		t.Fatalf("n8nDayHours: %v", err)
	}
	if _, ok := h.pvN8N.m["ab|2026-01-01"]; ok {
		t.Fatal("an entry past every TTL survived the write")
	}
	if _, ok := h.pvN8N.m["ab|2026-11-02"]; !ok {
		t.Fatal("a fresh entry was dropped")
	}
	if _, ok := h.pvN8N.m["ab|2026-11-03"]; !ok {
		t.Fatal("the day just fetched was not cached")
	}
}

func TestPvForecastUnsupportedWithoutAnySource(t *testing.T) {
	store := &mockStore{pvStations: map[string]pvplan.Station{
		// On the model but with no planes yet.
		"sm": {OrganizationID: "sm", Source: pvplan.SourceModel},
	}}
	h := NewHandlers(store, "*")
	for _, org := range []string{"demo-org", "sm"} {
		rec := httptest.NewRecorder()
		h.pvForecast(rec, pvForecastRequest(org, "2026-11-03"))
		if got := decodePvForecast(t, rec); got.Supported {
			t.Fatalf("%s: supported, want hidden", org)
		}
	}
}

func TestPvForecastLabelsTheWholeElevatorFromItsDay(t *testing.T) {
	de := modelStation("de")
	de.WholeElevatorFrom = "2026-09-24"
	h := NewHandlers(&mockStore{pvStations: map[string]pvplan.Station{"de": de}}, "*")
	for date, whole := range map[string]bool{"2026-09-23": false, "2026-09-24": true} {
		rec := httptest.NewRecorder()
		h.pvForecast(rec, pvForecastRequest("de", date))
		if got := decodePvForecast(t, rec); got.WholeElevator != whole {
			t.Fatalf("%s: whole_elevator = %v, want %v", date, got.WholeElevator, whole)
		}
	}
}

// On the model, a month's plan is the sum of its day-ahead days; the
// n8n day cache, which keeps the flow's numbers for a switch back, is
// not read.
func TestPvPlanSummaryOnTheModelSumsItsOwnDays(t *testing.T) {
	store := &mockStore{
		pvStations: map[string]pvplan.Station{"ab": modelStation("ab")},
		pvModelDays: []PvPlanDayTotal{
			{Day: time.Date(2024, 5, 1, 0, 0, 0, 0, time.UTC), PlannedKwh: 2000},
			{Day: time.Date(2024, 5, 2, 0, 0, 0, 0, time.UTC), PlannedKwh: 1800},
		},
		pvPlanDays: []PvPlanDayTotal{
			{Day: time.Date(2024, 5, 1, 0, 0, 0, 0, time.UTC), PlannedKwh: 9999, FetchedAt: time.Now()},
		},
	}
	h := NewHandlers(store, "*")

	rec := httptest.NewRecorder()
	h.pvPlanSummary(rec, pvPlanRequest("ab",
		"2024-05-01T00:00:00+03:00", "2024-05-04T00:00:00+03:00", "Europe/Kyiv"))
	got := decodePvPlan(t, rec)

	if got.Source != "model" || !got.Supported {
		t.Fatalf("source=%q supported=%v, want the model", got.Source, got.Supported)
	}
	if got.PlannedKwh != 3800 || got.DaysCovered != 2 || got.DaysExpected != 3 {
		t.Fatalf("plan = %v over %d/%d days, want 3800 over 2/3", got.PlannedKwh, got.DaysCovered, got.DaysExpected)
	}
}

// Control acts on the freshest model revision, falls back to the
// day-ahead plan, and never plans one site's battery on the whole
// elevator's sun.
func TestPvPlanControlHoursFollowTheModel(t *testing.T) {
	kyiv, err := time.LoadLocation("Europe/Kyiv")
	if err != nil {
		t.Skip("no tzdata")
	}
	de := modelStation("de")
	de.WholeElevatorFrom = "2026-09-24"
	store := &mockStore{
		pvStations: map[string]pvplan.Station{"ab": modelStation("ab"), "de": de},
		pvHours: map[string]map[int]float64{
			"ab|2026-11-03|latest":    {12: 300},
			"ab|2026-11-03|day_ahead": {12: 280},
			"ab|2026-11-04|day_ahead": {12: 260},
			"de|2026-11-03|latest":    {12: 400},
		},
	}
	h := NewHandlers(store, "*")
	ctx := context.Background()

	if hours, ok := h.pvPlanControlHours(ctx, "ab", time.Date(2026, 11, 3, 0, 0, 0, 0, kyiv), kyiv); !ok || hours[12] != 300 {
		t.Fatalf("today = %v %v, want the latest 300", hours, ok)
	}
	if hours, ok := h.pvPlanControlHours(ctx, "ab", time.Date(2026, 11, 4, 0, 0, 0, 0, kyiv), kyiv); !ok || hours[12] != 260 {
		t.Fatalf("tomorrow = %v %v, want the day-ahead 260", hours, ok)
	}
	if _, ok := h.pvPlanControlHours(ctx, "de", time.Date(2026, 11, 3, 0, 0, 0, 0, kyiv), kyiv); ok {
		t.Fatal("a whole-elevator day must not reach control")
	}
}

// A passport that can't be read says nothing about the station's
// source. Falling back to n8n would serve a model station the flow's
// numbers and hand control Дубовʼязівський's whole-elevator sun, so
// every reader shows no plan instead — and never asks the flow.
func TestPvPlanReadsShowNoPlanWhenThePassportCantBeRead(t *testing.T) {
	kyiv, err := time.LoadLocation("Europe/Kyiv")
	if err != nil {
		t.Skip("no tzdata")
	}
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(`[{"hour_ending": 12, "orientation_idx": 1, "planned_kwh": 500}]`))
	}))
	defer srv.Close()

	store := &mockStore{pvStationErr: errors.New("connection reset")}
	h := NewHandlers(store, "*")
	h.SetPvPlanClient(pvplan.NewClient(srv.URL, srv.Client()))

	rec := httptest.NewRecorder()
	h.pvForecast(rec, pvForecastRequest("de", "2026-11-03"))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("pv-forecast status = %d, want 500", rec.Code)
	}

	rec = httptest.NewRecorder()
	h.pvPlanSummary(rec, pvPlanRequest("de",
		"2026-10-01T00:00:00+03:00", "2026-10-03T00:00:00+03:00", "Europe/Kyiv"))
	if got := decodePvPlan(t, rec); got.Supported {
		t.Fatalf("pv-plan-summary = %+v, want no plan", got)
	}

	if _, ok := h.pvPlanControlHours(context.Background(), "de", time.Date(2026, 11, 3, 0, 0, 0, 0, kyiv), kyiv); ok {
		t.Fatal("control got a plan for a station whose passport couldn't be read")
	}
	if n := calls.Load(); n != 0 {
		t.Fatalf("the flow was asked %d times; a failed passport read must not fall back to it", n)
	}
}

// --- the passport's PUT goes through the planner ---

type apiPvStore struct {
	mu       sync.Mutex
	stations map[string]pvplan.Station
	saveErr  error
}

func (s *apiPvStore) Station(_ context.Context, org string) (pvplan.Station, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.stations[org]
	return st, ok, nil
}

func (s *apiPvStore) SaveStation(_ context.Context, st pvplan.Station, _ string) (pvplan.Station, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.saveErr != nil {
		return pvplan.Station{}, s.saveErr
	}
	s.stations[st.OrganizationID] = st
	return st, nil
}

func (s *apiPvStore) History(context.Context, string, int) ([]pvplan.HistoryEntry, error) {
	return nil, nil
}

func (s *apiPvStore) SeedStations(context.Context, []pvplan.Station) ([]string, error) {
	return nil, nil
}

func (s *apiPvStore) SaveDays(context.Context, string, pvplan.Kind, string, []pvplan.PlanDay) error {
	return nil
}

func (s *apiPvStore) HasDay(context.Context, string, string, pvplan.Kind) (bool, error) {
	return false, nil
}

func (s *apiPvStore) MarkHistoryBackfilled(context.Context, string, time.Time) error { return nil }

func (s *apiPvStore) DataStart(context.Context, string) (time.Time, bool, error) {
	return time.Time{}, false, nil
}

type offlineWeather struct{}

func (offlineWeather) Forecast(context.Context, pvplan.Plane) ([]pvplan.HourInput, error) {
	return nil, errors.New("offline")
}

func (offlineWeather) PreviousDay1(context.Context, pvplan.Plane, string, string) ([]pvplan.HourInput, error) {
	return nil, errors.New("offline")
}

func newPassportHandlers() (*Handlers, *apiPvStore) {
	pv := &apiPvStore{stations: map[string]pvplan.Station{}}
	h := NewHandlers(&mockStore{}, "*")
	h.SetPvPlanner(pvplan.NewPlanner(pv, offlineWeather{}, time.UTC, slog.New(slog.NewTextHandler(io.Discard, nil))))
	return h, pv
}

func putPassport(h *Handlers, org string, st pvplan.Station) *httptest.ResponseRecorder {
	body, _ := json.Marshal(st)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/pv-orientations?organization_id="+org, bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.pvOrientations(rec, req)
	return rec
}

func TestPvOrientationsSwitchesAStationToTheModel(t *testing.T) {
	h, pv := newPassportHandlers()
	rec := putPassport(h, "ab", modelStation("ab"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rec.Code, rec.Body.String())
	}
	if pv.stations["ab"].Source != pvplan.SourceModel {
		t.Fatalf("saved source = %q, want model", pv.stations["ab"].Source)
	}
}

func TestPvOrientationsRefusesASourceTheStationCantUse(t *testing.T) {
	h, pv := newPassportHandlers()

	// The model with nothing to model.
	empty := pvplan.Station{Source: pvplan.SourceModel}
	if rec := putPassport(h, "ab", empty); rec.Code != http.StatusBadRequest {
		t.Fatalf("model without planes: status %d, want 400", rec.Code)
	}
	// n8n for a station the flow has no code for.
	onN8N := modelStation("demo-org")
	onN8N.Source = pvplan.SourceN8N
	if rec := putPassport(h, "demo-org", onN8N); rec.Code != http.StatusBadRequest {
		t.Fatalf("n8n without a code: status %d, want 400", rec.Code)
	}
	if len(pv.stations) != 0 {
		t.Fatalf("a refused passport was saved: %v", pv.stations)
	}
}

// A passport that is fine but fails to store is the server's fault, not
// the engineer's: 500, with no validation text to act on.
func TestPvOrientationsStoreFailureIsAServerError(t *testing.T) {
	h, pv := newPassportHandlers()
	pv.saveErr = errors.New("deadlock detected")
	rec := putPassport(h, "ab", modelStation("ab"))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, body = %q, want 500", rec.Code, rec.Body.String())
	}
}
