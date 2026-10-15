package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/nesh/sestelemetry/internal/auth"
	"github.com/nesh/sestelemetry/internal/storage"
)

const maxUserNameLen = 200

const lastGlobalAdminMessage = "це останній адміністратор усіх організацій: спершу призначте іншого"

type userCreateRequest struct {
	Email    string      `json:"email"`
	Name     string      `json:"name"`
	Password string      `json:"password"`
	Grants   []grantJSON `json:"grants"`
}

// userUpdateRequest changes only the fields it carries. An empty
// password leaves the current one in place.
type userUpdateRequest struct {
	Name     *string      `json:"name"`
	Disabled *bool        `json:"disabled"`
	Password *string      `json:"password"`
	Grants   *[]grantJSON `json:"grants"`
}

// users handles /api/v1/users, open to administrators of every
// organization only:
//
//	GET              — every account with its grants
//	POST             — create an account
//	PUT ?user_id=    — change name, password, disabled flag or grants
//	DELETE ?user_id= — delete an account with its sessions and grants
//
// The last enabled administrator of every organization can be neither
// disabled, demoted nor deleted.
func (h *Handlers) users(w http.ResponseWriter, r *http.Request) {
	if !h.requireAuthConfigured(w) {
		return
	}
	switch r.Method {
	case http.MethodGet:
		h.listUsers(w, r)
	case http.MethodPost:
		h.createUser(w, r)
	case http.MethodPut:
		h.updateUser(w, r)
	case http.MethodDelete:
		h.deleteUser(w, r)
	default:
		w.Header().Set("Allow", "GET, POST, PUT, DELETE")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handlers) listUsers(w http.ResponseWriter, r *http.Request) {
	users, err := h.auth.store.ListUsers(r.Context())
	if err != nil {
		h.log.Error("api_users_list", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	grants, err := h.auth.store.AllGrants(r.Context())
	if err != nil {
		h.log.Error("api_users_list", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	out := make([]userJSON, 0, len(users))
	for _, u := range users {
		out = append(out, toUserJSON(u, grants[u.ID]))
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": out})
}

func (h *Handlers) createUser(w http.ResponseWriter, r *http.Request) {
	var req userCreateRequest
	if !decodeStrictJSON(w, r, &req) {
		return
	}
	email := normalizeEmail(req.Email)
	if err := validateEmail(email); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	name, err := cleanUserName(req.Name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	grants, err := h.parseGrants(req.Grants)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// The administrator hands this password out, so it only lets the
	// user in to set their own.
	user := storage.UserRow{Email: email, Name: name, PasswordHash: hash, MustChangePassword: true}
	id, err := h.auth.store.CreateUser(r.Context(), user, grants)
	if errors.Is(err, storage.ErrEmailTaken) {
		http.Error(w, "користувач з таким email уже існує", http.StatusConflict)
		return
	}
	if err != nil {
		h.log.Error("api_users_create", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	user, _, err = h.auth.store.UserByID(r.Context(), id)
	if err != nil {
		h.log.Error("api_users_create", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	h.log.Info("api_users_create_ok", "user_id", id, "by", principalFrom(r.Context()).UserID, "grants", len(grants))
	writeJSON(w, http.StatusCreated, toUserJSON(user, grants))
}

func (h *Handlers) updateUser(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("user_id")), 10, 64)
	if err != nil {
		http.Error(w, "user_id is required", http.StatusBadRequest)
		return
	}
	var req userUpdateRequest
	if !decodeStrictJSON(w, r, &req) {
		return
	}
	ctx := r.Context()
	user, ok, err := h.auth.store.UserByID(ctx, id)
	if err != nil {
		h.log.Error("api_users_update", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, "user not found", http.StatusNotFound)
		return
	}
	current, err := h.auth.store.UserGrants(ctx, id)
	if err != nil {
		h.log.Error("api_users_update", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	var upd userUpdate
	if req.Name != nil {
		name, err := cleanUserName(*req.Name)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		upd.Name = &name
	}
	upd.Disabled = req.Disabled
	if req.Password != nil && *req.Password != "" {
		hash, err := auth.HashPassword(*req.Password)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		upd.PasswordHash = &hash
		// A password set for someone else is handed out, so it is
		// temporary; setting your own here is not.
		pending := id != principalFrom(ctx).UserID
		upd.MustChangePassword = &pending
	}
	// The edit form always sends the grants; only a real change counts,
	// so fixing a name doesn't sign the user out.
	grants := current
	if req.Grants != nil {
		parsed, err := h.parseGrants(*req.Grants)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if !auth.SameGrants(current, parsed) {
			upd.Grants = &parsed
		}
		grants = parsed
	}

	disabled := user.Disabled
	if upd.Disabled != nil {
		disabled = *upd.Disabled
	}
	if disabled || !isGlobalAdmin(grants) {
		last, err := h.lastGlobalAdmin(ctx, user, current)
		if err != nil {
			h.log.Error("api_users_update", "err", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		if last {
			http.Error(w, lastGlobalAdminMessage, http.StatusConflict)
			return
		}
	}

	upd.RevokeSessions = upd.Grants != nil || upd.PasswordHash != nil || (disabled && !user.Disabled)
	ok, err = h.auth.store.UpdateUser(ctx, id, upd)
	if err != nil {
		h.log.Error("api_users_update", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, "user not found", http.StatusNotFound)
		return
	}
	h.auth.forgetUser(id)
	user, _, err = h.auth.store.UserByID(ctx, id)
	if err != nil {
		h.log.Error("api_users_update", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	h.log.Info("api_users_update_ok",
		"user_id", id,
		"by", principalFrom(ctx).UserID,
		"grants_changed", upd.Grants != nil,
		"password_changed", upd.PasswordHash != nil,
		"disabled", disabled,
	)
	writeJSON(w, http.StatusOK, toUserJSON(user, grants))
}

// deleteUser removes an account for good; disabling keeps it. Your own
// account can't be deleted here either.
func (h *Handlers) deleteUser(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("user_id")), 10, 64)
	if err != nil {
		http.Error(w, "user_id is required", http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	user, ok, err := h.auth.store.UserByID(ctx, id)
	if err != nil {
		h.log.Error("api_users_delete", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, "user not found", http.StatusNotFound)
		return
	}
	grants, err := h.auth.store.UserGrants(ctx, id)
	if err != nil {
		h.log.Error("api_users_delete", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	last, err := h.lastGlobalAdmin(ctx, user, grants)
	if err != nil {
		h.log.Error("api_users_delete", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if last {
		http.Error(w, lastGlobalAdminMessage, http.StatusConflict)
		return
	}
	by := principalFrom(ctx).UserID
	if id == by {
		http.Error(w, "свій обліковий запис видалити не можна", http.StatusConflict)
		return
	}
	ok, err = h.auth.store.DeleteUser(ctx, id)
	if err != nil {
		h.log.Error("api_users_delete", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, "user not found", http.StatusNotFound)
		return
	}
	h.auth.forgetUser(id)
	h.log.Info("api_users_delete_ok", "user_id", id, "by", by)
	w.WriteHeader(http.StatusNoContent)
}

// parseGrants validates the grants of a create/update request against
// the roles and the configured organizations.
func (h *Handlers) parseGrants(in []grantJSON) ([]auth.Grant, error) {
	known := make(map[string]bool, len(h.organizations))
	for _, o := range h.organizations {
		known[o.ID] = true
	}
	out := make([]auth.Grant, 0, len(in))
	for _, g := range in {
		if !g.Role.Valid() {
			return nil, fmt.Errorf("невідома роль %q", g.Role)
		}
		org := ""
		if g.OrganizationID != nil {
			org = strings.TrimSpace(*g.OrganizationID)
		}
		if org != "" && !known[org] {
			return nil, fmt.Errorf("невідома організація %q", org)
		}
		out = append(out, auth.Grant{Role: g.Role, OrganizationID: org})
	}
	return auth.NormalizeGrants(out), nil
}

func isGlobalAdmin(grants []auth.Grant) bool {
	return (&auth.Principal{Grants: grants}).IsGlobalAdmin()
}

// lastGlobalAdmin reports whether user, holding grants, is the only
// enabled administrator of every organization: the one account left
// that can manage the others.
func (h *Handlers) lastGlobalAdmin(ctx context.Context, user storage.UserRow, grants []auth.Grant) (bool, error) {
	if user.Disabled || !isGlobalAdmin(grants) {
		return false, nil
	}
	others, err := h.otherGlobalAdmins(ctx, user.ID)
	return others == 0, err
}

// otherGlobalAdmins counts the enabled administrators of every
// organization besides the given user.
func (h *Handlers) otherGlobalAdmins(ctx context.Context, exclude int64) (int, error) {
	users, err := h.auth.store.ListUsers(ctx)
	if err != nil {
		return 0, err
	}
	grants, err := h.auth.store.AllGrants(ctx)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, u := range users {
		if u.ID != exclude && !u.Disabled && isGlobalAdmin(grants[u.ID]) {
			n++
		}
	}
	return n, nil
}

func cleanUserName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if len([]rune(name)) > maxUserNameLen {
		return "", fmt.Errorf("ім'я не може бути довшим за %d символів", maxUserNameLen)
	}
	return name, nil
}

func decodeStrictJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxAuthBody)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		http.Error(w, fmt.Sprintf("invalid JSON body: %v", err), http.StatusBadRequest)
		return false
	}
	return true
}
