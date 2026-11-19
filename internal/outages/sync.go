package outages

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// SyncEvery is how often the API process re-reads every point's schedule.
const SyncEvery = 15 * time.Minute

// Store is the Postgres side of accounts, points and intervals.
type Store interface {
	Accounts(ctx context.Context) ([]AccountRow, error)
	Password(ctx context.Context, host string) (string, error)
	Points(ctx context.Context) ([]Point, error)
	ReplaceIntervals(ctx context.Context, organizationID string, list []Interval, read Read) error
	MarkRead(ctx context.Context, organizationID string, read Read) error
}

// AccountRow is one cabinet as stored: login plus whether a password is set.
// The password itself is only Password(host).
type AccountRow struct {
	Account
	PasswordConfigured bool `json:"password_configured"`
}

// Configured reports whether the cabinet can be signed in to.
func (a AccountRow) Configured() bool {
	_, ok := OperatorFor(a.Host)
	return a.Login != "" && a.PasswordConfigured && ok
}

// Status is what the settings page shows about the sync.
type Status struct {
	Configured  bool      `json:"configured"`
	LastAttempt time.Time `json:"last_attempt"`
	LastSuccess time.Time `json:"last_success"`
	LastError   string    `json:"last_error,omitempty"`
}

// Syncer keeps outage_intervals filled for every saved EIC: today and
// tomorrow in Kyiv. It rereads accounts and points each pass, so a save
// on the settings or passport page takes effect without a restart.
type Syncer struct {
	store      Store
	loc        *time.Location
	log        *slog.Logger
	httpClient *http.Client
	every      time.Duration
	now        func() time.Time
	wake       chan struct{}
	newClient  func(base, login, password string, loc *time.Location, httpClient *http.Client) *Client

	mu         sync.Mutex
	status     Status
	idleLogged bool
}

// NewSyncer builds a sync over store; wall-clock days are in loc.
func NewSyncer(store Store, loc *time.Location, log *slog.Logger) *Syncer {
	if log == nil {
		log = slog.Default()
	}
	if loc == nil {
		loc = time.UTC
	}
	return &Syncer{
		store:     store,
		loc:       loc,
		log:       log,
		every:     SyncEvery,
		now:       time.Now,
		wake:      make(chan struct{}, 1),
		newClient: NewClient,
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

// Run syncs at start, then every SyncEvery or when woken, until ctx ends.
func (s *Syncer) Run(ctx context.Context) {
	for {
		if err := s.SyncOnce(ctx); err != nil && ctx.Err() == nil {
			s.log.Warn("outages_sync_failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-s.wake:
		case <-time.After(s.every):
		}
	}
}

// SyncOnce runs one pass. Idle (nil error) when no cabinet is configured
// or no station has an EIC yet.
func (s *Syncer) SyncOnce(ctx context.Context) error {
	accounts, err := s.store.Accounts(ctx)
	if err != nil {
		return err
	}
	points, err := s.store.Points(ctx)
	if err != nil {
		return err
	}
	byHost := map[string][]Point{}
	for _, p := range points {
		if p.EIC == "" || p.Host == "" {
			continue
		}
		byHost[p.Host] = append(byHost[p.Host], p)
	}
	configured := false
	for _, a := range accounts {
		if a.Configured() && len(byHost[a.Host]) > 0 {
			configured = true
			break
		}
	}
	s.mu.Lock()
	announce := !configured && !s.idleLogged
	s.idleLogged = !configured
	s.status.Configured = configured
	s.mu.Unlock()
	if !configured {
		if announce {
			s.log.Info("outages_sync_not_configured", "hint", "fill in e-svitlo cabinets and station EIC codes")
		}
		return nil
	}

	attempt := s.now()
	var firstErr error
	for _, a := range accounts {
		if !a.Configured() {
			continue
		}
		list := byHost[a.Host]
		if len(list) == 0 {
			continue
		}
		if err := s.syncAccount(ctx, a, list, attempt); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			s.log.Warn("outages_sync_account", "host", a.Host, "err", err)
		}
	}
	s.mark(attempt, firstErr)
	return firstErr
}

func (s *Syncer) syncAccount(ctx context.Context, a AccountRow, points []Point, attempt time.Time) error {
	password, err := s.store.Password(ctx, a.Host)
	if err != nil {
		return err
	}
	client := s.newClient(BaseURL(a.Host), a.Login, password, s.loc, s.httpClient)
	if err := client.SignIn(ctx); err != nil {
		for _, p := range points {
			_ = s.store.MarkRead(ctx, p.OrganizationID, Read{AttemptedAt: attempt, Error: err.Error()})
		}
		return err
	}
	var firstErr error
	for _, p := range points {
		if err := s.syncPoint(ctx, client, p, attempt); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (s *Syncer) syncPoint(ctx context.Context, client *Client, p Point, attempt time.Time) error {
	list, err := client.Outages(ctx, p.EIC)
	if err != nil {
		_ = s.store.MarkRead(ctx, p.OrganizationID, Read{AttemptedAt: attempt, Error: err.Error()})
		return err
	}
	read := Read{AttemptedAt: attempt, SucceededAt: attempt}
	if err := s.store.ReplaceIntervals(ctx, p.OrganizationID, list, read); err != nil {
		_ = s.store.MarkRead(ctx, p.OrganizationID, Read{AttemptedAt: attempt, Error: err.Error()})
		return err
	}
	return nil
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
