package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nesh/sestelemetry/internal/auth"
	"github.com/nesh/sestelemetry/internal/fxrates"
	"github.com/nesh/sestelemetry/internal/storage"
)

// fakeFxStore keeps the rates and their settings in memory.
type fakeFxStore struct {
	settings fxrates.Settings
	password string
	saved    bool
	rates    map[string]map[string]float64
}

func (f *fakeFxStore) Settings(context.Context) (fxrates.Settings, bool, bool, error) {
	return f.settings, f.password != "", f.saved, nil
}

func (f *fakeFxStore) Password(context.Context) (string, error) { return f.password, nil }

func (f *fakeFxStore) SaveSettings(_ context.Context, s fxrates.Settings, password *string) error {
	f.settings, f.saved = s, true
	if password != nil {
		f.password = *password
	}
	return nil
}

func (f *fakeFxStore) Rates(_ context.Context, currency string) (map[string]float64, error) {
	return f.rates[currency], nil
}

func (f *fakeFxStore) Coverage(context.Context) ([]storage.FxCoverage, error) {
	var out []storage.FxCoverage
	for cur, days := range f.rates {
		c := storage.FxCoverage{Currency: cur, Days: len(days)}
		for d := range days {
			day, _ := time.Parse("2006-01-02", d)
			if c.FirstDay.IsZero() || day.Before(c.FirstDay) {
				c.FirstDay = day
			}
			if day.After(c.LastDay) {
				c.LastDay = day
			}
		}
		out = append(out, c)
	}
	return out, nil
}

type fakeFxSync struct{ woken int }

func (f *fakeFxSync) Wake()                  { f.woken++ }
func (f *fakeFxSync) Status() fxrates.Status { return fxrates.Status{Configured: true} }

func TestFxSettingsKeepThePasswordOnTheServer(t *testing.T) {
	h, accounts := authTestHandlers(t, &mockStore{})
	fx, sync := &fakeFxStore{}, &fakeFxSync{}
	h.SetFxRates(fx, sync)
	router := h.Router()
	cookie := sessionCookie(t, accounts, "root@example.com", auth.Grant{Role: auth.RoleAdmin})
	put := func(body map[string]any) (int, string) {
		t.Helper()
		rec := serve(router, http.MethodPut, "/api/v1/fx-settings", cookie, body)
		return rec.Code, rec.Body.String()
	}
	get := func() (FxSettingsResponse, string) {
		t.Helper()
		rec := serve(router, http.MethodGet, "/api/v1/fx-settings", cookie, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET: %d %s", rec.Code, rec.Body.String())
		}
		var out FxSettingsResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out, rec.Body.String()
	}

	got, _ := get()
	if got.Saved || got.PasswordConfigured || got.DefaultURL != fxrates.DefaultURL || len(got.Coverage) != len(fxrates.Currencies) {
		t.Fatalf("before the first save = %+v", got)
	}

	if code, body := put(map[string]any{"url": "", "username": " BankRates ", "password": " s3cret\n"}); code != http.StatusNoContent {
		t.Fatalf("save: %d %s", code, body)
	}
	if fx.password != "s3cret" || fx.settings.Username != "BankRates" {
		t.Errorf("stored %+v / %q, want both trimmed", fx.settings, fx.password)
	}
	if sync.woken != 1 {
		t.Errorf("a save woke the sync %d times, want 1", sync.woken)
	}
	got, raw := get()
	if !got.Saved || !got.PasswordConfigured {
		t.Errorf("after save = %+v", got)
	}
	if strings.Contains(raw, "s3cret") {
		t.Fatalf("GET leaked the password: %s", raw)
	}

	// The login can change without retyping the password, the address can't.
	if code, body := put(map[string]any{"url": "", "username": "Other"}); code != http.StatusNoContent || fx.password != "s3cret" {
		t.Errorf("new login: %d %s, password %q", code, body, fx.password)
	}
	if code, body := put(map[string]any{"url": "https://elsewhere.example.com/hs/apbank", "username": "Other"}); code != http.StatusBadRequest || !strings.Contains(body, "адресу сервісу змінено") {
		t.Errorf("new address with the stored password: %d %s", code, body)
	}
	if code, _ := put(map[string]any{"url": "web1c/no-scheme", "username": "x"}); code != http.StatusBadRequest {
		t.Errorf("an address without a scheme: %d", code)
	}

	// Only an administrator of every organization opens the page.
	local := sessionCookie(t, accounts, "ze@example.com", auth.Grant{Role: auth.RoleAdmin, OrganizationID: "ze"})
	if rec := serve(router, http.MethodGet, "/api/v1/fx-settings", local, nil); rec.Code != http.StatusForbidden {
		t.Errorf("an org admin: %d", rec.Code)
	}
}

func TestFxSettingsTestAsksTheERP(t *testing.T) {
	erp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, pass, _ := r.BasicAuth(); pass != "good" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		fmt.Fprint(w, `{"success":true,"data":[{"currency":"USD","exchange_rate":45.35},{"currency":"EUR","exchange_rate":50.78}]}`)
	}))
	defer erp.Close()

	h, accounts := authTestHandlers(t, &mockStore{})
	fx := &fakeFxStore{settings: fxrates.Settings{URL: erp.URL, Username: "BankRates"}, password: "good", saved: true}
	h.SetFxRates(fx, &fakeFxSync{})
	router := h.Router()
	cookie := sessionCookie(t, accounts, "root@example.com", auth.Grant{Role: auth.RoleAdmin})
	test := func(body map[string]any) (int, FxSettingsTestResponse, string) {
		t.Helper()
		rec := serve(router, http.MethodPost, "/api/v1/fx-settings/test", cookie, body)
		var out FxSettingsTestResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out, rec.Body.String()
	}

	if code, out, raw := test(map[string]any{"url": erp.URL, "username": "BankRates"}); code != http.StatusOK || !out.OK || out.Rates["USD"] != 45.35 {
		t.Errorf("stored password: %d %s", code, raw)
	}
	if code, out, raw := test(map[string]any{"url": erp.URL, "username": "BankRates", "password": "bad"}); code != http.StatusOK || out.OK || out.Error != "Невірний логін або пароль" {
		t.Errorf("typed wrong password: %d %s", code, raw)
	}
	if code, _, raw := test(map[string]any{"url": "https://elsewhere.example.com/hs/apbank", "username": "BankRates"}); code != http.StatusBadRequest {
		t.Errorf("stored password to another address: %d %s", code, raw)
	}
}

func TestFxRatesBorrowTheNearestDay(t *testing.T) {
	h, accounts := authTestHandlers(t, &mockStore{})
	h.SetFxRates(&fakeFxStore{rates: map[string]map[string]float64{"USD": {"2026-10-01": 41, "2026-10-03": 42}}}, &fakeFxSync{})
	router := h.Router()
	cookie := sessionCookie(t, accounts, "eco@example.com", auth.Grant{Role: auth.RoleEconomist, OrganizationID: "ze"})

	rec := serve(router, http.MethodGet, "/api/v1/fx-rates?currency=usd&from=2026-10-01&to=2026-10-03", cookie, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET: %d %s", rec.Code, rec.Body.String())
	}
	var out FxRatesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	want := []FxRateDay{{"2026-10-01", 41, false}, {"2026-10-02", 41, true}, {"2026-10-03", 42, false}}
	if out.Currency != "USD" || fmt.Sprint(out.Days) != fmt.Sprint(want) {
		t.Errorf("got %+v, want %+v", out, want)
	}
	if rec := serve(router, http.MethodGet, "/api/v1/fx-rates?currency=UAH&from=2026-10-01&to=2026-10-03", cookie, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("UAH has no rates to serve: %d", rec.Code)
	}
	if rec := serve(router, http.MethodGet, "/api/v1/fx-rates?currency=EUR&from=2026-10-01&to=2026-10-03", cookie, nil); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("a currency without rates: %d", rec.Code)
	}
}
