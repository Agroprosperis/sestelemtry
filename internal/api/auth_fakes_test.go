package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nesh/sestelemetry/internal/auth"
	"github.com/nesh/sestelemetry/internal/storage"
)

// memAuthStore is an in-memory authStore.
type memAuthStore struct {
	mu       sync.Mutex
	nextID   int64
	users    map[int64]storage.UserRow
	grants   map[int64][]auth.Grant
	sessions map[string]storage.SessionRow
	touches  int
}

func newMemAuthStore() *memAuthStore {
	return &memAuthStore{
		users:    map[int64]storage.UserRow{},
		grants:   map[int64][]auth.Grant{},
		sessions: map[string]storage.SessionRow{},
	}
}

func (m *memAuthStore) CountUsers(context.Context) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.users), nil
}

func (m *memAuthStore) CreateUser(_ context.Context, u storage.UserRow, grants []auth.Grant) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, existing := range m.users {
		if strings.EqualFold(existing.Email, u.Email) {
			return 0, storage.ErrEmailTaken
		}
	}
	m.nextID++
	u.ID = m.nextID
	if u.AuthProvider == "" {
		u.AuthProvider = "local"
	}
	u.CreatedAt = time.Now()
	m.users[u.ID] = u
	m.grants[u.ID] = append([]auth.Grant(nil), grants...)
	return u.ID, nil
}

func (m *memAuthStore) UserByEmail(_ context.Context, email string) (storage.UserRow, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, u := range m.users {
		if strings.EqualFold(u.Email, email) {
			return u, true, nil
		}
	}
	return storage.UserRow{}, false, nil
}

func (m *memAuthStore) UserByID(_ context.Context, id int64) (storage.UserRow, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[id]
	return u, ok, nil
}

func (m *memAuthStore) ListUsers(context.Context) ([]storage.UserRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]storage.UserRow, 0, len(m.users))
	for id := int64(1); id <= m.nextID; id++ {
		if u, ok := m.users[id]; ok {
			out = append(out, u)
		}
	}
	return out, nil
}

func (m *memAuthStore) UserGrants(_ context.Context, userID int64) ([]auth.Grant, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]auth.Grant(nil), m.grants[userID]...), nil
}

func (m *memAuthStore) AllGrants(context.Context) (map[int64][]auth.Grant, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[int64][]auth.Grant, len(m.grants))
	for id, g := range m.grants {
		out[id] = append([]auth.Grant(nil), g...)
	}
	return out, nil
}

func (m *memAuthStore) UpdateUser(_ context.Context, id int64, upd userUpdate) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[id]
	if !ok {
		return false, nil
	}
	if upd.Name != nil {
		u.Name = *upd.Name
	}
	if upd.Disabled != nil {
		u.Disabled = *upd.Disabled
	}
	if upd.PasswordHash != nil {
		u.PasswordHash = *upd.PasswordHash
	}
	if upd.MustChangePassword != nil {
		u.MustChangePassword = *upd.MustChangePassword
	}
	m.users[id] = u
	if upd.Grants != nil {
		m.grants[id] = append([]auth.Grant(nil), (*upd.Grants)...)
	}
	if upd.RevokeSessions {
		for k, s := range m.sessions {
			if s.UserID == id && k != string(upd.KeepSession) {
				delete(m.sessions, k)
			}
		}
	}
	return true, nil
}

func (m *memAuthStore) CreateSession(_ context.Context, tokenHash []byte, userID int64, expiresAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[string(tokenHash)] = storage.SessionRow{UserID: userID, ExpiresAt: expiresAt, LastSeenAt: time.Now()}
	return nil
}

func (m *memAuthStore) Session(_ context.Context, tokenHash []byte) (storage.SessionRow, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[string(tokenHash)]
	return s, ok, nil
}

func (m *memAuthStore) TouchSession(_ context.Context, tokenHash []byte, lastSeen, expiresAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.touches++
	if s, ok := m.sessions[string(tokenHash)]; ok {
		s.LastSeenAt, s.ExpiresAt = lastSeen, expiresAt
		m.sessions[string(tokenHash)] = s
	}
	return nil
}

func (m *memAuthStore) DeleteSession(_ context.Context, tokenHash []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, string(tokenHash))
	return nil
}

func (m *memAuthStore) DeleteExpiredSessions(_ context.Context, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, s := range m.sessions {
		if s.ExpiresAt.Before(now) {
			delete(m.sessions, k)
		}
	}
	return nil
}

var testOrganizations = []OrganizationInfo{{ID: "ze", Name: "ZE"}, {ID: "pe", Name: "PE"}, {ID: "ab", Name: "AB"}}

// authTestHandlers returns handlers with authentication on, three
// configured organizations and edge sites ze/pe whose shadow telemetry
// lands under "<site>-edge".
func authTestHandlers(t *testing.T, store storeReader) (*Handlers, *memAuthStore) {
	t.Helper()
	if store == nil {
		store = &mockStore{}
	}
	h := NewHandlers(store, "*")
	h.SetOrganizations(testOrganizations)
	h.SetEdgeIngest(&EdgeIngest{
		Tokens:    map[string]string{"ze": "t-ze", "pe": "t-pe"},
		OrgSuffix: "-edge",
		Log:       slog.Default(),
	})
	accounts := newMemAuthStore()
	h.SetAuth(NewAuth(accounts, AuthOptions{}))
	return h, accounts
}

// sessionCookie creates an account with the given grants and a live
// session for it, without going through the password check.
func sessionCookie(t *testing.T, accounts *memAuthStore, email string, grants ...auth.Grant) *http.Cookie {
	t.Helper()
	id, err := accounts.CreateUser(context.Background(), storage.UserRow{Email: email}, grants)
	if err != nil {
		t.Fatal(err)
	}
	token, digest := auth.NewSessionToken()
	if err := accounts.CreateSession(context.Background(), digest, id, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	return &http.Cookie{Name: sessionCookieName, Value: token}
}

// serve sends one request through handler, with the CSRF header on
// state-changing methods.
func serve(handler http.Handler, method, target string, cookie *http.Cookie, body any) *httptest.ResponseRecorder {
	var rdr io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rdr = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, target, rdr)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	if isUnsafeMethod(method) {
		req.Header.Set(csrfHeader, "fetch")
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

// authOnlyRouter runs the real access table in front of stub handlers
// answering 418, so a test sees exactly what the auth layer let
// through.
func authOnlyRouter(h *Handlers) http.Handler {
	mux := http.NewServeMux()
	access := map[string]routeAccess{}
	for _, rt := range h.routes() {
		mux.HandleFunc(rt.pattern, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusTeapot)
		})
		access[rt.pattern] = rt.access
	}
	return h.withAuth(mux, access, mux)
}

func loginCookie(t *testing.T, router http.Handler, email, password string) *http.Cookie {
	t.Helper()
	rec := serve(router, http.MethodPost, "/api/v1/auth/login", nil, authLoginRequest{Email: email, Password: password})
	if rec.Code != http.StatusOK {
		t.Fatalf("login %s: status = %d (%s)", email, rec.Code, rec.Body.String())
	}
	return sessionFromResponse(t, rec)
}

func sessionFromResponse(t *testing.T, rec *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookieName {
			return c
		}
	}
	t.Fatalf("no %s cookie in response", sessionCookieName)
	return nil
}

func strPtr(s string) *string { return &s }

func jsonNumber(n int64) string {
	raw, _ := json.Marshal(n)
	return string(raw)
}
