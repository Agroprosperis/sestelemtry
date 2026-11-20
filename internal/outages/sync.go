package outages

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// SyncEvery is how often the API process rereads every station's
// schedule. The operators publish today's and tomorrow's hours and change
// them during the day, rarely more than hourly.
const SyncEvery = time.Hour

// Store is the Postgres side of the stations' accounts and outages.
type Store interface {
	Accounts(ctx context.Context) ([]AccountRow, error)
	Password(ctx context.Context, organizationID string) (string, error)
	// ReplaceIntervals stores a successful read: the station's intervals
	// starting at or after from become list, earlier ones stay.
	ReplaceIntervals(ctx context.Context, organizationID string, from time.Time, list []Interval, read Read) error
	MarkRead(ctx context.Context, organizationID string, read Read) error
}

// AccountRow is a station's account as stored: the account plus whether
// a password is set. The password itself is only Password(org).
type AccountRow struct {
	Account
	PasswordConfigured bool `json:"password_configured"`
}

// Configured reports whether the cabinet can be signed in to.
func (a AccountRow) Configured() bool {
	_, ok := OperatorFor(a.Host)
	return a.Login != "" && a.PasswordConfigured && ok
}

// Status is the last pass over all stations, for the logs and the
// passport.
type Status struct {
	Configured  bool      `json:"configured"`
	Stations    int       `json:"stations"`
	LastAttempt time.Time `json:"last_attempt"`
	LastSuccess time.Time `json:"last_success"`
	LastError   string    `json:"last_error,omitempty"`
}

// Syncer reads, every hour, the planned outages of each station whose
// passport holds a cabinet account, and stores them with the outcome of
// the read. It rereads the accounts each pass, so a save on a passport
// takes effect without a restart; Wake runs a pass at once.
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

// NewSyncer builds a sync over store; days are civil days in loc.
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

// cabinet is the stations that sign in with the same credentials: one
// sign-in serves them all.
type cabinet struct {
	host, login, password string
	stations              []AccountRow
}

// SyncOnce reads every configured station once. It returns the first
// error after trying them all, nil when none is configured.
func (s *Syncer) SyncOnce(ctx context.Context) error {
	rows, err := s.store.Accounts(ctx)
	if err != nil {
		return err
	}
	var configured []AccountRow
	for _, a := range rows {
		if a.Configured() {
			configured = append(configured, a)
		}
	}
	s.mu.Lock()
	announce := len(configured) == 0 && !s.idleLogged
	s.idleLogged = len(configured) == 0
	s.status.Configured = len(configured) > 0
	s.status.Stations = len(configured)
	s.mu.Unlock()
	if len(configured) == 0 {
		if announce {
			s.log.Info("outages_sync_not_configured", "hint", "set an e-svitlo cabinet on a station passport")
		}
		return nil
	}

	attempt := s.now()
	var firstErr error
	keep := func(err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	cabinets, err := s.cabinets(ctx, configured)
	if err != nil {
		return err
	}
	for _, c := range cabinets {
		keep(s.syncCabinet(ctx, c, attempt))
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	s.mark(attempt, firstErr)
	return firstErr
}

// cabinets groups the stations by the credentials they sign in with.
func (s *Syncer) cabinets(ctx context.Context, stations []AccountRow) ([]*cabinet, error) {
	byKey := map[string]*cabinet{}
	var out []*cabinet
	for _, a := range stations {
		password, err := s.store.Password(ctx, a.OrganizationID)
		if err != nil {
			return nil, err
		}
		key := a.Host + "\x00" + a.Login + "\x00" + password
		c, ok := byKey[key]
		if !ok {
			c = &cabinet{host: a.Host, login: a.Login, password: password}
			byKey[key] = c
			out = append(out, c)
		}
		c.stations = append(c.stations, a)
	}
	return out, nil
}

func (s *Syncer) syncCabinet(ctx context.Context, c *cabinet, attempt time.Time) error {
	client := s.newClient(BaseURL(c.host), c.login, c.password, s.loc, s.httpClient)
	if err := client.SignIn(ctx); err != nil {
		for _, a := range c.stations {
			s.fail(ctx, a, attempt, err)
		}
		return err
	}
	local := attempt.In(s.loc)
	dayStart := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, s.loc)
	var firstErr error
	for _, a := range c.stations {
		list, err := client.Outages(ctx, a.EIC)
		if err == nil {
			err = s.store.ReplaceIntervals(ctx, a.OrganizationID, dayStart, list, Read{AttemptedAt: attempt, SucceededAt: attempt})
		}
		if err != nil {
			s.fail(ctx, a, attempt, err)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		s.log.Info("outages_read", "organization_id", a.OrganizationID, "host", c.host, "intervals", len(list))
	}
	return firstErr
}

func (s *Syncer) fail(ctx context.Context, a AccountRow, attempt time.Time, err error) {
	s.log.Warn("outages_read_failed", "organization_id", a.OrganizationID, "host", a.Host, "err", err)
	if merr := s.store.MarkRead(ctx, a.OrganizationID, Read{AttemptedAt: attempt, Error: err.Error()}); merr != nil {
		s.log.Warn("outages_mark_read_failed", "organization_id", a.OrganizationID, "err", merr)
	}
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
