package pvplan

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// Kind separates the two plans kept for every model hour.
//
// DayAhead is what plan-vs-actual compares against: a day's plan is
// frozen at the last run before the day starts, the way a РДН bid is,
// so intraday revisions can't chase the weather the day already showed.
// Latest is the freshest revision, for control (dispatch, edge, the
// УЗЕ plan for tomorrow), which should always act on the best guess.
type Kind string

const (
	DayAhead Kind = "day_ahead"
	Latest   Kind = "latest"
)

// Store persists passports and model plans.
type Store interface {
	Station(ctx context.Context, org string) (Station, bool, error)
	// SaveStation replaces the passport and records what changed.
	SaveStation(ctx context.Context, st Station, by string) (Station, error)
	History(ctx context.Context, org string, limit int) ([]HistoryEntry, error)
	// SeedStations writes the passports of stations that have none and
	// returns which it wrote.
	SeedStations(ctx context.Context, stations []Station) ([]string, error)
	// SaveDays replaces the given days' hours of one kind.
	SaveDays(ctx context.Context, org string, kind Kind, version string, days []PlanDay) error
	HasDay(ctx context.Context, org, day string, kind Kind) (bool, error)
	MarkHistoryBackfilled(ctx context.Context, org string, at time.Time) error
	// DataStart is the first instant the organization has telemetry.
	DataStart(ctx context.Context, org string) (time.Time, bool, error)
}

// Weather is the forecast source.
type Weather interface {
	Forecast(ctx context.Context, p Plane) ([]HourInput, error)
	PreviousDay1(ctx context.Context, p Plane, fromDay, toDay string) ([]HourInput, error)
}

// Coordinates is a fallback site location (config.yaml).
type Coordinates struct {
	Latitude  float64
	Longitude float64
}

// runHours are the local hours the forecast is refreshed at. 18:00 is
// the last run before midnight, so it is what freezes tomorrow's
// day-ahead plan.
var runHours = []int{6, 18}

// historyEarliest bounds the archive: Open-Meteo keeps previous runs
// from January 2024.
const historyEarliest = "2024-01-01"

// historyChunkDays keeps each archive request well inside what the API
// answers in one go.
const historyChunkDays = 31

// Planner computes model plans for the stations this deployment
// serves, on a schedule and on demand.
type Planner struct {
	store   Store
	weather Weather
	loc     *time.Location
	log     *slog.Logger

	// now and pause are swapped by tests.
	now   func() time.Time
	pause time.Duration

	mu        sync.Mutex
	base      context.Context
	orgs      []string
	locations map[string]Coordinates
	history   map[string]*HistoryStatus
	inflight  map[string]bool
	rerun     map[string]bool
}

// NewPlanner wires a planner; loc is the zone civil days are counted in.
func NewPlanner(store Store, weather Weather, loc *time.Location, log *slog.Logger) *Planner {
	if loc == nil {
		loc = time.UTC
	}
	if log == nil {
		log = slog.Default()
	}
	return &Planner{
		store:    store,
		weather:  weather,
		loc:      loc,
		log:      log,
		now:      time.Now,
		pause:    time.Second,
		base:     context.Background(),
		history:  map[string]*HistoryStatus{},
		inflight: map[string]bool{},
		rerun:    map[string]bool{},
	}
}

// SetOrganizations names the organizations this deployment serves and
// their config.yaml locations. Only these get seeded and planned, so a
// dev stack with a demo site never calls Open-Meteo for real sites.
func (p *Planner) SetOrganizations(orgs []string, locations map[string]Coordinates) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.orgs = append([]string(nil), orgs...)
	p.locations = locations
}

func (p *Planner) organizations() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.orgs...)
}

// Location is the zone the planner counts civil days in.
func (p *Planner) Location() *time.Location {
	return p.loc
}

// DefaultCoordinates returns the config.yaml location of org.
func (p *Planner) DefaultCoordinates(org string) (Coordinates, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	c, ok := p.locations[org]
	return c, ok
}

// Seed writes the starting passports of the served stations that have
// none yet.
func (p *Planner) Seed(ctx context.Context) error {
	seed, err := SeedStations()
	if err != nil {
		return err
	}
	served := map[string]bool{}
	for _, org := range p.organizations() {
		served[org] = true
	}
	var mine []Station
	for _, st := range seed {
		if served[st.OrganizationID] {
			mine = append(mine, st)
		}
	}
	written, err := p.store.SeedStations(ctx, mine)
	if err != nil {
		return err
	}
	if len(written) > 0 {
		p.log.Info("pv_plan_seeded", "stations", written)
	}
	return nil
}

// Station returns org's passport.
func (p *Planner) Station(ctx context.Context, org string) (Station, bool, error) {
	return p.store.Station(ctx, org)
}

// History returns org's passport edits, newest first.
func (p *Planner) History(ctx context.Context, org string, limit int) ([]HistoryEntry, error) {
	return p.store.History(ctx, org, limit)
}

// SaveStation validates and stores a passport, then refreshes the
// station's today/tomorrow plans in the background so the edit shows
// in control and in tomorrow's plan right away. Today's frozen
// day-ahead plan is left alone.
func (p *Planner) SaveStation(ctx context.Context, st Station, by string) (Station, error) {
	if err := st.Validate(); err != nil {
		return Station{}, err
	}
	saved, err := p.store.SaveStation(ctx, st, by)
	if err != nil {
		return Station{}, err
	}
	go func() {
		ctx, cancel := context.WithTimeout(p.baseContext(), 5*time.Minute)
		defer cancel()
		if err := p.Forecast(ctx, saved.OrganizationID); err != nil {
			p.log.Warn("pv_plan_forecast_after_save", "organization_id", saved.OrganizationID, "err", err)
		}
	}()
	return saved, nil
}

func (p *Planner) baseContext() context.Context {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.base
}

// coordinates resolves where a station is: its passport, else config.
func (p *Planner) coordinates(st Station) (Coordinates, bool) {
	if st.Latitude != nil && st.Longitude != nil {
		return Coordinates{Latitude: *st.Latitude, Longitude: *st.Longitude}, true
	}
	return p.DefaultCoordinates(st.OrganizationID)
}

// errNoCoordinates is shown in the passport when a rebuild can't start.
func errNoCoordinates(org string) error {
	return fmt.Errorf("немає координат станції %s: задайте їх у паспорті або в config.yaml", org)
}

// fetchSeries asks the weather source once per enabled plane.
func (p *Planner) fetchSeries(ctx context.Context, st Station, get func(Plane) ([]HourInput, error)) ([][]HourInput, error) {
	where, ok := p.coordinates(st)
	if !ok {
		return nil, errNoCoordinates(st.OrganizationID)
	}
	planes := st.EnabledOrientations()
	series := make([][]HourInput, len(planes))
	for i, o := range planes {
		if i > 0 {
			if err := sleep(ctx, p.pause); err != nil {
				return nil, err
			}
		}
		hours, err := get(Plane{Latitude: where.Latitude, Longitude: where.Longitude, TiltDeg: o.TiltDeg, AzimuthDeg: o.AzimuthDeg})
		if err != nil {
			return nil, fmt.Errorf("орієнтація №%d: %w", o.Idx, err)
		}
		series[i] = hours
	}
	return series, nil
}

// Forecast recomputes org's plan for today and tomorrow from the live
// forecast. Every run rewrites both days' Latest plan; DayAhead is
// written for tomorrow only, which freezes it once the day starts. A
// day with no day-ahead plan at all (first run, or the service was
// down the evening before) takes its first run of the day instead.
func (p *Planner) Forecast(ctx context.Context, org string) error {
	if !p.claim(org) {
		return nil
	}
	defer p.release(org)
	for {
		err := p.forecastOnce(ctx, org)
		if ctx.Err() != nil || !p.takeRerun(org) {
			return err
		}
	}
}

func (p *Planner) forecastOnce(ctx context.Context, org string) error {
	st, ok, err := p.store.Station(ctx, org)
	if err != nil || !ok || !st.Modelled() {
		return err
	}
	series, err := p.fetchSeries(ctx, st, func(pl Plane) ([]HourInput, error) {
		return p.weather.Forecast(ctx, pl)
	})
	if err != nil {
		return err
	}
	now := p.now().In(p.loc)
	today := now.Format("2006-01-02")
	tomorrow := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, p.loc).Format("2006-01-02")

	var latest, dayAhead []PlanDay
	for _, d := range GroupDays(PlanHours(st, series), p.loc) {
		switch d.Day {
		case tomorrow:
			latest = append(latest, d)
			dayAhead = append(dayAhead, d)
		case today:
			latest = append(latest, d)
			has, err := p.store.HasDay(ctx, org, today, DayAhead)
			if err != nil {
				return err
			}
			if !has {
				dayAhead = append(dayAhead, d)
			}
		}
	}
	if err := p.store.SaveDays(ctx, org, Latest, ModelVersion, latest); err != nil {
		return err
	}
	if err := p.store.SaveDays(ctx, org, DayAhead, ModelVersion, dayAhead); err != nil {
		return err
	}
	p.log.Info("pv_plan_forecast_ok", "organization_id", org, "latest_days", len(latest), "day_ahead_days", len(dayAhead))
	return nil
}

// claim keeps one forecast run per station at a time. A request that
// finds one running asks it to go again once done, so a passport saved
// mid-run is not computed with the old planes and then forgotten.
func (p *Planner) claim(org string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.inflight[org] {
		p.rerun[org] = true
		return false
	}
	p.inflight[org] = true
	return true
}

func (p *Planner) takeRerun(org string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	again := p.rerun[org]
	delete(p.rerun, org)
	return again
}

func (p *Planner) release(org string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.inflight, org)
}

// ForecastAll runs Forecast for every served station, one after another
// to stay a polite Open-Meteo client.
func (p *Planner) ForecastAll(ctx context.Context) {
	for i, org := range p.organizations() {
		if i > 0 {
			if err := sleep(ctx, p.pause); err != nil {
				return
			}
		}
		if err := p.Forecast(ctx, org); err != nil {
			p.log.Warn("pv_plan_forecast_failed", "organization_id", org, "err", err)
		}
	}
}

// Run seeds the passports, refreshes the forecast, rebuilds (in the
// background) the history of any station that has never had it, and
// then refreshes the forecast at every run hour until ctx ends.
func (p *Planner) Run(ctx context.Context) {
	p.mu.Lock()
	p.base = ctx
	p.mu.Unlock()

	if err := p.Seed(ctx); err != nil {
		p.log.Error("pv_plan_seed_failed", "err", err)
	}
	p.ForecastAll(ctx)
	go p.backfillMissing(ctx)
	for {
		next := NextRun(p.now(), p.loc)
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Until(next)):
		}
		p.ForecastAll(ctx)
	}
}

// NextRun is the first run hour strictly after now.
func NextRun(now time.Time, loc *time.Location) time.Time {
	local := now.In(loc)
	for dayOffset := 0; dayOffset < 2; dayOffset++ {
		for _, h := range runHours {
			t := time.Date(local.Year(), local.Month(), local.Day()+dayOffset, h, 0, 0, 0, loc)
			if t.After(now) {
				return t
			}
		}
	}
	return now.Add(12 * time.Hour)
}

// HistoryStatus reports a history rebuild of one station.
type HistoryStatus struct {
	Running    bool       `json:"running"`
	From       string     `json:"from,omitempty"`
	To         string     `json:"to,omitempty"`
	Done       int        `json:"done"`
	Total      int        `json:"total"`
	Days       int        `json:"days"`
	Error      string     `json:"error,omitempty"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

var errHistoryRunning = errors.New("history rebuild already running")

// HistoryStatusOf returns the latest rebuild of org (zero when none
// ran since the process started).
func (p *Planner) HistoryStatusOf(org string) HistoryStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	if s, ok := p.history[org]; ok {
		return *s
	}
	return HistoryStatus{}
}

// StartHistory rebuilds org's day-ahead plan for the civil days
// [from, to] (empty: from its first telemetry day to yesterday) in the
// background and returns at once.
func (p *Planner) StartHistory(org, from, to string) (HistoryStatus, error) {
	status, err := p.beginHistory(org)
	if err != nil {
		return p.HistoryStatusOf(org), err
	}
	go func() {
		err := p.runHistory(p.baseContext(), org, from, to, status)
		p.finishHistory(org, status, err)
	}()
	return p.HistoryStatusOf(org), nil
}

// ErrHistoryRunning reports whether err says a rebuild is in progress.
func ErrHistoryRunning(err error) bool {
	return errors.Is(err, errHistoryRunning)
}

// backfillMissing rebuilds the history of every served station that has
// never had one, one station at a time.
func (p *Planner) backfillMissing(ctx context.Context) {
	for _, org := range p.organizations() {
		st, ok, err := p.store.Station(ctx, org)
		if err != nil || !ok || !st.Modelled() || st.HistoryBackfilledAt != nil {
			continue
		}
		status, err := p.beginHistory(org)
		if err != nil {
			continue
		}
		err = p.runHistory(ctx, org, "", "", status)
		p.finishHistory(org, status, err)
		if ctx.Err() != nil {
			return
		}
	}
}

func (p *Planner) beginHistory(org string) (*HistoryStatus, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if s, ok := p.history[org]; ok && s.Running {
		return nil, errHistoryRunning
	}
	started := p.now()
	status := &HistoryStatus{Running: true, StartedAt: &started}
	p.history[org] = status
	return status, nil
}

func (p *Planner) finishHistory(org string, status *HistoryStatus, err error) {
	finished := p.now()
	p.mu.Lock()
	status.Running = false
	status.FinishedAt = &finished
	if err != nil {
		status.Error = err.Error()
	}
	p.mu.Unlock()
	if err != nil {
		p.log.Warn("pv_plan_history_failed", "organization_id", org, "err", err)
	}
}

func (p *Planner) runHistory(ctx context.Context, org, from, to string, status *HistoryStatus) error {
	st, ok, err := p.store.Station(ctx, org)
	if err != nil {
		return err
	}
	if !ok || !st.Modelled() {
		return fmt.Errorf("станція %s не має орієнтацій для моделі", org)
	}
	now := p.now().In(p.loc)
	if to == "" {
		to = time.Date(now.Year(), now.Month(), now.Day()-1, 0, 0, 0, 0, p.loc).Format("2006-01-02")
	}
	if from == "" {
		start, ok, err := p.store.DataStart(ctx, org)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		from = start.In(p.loc).Format("2006-01-02")
	}
	if from < historyEarliest {
		from = historyEarliest
	}
	if from > to {
		return nil
	}
	chunks, err := dayChunks(from, to, historyChunkDays)
	if err != nil {
		return err
	}
	planes := len(st.EnabledOrientations())
	p.mu.Lock()
	status.From, status.To, status.Total = from, to, planes*len(chunks)
	p.mu.Unlock()

	series := make([][]HourInput, planes)
	first := true
	where, ok := p.coordinates(st)
	if !ok {
		return errNoCoordinates(org)
	}
	for i, o := range st.EnabledOrientations() {
		plane := Plane{Latitude: where.Latitude, Longitude: where.Longitude, TiltDeg: o.TiltDeg, AzimuthDeg: o.AzimuthDeg}
		for _, c := range chunks {
			if !first {
				if err := sleep(ctx, p.pause); err != nil {
					return err
				}
			}
			first = false
			// The UTC day before the chunk holds the first local
			// hours of its first day.
			hours, err := p.weather.PreviousDay1(ctx, plane, c[0].AddDate(0, 0, -1).Format("2006-01-02"), c[1].Format("2006-01-02"))
			if err != nil {
				return fmt.Errorf("орієнтація №%d, %s — %s: %w", o.Idx, c[0].Format("2006-01-02"), c[1].Format("2006-01-02"), err)
			}
			series[i] = append(series[i], hours...)
			p.mu.Lock()
			status.Done++
			p.mu.Unlock()
		}
	}
	var days []PlanDay
	for _, d := range GroupDays(PlanHours(st, series), p.loc) {
		if d.Day >= from && d.Day <= to {
			days = append(days, d)
		}
	}
	if err := p.store.SaveDays(ctx, org, DayAhead, HistoryModelVersion, days); err != nil {
		return err
	}
	if err := p.store.MarkHistoryBackfilled(ctx, org, p.now()); err != nil {
		return err
	}
	p.mu.Lock()
	status.Days = len(days)
	p.mu.Unlock()
	p.log.Info("pv_plan_history_ok", "organization_id", org, "from", from, "to", to, "days", len(days))
	return nil
}

// dayChunks splits the inclusive date span into windows of at most n
// days, as [first, last] pairs of UTC midnights.
func dayChunks(from, to string, n int) ([][2]time.Time, error) {
	start, err := time.Parse("2006-01-02", from)
	if err != nil {
		return nil, err
	}
	end, err := time.Parse("2006-01-02", to)
	if err != nil {
		return nil, err
	}
	var out [][2]time.Time
	for s := start; !s.After(end); s = s.AddDate(0, 0, n) {
		e := s.AddDate(0, 0, n-1)
		if e.After(end) {
			e = end
		}
		out = append(out, [2]time.Time{s, e})
	}
	return out, nil
}

func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}
