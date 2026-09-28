package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

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

var (
	errBadCredentials  = errors.New("невірний email або пароль")
	errAccountDisabled = errors.New("обліковий запис вимкнено")
	errNoAccounts      = errors.New("no accounts yet: set AUTH_BOOTSTRAP_EMAIL and AUTH_BOOTSTRAP_PASSWORD to create the first administrator")
)

// throttledError reports a login refused because of recent failures.
type throttledError struct{ retryAfter time.Duration }

func (e *throttledError) Error() string {
	return "забагато невдалих спроб входу, спробуйте пізніше"
}

// userUpdate lists the account fields to change; nil leaves a field
// as it is.
type userUpdate struct {
	Name         *string
	Disabled     *bool
	PasswordHash *string
	Grants       *[]auth.Grant
	// RevokeSessions signs the user out everywhere except the session
	// whose token hash is KeepSession.
	RevokeSessions bool
	KeepSession    []byte
}

// authStore is the persistence the auth layer needs. *AuthStore
// implements it on top of internal/storage.
type authStore interface {
	CountUsers(ctx context.Context) (int, error)
	CreateUser(ctx context.Context, u storage.UserRow, grants []auth.Grant) (int64, error)
	UserByEmail(ctx context.Context, email string) (storage.UserRow, bool, error)
	UserByID(ctx context.Context, id int64) (storage.UserRow, bool, error)
	ListUsers(ctx context.Context) ([]storage.UserRow, error)
	UserGrants(ctx context.Context, userID int64) ([]auth.Grant, error)
	AllGrants(ctx context.Context) (map[int64][]auth.Grant, error)
	UpdateUser(ctx context.Context, id int64, upd userUpdate) (bool, error)
	CreateSession(ctx context.Context, tokenHash []byte, userID int64, expiresAt time.Time) error
	Session(ctx context.Context, tokenHash []byte) (storage.SessionRow, bool, error)
	TouchSession(ctx context.Context, tokenHash []byte, lastSeen, expiresAt time.Time) error
	DeleteSession(ctx context.Context, tokenHash []byte) error
	DeleteExpiredSessions(ctx context.Context, now time.Time) error
}

// AuthStore adapts the storage package's account tables to the auth
// layer.
type AuthStore struct {
	pool *pgxpool.Pool
}

func NewAuthStore(pool *pgxpool.Pool) *AuthStore {
	return &AuthStore{pool: pool}
}

func (s *AuthStore) CountUsers(ctx context.Context) (int, error) {
	return storage.CountUsers(ctx, s.pool)
}

func (s *AuthStore) CreateUser(ctx context.Context, u storage.UserRow, grants []auth.Grant) (int64, error) {
	return storage.InsertUser(ctx, s.pool, u, grantRows(grants))
}

func (s *AuthStore) UserByEmail(ctx context.Context, email string) (storage.UserRow, bool, error) {
	return storage.GetUserByEmail(ctx, s.pool, email)
}

func (s *AuthStore) UserByID(ctx context.Context, id int64) (storage.UserRow, bool, error) {
	return storage.GetUser(ctx, s.pool, id)
}

func (s *AuthStore) ListUsers(ctx context.Context) ([]storage.UserRow, error) {
	return storage.ListUsers(ctx, s.pool)
}

func (s *AuthStore) UserGrants(ctx context.Context, userID int64) ([]auth.Grant, error) {
	rows, err := storage.ListRoleGrants(ctx, s.pool, userID)
	if err != nil {
		return nil, err
	}
	out := make([]auth.Grant, 0, len(rows))
	for _, r := range rows {
		out = append(out, auth.Grant{Role: auth.Role(r.Role), OrganizationID: r.OrganizationID})
	}
	return out, nil
}

func (s *AuthStore) AllGrants(ctx context.Context) (map[int64][]auth.Grant, error) {
	rows, err := storage.ListAllRoleGrants(ctx, s.pool)
	if err != nil {
		return nil, err
	}
	out := make(map[int64][]auth.Grant)
	for _, r := range rows {
		out[r.UserID] = append(out[r.UserID], auth.Grant{Role: auth.Role(r.Role), OrganizationID: r.OrganizationID})
	}
	return out, nil
}

func (s *AuthStore) UpdateUser(ctx context.Context, id int64, upd userUpdate) (bool, error) {
	su := storage.UserUpdate{
		Name:           upd.Name,
		Disabled:       upd.Disabled,
		PasswordHash:   upd.PasswordHash,
		RevokeSessions: upd.RevokeSessions,
		KeepSession:    upd.KeepSession,
	}
	if upd.Grants != nil {
		rows := grantRows(*upd.Grants)
		su.Grants = &rows
	}
	return storage.UpdateUser(ctx, s.pool, id, su)
}

func (s *AuthStore) CreateSession(ctx context.Context, tokenHash []byte, userID int64, expiresAt time.Time) error {
	return storage.InsertSession(ctx, s.pool, tokenHash, userID, expiresAt)
}

func (s *AuthStore) Session(ctx context.Context, tokenHash []byte) (storage.SessionRow, bool, error) {
	return storage.GetSession(ctx, s.pool, tokenHash)
}

func (s *AuthStore) TouchSession(ctx context.Context, tokenHash []byte, lastSeen, expiresAt time.Time) error {
	return storage.TouchSession(ctx, s.pool, tokenHash, lastSeen, expiresAt)
}

func (s *AuthStore) DeleteSession(ctx context.Context, tokenHash []byte) error {
	return storage.DeleteSession(ctx, s.pool, tokenHash)
}

func (s *AuthStore) DeleteExpiredSessions(ctx context.Context, now time.Time) error {
	return storage.DeleteExpiredSessions(ctx, s.pool, now)
}

func grantRows(grants []auth.Grant) []storage.RoleGrantRow {
	out := make([]storage.RoleGrantRow, 0, len(grants))
	for _, g := range grants {
		out = append(out, storage.RoleGrantRow{Role: string(g.Role), OrganizationID: g.OrganizationID})
	}
	return out
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

type cachedSession struct {
	principal *auth.Principal
	expiresAt time.Time
	lastSeen  time.Time
	loadedAt  time.Time
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
// account exists yet, and reports whether it did. Once any account
// exists it changes nothing, so the variables can stay set.
func (a *Auth) Bootstrap(ctx context.Context, email, password string) (bool, error) {
	n, err := a.store.CountUsers(ctx)
	if err != nil {
		return false, err
	}
	if n > 0 {
		return false, nil
	}
	email = normalizeEmail(email)
	if email == "" || password == "" {
		return false, errNoAccounts
	}
	if err := validateEmail(email); err != nil {
		return false, fmt.Errorf("AUTH_BOOTSTRAP_EMAIL: %w", err)
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return false, fmt.Errorf("AUTH_BOOTSTRAP_PASSWORD: %w", err)
	}
	user := storage.UserRow{Email: email, Name: "Адміністратор", PasswordHash: hash}
	if _, err := a.store.CreateUser(ctx, user, []auth.Grant{{Role: auth.RoleAdmin}}); err != nil {
		return false, err
	}
	return true, nil
}

// login checks the credentials and opens a session.
func (a *Auth) login(ctx context.Context, email, password string) (*auth.Principal, string, time.Time, error) {
	email = normalizeEmail(email)
	now := a.now()
	if blocked, wait := a.throttle.Blocked(email, now); blocked {
		return nil, "", time.Time{}, &throttledError{retryAfter: wait}
	}
	user, found, err := a.store.UserByEmail(ctx, email)
	if err != nil {
		return nil, "", time.Time{}, err
	}
	hash := ""
	if found {
		hash = user.PasswordHash
	}
	match, err := a.checkPassword(ctx, hash, password)
	if err != nil {
		return nil, "", time.Time{}, err
	}
	if !match {
		a.throttle.Fail(email, now)
		return nil, "", time.Time{}, errBadCredentials
	}
	if user.Disabled {
		return nil, "", time.Time{}, errAccountDisabled
	}
	a.throttle.Reset(email)
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
	p := &auth.Principal{UserID: user.ID, Email: user.Email, Name: user.Name, Grants: grants}
	a.mu.Lock()
	a.storeLocked(string(digest), cachedSession{principal: p, expiresAt: expires, lastSeen: now, loadedAt: now}, now)
	a.mu.Unlock()
	return p, token, expires, nil
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

// authenticate resolves the request's session cookie; a nil principal
// means not signed in. A session in use gets its expiry pushed forward
// (and the cookie re-issued) at most every sessionTouchEvery.
func (a *Auth) authenticate(w http.ResponseWriter, r *http.Request) (*auth.Principal, error) {
	token := sessionToken(r)
	if token == "" {
		return nil, nil
	}
	digest := auth.TokenDigest(token)
	key := string(digest)
	now := a.now()

	a.mu.Lock()
	s, cached := a.sessions[key]
	rev := a.revision
	a.mu.Unlock()

	if !cached || now.Sub(s.loadedAt) >= sessionCacheTTL {
		loaded, ok, err := a.loadSession(r.Context(), digest, now)
		if err != nil {
			return nil, err
		}
		if !ok {
			a.forget(key)
			return nil, nil
		}
		s = loaded
	}
	if !now.Before(s.expiresAt) {
		a.forget(key)
		return nil, nil
	}
	if now.Sub(s.lastSeen) >= sessionTouchEvery {
		expires := now.Add(sessionTTL)
		if err := a.store.TouchSession(r.Context(), digest, now, expires); err != nil {
			a.log.Warn("auth_session_touch", "err", err)
		} else {
			s.lastSeen, s.expiresAt = now, expires
			a.setSessionCookie(w, token, expires)
		}
	}

	a.mu.Lock()
	if a.revision == rev {
		a.storeLocked(key, s, now)
	}
	a.mu.Unlock()
	return s.principal, nil
}

func (a *Auth) loadSession(ctx context.Context, digest []byte, now time.Time) (cachedSession, bool, error) {
	sess, ok, err := a.store.Session(ctx, digest)
	if err != nil || !ok {
		return cachedSession{}, false, err
	}
	if !now.Before(sess.ExpiresAt) {
		return cachedSession{}, false, nil
	}
	user, ok, err := a.store.UserByID(ctx, sess.UserID)
	if err != nil || !ok || user.Disabled {
		return cachedSession{}, false, err
	}
	grants, err := a.store.UserGrants(ctx, user.ID)
	if err != nil {
		return cachedSession{}, false, err
	}
	return cachedSession{
		principal: &auth.Principal{UserID: user.ID, Email: user.Email, Name: user.Name, Grants: grants},
		expiresAt: sess.ExpiresAt,
		lastSeen:  sess.LastSeenAt,
		loadedAt:  now,
	}, true, nil
}

// storeLocked caches s under key; a.mu must be held.
func (a *Auth) storeLocked(key string, s cachedSession, now time.Time) {
	if len(a.sessions) >= sessionCachePrune {
		for k, c := range a.sessions {
			if now.Sub(c.loadedAt) >= sessionCacheTTL {
				delete(a.sessions, k)
			}
		}
	}
	a.sessions[key] = s
}

func (a *Auth) forget(key string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.revision++
	delete(a.sessions, key)
}

// forgetUser drops every cached session of a user whose account or
// grants just changed; the next request reloads from the database.
func (a *Auth) forgetUser(userID int64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.revision++
	for k, s := range a.sessions {
		if s.principal.UserID == userID {
			delete(a.sessions, k)
		}
	}
}

func (a *Auth) setSessionCookie(w http.ResponseWriter, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   int(expires.Sub(a.now()) / time.Second),
		HttpOnly: true,
		Secure:   a.cookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
}

func (a *Auth) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   a.cookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
}

func sessionToken(r *http.Request) string {
	c, err := r.Cookie(sessionCookieName)
	if err != nil {
		return ""
	}
	return c.Value
}

type principalKey struct{}

func withPrincipal(ctx context.Context, p *auth.Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

func principalFrom(ctx context.Context) *auth.Principal {
	p, _ := ctx.Value(principalKey{}).(*auth.Principal)
	return p
}

// canOnOrg reports whether p holds any of perms on org. Edge shadow
// telemetry ("<site>" + EDGE_ORG_SUFFIX) is covered by the site's grant.
func (h *Handlers) canOnOrg(p *auth.Principal, org string, perms ...auth.Permission) bool {
	site, shadow := h.edgeShadowSite(org)
	for _, perm := range perms {
		if p.Can(perm, org) || (shadow && p.Can(perm, site)) {
			return true
		}
	}
	return false
}

// edgeShadowSite maps a shadow organization back to its edge site. Only
// sites with an edge token have shadow telemetry, so "<org>-edge" of
// any other organization maps to nothing.
func (h *Handlers) edgeShadowSite(org string) (string, bool) {
	if h.edge == nil || h.edge.OrgSuffix == "" {
		return "", false
	}
	site, ok := strings.CutSuffix(org, h.edge.OrgSuffix)
	if !ok || site == "" {
		return "", false
	}
	_, isEdge := h.edge.Tokens[site]
	return site, isEdge
}

// visibleTo reports whether a list entry for org may be shown to the
// request's principal. Without SetAuth everything is visible.
func (h *Handlers) visibleTo(r *http.Request, org string, perms ...auth.Permission) bool {
	if h.auth == nil {
		return true
	}
	return h.canOnOrg(principalFrom(r.Context()), org, perms...)
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
