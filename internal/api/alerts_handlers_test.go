package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nesh/sestelemetry/internal/alerts"
)

func savedSettings() alerts.Settings {
	s := alerts.DefaultSettings()
	s.Enabled = true
	s.SMTP.Host = "smtp.example.com"
	s.SMTP.From = "alerts@example.com"
	s.Recipients = []string{"ops@example.com"}
	return s
}

// TestAlertSettingsNeverReturnsPassword is the security-critical
// assertion of this endpoint: the response may say a password exists,
// but must never carry it.
func TestAlertSettingsNeverReturnsPassword(t *testing.T) {
	saved := savedSettings()
	store := &mockStore{
		alertSettings:    &saved,
		alertPasswordSet: true,
		alertPassword:    "sup3r-s3cret",
	}
	h := NewHandlers(store, "*")
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/alert-settings", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 got %d body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "sup3r-s3cret") {
		t.Fatalf("password leaked into the response: %s", rec.Body.String())
	}
	var got AlertSettingsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.SMTPPasswordConfigured || !got.Saved {
		t.Fatalf("flags = %+v", got)
	}
	if got.SMTP.Host != "smtp.example.com" || len(got.Recipients) != 1 {
		t.Fatalf("payload = %+v", got.Settings)
	}
	// Durations must be readable strings, not nanosecond integers.
	if !strings.Contains(rec.Body.String(), `"stale_after":"10m0s"`) {
		t.Fatalf("durations must serialize as strings: %s", rec.Body.String())
	}
}

// TestAlertSettingsFallsBackToConfig keeps a YAML-configured deployment
// from seeing an empty form (and then saving those blanks over a working
// setup).
func TestAlertSettingsFallsBackToConfig(t *testing.T) {
	h := NewHandlers(&mockStore{}, "*")
	fallback := savedSettings()
	fallback.SMTP.Host = "mail.from-yaml.example"
	h.SetAlertFallback(fallback, "env-password")

	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/alert-settings", nil))

	var got AlertSettingsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Saved {
		t.Fatal("saved must be false when nothing is stored")
	}
	if got.SMTP.Host != "mail.from-yaml.example" {
		t.Fatalf("host = %q", got.SMTP.Host)
	}
	if !got.SMTPPasswordConfigured {
		t.Fatal("an SMTP_PASSWORD in the environment counts as configured")
	}
	if strings.Contains(rec.Body.String(), "env-password") {
		t.Fatalf("password leaked: %s", rec.Body.String())
	}
}

func TestAlertSettingsDefaultsWithoutFallback(t *testing.T) {
	h := NewHandlers(&mockStore{}, "*")
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/alert-settings", nil))

	var got AlertSettingsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Enabled {
		t.Fatal("an unconfigured deployment must not report alerts as on")
	}
	if got.SMTP.Port != 587 || got.SMTP.TLS != alerts.TLSStartTLS {
		t.Fatalf("smtp defaults = %+v", got.SMTP)
	}
}

func putAlertSettings(t *testing.T, h *Handlers, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/alert-settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	return rec
}

const validSettingsBody = `{
	"enabled": true,
	"check_interval": "1m",
	"stale_after": "10m",
	"repeat_interval": "6h",
	"notify_recovery": true,
	"smtp": {"host": "smtp.example.com", "port": 587, "tls": "starttls", "username": "", "from": "alerts@example.com", "timeout": "20s"},
	"recipients": ["ops@example.com"]
}`

func TestPutAlertSettingsStoresPayload(t *testing.T) {
	store := &mockStore{}
	h := NewHandlers(store, "*")
	rec := putAlertSettings(t, h, validSettingsBody)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("want 204 got %d body=%s", rec.Code, rec.Body.String())
	}
	if store.alertSettingsWrite == nil {
		t.Fatal("nothing was written")
	}
	if store.alertSettingsWrite.StaleAfter.Duration() != 10*time.Minute {
		t.Fatalf("stale_after = %v", store.alertSettingsWrite.StaleAfter)
	}
}

// TestPutAlertSettingsPasswordSemantics locks in the three-way contract
// the dashboard depends on: editing recipients must not require
// retyping the mail password.
func TestPutAlertSettingsPasswordSemantics(t *testing.T) {
	cases := map[string]struct {
		body       string
		wantNil    bool
		wantString string
	}{
		"omitted keeps stored": {body: validSettingsBody, wantNil: true},
		"empty clears": {
			body:       strings.Replace(validSettingsBody, `"recipients"`, `"smtp_password": "", "recipients"`, 1),
			wantString: "",
		},
		"value replaces": {
			body:       strings.Replace(validSettingsBody, `"recipients"`, `"smtp_password": "  hunter2\n  ", "recipients"`, 1),
			wantString: "hunter2",
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			store := &mockStore{}
			h := NewHandlers(store, "*")
			if rec := putAlertSettings(t, h, c.body); rec.Code != http.StatusNoContent {
				t.Fatalf("want 204 got %d body=%s", rec.Code, rec.Body.String())
			}
			if !store.alertPasswordWriteSet {
				t.Fatal("upsert was not called")
			}
			got := store.alertPasswordWrite
			if c.wantNil {
				if got != nil {
					t.Fatalf("password = %q, want nil (keep stored)", *got)
				}
				return
			}
			if got == nil {
				t.Fatal("password = nil, want a value")
			}
			if *got != c.wantString {
				t.Fatalf("password = %q, want %q", *got, c.wantString)
			}
		})
	}
}

func TestPutAlertSettingsRejectsBadPayload(t *testing.T) {
	cases := map[string]string{
		"unknown field":     strings.Replace(validSettingsBody, `"enabled"`, `"nope": 1, "enabled"`, 1),
		"numeric duration":  strings.Replace(validSettingsBody, `"stale_after": "10m"`, `"stale_after": 600`, 1),
		"stale below check": strings.Replace(validSettingsBody, `"stale_after": "10m"`, `"stale_after": "30s"`, 1),
		"bad recipient":     strings.Replace(validSettingsBody, `"ops@example.com"`, `"ops at example.com"`, 1),
		"enabled no host":   strings.Replace(validSettingsBody, `"host": "smtp.example.com"`, `"host": ""`, 1),
		"broken json":       `{`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			store := &mockStore{}
			h := NewHandlers(store, "*")
			if rec := putAlertSettings(t, h, body); rec.Code != http.StatusBadRequest {
				t.Fatalf("want 400 got %d body=%s", rec.Code, rec.Body.String())
			}
			if store.alertSettingsWrite != nil {
				t.Fatal("a rejected payload must not be stored")
			}
		})
	}
}

func TestAlertSettingsRejectsOtherMethods(t *testing.T) {
	h := NewHandlers(&mockStore{}, "*")
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/v1/alert-settings", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("want 405 got %d", rec.Code)
	}
}

func TestOrganizationAlertSettingsRoundTrip(t *testing.T) {
	store := &mockStore{}
	h := NewHandlers(store, "*")

	req := httptest.NewRequest(http.MethodPut,
		"/api/v1/organization-alert-settings?organization_id=ke",
		strings.NewReader(`{"enabled": true, "recipients": [" ke@example.com ", "", "ke@example.com"]}`))
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("want 204 got %d body=%s", rec.Code, rec.Body.String())
	}
	if store.orgAlertLastOrg != "ke" {
		t.Fatalf("organization = %q", store.orgAlertLastOrg)
	}
	if len(store.orgAlertLastWrite.Recipients) != 1 || store.orgAlertLastWrite.Recipients[0] != "ke@example.com" {
		t.Fatalf("recipients must be trimmed and deduped: %#v", store.orgAlertLastWrite.Recipients)
	}

	rec = httptest.NewRecorder()
	h.Router().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/organization-alert-settings", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 got %d", rec.Code)
	}
	var got OrgAlertSettingsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if entry, ok := got.Organizations["ke"]; !ok || !entry.Enabled {
		t.Fatalf("organizations = %#v", got.Organizations)
	}
}

func TestOrganizationAlertSettingsEmptyMap(t *testing.T) {
	h := NewHandlers(&mockStore{}, "*")
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/organization-alert-settings", nil))
	// An explicit empty object keeps the frontend from special-casing
	// null before it can index the map.
	if !strings.Contains(rec.Body.String(), `"organizations":{}`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestPutOrganizationAlertSettingsRequiresOrgID(t *testing.T) {
	h := NewHandlers(&mockStore{}, "*")
	req := httptest.NewRequest(http.MethodPut, "/api/v1/organization-alert-settings",
		strings.NewReader(`{"enabled": true}`))
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400 got %d", rec.Code)
	}
}

func TestTestEmailRequiresRecipients(t *testing.T) {
	saved := savedSettings()
	saved.Recipients = nil
	h := NewHandlers(&mockStore{alertSettings: &saved}, "*")
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/alert-settings/test-email", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400 got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "no recipients") {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestTestEmailRejectsNonPost(t *testing.T) {
	h := NewHandlers(&mockStore{}, "*")
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/alert-settings/test-email", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("want 405 got %d", rec.Code)
	}
}

// TestTestEmailReportsRelayFailure surfaces the relay's own complaint,
// which is what the operator needs to fix the settings they just typed.
func TestTestEmailReportsRelayFailure(t *testing.T) {
	saved := savedSettings()
	// Port 1 on localhost refuses instantly; no network required.
	saved.SMTP.Host = "127.0.0.1"
	saved.SMTP.Port = 1
	saved.SMTP.TLS = alerts.TLSNone
	saved.SMTP.Timeout = alerts.Duration(time.Second)
	h := NewHandlers(&mockStore{alertSettings: &saved}, "*")
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/alert-settings/test-email", nil))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("want 502 got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestTestEmailUsesOrganizationOverride proves the button tests the
// address that would really receive the alert, not the default list.
func TestTestEmailUsesOrganizationOverride(t *testing.T) {
	saved := savedSettings()
	saved.SMTP.Host = "127.0.0.1"
	saved.SMTP.Port = 1
	saved.SMTP.TLS = alerts.TLSNone
	saved.SMTP.Timeout = alerts.Duration(time.Second)
	// No default list at all: only the override can supply an address,
	// so reaching the relay proves it was used.
	saved.Recipients = nil
	store := &mockStore{
		alertSettings: &saved,
		orgAlertSettings: map[string]alerts.OrgSettings{
			"ke": {Enabled: true, Recipients: []string{"ke@example.com"}},
		},
	}
	h := NewHandlers(store, "*")
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec,
		httptest.NewRequest(http.MethodPost, "/api/v1/alert-settings/test-email?organization_id=ke", nil))
	// Delivery still fails against the dead port, but resolving the
	// recipient list had to succeed to get that far.
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("want 502 got %d body=%s", rec.Code, rec.Body.String())
	}
}
