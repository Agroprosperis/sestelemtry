package edge

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"math"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const EvConsoleAuthLocked = "CONSOLE_LOGIN_LOCKED"

const (
	consoleMaxFails = 5
	consoleLockFor  = 15 * time.Minute
	// consoleCSRFHeader must accompany every POST: a cross-site form
	// can't set it, and a cross-origin fetch that does needs a CORS
	// preflight the console never answers.
	consoleCSRFHeader = "X-Requested-With"
)

type consoleVerdict int

const (
	consoleOK consoleVerdict = iota
	consoleNotSet
	consoleCorrupt
	consoleDenied
	consoleLocked
)

// consoleGate checks the console login. Failures are counted per client
// address, so nobody on the LAN can lock the operators out. A matched
// login is remembered for the stored hash it matched, so the status
// poll skips PBKDF2 and a changed password takes effect at once.
type consoleGate struct {
	path string

	mu    sync.Mutex
	fails map[string]int
	until map[string]time.Time
	known map[[32]byte]string
}

type consoleCheck struct {
	verdict consoleVerdict
	// retryAfter is the rest of the lockout (consoleLocked).
	retryAfter time.Duration
	// lockedNow marks the attempt that started the lockout.
	lockedNow bool
	user      string
}

func (g *consoleGate) check(ip, user, pass string, hasCreds bool, now time.Time) consoleCheck {
	if g == nil {
		return consoleCheck{verdict: consoleNotSet}
	}
	f, err := readSecretFile(g.path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return consoleCheck{verdict: consoleNotSet}
	case err != nil:
		return consoleCheck{verdict: consoleCorrupt}
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	if g.fails == nil {
		g.fails, g.until, g.known = map[string]int{}, map[string]time.Time{}, map[[32]byte]string{}
	}
	if until, ok := g.until[ip]; ok {
		if now.Before(until) {
			return consoleCheck{verdict: consoleLocked, retryAfter: until.Sub(now)}
		}
		delete(g.until, ip)
	}
	if !hasCreds {
		return consoleCheck{verdict: consoleDenied}
	}

	key := sha256.Sum256([]byte(user + ":" + pass))
	if g.known[key] == f.Hash && user == f.User {
		return consoleCheck{verdict: consoleOK, user: user}
	}
	userOK := subtle.ConstantTimeCompare([]byte(user), []byte(f.User)) == 1
	match, err := f.matches(pass)
	if err != nil {
		return consoleCheck{verdict: consoleCorrupt}
	}
	if userOK && match {
		for k, h := range g.known {
			if h != f.Hash {
				delete(g.known, k)
			}
		}
		g.known[key] = f.Hash
		delete(g.fails, ip)
		return consoleCheck{verdict: consoleOK, user: user}
	}
	g.fails[ip]++
	if g.fails[ip] >= consoleMaxFails {
		delete(g.fails, ip)
		g.until[ip] = now.Add(consoleLockFor)
		return consoleCheck{verdict: consoleLocked, retryAfter: consoleLockFor, lockedNow: true}
	}
	return consoleCheck{verdict: consoleDenied}
}

type consoleUserKey struct{}

// consoleBy names who acted from the console, for events and audit.
func consoleBy(r *http.Request) string {
	user, _ := r.Context().Value(consoleUserKey{}).(string)
	if user == "" {
		return "local-console " + remoteIP(r)
	}
	return "local-console " + user + " " + remoteIP(r)
}

func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// withConsoleAuth puts every console request behind the device login.
func (s *Service) withConsoleAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, hasCreds := r.BasicAuth()
		ip := remoteIP(r)
		now := time.Now().UTC()
		res := s.console.check(ip, user, pass, hasCreds, now)
		switch res.verdict {
		case consoleNotSet:
			http.Error(w, "Пароль консолі не задано: ems-edge -set-console-password", http.StatusServiceUnavailable)
			return
		case consoleCorrupt:
			http.Error(w, "Файл пароля консолі пошкоджено: задайте його заново (ems-edge -set-console-password)", http.StatusServiceUnavailable)
			return
		case consoleLocked:
			if res.lockedNow {
				s.log.Warn("edge_console_login_locked", "ip", ip, "user", user)
				s.pushEvent(Event{TS: now, Severity: SevWarning, Code: EvConsoleAuthLocked,
					Message: "вхід у консоль заблоковано на " + consoleLockFor.String() + " після невірних паролів (" + ip + ")"})
			}
			w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(res.retryAfter.Seconds()))))
			http.Error(w, "забагато невірних спроб входу — спробуйте пізніше", http.StatusTooManyRequests)
			return
		case consoleDenied:
			w.Header().Set("WWW-Authenticate", `Basic realm="EMS edge `+s.cfg.SiteID+`", charset="UTF-8"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.Method == http.MethodPost && strings.TrimSpace(r.Header.Get(consoleCSRFHeader)) == "" {
			http.Error(w, "forbidden: missing "+consoleCSRFHeader+" header", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), consoleUserKey{}, res.user)))
	})
}
