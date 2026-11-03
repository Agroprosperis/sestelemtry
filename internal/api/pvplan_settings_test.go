package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nesh/sestelemetry/internal/auth"
	"github.com/nesh/sestelemetry/internal/pvplan"
)

func pvSettingsBody(s pvplan.Settings, key *string) map[string]any {
	body := map[string]any{
		"forecast_url":      s.ForecastURL,
		"previous_runs_url": s.PreviousRunsURL,
		"n8n_webhook_url":   s.N8NWebhookURL,
	}
	if key != nil {
		body["api_key"] = *key
	}
	return body
}

// pvSettingsOf reads the mock's settings the way the upstreams read the
// database in production.
type pvSettingsOf struct{ m *mockStore }

func (s pvSettingsOf) Settings(ctx context.Context) (pvplan.Settings, bool, bool, error) {
	return s.m.PvPlanSettings(ctx)
}

func (s pvSettingsOf) OpenMeteoKey(ctx context.Context) (string, error) {
	return s.m.PvPlanOpenMeteoKey(ctx)
}

func TestPvPlanSettingsKeepTheKeyOnTheServer(t *testing.T) {
	store := &mockStore{}
	h, accounts := authTestHandlers(t, store)
	router := h.Router()
	cookie := sessionCookie(t, accounts, "root@example.com", auth.Grant{Role: auth.RoleAdmin})
	put := func(s pvplan.Settings, key *string) (int, string) {
		t.Helper()
		rec := serve(router, http.MethodPut, "/api/v1/pv-plan-settings", cookie, pvSettingsBody(s, key))
		return rec.Code, rec.Body.String()
	}
	get := func() (PvPlanSettingsResponse, string) {
		t.Helper()
		rec := serve(router, http.MethodGet, "/api/v1/pv-plan-settings", cookie, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET: %d %s", rec.Code, rec.Body.String())
		}
		var out PvPlanSettingsResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out, rec.Body.String()
	}

	got, _ := get()
	if got.Saved || got.APIKeyConfigured || got.Settings != (pvplan.Settings{}) || got.Defaults != pvplan.DefaultSettings() {
		t.Fatalf("before the first save = %+v, want empty fields and the defaults", got)
	}

	commercial := pvplan.Settings{
		ForecastURL:     " https://customer-api.open-meteo.com/v1/forecast ",
		PreviousRunsURL: "https://customer-previous-runs-api.open-meteo.com/v1/forecast",
	}
	if code, body := put(commercial, strPtr(" om-secret\n")); code != http.StatusNoContent {
		t.Fatalf("save: %d %s", code, body)
	}
	if store.pvKey != "om-secret" || store.pvSettings.ForecastURL != "https://customer-api.open-meteo.com/v1/forecast" {
		t.Errorf("stored %+v with key %q, want both trimmed", store.pvSettings, store.pvKey)
	}
	got, raw := get()
	if !got.Saved || !got.APIKeyConfigured {
		t.Errorf("after save = %+v", got)
	}
	if strings.Contains(raw, "om-secret") {
		t.Fatalf("GET leaked the key: %s", raw)
	}

	// The webhook gets no key, so pointing it elsewhere keeps the key.
	edited := *store.pvSettings
	edited.N8NWebhookURL = "https://n8n.example.com/webhook/plan"
	if code, body := put(edited, nil); code != http.StatusNoContent || store.pvKey != "om-secret" {
		t.Errorf("new webhook without the key: %d %s, key %q", code, body, store.pvKey)
	}
	// The stored key never follows Open-Meteo to another address.
	moved := edited
	moved.ForecastURL = "https://elsewhere.example.com/v1/forecast"
	if code, body := put(moved, nil); code != http.StatusBadRequest || !strings.Contains(body, "адресу Open-Meteo змінено") {
		t.Errorf("new address with the stored key: %d %s", code, body)
	}
	if code, body := put(pvplan.Settings{}, strPtr("")); code != http.StatusNoContent || store.pvKey != "" {
		t.Errorf("back to the free API without a key: %d %s, key %q", code, body, store.pvKey)
	}

	for name, body := range map[string]map[string]any{
		"scheme":  pvSettingsBody(pvplan.Settings{ForecastURL: "ftp://api.open-meteo.com/v1/forecast"}, nil),
		"query":   pvSettingsBody(pvplan.Settings{PreviousRunsURL: "https://previous-runs-api.open-meteo.com/v1/forecast?apikey=x"}, nil),
		"unknown": {"forecast_url": "", "timeout": 5},
	} {
		if rec := serve(router, http.MethodPut, "/api/v1/pv-plan-settings", cookie, body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d", name, rec.Code)
		}
	}
}

// A save repoints the upstreams at once: the next n8n read goes to the
// new webhook, with no restart and no wait for the settings cache.
func TestPvPlanSettingsSaveRepointsTheUpstreams(t *testing.T) {
	var (
		mu    sync.Mutex
		paths []string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		_, _ = w.Write([]byte(`[{"hour_ending":12,"orientation_idx":1,"planned_kwh":40}]`))
	}))
	t.Cleanup(srv.Close)

	store := &mockStore{pvSettings: &pvplan.Settings{N8NWebhookURL: srv.URL + "/old"}}
	h, accounts := authTestHandlers(t, store)
	h.SetPvPlanUpstreams(pvplan.NewUpstreams(pvSettingsOf{store}))
	router := h.Router()
	cookie := sessionCookie(t, accounts, "root@example.com", auth.Grant{Role: auth.RoleAdmin})
	day := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)

	if _, err := h.pvPlan.DayHourly(context.Background(), "AB", day); err != nil {
		t.Fatalf("DayHourly: %v", err)
	}
	if rec := serve(router, http.MethodPut, "/api/v1/pv-plan-settings", cookie,
		pvSettingsBody(pvplan.Settings{N8NWebhookURL: srv.URL + "/new"}, nil)); rec.Code != http.StatusNoContent {
		t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
	}
	if _, err := h.pvPlan.DayHourly(context.Background(), "AB", day); err != nil {
		t.Fatalf("DayHourly after the save: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(paths) != 2 || paths[0] != "/old" || paths[1] != "/new" {
		t.Fatalf("asked %v, want /old then /new", paths)
	}
}

func TestPvPlanSettingsConnectionTest(t *testing.T) {
	var (
		mu   sync.Mutex
		keys = map[string]string{}
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.URL.Query().Get("apikey")
		mu.Lock()
		keys[r.URL.Path] = key
		mu.Unlock()
		switch {
		case r.URL.Path == "/hook":
			_, _ = w.Write([]byte(`[{"hour_ending":12,"orientation_idx":1,"planned_kwh":40},{"hour_ending":13,"orientation_idx":1,"planned_kwh":35}]`))
		case key == "om-wrong":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":true,"reason":"Invalid API key"}`))
		case r.URL.Path == "/previous":
			_, _ = w.Write([]byte(`{"hourly":{"time":[1762171200],"temperature_2m_previous_day1":[3],"global_tilted_irradiance_previous_day1":[120]}}`))
		default:
			_, _ = w.Write([]byte(`{"hourly":{"time":[1762171200,1762174800],"temperature_2m":[5,6],"global_tilted_irradiance":[300,310]}}`))
		}
	}))
	t.Cleanup(srv.Close)
	saved := pvplan.Settings{ForecastURL: srv.URL + "/forecast", PreviousRunsURL: srv.URL + "/previous", N8NWebhookURL: srv.URL + "/hook"}
	store := &mockStore{pvSettings: &saved, pvKey: "om-stored"}
	h, accounts := authTestHandlers(t, store)
	router := h.Router()
	cookie := sessionCookie(t, accounts, "root@example.com", auth.Grant{Role: auth.RoleAdmin})
	test := func(s pvplan.Settings, key *string) (int, PvPlanSettingsTestResponse, string) {
		t.Helper()
		rec := serve(router, http.MethodPost, "/api/v1/pv-plan-settings/test", cookie, pvSettingsBody(s, key))
		var out PvPlanSettingsTestResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out, rec.Body.String()
	}

	code, got, body := test(saved, nil)
	if code != http.StatusOK || !got.Forecast.OK || got.Forecast.Hours != 2 || !got.PreviousRuns.OK || got.PreviousRuns.Hours != 1 {
		t.Fatalf("test on the saved settings: %d %s", code, body)
	}
	if got.N8N == nil || !got.N8N.OK || got.N8N.Hours != 2 || got.N8NOrganizationID != "ze" {
		t.Fatalf("n8n check = %+v for %q, want the first served station with a flow code", got.N8N, got.N8NOrganizationID)
	}
	mu.Lock()
	if keys["/forecast"] != "om-stored" || keys["/previous"] != "om-stored" || keys["/hook"] != "" {
		t.Errorf("keys sent = %v, want the stored one to Open-Meteo only", keys)
	}
	mu.Unlock()
	if store.pvSettingsWrites != 0 {
		t.Error("the connection test saved the settings")
	}

	moved := saved
	moved.ForecastURL = srv.URL + "/elsewhere"
	if code, _, body := test(moved, nil); code != http.StatusBadRequest || !strings.Contains(body, "адресу Open-Meteo змінено") {
		t.Errorf("stored key to another address: %d %s", code, body)
	}
	code, got, body = test(saved, strPtr("om-wrong"))
	if code != http.StatusOK || got.Forecast.OK || got.Forecast.Error != "Open-Meteo відхилив запит: Invalid API key" || !got.N8N.OK {
		t.Errorf("a rejected key: %d %s", code, body)
	}
}
