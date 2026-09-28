package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"sync"
	"time"
)

// NewSessionToken returns a fresh cookie secret and the digest the
// database stores in its place: a leaked sessions table can't be
// replayed as cookies.
func NewSessionToken() (token string, digest []byte) {
	token = rand.Text()
	return token, TokenDigest(token)
}

// TokenDigest is the stored form of a session cookie secret.
func TokenDigest(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// Throttle locks an account out of password checks after repeated
// failures. It is keyed by email rather than client address: behind the
// Vite dev-server proxy every request arrives from the same IP.
type Throttle struct {
	max    int
	window time.Duration

	mu       sync.Mutex
	failures map[string]failureWindow
}

type failureWindow struct {
	count int
	first time.Time
}

// throttlePruneAt bounds the failure map: once it holds this many keys,
// windows that have already expired are dropped.
const throttlePruneAt = 1024

// NewThrottle allows max failed attempts per key within window; the
// lockout lasts until the window that started with the first failure
// ends.
func NewThrottle(max int, window time.Duration) *Throttle {
	return &Throttle{max: max, window: window, failures: map[string]failureWindow{}}
}

// Blocked reports whether key is locked out at now, and for how long.
func (t *Throttle) Blocked(key string, now time.Time) (bool, time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	f, ok := t.failures[key]
	if !ok {
		return false, 0
	}
	end := f.first.Add(t.window)
	if !now.Before(end) {
		delete(t.failures, key)
		return false, 0
	}
	if f.count < t.max {
		return false, 0
	}
	return true, end.Sub(now)
}

// Fail records a failed attempt for key.
func (t *Throttle) Fail(key string, now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	f, ok := t.failures[key]
	if !ok || !now.Before(f.first.Add(t.window)) {
		f = failureWindow{first: now}
	}
	f.count++
	t.failures[key] = f
	if len(t.failures) >= throttlePruneAt {
		for k, w := range t.failures {
			if !now.Before(w.first.Add(t.window)) {
				delete(t.failures, k)
			}
		}
	}
}

// Reset clears key after a successful login.
func (t *Throttle) Reset(key string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.failures, key)
}
