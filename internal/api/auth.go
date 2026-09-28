package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/nesh/sestelemetry/internal/auth"
	"github.com/nesh/sestelemetry/internal/storage"
)

const (
	sessionCookieName = "ses_session"
	sessionTTL        = 14 * 24 * time.Hour
	// The dashboard polls /api/v1/current every second, so a resolved
	// session is served from memory this long before the database is
	// asked again. It is also the longest a revoked session can survive
	// on a second API process.
	sessionCacheTTL = 30 * time.Second
	// sessionTouchEvery bounds how often the sliding expiry is written
	// back while a session is in use.
	sessionTouchEvery = 10 * time.Minute
	sessionCachePrune = 1024

	loginMaxFailures = 5
	loginLockout     = 15 * time.Minute
	// passwordCheckSlots bounds concurrent bcrypt comparisons. Each costs
	// ~0.25 s of CPU and the process also carries the edge uplink, so a
	// flood of login attempts queues instead of starving it.
	passwordCheckSlots = 2

	// csrfHeader must accompany every state-changing request: a
	// cross-site form can't set it, and a cross-origin fetch that does
	// has to pass a CORS preflight first. Comparing Origin with Host
	// would not work — the Vite proxy rewrites Host (changeOrigin).
	csrfHeader = "X-Requested-With"

	// maxDayWindow caps telemetry reads for roles without
	// analytics.full: a civil day across a DST change lasts 25 h, and
	// the planner's «Заповнити з учора (факт)» asks for 48 h.
	maxDayWindow = 49 * time.Hour
)

// The factory account created on a database without accounts when
// AUTH_BOOTSTRAP_* are not set. It can do nothing but change its
// password until it does.
const (
	defaultAdminLogin    = "admin"
	defaultAdminPassword = "admin"
)

var (
	errBadCredentials  = errors.New("невірний логін або пароль")
	errAccountDisabled = errors.New("обліковий запис вимкнено")
)

// BootstrapResult says which first account Bootstrap created.
type BootstrapResult int

const (
	// BootstrapNone: accounts already exist, nothing was created.
	BootstrapNone BootstrapResult = iota
	// BootstrapFromEnv: the administrator named by AUTH_BOOTSTRAP_*.
	BootstrapFromEnv
	// BootstrapDefaultAdmin: admin/admin, password change pending.
	BootstrapDefaultAdmin
)

// throttledError reports a login refused because of recent failures.
type throttledError struct{ retryAfter time.Duration }

func (e *throttledError) Error() string {
	return "забагато невдалих спроб входу, спробуйте пізніше"
}

// AuthOptions configures NewAuth.
type AuthOptions struct {
	// CookieSecure marks the session cookie Secure. Enable it only
	// behind HTTPS: browsers drop Secure cookies set over plain HTTP.
	CookieSecure bool
	Log          *slog.Logger
}

// Auth resolves session cookies to principals and owns the login state:
// the failed-attempt throttle and the in-memory session cache.
type Auth struct {
	store        authStore
	log          *slog.Logger
	cookieSecure bool
	throttle     *auth.Throttle
	checkSlots   chan struct{}
	now          func() time.Time

	mu sync.Mutex
	// revision grows on every removal, so a lookup that raced a
	// revocation doesn't put the revoked session back into the cache.
	revision int
	sessions map[string]cachedSession
}

func NewAuth(store authStore, opts AuthOptions) *Auth {
	log := opts.Log
	if log == nil {
		log = slog.Default()
	}
	return &Auth{
		store:        store,
		log:          log,
		cookieSecure: opts.CookieSecure,
		throttle:     auth.NewThrottle(loginMaxFailures, loginLockout),
		checkSlots:   make(chan struct{}, passwordCheckSlots),
		now:          time.Now,
		sessions:     map[string]cachedSession{},
	}
}

// SetAuth requires a session on every route except the health probes,
// login/logout and the Bearer-authenticated edge uplink. cmd/api always
// installs it; a Handlers without it serves every route openly, which
// is what the handler tests rely on.
func (h *Handlers) SetAuth(a *Auth) {
	h.auth = a
}

// Bootstrap creates an administrator of every organization when no
// account exists yet: the one AUTH_BOOTSTRAP_EMAIL/PASSWORD name when
// both are set, otherwise admin/admin with a password change pending.
// Once any account exists it changes nothing, so the variables can stay
// set.
func (a *Auth) Bootstrap(ctx context.Context, email, password string) (BootstrapResult, error) {
	n, err := a.store.CountUsers(ctx)
	if err != nil {
		return BootstrapNone, err
	}
	if n > 0 {
		return BootstrapNone, nil
	}
	user := storage.UserRow{Name: "Адміністратор"}
	result := BootstrapFromEnv
	email = normalizeEmail(email)
	if email == "" || password == "" {
		result = BootstrapDefaultAdmin
		user.Email, user.MustChangePassword = defaultAdminLogin, true
		if user.PasswordHash, err = auth.HashTemporaryPassword(defaultAdminPassword); err != nil {
			return BootstrapNone, err
		}
	} else {
		if err := validateEmail(email); err != nil {
			return BootstrapNone, fmt.Errorf("AUTH_BOOTSTRAP_EMAIL: %w", err)
		}
		user.Email = email
		if user.PasswordHash, err = auth.HashPassword(password); err != nil {
			return BootstrapNone, fmt.Errorf("AUTH_BOOTSTRAP_PASSWORD: %w", err)
		}
	}
	if _, err := a.store.CreateUser(ctx, user, []auth.Grant{{Role: auth.RoleAdmin}}); err != nil {
		return BootstrapNone, err
	}
	return result, nil
}

// login checks the credentials and opens a session.
func (a *Auth) login(ctx context.Context, email, password string) (*auth.Principal, string, time.Time, error) {
	email = normalizeEmail(email)
	user, found, err := a.store.UserByEmail(ctx, email)
	if err != nil {
		return nil, "", time.Time{}, err
	}
	hash := ""
	if found {
		hash = user.PasswordHash
	}
	if err := a.verifyPassword(ctx, email, hash, password); err != nil {
		return nil, "", time.Time{}, err
	}
	if user.Disabled {
		return nil, "", time.Time{}, errAccountDisabled
	}
	now := a.now()
	grants, err := a.store.UserGrants(ctx, user.ID)
	if err != nil {
		return nil, "", time.Time{}, err
	}
	token, digest := auth.NewSessionToken()
	expires := now.Add(sessionTTL)
	if err := a.store.CreateSession(ctx, digest, user.ID, expires); err != nil {
		return nil, "", time.Time{}, err
	}
	if err := a.store.DeleteExpiredSessions(ctx, now); err != nil {
		a.log.Warn("auth_expired_sessions_cleanup", "err", err)
	}
	p := principalOf(user, grants)
	a.mu.Lock()
	a.storeLocked(string(digest), cachedSession{principal: p, expiresAt: expires, lastSeen: now, loadedAt: now}, now)
	a.mu.Unlock()
	return p, token, expires, nil
}

// verifyPassword checks password against hash for the account behind
// key (its normalized email) under the failed-attempt throttle: a
// *throttledError while the account is locked out, errBadCredentials on
// a mismatch (counted), nil on a match (which clears the count).
func (a *Auth) verifyPassword(ctx context.Context, key, hash, password string) error {
	now := a.now()
	if blocked, wait := a.throttle.Blocked(key, now); blocked {
		return &throttledError{retryAfter: wait}
	}
	match, err := a.checkPassword(ctx, hash, password)
	if err != nil {
		return err
	}
	if !match {
		a.throttle.Fail(key, now)
		return errBadCredentials
	}
	a.throttle.Reset(key)
	return nil
}

// checkPassword runs one bcrypt comparison once a slot is free.
func (a *Auth) checkPassword(ctx context.Context, hash, password string) (bool, error) {
	select {
	case a.checkSlots <- struct{}{}:
	case <-ctx.Done():
		return false, ctx.Err()
	}
	defer func() { <-a.checkSlots }()
	return auth.CheckPassword(hash, password), nil
}

// logout ends the session behind token.
func (a *Auth) logout(ctx context.Context, token string) error {
	digest := auth.TokenDigest(token)
	a.forget(string(digest))
	return a.store.DeleteSession(ctx, digest)
}

func principalOf(user storage.UserRow, grants []auth.Grant) *auth.Principal {
	return &auth.Principal{
		UserID:             user.ID,
		Email:              user.Email,
		Name:               user.Name,
		Grants:             grants,
		MustChangePassword: user.MustChangePassword,
	}
}

func normalizeEmail(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

func validateEmail(email string) error {
	at := strings.IndexByte(email, '@')
	if len(email) > 254 || at <= 0 || at != strings.LastIndexByte(email, '@') ||
		at == len(email)-1 || strings.ContainsAny(email, " \t\r\n<>,;\"") {
		return errors.New("некоректний email")
	}
	return nil
}
