package api

import (
	"context"
	"net/http"
	"time"

	"github.com/nesh/sestelemetry/internal/auth"
)

type cachedSession struct {
	principal *auth.Principal
	expiresAt time.Time
	lastSeen  time.Time
	loadedAt  time.Time
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
		principal: principalOf(user, grants),
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
