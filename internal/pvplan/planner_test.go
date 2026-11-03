package pvplan

import (
	"context"
	"io"
	"log/slog"
	"sort"
	"sync"
	"testing"
	"time"
)

type savedDays struct {
	org     string
	kind    Kind
	version string
	days    []string
}

type fakeStore struct {
	mu         sync.Mutex
	stations   map[string]Station
	seedCalls  [][]string
	saved      []savedDays
	hasDay     map[string]bool // org|day|kind
	backfilled map[string]time.Time
	dataStart  map[string]time.Time
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		stations:   map[string]Station{},
		hasDay:     map[string]bool{},
		backfilled: map[string]time.Time{},
		dataStart:  map[string]time.Time{},
	}
}

func (f *fakeStore) Station(_ context.Context, org string) (Station, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	st, ok := f.stations[org]
	return st, ok, nil
}

func (f *fakeStore) SaveStation(_ context.Context, st Station, _ string) (Station, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stations[st.OrganizationID] = st
	return st, nil
}

func (f *fakeStore) History(context.Context, string, int) ([]HistoryEntry, error) { return nil, nil }

// SeedStations mirrors the storage contract: a station that already has
// a passport is left as it is.
func (f *fakeStore) SeedStations(_ context.Context, stations []Station) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var orgs, written []string
	for _, st := range stations {
		orgs = append(orgs, st.OrganizationID)
		if _, ok := f.stations[st.OrganizationID]; ok {
			continue
		}
		f.stations[st.OrganizationID] = st
		written = append(written, st.OrganizationID)
	}
	f.seedCalls = append(f.seedCalls, orgs)
	return written, nil
}

func (f *fakeStore) SaveDays(_ context.Context, org string, kind Kind, version string, days []PlanDay) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	rec := savedDays{org: org, kind: kind, version: version}
	for _, d := range days {
		rec.days = append(rec.days, d.Day)
	}
	sort.Strings(rec.days)
	f.saved = append(f.saved, rec)
	return nil
}

func (f *fakeStore) HasDay(_ context.Context, org, day string, kind Kind) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hasDay[org+"|"+day+"|"+string(kind)], nil
}

func (f *fakeStore) MarkHistoryBackfilled(_ context.Context, org string, at time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.backfilled[org] = at
	return nil
}

func (f *fakeStore) DataStart(_ context.Context, org string) (time.Time, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.dataStart[org]
	return t, ok, nil
}

func (f *fakeStore) savedOf(kind Kind) []savedDays {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []savedDays
	for _, s := range f.saved {
		if s.kind == kind && len(s.days) > 0 {
			out = append(out, s)
		}
	}
	return out
}

// fakeWeather answers every hour of the asked span with the same sun.
type fakeWeather struct {
	mu        sync.Mutex
	now       time.Time
	forecasts int
	previous  [][2]string
}

func sunnyHours(from, to time.Time) []HourInput {
	var out []HourInput
	for t := from; t.Before(to); t = t.Add(time.Hour) {
		out = append(out, HourInput{Start: t, TempC: 20, GtiWm2: 500})
	}
	return out
}

func (w *fakeWeather) Forecast(context.Context, Plane) ([]HourInput, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.forecasts++
	day := time.Date(w.now.Year(), w.now.Month(), w.now.Day(), 0, 0, 0, 0, time.UTC)
	return sunnyHours(day.AddDate(0, 0, -1), day.AddDate(0, 0, 3)), nil
}

func (w *fakeWeather) PreviousDay1(_ context.Context, _ Plane, fromDay, toDay string) ([]HourInput, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.previous = append(w.previous, [2]string{fromDay, toDay})
	from, _ := time.Parse("2006-01-02", fromDay)
	to, _ := time.Parse("2006-01-02", toDay)
	return sunnyHours(from, to.AddDate(0, 0, 1)), nil
}

func kyivOrSkip(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Kyiv")
	if err != nil {
		t.Skip("no tzdata")
	}
	return loc
}

func newTestPlanner(t *testing.T, now time.Time) (*Planner, *fakeStore, *fakeWeather) {
	t.Helper()
	store := newFakeStore()
	weather := &fakeWeather{now: now.UTC()}
	p := NewPlanner(store, weather, now.Location(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	p.now = func() time.Time { return now }
	p.pause = 0
	return p, store, weather
}

func abStation() Station {
	lat, lon := 49.0406538, 27.7419296
	st := pdeStation()
	st.OrganizationID = "ab"
	st.Latitude, st.Longitude = &lat, &lon
	return st
}

// A day's day-ahead plan is the last one computed before it starts:
// once today has one, runs during the day only revise its latest plan.
func TestForecastFreezesTheDayAheadPlanOnceTheDayStarts(t *testing.T) {
	loc := kyivOrSkip(t)
	p, store, _ := newTestPlanner(t, time.Date(2026, 11, 3, 10, 0, 0, 0, loc))
	store.stations["ab"] = abStation()
	store.hasDay["ab|2026-11-03|day_ahead"] = true

	if err := p.Forecast(context.Background(), "ab"); err != nil {
		t.Fatalf("Forecast: %v", err)
	}
	latest := store.savedOf(Latest)
	if len(latest) != 1 || len(latest[0].days) != 2 || latest[0].days[0] != "2026-11-03" || latest[0].days[1] != "2026-11-04" {
		t.Fatalf("latest = %+v, want today and tomorrow", latest)
	}
	dayAhead := store.savedOf(DayAhead)
	if len(dayAhead) != 1 || len(dayAhead[0].days) != 1 || dayAhead[0].days[0] != "2026-11-04" {
		t.Fatalf("day_ahead = %+v, want tomorrow only", dayAhead)
	}
	if dayAhead[0].version != ModelVersion {
		t.Fatalf("version = %q, want %q", dayAhead[0].version, ModelVersion)
	}
}

// The first run of a day that has no day-ahead plan (the service was
// down the evening before, or just went live) stands in for one.
func TestForecastFillsAMissingDayAheadPlanForToday(t *testing.T) {
	loc := kyivOrSkip(t)
	p, store, _ := newTestPlanner(t, time.Date(2026, 11, 3, 10, 0, 0, 0, loc))
	store.stations["ab"] = abStation()

	if err := p.Forecast(context.Background(), "ab"); err != nil {
		t.Fatalf("Forecast: %v", err)
	}
	dayAhead := store.savedOf(DayAhead)
	if len(dayAhead) != 1 || len(dayAhead[0].days) != 2 {
		t.Fatalf("day_ahead = %+v, want today and tomorrow", dayAhead)
	}
}

// The model runs for a station on the n8n source too, so switching it
// finds the plan ready — but a passport without planes has nothing to
// ask the weather about.
func TestForecastRunsWhateverTheSourceButNeedsPlanes(t *testing.T) {
	loc := kyivOrSkip(t)
	p, store, weather := newTestPlanner(t, time.Date(2026, 11, 3, 10, 0, 0, 0, loc))
	onN8N := abStation()
	onN8N.Source = SourceN8N
	store.stations["ab"] = onN8N
	store.stations["ke"] = Station{OrganizationID: "ke", Source: SourceN8N}

	if err := p.Forecast(context.Background(), "ab"); err != nil {
		t.Fatalf("Forecast ab: %v", err)
	}
	if err := p.Forecast(context.Background(), "ke"); err != nil {
		t.Fatalf("Forecast ke: %v", err)
	}
	if weather.forecasts != 1 {
		t.Fatalf("weather asked %d times, want once (ab's single plane; ke has none)", weather.forecasts)
	}
	for _, s := range store.saved {
		if s.org == "ke" && len(s.days) > 0 {
			t.Fatalf("ke got a plan without planes: %+v", s)
		}
	}
}

// History is rebuilt from the station's first telemetry day to
// yesterday, from the day-ahead archive, and marked done.
func TestHistoryRebuildsFromTheFirstTelemetryDay(t *testing.T) {
	loc := kyivOrSkip(t)
	p, store, weather := newTestPlanner(t, time.Date(2026, 11, 3, 10, 0, 0, 0, loc))
	store.stations["ab"] = abStation()
	store.dataStart["ab"] = time.Date(2026, 10, 30, 8, 0, 0, 0, time.UTC)

	status, err := p.beginHistory("ab")
	if err != nil {
		t.Fatalf("beginHistory: %v", err)
	}
	if err := p.runHistory(context.Background(), "ab", "", "", status); err != nil {
		t.Fatalf("runHistory: %v", err)
	}
	if status.From != "2026-10-30" || status.To != "2026-11-02" || status.Days != 4 {
		t.Fatalf("status = %+v, want 2026-10-30..2026-11-02, 4 days", status)
	}
	// The UTC day before holds the first local hours of the first day.
	if len(weather.previous) != 1 || weather.previous[0] != [2]string{"2026-10-29", "2026-11-02"} {
		t.Fatalf("archive asked for %v", weather.previous)
	}
	dayAhead := store.savedOf(DayAhead)
	if len(dayAhead) != 1 || dayAhead[0].version != HistoryModelVersion || len(dayAhead[0].days) != 4 {
		t.Fatalf("saved = %+v, want 4 history days", dayAhead)
	}
	if _, ok := store.backfilled["ab"]; !ok {
		t.Fatal("a finished rebuild must be recorded, or every restart redoes it")
	}
}

func TestHistoryRunsOneRebuildPerStationAtATime(t *testing.T) {
	loc := kyivOrSkip(t)
	p, _, _ := newTestPlanner(t, time.Date(2026, 11, 3, 10, 0, 0, 0, loc))
	if _, err := p.beginHistory("ab"); err != nil {
		t.Fatalf("first: %v", err)
	}
	if _, err := p.beginHistory("ab"); !ErrHistoryRunning(err) {
		t.Fatalf("second: err = %v, want the running error", err)
	}
}

// Only stations this deployment serves are seeded, and a station that
// already has a passport keeps it.
func TestSeedOnlyServedStationsAndKeepsEdits(t *testing.T) {
	loc := kyivOrSkip(t)
	p, store, _ := newTestPlanner(t, time.Date(2026, 11, 3, 10, 0, 0, 0, loc))
	edited := abStation()
	edited.PacLimitKw = 999
	store.stations["ab"] = edited
	p.SetOrganizations([]string{"ab", "ze"}, nil)

	if err := p.Seed(context.Background()); err != nil {
		t.Fatalf("Seed: %v", err)
	}
	if len(store.seedCalls) != 1 {
		t.Fatalf("seed calls = %v", store.seedCalls)
	}
	offered := store.seedCalls[0]
	sort.Strings(offered)
	if len(offered) != 2 || offered[0] != "ab" || offered[1] != "ze" {
		t.Fatalf("offered %v, want only the served ab and ze", offered)
	}
	if store.stations["ab"].PacLimitKw != 999 {
		t.Fatal("seeding overwrote an edited passport")
	}
	if _, ok := store.stations["ze"]; !ok {
		t.Fatal("ze had no passport and must have been seeded")
	}
}

func TestNextRunIsTheNextSixOrEighteen(t *testing.T) {
	loc := kyivOrSkip(t)
	cases := []struct{ now, want time.Time }{
		{time.Date(2026, 11, 3, 5, 0, 0, 0, loc), time.Date(2026, 11, 3, 6, 0, 0, 0, loc)},
		{time.Date(2026, 11, 3, 6, 0, 0, 0, loc), time.Date(2026, 11, 3, 18, 0, 0, 0, loc)},
		{time.Date(2026, 11, 3, 19, 0, 0, 0, loc), time.Date(2026, 11, 4, 6, 0, 0, 0, loc)},
	}
	for _, c := range cases {
		if got := NextRun(c.now, loc); !got.Equal(c.want) {
			t.Errorf("NextRun(%v) = %v, want %v", c.now, got, c.want)
		}
	}
}
