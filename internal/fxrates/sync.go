package fxrates

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// FirstDay is the earliest civil day any economics reach (ze's chart
// floor); the sync keeps rates from here on.
const FirstDay = "2024-08-01"

// RateHour is the local hour a day's rate is taken at. A stored rate is
// final once it was fetched at or after that hour of its day: until then
// today carries the current rate.
const RateHour = 12

// Rate is one stored rate. Day is a civil date; only its date part counts.
type Rate struct {
	Day        time.Time
	Currency   string
	UahPerUnit float64
}

// Store is the Postgres side of the rates.
type Store interface {
	Settings(ctx context.Context) (Settings, bool, bool, error)
	Password(ctx context.Context) (string, error)
	// FetchTimes returns, for each civil date in [from, to] that has a rate
	// for every one of Currencies, the earliest of their fetch times.
	FetchTimes(ctx context.Context, from, to time.Time) (map[string]time.Time, error)
	SaveRates(ctx context.Context, rates []Rate) error
}

// Status is what the settings page shows about the sync.
type Status struct {
	Configured  bool      `json:"configured"`
	LastAttempt time.Time `json:"last_attempt"`
	LastSuccess time.Time `json:"last_success"`
	LastError   string    `json:"last_error,omitempty"`
}

// Syncer keeps fx_rates filled: every missing past day once, today on
// every pass. It reads the settings on each pass, so a save on the
// settings page takes effect without a restart.
type Syncer struct {
	store      Store
	loc        *time.Location
	log        *slog.Logger
	httpClient *http.Client
	from       time.Time
	pause      time.Duration
	every      time.Duration
	now        func() time.Time
	wake       chan struct{}

	mu         sync.Mutex
	status     Status
	idleLogged bool
}

// NewSyncer builds a sync over store; days are civil days in loc.
func NewSyncer(store Store, loc *time.Location, log *slog.Logger) *Syncer {
	if log == nil {
		log = slog.Default()
	}
	from, _ := time.ParseInLocation("2006-01-02", FirstDay, loc)
	return &Syncer{
		store: store,
		loc:   loc,
		log:   log,
		from:  from,
		pause: 50 * time.Millisecond,
		every: time.Hour,
		now:   time.Now,
		wake:  make(chan struct{}, 1),
	}
}

// Wake asks the loop for a pass now instead of at the next tick.
func (s *Syncer) Wake() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Status reports the last pass.
func (s *Syncer) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

// Run syncs at start, then every hour or when woken, until ctx ends.
func (s *Syncer) Run(ctx context.Context) {
	for {
		if err := s.SyncOnce(ctx); err != nil && ctx.Err() == nil {
			s.log.Warn("fx_sync_failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-s.wake:
		case <-time.After(s.every):
		}
	}
}

// SyncOnce runs one pass. It returns nil when the settings are not filled
// in yet: there is nothing to do until a save wakes the loop.
func (s *Syncer) SyncOnce(ctx context.Context) error {
	settings, passwordSet, _, err := s.store.Settings(ctx)
	if err != nil {
		return err
	}
	configured := settings.Configured(passwordSet)
	s.mu.Lock()
	announce := !configured && !s.idleLogged
	s.idleLogged = !configured
	s.status.Configured = configured
	s.mu.Unlock()
	if !configured {
		if announce {
			s.log.Info("fx_sync_not_configured", "hint", "fill in the «Курси валют ERP» page")
		}
		return nil
	}
	password, err := s.store.Password(ctx)
	if err != nil {
		return err
	}
	client := NewClient(settings, password, s.httpClient)

	now := s.now().In(s.loc)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, s.loc)
	fetched, err := s.store.FetchTimes(ctx, s.from, today)
	if err != nil {
		return err
	}

	attempt := s.now()
	filled := 0
	for day := s.from; day.Before(today); day = day.AddDate(0, 0, 1) {
		noon := time.Date(day.Year(), day.Month(), day.Day(), RateHour, 0, 0, 0, s.loc)
		if at, ok := fetched[day.Format("2006-01-02")]; ok && !at.Before(noon) {
			continue
		}
		if err := s.fetchDay(ctx, client, day, noon); err != nil {
			s.mark(attempt, err)
			return err
		}
		filled++
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(s.pause):
		}
	}
	at := time.Date(today.Year(), today.Month(), today.Day(), RateHour, 0, 0, 0, s.loc)
	if now.Before(at) {
		at = now
	}
	if err := s.fetchDay(ctx, client, today, at); err != nil {
		s.mark(attempt, err)
		return err
	}
	if filled > 0 {
		s.log.Info("fx_sync_backfilled", "days", filled)
	}
	s.mark(attempt, nil)
	return nil
}

func (s *Syncer) fetchDay(ctx context.Context, client *Client, day, at time.Time) error {
	rates, err := client.Rates(ctx, at, Currencies)
	if err != nil {
		return err
	}
	rows := make([]Rate, 0, len(rates))
	for _, cur := range Currencies {
		rows = append(rows, Rate{Day: day, Currency: cur, UahPerUnit: rates[cur]})
	}
	return s.store.SaveRates(ctx, rows)
}

func (s *Syncer) mark(attempt time.Time, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.LastAttempt = attempt
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			s.status.LastError = err.Error()
		}
		return
	}
	s.status.LastSuccess = attempt
	s.status.LastError = ""
}
