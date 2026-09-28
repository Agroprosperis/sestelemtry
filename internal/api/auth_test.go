package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nesh/sestelemetry/internal/alerts"
	"github.com/nesh/sestelemetry/internal/auth"
	"github.com/nesh/sestelemetry/internal/storage"
)

func TestEveryRouteHasAccessRule(t *testing.T) {
	h := NewHandlers(&mockStore{}, "*")
	seen := map[string]bool{}
	for _, rt := range h.routes() {
		if seen[rt.pattern] {
			t.Errorf("%s registered twice", rt.pattern)
		}
		seen[rt.pattern] = true
		if _, hasDefault := rt.access.rules[""]; rt.access.kind == accessSession && !hasDefault {
			t.Errorf("%s: a session route needs a default rule", rt.pattern)
		}
		if rt.access.kind != accessSession && (len(rt.access.rules) > 0 || rt.access.pendingOK) {
			t.Errorf("%s: public and edge routes take no rules", rt.pattern)
		}
	}
	for _, p := range []string{"/api/v1/edge/batch", "/api/v1/edge/heartbeat", "/api/v1/edge/manifest"} {
		if !seen[p] {
			t.Errorf("%s missing from the route table", p)
		}
	}
}

func TestAuthRouteMatrix(t *testing.T) {
	h, accounts := authTestHandlers(t, nil)
	router := authOnlyRouter(h)

	grants := map[string][]auth.Grant{
		"engineer":   {{Role: auth.RoleEngineer, OrganizationID: "ze"}},
		"engineerAB": {{Role: auth.RoleEngineer, OrganizationID: "ab"}},
		"control":    {{Role: auth.RoleControlEngineer, OrganizationID: "ze"}},
		"economist":  {{Role: auth.RoleEconomist, OrganizationID: "ze"}, {Role: auth.RoleEconomist, OrganizationID: "pe"}},
		"mixed":      {{Role: auth.RoleEconomist, OrganizationID: "ze"}, {Role: auth.RoleEngineer, OrganizationID: "pe"}},
		"admin":      {{Role: auth.RoleAdmin, OrganizationID: "ze"}},
		"global":     {{Role: auth.RoleAdmin}},
	}
	cookies := map[string]*http.Cookie{}
	for who, g := range grants {
		cookies[who] = sessionCookie(t, accounts, who+"@example.com", g...)
	}

	const (
		ok     = http.StatusTeapot
		denied = http.StatusForbidden
		anon   = http.StatusUnauthorized
	)
	day := "&from=2026-09-01T00:00:00Z&to=2026-09-02T00:00:00Z"
	dstDay := "&from=2026-10-24T21:00:00Z&to=2026-10-25T22:00:00Z"
	twoDays := "&from=2026-08-31T00:00:00Z&to=2026-09-02T00:00:00Z"
	week := "&from=2026-08-26T00:00:00Z&to=2026-09-02T00:00:00Z"
	year := "&from=2026-01-01T00:00:00Z&to=2026-09-02T00:00:00Z"
	series := "/api/v1/timeseries?metric_keys=load_power_kw&organization_id="

	cases := []struct {
		who, method, target string
		want                int
	}{
		{"engineer", http.MethodGet, series + "ze" + day, ok},
		{"engineer", http.MethodGet, series + "ze" + dstDay, ok},
		{"engineer", http.MethodGet, series + "ze", ok},
		{"engineer", http.MethodGet, series + "ze" + week, denied},
		{"engineer", http.MethodGet, series + "ze&from=2026-01-01T00:00:00Z", denied},
		{"engineer", http.MethodGet, "/api/v1/energy-summary?organization_id=ze" + year, denied},
		{"engineer", http.MethodGet, "/api/v1/current?organization_id=ze", ok},
		{"engineer", http.MethodGet, "/api/v1/current?organization_id=ze-edge", ok},
		{"engineer", http.MethodGet, "/api/v1/current?organization_id=pe", denied},
		// ab has no edge device, so "ab-edge" is not its shadow.
		{"engineerAB", http.MethodGet, "/api/v1/current?organization_id=ab", ok},
		{"engineerAB", http.MethodGet, "/api/v1/current?organization_id=ab-edge", denied},
		{"engineer", http.MethodGet, "/api/v1/current", denied},
		{"engineer", http.MethodGet, "/api/v1/uze-plan?organization_id=ze", ok},
		{"engineer", http.MethodGet, "/api/v1/weather-forecast?organization_id=ze", ok},
		{"engineer", http.MethodGet, "/api/v1/dam-prices", ok},
		{"engineer", http.MethodGet, "/api/v1/edge/fleet", ok},
		{"engineer", http.MethodGet, "/api/v1/organizations", ok},
		{"engineer", http.MethodGet, "/api/v1/pv-plan-summary?organization_id=ze", denied},
		{"engineer", http.MethodGet, "/api/v1/economics/daily?organization_id=ze", denied},
		{"engineer", http.MethodGet, "/api/v1/edge/status?site_id=ze", denied},
		{"engineer", http.MethodGet, "/api/v1/samples?organization_id=ze", denied},
		{"engineer", http.MethodGet, "/api/v1/registers", denied},
		{"engineer", http.MethodGet, "/api/v1/users", denied},
		{"engineer", http.MethodGet, "/swagger", denied},

		{"control", http.MethodGet, series + "ze" + twoDays, ok},
		{"control", http.MethodGet, "/api/v1/energy-summary?organization_id=ze" + week, denied},
		{"control", http.MethodGet, "/api/v1/edge/sites", ok},
		{"control", http.MethodGet, "/api/v1/edge/status?site_id=ze", ok},
		{"control", http.MethodGet, "/api/v1/edge/status?site_id=pe", denied},
		{"control", http.MethodGet, "/api/v1/edge/settings?site_id=ze", ok},
		{"control", http.MethodPut, "/api/v1/edge/settings?site_id=ze", ok},
		{"control", http.MethodPut, "/api/v1/edge/settings?site_id=pe", denied},
		{"control", http.MethodPut, "/api/v1/edge/load-plan?site_id=ze", ok},
		{"control", http.MethodPost, "/api/v1/edge/manifest/publish?site_id=ze", ok},
		{"control", http.MethodPost, "/api/v1/edge/manifest/publish-manual?site_id=ze", ok},
		{"control", http.MethodPut, "/api/v1/organization-tariffs?organization_id=ze", denied},

		{"economist", http.MethodGet, "/api/v1/economics/daily?organization_id=pe", ok},
		{"economist", http.MethodGet, "/api/v1/economics/daily?organization_id=ab", denied},
		{"economist", http.MethodGet, "/api/v1/economics/portfolio", ok},
		{"economist", http.MethodGet, "/api/v1/pv-plan-summary?organization_id=ze" + year, ok},
		{"economist", http.MethodPut, "/api/v1/organization-tariffs?organization_id=ze", ok},
		{"economist", http.MethodDelete, "/api/v1/organization-tariff-schedule?organization_id=ze", ok},
		{"economist", http.MethodPost, "/api/v1/economics/recompute?organization_id=ze", ok},
		{"economist", http.MethodPost, "/api/v1/dam-prices/refresh", ok},
		{"economist", http.MethodPost, "/api/v1/dam-prices/refresh-range", ok},
		{"economist", http.MethodGet, "/api/v1/current?organization_id=ze", denied},
		{"economist", http.MethodGet, "/api/v1/edge/fleet", denied},
		{"economist", http.MethodPut, "/api/v1/edge/settings?site_id=ze", denied},

		{"mixed", http.MethodGet, "/api/v1/economics/daily?organization_id=ze", ok},
		{"mixed", http.MethodGet, "/api/v1/economics/daily?organization_id=pe", denied},
		{"mixed", http.MethodGet, "/api/v1/current?organization_id=pe", ok},
		{"mixed", http.MethodGet, "/api/v1/current?organization_id=ze", denied},

		{"admin", http.MethodGet, "/api/v1/energy-summary?organization_id=ze" + year, ok},
		{"admin", http.MethodGet, "/api/v1/samples?organization_id=ze", ok},
		{"admin", http.MethodGet, "/api/v1/samples?organization_id=pe", denied},
		{"admin", http.MethodPost, "/api/v1/askoe/import?organization_id=ze", ok},
		{"admin", http.MethodGet, "/api/v1/organization-alert-settings", ok},
		{"admin", http.MethodPut, "/api/v1/organization-alert-settings?organization_id=ze", ok},
		{"admin", http.MethodPut, "/api/v1/organization-alert-settings?organization_id=pe", denied},
		{"admin", http.MethodGet, "/api/v1/alert-settings", denied},
		{"admin", http.MethodPost, "/api/v1/alert-settings/test-email", denied},
		{"admin", http.MethodPost, "/api/v1/alert-settings/test-email?organization_id=ze", ok},
		{"admin", http.MethodGet, "/api/v1/users", denied},
		{"admin", http.MethodGet, "/swagger/openapi.yaml", ok},

		{"global", http.MethodGet, "/api/v1/users", ok},
		{"global", http.MethodPut, "/api/v1/alert-settings", ok},
		{"global", http.MethodPost, "/api/v1/alert-settings/test-email", ok},
		{"global", http.MethodGet, "/api/v1/current", ok},
		{"global", http.MethodGet, series + "ab" + year, ok},

		{"", http.MethodGet, "/healthz", ok},
		{"", http.MethodGet, "/readyz", ok},
		{"", http.MethodPost, "/api/v1/auth/login", ok},
		{"", http.MethodGet, "/api/v1/current?organization_id=ze", anon},
		{"", http.MethodGet, "/api/v1/auth/me", anon},
		{"", http.MethodGet, "/swagger", anon},
	}
	for _, c := range cases {
		rec := serve(router, c.method, c.target, cookies[c.who], nil)
		if rec.Code != c.want {
			t.Errorf("%s %s %s: status = %d, want %d (%s)", c.who, c.method, c.target, rec.Code, c.want, strings.TrimSpace(rec.Body.String()))
		}
	}
}

func TestAuthEdgeUplinkKeepsBearer(t *testing.T) {
	h, _ := authTestHandlers(t, nil)
	// No session and no CSRF header: the handler's own Bearer check
	// decides.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/edge/heartbeat", strings.NewReader(`{"site_id":"ze"}`))
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "unauthorized") {
		t.Fatalf("status = %d (%s), want the edge handler's 401", rec.Code, rec.Body.String())
	}
	req = httptest.NewRequest(http.MethodGet, "/api/v1/edge/manifest?site_id=ze", nil)
	rec = httptest.NewRecorder()
	authOnlyRouter(h).ServeHTTP(rec, req)
	if rec.Code != http.StatusTeapot {
		t.Fatalf("manifest poll: status = %d, want it passed to the handler", rec.Code)
	}
}

func TestAuthRequiresCSRFHeader(t *testing.T) {
	h, accounts := authTestHandlers(t, nil)
	cookie := sessionCookie(t, accounts, "eco@example.com", auth.Grant{Role: auth.RoleEconomist})
	router := authOnlyRouter(h)
	for _, target := range []string{"/api/v1/organization-tariffs?organization_id=ze", "/api/v1/auth/login"} {
		req := httptest.NewRequest(http.MethodPut, target, strings.NewReader(`{}`))
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s without %s: status = %d, want 403", target, csrfHeader, rec.Code)
		}
	}
	if rec := serve(router, http.MethodPut, "/api/v1/organization-tariffs?organization_id=ze", cookie, nil); rec.Code != http.StatusTeapot {
		t.Fatalf("with the header: status = %d, want it passed", rec.Code)
	}
}

func TestAuthLoginSessionLogout(t *testing.T) {
	h, accounts := authTestHandlers(t, nil)
	created, err := h.auth.Bootstrap(context.Background(), " Admin@Example.com ", "correct horse battery")
	if err != nil || created != BootstrapFromEnv {
		t.Fatalf("Bootstrap = %v, %v", created, err)
	}
	if again, err := h.auth.Bootstrap(context.Background(), "", ""); err != nil || again != BootstrapNone {
		t.Fatalf("second Bootstrap = %v, %v; want a no-op", again, err)
	}
	router := h.Router()

	rec := serve(router, http.MethodPost, "/api/v1/auth/login", nil, authLoginRequest{Email: "admin@example.com", Password: "wrong password!"})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password: status = %d", rec.Code)
	}
	rec = serve(router, http.MethodPost, "/api/v1/auth/login", nil, authLoginRequest{Email: "ADMIN@example.com", Password: "correct horse battery"})
	if rec.Code != http.StatusOK {
		t.Fatalf("login: status = %d (%s)", rec.Code, rec.Body.String())
	}
	cookie := sessionFromResponse(t, rec)
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode || cookie.Path != "/" || cookie.MaxAge <= 0 {
		t.Fatalf("cookie attributes: %+v", cookie)
	}
	var me AuthMeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &me); err != nil {
		t.Fatal(err)
	}
	if !me.GlobalAdmin || me.User.Email != "admin@example.com" || len(me.Organizations) != len(testOrganizations) {
		t.Fatalf("login body: %+v", me)
	}
	for _, org := range me.Organizations {
		if len(org.Permissions) == 0 {
			t.Fatalf("%s has no permissions for an admin of every organization", org.ID)
		}
	}

	if rec := serve(router, http.MethodGet, "/api/v1/auth/me", cookie, nil); rec.Code != http.StatusOK {
		t.Fatalf("me: status = %d", rec.Code)
	}
	if rec := serve(router, http.MethodGet, "/api/v1/current?organization_id=ze", cookie, nil); rec.Code != http.StatusOK {
		t.Fatalf("data route with session: status = %d", rec.Code)
	}

	rec = serve(router, http.MethodPost, "/api/v1/auth/logout", cookie, nil)
	if rec.Code != http.StatusNoContent || sessionFromResponse(t, rec).MaxAge >= 0 {
		t.Fatalf("logout: status = %d, cookie must be cleared", rec.Code)
	}
	if rec := serve(router, http.MethodGet, "/api/v1/auth/me", cookie, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("me after logout: status = %d, want 401", rec.Code)
	}
	if len(accounts.sessions) != 0 {
		t.Fatalf("logout left %d sessions", len(accounts.sessions))
	}
}

func TestAuthLoginThrottleAndDisabled(t *testing.T) {
	h, accounts := authTestHandlers(t, nil)
	hash, err := auth.HashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := accounts.CreateUser(context.Background(), storage.UserRow{Email: "eng@example.com", PasswordHash: hash},
		[]auth.Grant{{Role: auth.RoleEngineer, OrganizationID: "ze"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := accounts.CreateUser(context.Background(), storage.UserRow{Email: "gone@example.com", PasswordHash: hash, Disabled: true}, nil); err != nil {
		t.Fatal(err)
	}
	router := h.Router()

	rec := serve(router, http.MethodPost, "/api/v1/auth/login", nil, authLoginRequest{Email: "gone@example.com", Password: "correct horse battery"})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("disabled account: status = %d, want 403", rec.Code)
	}

	for i := 0; i < loginMaxFailures; i++ {
		rec := serve(router, http.MethodPost, "/api/v1/auth/login", nil, authLoginRequest{Email: "eng@example.com", Password: "guess guess guess"})
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("failure %d: status = %d", i+1, rec.Code)
		}
	}
	rec = serve(router, http.MethodPost, "/api/v1/auth/login", nil, authLoginRequest{Email: "eng@example.com", Password: "correct horse battery"})
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("locked account: status = %d, Retry-After = %q", rec.Code, rec.Header().Get("Retry-After"))
	}
}

func TestAuthBootstrapRejectsBadVariables(t *testing.T) {
	a := NewAuth(newMemAuthStore(), AuthOptions{})
	if _, err := a.Bootstrap(context.Background(), "admin@example.com", "short"); !errors.Is(err, auth.ErrPasswordTooShort) {
		t.Fatalf("short password: err = %v", err)
	}
	if _, err := a.Bootstrap(context.Background(), "not-an-email", "correct horse battery"); err == nil {
		t.Fatal("malformed email accepted")
	}
}

func TestAuthDefaultAdminMustChangePassword(t *testing.T) {
	h, accounts := authTestHandlers(t, nil)
	// Only half of the variables set counts as none.
	result, err := h.auth.Bootstrap(context.Background(), "ops@example.com", "")
	if err != nil || result != BootstrapDefaultAdmin {
		t.Fatalf("Bootstrap = %v, %v; want the admin/admin account", result, err)
	}
	router := h.Router()

	rec := serve(router, http.MethodPost, "/api/v1/auth/login", nil, authLoginRequest{Email: "Admin", Password: "admin"})
	if rec.Code != http.StatusOK {
		t.Fatalf("login admin/admin: status = %d (%s)", rec.Code, rec.Body.String())
	}
	cookie := sessionFromResponse(t, rec)
	var me AuthMeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &me); err != nil {
		t.Fatal(err)
	}
	if !me.MustChangePassword || !me.GlobalAdmin {
		t.Fatalf("login body: %+v; want a global admin with a pending password change", me)
	}

	for _, target := range []string{"/api/v1/current?organization_id=ze", "/api/v1/users", "/api/v1/organizations"} {
		if rec := serve(router, http.MethodGet, target, cookie, nil); rec.Code != http.StatusForbidden {
			t.Fatalf("%s before the change: status = %d, want 403", target, rec.Code)
		}
	}
	if rec := serve(router, http.MethodGet, "/api/v1/auth/me", cookie, nil); rec.Code != http.StatusOK {
		t.Fatalf("me before the change: status = %d, want 200", rec.Code)
	}

	// "admin" itself is below the length policy, so it can't be kept.
	rec = serve(router, http.MethodPost, "/api/v1/auth/password", cookie, authPasswordRequest{CurrentPassword: "admin", NewPassword: "admin"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("keeping admin: status = %d, want 400", rec.Code)
	}
	rec = serve(router, http.MethodPost, "/api/v1/auth/password", cookie, authPasswordRequest{CurrentPassword: "admin", NewPassword: "a proper passphrase"})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("change: status = %d (%s)", rec.Code, rec.Body.String())
	}
	if rec := serve(router, http.MethodGet, "/api/v1/users", cookie, nil); rec.Code != http.StatusOK {
		t.Fatalf("users after the change: status = %d, want 200", rec.Code)
	}
	user, _, _ := accounts.UserByEmail(context.Background(), "admin")
	if user.MustChangePassword {
		t.Fatal("the pending flag must clear with the change")
	}
}

func TestAuthListsFollowScope(t *testing.T) {
	store := &mockStore{orgAlertSettings: map[string]alerts.OrgSettings{
		"ze": {Enabled: true},
		"pe": {Enabled: false},
	}}
	h, accounts := authTestHandlers(t, store)
	router := h.Router()

	engineer := sessionCookie(t, accounts, "eng@example.com", auth.Grant{Role: auth.RoleEngineer, OrganizationID: "ze"})
	var orgs OrganizationsResponse
	rec := serve(router, http.MethodGet, "/api/v1/organizations", engineer, nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &orgs); err != nil {
		t.Fatal(err)
	}
	if len(orgs.Organizations) != 1 || orgs.Organizations[0].ID != "ze" {
		t.Fatalf("organizations = %+v, want ze only", orgs.Organizations)
	}

	control := sessionCookie(t, accounts, "ctl@example.com", auth.Grant{Role: auth.RoleControlEngineer, OrganizationID: "pe"})
	var sites struct {
		Sites []string `json:"sites"`
	}
	rec = serve(router, http.MethodGet, "/api/v1/edge/sites", control, nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &sites); err != nil {
		t.Fatal(err)
	}
	if len(sites.Sites) != 1 || sites.Sites[0] != "pe" {
		t.Fatalf("edge sites = %v, want [pe]", sites.Sites)
	}

	admin := sessionCookie(t, accounts, "adm@example.com", auth.Grant{Role: auth.RoleAdmin, OrganizationID: "ze"})
	var overrides OrgAlertSettingsResponse
	rec = serve(router, http.MethodGet, "/api/v1/organization-alert-settings", admin, nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &overrides); err != nil {
		t.Fatal(err)
	}
	if _, ok := overrides.Organizations["ze"]; !ok || len(overrides.Organizations) != 1 {
		t.Fatalf("alert overrides = %+v, want ze only", overrides.Organizations)
	}
}

func TestUsersAPI(t *testing.T) {
	h, accounts := authTestHandlers(t, nil)
	router := h.Router()
	global := sessionCookie(t, accounts, "root@example.com", auth.Grant{Role: auth.RoleAdmin})
	ze, pe := "ze", "pe"

	rec := serve(router, http.MethodPost, "/api/v1/users", global, userCreateRequest{
		Email:    " Economist@Example.com ",
		Name:     "Олена",
		Password: "economics rules",
		Grants: []grantJSON{
			{Role: auth.RoleEconomist, OrganizationID: &ze},
			{Role: auth.RoleEngineer, OrganizationID: &pe},
			{Role: auth.RoleEngineer},
		},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: status = %d (%s)", rec.Code, rec.Body.String())
	}
	var created userJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Email != "economist@example.com" || len(created.Grants) != 2 {
		t.Fatalf("created = %+v; want a lowercased email and the pe grant folded into «all»", created)
	}
	if !created.MustChangePassword {
		t.Fatal("a password handed out by an administrator must be temporary")
	}

	bad := []userCreateRequest{
		{Email: "economist@example.com", Password: "economics rules"},
		{Email: "x@example.com", Password: "economics rules", Grants: []grantJSON{{Role: "operator"}}},
		{Email: "y@example.com", Password: "economics rules", Grants: []grantJSON{{Role: auth.RoleEngineer, OrganizationID: strPtr("zz")}}},
		{Email: "z@example.com", Password: "short"},
		{Email: "no-at-sign", Password: "economics rules"},
	}
	wantBad := []int{http.StatusConflict, http.StatusBadRequest, http.StatusBadRequest, http.StatusBadRequest, http.StatusBadRequest}
	for i, req := range bad {
		if rec := serve(router, http.MethodPost, "/api/v1/users", global, req); rec.Code != wantBad[i] {
			t.Errorf("bad create %d: status = %d, want %d (%s)", i, rec.Code, wantBad[i], rec.Body.String())
		}
	}

	// Until the user sets their own password, the temporary one opens
	// nothing but the password form.
	token, digest := auth.NewSessionToken()
	_ = accounts.CreateSession(context.Background(), digest, created.ID, time.Now().Add(time.Hour))
	userCookie := &http.Cookie{Name: sessionCookieName, Value: token}
	if rec := serve(router, http.MethodGet, "/api/v1/economics/daily?organization_id=ze", userCookie, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("economist with the temporary password: status = %d, want 403", rec.Code)
	}
	if rec := serve(router, http.MethodPost, "/api/v1/auth/password", userCookie,
		authPasswordRequest{CurrentPassword: "economics rules", NewPassword: "my own passphrase"}); rec.Code != http.StatusNoContent {
		t.Fatalf("set own password: status = %d (%s)", rec.Code, rec.Body.String())
	}

	// The account's live session ends when its grants change.
	if rec := serve(router, http.MethodGet, "/api/v1/economics/daily?organization_id=ze", userCookie, nil); rec.Code == http.StatusUnauthorized || rec.Code == http.StatusForbidden {
		t.Fatalf("economist before the change: status = %d", rec.Code)
	}
	target := "/api/v1/users?user_id=" + jsonNumber(created.ID)
	// The edit form resends the unchanged grants with a new name: the
	// user stays signed in.
	rec = serve(router, http.MethodPut, target, global, map[string]any{
		"name":     "Олена Петрівна",
		"disabled": false,
		"grants":   created.Grants,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("rename: status = %d (%s)", rec.Code, rec.Body.String())
	}
	if rec := serve(router, http.MethodGet, "/api/v1/auth/me", userCookie, nil); rec.Code != http.StatusOK {
		t.Fatalf("session after a rename: status = %d, want 200", rec.Code)
	}
	rec = serve(router, http.MethodPut, target, global, map[string]any{
		"grants": []grantJSON{{Role: auth.RoleEconomist, OrganizationID: &pe}},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("update grants: status = %d (%s)", rec.Code, rec.Body.String())
	}
	if rec := serve(router, http.MethodGet, "/api/v1/auth/me", userCookie, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("session after grant change: status = %d, want 401", rec.Code)
	}

	var list struct {
		Users []userJSON `json:"users"`
	}
	rec = serve(router, http.MethodGet, "/api/v1/users", global, nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Users) != 2 {
		t.Fatalf("users = %d, want 2", len(list.Users))
	}

	// A reset by an administrator is temporary again; setting your own
	// password on this page is not.
	rootTarget := "/api/v1/users?user_id=" + jsonNumber(list.Users[0].ID)
	var reset userJSON
	rec = serve(router, http.MethodPut, target, global, map[string]any{"password": "reset by admin 1"})
	if err := json.Unmarshal(rec.Body.Bytes(), &reset); err != nil || !reset.MustChangePassword {
		t.Fatalf("reset: status = %d, body %s; want a temporary password", rec.Code, rec.Body.String())
	}
	rec = serve(router, http.MethodPut, rootTarget, global, map[string]any{"password": "my own new one"})
	if err := json.Unmarshal(rec.Body.Bytes(), &reset); err != nil || reset.MustChangePassword {
		t.Fatalf("own reset: status = %d, body %s; want a permanent password", rec.Code, rec.Body.String())
	}
	global = loginCookie(t, router, "root@example.com", "my own new one")

	// The last administrator of every organization can't lock everyone out.
	if rec := serve(router, http.MethodPut, rootTarget, global, map[string]any{"disabled": true}); rec.Code != http.StatusConflict {
		t.Fatalf("disable last global admin: status = %d, want 409", rec.Code)
	}
	if rec := serve(router, http.MethodPut, rootTarget, global, map[string]any{
		"grants": []grantJSON{{Role: auth.RoleAdmin, OrganizationID: &ze}},
	}); rec.Code != http.StatusConflict {
		t.Fatalf("demote last global admin: status = %d, want 409", rec.Code)
	}
}

func TestAuthPasswordChange(t *testing.T) {
	h, accounts := authTestHandlers(t, nil)
	hash, err := auth.HashPassword("old password 1")
	if err != nil {
		t.Fatal(err)
	}
	id, _ := accounts.CreateUser(context.Background(), storage.UserRow{Email: "eng@example.com", PasswordHash: hash},
		[]auth.Grant{{Role: auth.RoleEngineer, OrganizationID: "ze"}})
	open := func() *http.Cookie {
		token, digest := auth.NewSessionToken()
		_ = accounts.CreateSession(context.Background(), digest, id, time.Now().Add(time.Hour))
		return &http.Cookie{Name: sessionCookieName, Value: token}
	}
	current, other := open(), open()
	router := h.Router()

	rec := serve(router, http.MethodPost, "/api/v1/auth/password", current, authPasswordRequest{CurrentPassword: "nope nope nope", NewPassword: "new password 22"})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("wrong current password: status = %d", rec.Code)
	}
	rec = serve(router, http.MethodPost, "/api/v1/auth/password", current, authPasswordRequest{CurrentPassword: "old password 1", NewPassword: "tiny"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("weak new password: status = %d", rec.Code)
	}
	rec = serve(router, http.MethodPost, "/api/v1/auth/password", current, authPasswordRequest{CurrentPassword: "old password 1", NewPassword: "new password 22"})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("change: status = %d (%s)", rec.Code, rec.Body.String())
	}
	if rec := serve(router, http.MethodGet, "/api/v1/auth/me", current, nil); rec.Code != http.StatusOK {
		t.Fatalf("current session after change: status = %d, want 200", rec.Code)
	}
	if rec := serve(router, http.MethodGet, "/api/v1/auth/me", other, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("other session after change: status = %d, want 401", rec.Code)
	}
	user, _, _ := accounts.UserByID(context.Background(), id)
	if !auth.CheckPassword(user.PasswordHash, "new password 22") {
		t.Fatal("new password not stored")
	}
}

func TestAuthSessionSlidesAndExpires(t *testing.T) {
	h, accounts := authTestHandlers(t, nil)
	cookie := sessionCookie(t, accounts, "eng@example.com", auth.Grant{Role: auth.RoleEngineer, OrganizationID: "ze"})
	router := h.Router()
	start := time.Now()
	h.auth.now = func() time.Time { return start }

	if rec := serve(router, http.MethodGet, "/api/v1/auth/me", cookie, nil); rec.Code != http.StatusOK || len(rec.Result().Cookies()) != 0 {
		t.Fatalf("fresh session: status = %d, cookies = %d; want 200 and no re-issue", rec.Code, len(rec.Result().Cookies()))
	}
	h.auth.now = func() time.Time { return start.Add(sessionTouchEvery + time.Minute) }
	rec := serve(router, http.MethodGet, "/api/v1/auth/me", cookie, nil)
	if rec.Code != http.StatusOK || accounts.touches != 1 {
		t.Fatalf("idle session: status = %d, touches = %d", rec.Code, accounts.touches)
	}
	if sessionFromResponse(t, rec).MaxAge != int(sessionTTL/time.Second) {
		t.Fatal("touched session must re-issue a full-length cookie")
	}

	h.auth.now = func() time.Time { return start.Add(sessionTouchEvery + sessionTTL + time.Hour) }
	if rec := serve(router, http.MethodGet, "/api/v1/auth/me", cookie, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("expired session: status = %d, want 401", rec.Code)
	}
}

func TestCORSAllowsCredentialsForExplicitOrigin(t *testing.T) {
	h := NewHandlers(&mockStore{}, "http://dash.local:5173")
	h.SetAuth(NewAuth(newMemAuthStore(), AuthOptions{}))
	req := httptest.NewRequest(http.MethodOptions, "/api/v1/organization-tariff-schedule?organization_id=ze", nil)
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("preflight: status = %d, want 204 without a session", rec.Code)
	}
	hdr := rec.Header()
	if hdr.Get("Access-Control-Allow-Credentials") != "true" ||
		!strings.Contains(hdr.Get("Access-Control-Allow-Methods"), http.MethodDelete) ||
		!strings.Contains(hdr.Get("Access-Control-Allow-Headers"), csrfHeader) {
		t.Fatalf("preflight headers: %v", hdr)
	}

	wildcard := NewHandlers(&mockStore{}, "*")
	rec = httptest.NewRecorder()
	wildcard.Router().ServeHTTP(rec, httptest.NewRequest(http.MethodOptions, "/api/v1/current", nil))
	if rec.Header().Get("Access-Control-Allow-Credentials") != "" {
		t.Fatal("credentials must never be allowed for the * origin")
	}
}
