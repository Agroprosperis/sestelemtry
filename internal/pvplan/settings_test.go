package pvplan

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSettingsValidate(t *testing.T) {
	for name, s := range map[string]Settings{
		"empty means the defaults": {},
		"the defaults spelled out": DefaultSettings(),
		"a commercial endpoint":    {ForecastURL: "https://customer-api.open-meteo.com/v1/forecast"},
	} {
		if err := s.Validate(); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	for name, s := range map[string]Settings{
		"not http":      {ForecastURL: "ftp://api.open-meteo.com/v1/forecast"},
		"no host":       {PreviousRunsURL: "https:///v1/forecast"},
		"relative":      {N8NWebhookURL: "webhook/96bac28d"},
		"own query":     {ForecastURL: "https://api.open-meteo.com/v1/forecast?apikey=k"},
		"bare question": {N8NWebhookURL: "https://granary.app.n8n.cloud/webhook/x?"},
	} {
		if err := s.Validate(); err == nil {
			t.Errorf("%s: %+v passed", name, s)
		}
	}
}

func TestSameOpenMeteoComparesTheAddressesInForce(t *testing.T) {
	if !SameOpenMeteo(Settings{}, Settings{ForecastURL: DefaultForecastURL, N8NWebhookURL: "https://other.example.com/hook"}) {
		t.Error("an empty address is the default one; the n8n webhook gets no key")
	}
	if SameOpenMeteo(Settings{}, Settings{PreviousRunsURL: "https://customer-api.open-meteo.com/v1/forecast"}) {
		t.Error("a new archive address must count as a change")
	}
}

// fakeSettings is a settings store whose content a test changes.
type fakeSettings struct {
	mu    sync.Mutex
	s     Settings
	key   string
	err   error
	reads int
}

func (f *fakeSettings) Settings(context.Context) (Settings, bool, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads++
	return f.s, f.key != "", true, f.err
}

func (f *fakeSettings) OpenMeteoKey(context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.key, nil
}

func (f *fakeSettings) set(s Settings, key string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.s, f.key = s, key
}

// A save takes effect on the next request once the API says so, and
// the clients aren't rebuilt from the database on every request.
func TestUpstreamsFollowTheSavedSettings(t *testing.T) {
	var (
		mu   sync.Mutex
		seen []string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.URL.Path+" key="+r.URL.Query().Get("apikey"))
		mu.Unlock()
		if strings.HasPrefix(r.URL.Path, "/hook") {
			_, _ = w.Write([]byte(`[{"hour_ending":12,"orientation_idx":1,"planned_kwh":40}]`))
			return
		}
		_, _ = w.Write([]byte(`{"hourly":{"time":[1762171200],"temperature_2m":[5],"global_tilted_irradiance":[300]}}`))
	}))
	t.Cleanup(srv.Close)
	last := func() string {
		mu.Lock()
		defer mu.Unlock()
		return seen[len(seen)-1]
	}

	store := &fakeSettings{}
	store.set(Settings{ForecastURL: srv.URL + "/v1/forecast", N8NWebhookURL: srv.URL + "/hook"}, "k1")
	u := NewUpstreams(store)
	ctx := context.Background()
	plane := Plane{Latitude: 49, Longitude: 27, TiltDeg: 30}

	if _, err := u.Forecast(ctx, plane); err != nil {
		t.Fatalf("Forecast: %v", err)
	}
	if got := last(); got != "/v1/forecast key=k1" {
		t.Fatalf("asked %q, want the saved address with the saved key", got)
	}
	hours, err := u.DayHourly(ctx, "AB", time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC))
	if err != nil || hours[11] != 40 {
		t.Fatalf("DayHourly = %v, %v", hours, err)
	}
	if got := last(); got != "/hook key=" {
		t.Fatalf("asked %q, want the saved webhook without the Open-Meteo key", got)
	}
	if store.reads != 1 {
		t.Errorf("settings read %d times for two requests, want once", store.reads)
	}

	store.set(Settings{ForecastURL: srv.URL + "/v2/forecast", N8NWebhookURL: srv.URL + "/hook"}, "k2")
	u.Forget()
	if _, err := u.Forecast(ctx, plane); err != nil {
		t.Fatalf("Forecast after the save: %v", err)
	}
	if got := last(); got != "/v2/forecast key=k2" {
		t.Fatalf("asked %q after the save, want the new address and key", got)
	}
}

// Without its settings the planner has nothing to call: it fails
// rather than fall back to the public defaults behind the operator's
// back.
func TestUpstreamsFailWithoutTheirSettings(t *testing.T) {
	u := NewUpstreams(&fakeSettings{err: errors.New("connection refused")})
	if _, err := u.Forecast(context.Background(), Plane{}); err == nil || !strings.Contains(err.Error(), "налаштування прогнозу СЕС") {
		t.Fatalf("err = %v, want the settings read failure", err)
	}
	if _, _, err := u.DayTotal(context.Background(), "AB", time.Now()); err == nil {
		t.Fatal("the n8n flow was read without its settings")
	}
}
