package api

import (
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/nesh/sestelemetry/internal/auth"
	"github.com/nesh/sestelemetry/internal/storage"
)

const maxAuthBody = 16 << 10

type authLoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type authPasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

type authUserInfo struct {
	ID    int64  `json:"id"`
	Email string `json:"email"`
	Name  string `json:"name"`
}

// grantJSON is a role grant on the wire; a null organization_id means
// every organization.
type grantJSON struct {
	Role           auth.Role `json:"role"`
	OrganizationID *string   `json:"organization_id"`
}

type authOrganization struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Permissions []auth.Permission `json:"permissions"`
}

// AuthMeResponse is what the dashboard boots from: who is signed in and
// what they may do on each configured organization.
type AuthMeResponse struct {
	User        authUserInfo `json:"user"`
	GlobalAdmin bool         `json:"global_admin"`
	// MustChangePassword: the dashboard shows only the password form;
	// every other route answers 403 until the change.
	MustChangePassword bool               `json:"must_change_password"`
	Grants             []grantJSON        `json:"grants"`
	Organizations      []authOrganization `json:"organizations"`
}

func grantsJSON(grants []auth.Grant) []grantJSON {
	out := make([]grantJSON, 0, len(grants))
	for _, g := range grants {
		entry := grantJSON{Role: g.Role}
		if !g.AllOrganizations() {
			org := g.OrganizationID
			entry.OrganizationID = &org
		}
		out = append(out, entry)
	}
	return out
}

func (h *Handlers) meResponse(p *auth.Principal) AuthMeResponse {
	resp := AuthMeResponse{
		User:               authUserInfo{ID: p.UserID, Email: p.Email, Name: p.Name},
		GlobalAdmin:        p.IsGlobalAdmin(),
		MustChangePassword: p.MustChangePassword,
		Grants:             grantsJSON(p.Grants),
		Organizations:      []authOrganization{},
	}
	for _, org := range h.organizations {
		perms := p.PermissionsOn(org.ID)
		if len(perms) == 0 {
			continue
		}
		resp.Organizations = append(resp.Organizations, authOrganization{ID: org.ID, Name: org.Name, Permissions: perms})
	}
	return resp
}

func (h *Handlers) requireAuthConfigured(w http.ResponseWriter) bool {
	if h.auth == nil {
		http.Error(w, "authentication not configured", http.StatusServiceUnavailable)
		return false
	}
	return true
}

// authLogin handles POST /api/v1/auth/login: checks the credentials,
// sets the session cookie and answers with the same body as /auth/me.
func (h *Handlers) authLogin(w http.ResponseWriter, r *http.Request) {
	if !h.requireAuthConfigured(w) {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req authLoginRequest
	r.Body = http.MaxBytesReader(w, r.Body, maxAuthBody)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	p, token, expires, err := h.auth.login(r.Context(), req.Email, req.Password)
	var throttled *throttledError
	switch {
	case errors.As(err, &throttled):
		w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(throttled.retryAfter.Seconds()))))
		http.Error(w, err.Error(), http.StatusTooManyRequests)
		return
	case errors.Is(err, errBadCredentials):
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	case errors.Is(err, errAccountDisabled):
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	case err != nil:
		h.log.Error("api_auth_login", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	h.auth.setSessionCookie(w, token, expires)
	h.log.Info("api_auth_login_ok", "user_id", p.UserID)
	writeJSON(w, http.StatusOK, h.meResponse(p))
}

// authLogout handles POST /api/v1/auth/logout. It succeeds without a
// session too, so a stale tab can always get back to the login form.
func (h *Handlers) authLogout(w http.ResponseWriter, r *http.Request) {
	if !h.requireAuthConfigured(w) {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if token := sessionToken(r); token != "" {
		if err := h.auth.logout(r.Context(), token); err != nil {
			h.log.Error("api_auth_logout", "err", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
	}
	h.auth.clearSessionCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

// authMe handles GET /api/v1/auth/me.
func (h *Handlers) authMe(w http.ResponseWriter, r *http.Request) {
	if !h.requireAuthConfigured(w) {
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, h.meResponse(principalFrom(r.Context())))
}

// authPassword handles POST /api/v1/auth/password: the signed-in user
// changes their own password. Their other sessions are signed out.
func (h *Handlers) authPassword(w http.ResponseWriter, r *http.Request) {
	if !h.requireAuthConfigured(w) {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	p := principalFrom(r.Context())
	var req authPasswordRequest
	r.Body = http.MaxBytesReader(w, r.Body, maxAuthBody)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	now := h.auth.now()
	key := normalizeEmail(p.Email)
	if blocked, wait := h.auth.throttle.Blocked(key, now); blocked {
		w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(wait.Seconds()))))
		http.Error(w, (&throttledError{}).Error(), http.StatusTooManyRequests)
		return
	}
	user, ok, err := h.auth.store.UserByID(r.Context(), p.UserID)
	if err != nil {
		h.log.Error("api_auth_password", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	match := false
	if ok {
		if match, err = h.auth.checkPassword(r.Context(), user.PasswordHash, req.CurrentPassword); err != nil {
			h.log.Error("api_auth_password", "err", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
	}
	if !match {
		h.auth.throttle.Fail(key, now)
		http.Error(w, "поточний пароль невірний", http.StatusForbidden)
		return
	}
	hash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	changed := false
	upd := userUpdate{
		PasswordHash:       &hash,
		MustChangePassword: &changed,
		RevokeSessions:     true,
		KeepSession:        auth.TokenDigest(sessionToken(r)),
	}
	if _, err := h.auth.store.UpdateUser(r.Context(), p.UserID, upd); err != nil {
		h.log.Error("api_auth_password", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	h.auth.throttle.Reset(key)
	h.auth.forgetUser(p.UserID)
	w.WriteHeader(http.StatusNoContent)
}

// userJSON is one account on the user-management page.
type userJSON struct {
	ID           int64       `json:"id"`
	Email        string      `json:"email"`
	Name         string      `json:"name"`
	Disabled     bool        `json:"disabled"`
	AuthProvider string      `json:"auth_provider"`
	Grants       []grantJSON `json:"grants"`
	CreatedAt    time.Time   `json:"created_at"`
}

func toUserJSON(u storage.UserRow, grants []auth.Grant) userJSON {
	return userJSON{
		ID:           u.ID,
		Email:        u.Email,
		Name:         u.Name,
		Disabled:     u.Disabled,
		AuthProvider: u.AuthProvider,
		Grants:       grantsJSON(grants),
		CreatedAt:    u.CreatedAt.UTC(),
	}
}
