package pvplan

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Settings say where the plan's upstreams are: Open-Meteo for the
// model, the n8n flow for the stations still on it. Administrators of
// every organization edit them on the «Прогноз СЕС» page; an empty
// address stands for the default. The Open-Meteo key is stored apart
// and never read back.
type Settings struct {
	ForecastURL     string `json:"forecast_url"`
	PreviousRunsURL string `json:"previous_runs_url"`
	N8NWebhookURL   string `json:"n8n_webhook_url"`
}

// DefaultSettings are the addresses empty fields stand for.
func DefaultSettings() Settings {
	return Settings{
		ForecastURL:     DefaultForecastURL,
		PreviousRunsURL: DefaultPreviousRunsURL,
		N8NWebhookURL:   DefaultWebhookURL,
	}
}

// Normalize trims the addresses.
func (s *Settings) Normalize() {
	s.ForecastURL = strings.TrimSpace(s.ForecastURL)
	s.PreviousRunsURL = strings.TrimSpace(s.PreviousRunsURL)
	s.N8NWebhookURL = strings.TrimSpace(s.N8NWebhookURL)
}

// Validate reports the first address an administrator has to fix, in
// Ukrainian: the page shows it as is. The clients append the query
// themselves, so an address can't carry one.
func (s Settings) Validate() error {
	for _, f := range []struct{ label, value string }{
		{"адреса прогнозу Open-Meteo", s.ForecastURL},
		{"адреса архіву прогнозів Open-Meteo", s.PreviousRunsURL},
		{"адреса вебхука n8n", s.N8NWebhookURL},
	} {
		if f.value == "" {
			continue
		}
		u, err := url.Parse(f.value)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			return fmt.Errorf("%s має бути повною адресою http:// або https://", f.label)
		}
		if strings.ContainsAny(f.value, "?#") {
			return fmt.Errorf("%s — без параметрів після «?»: запит і ключ консоль додає сама", f.label)
		}
	}
	return nil
}

func (s Settings) withDefaults() Settings {
	d := DefaultSettings()
	if s.ForecastURL == "" {
		s.ForecastURL = d.ForecastURL
	}
	if s.PreviousRunsURL == "" {
		s.PreviousRunsURL = d.PreviousRunsURL
	}
	if s.N8NWebhookURL == "" {
		s.N8NWebhookURL = d.N8NWebhookURL
	}
	return s
}

// SameOpenMeteo reports whether a and b send Open-Meteo requests, and
// so the stored key, to the same addresses.
func SameOpenMeteo(a, b Settings) bool {
	a, b = a.withDefaults(), b.withDefaults()
	return a.ForecastURL == b.ForecastURL && a.PreviousRunsURL == b.PreviousRunsURL
}

// SettingsStore reads what the settings page saved.
type SettingsStore interface {
	// Settings returns the saved settings, whether an Open-Meteo key is
	// stored, and whether anything was saved at all.
	Settings(ctx context.Context) (s Settings, keySet, saved bool, err error)
	// OpenMeteoKey returns the stored key: the only read of it.
	OpenMeteoKey(ctx context.Context) (string, error)
}

// settingsTTL is how long clients serve without rereading the settings.
// A save through the API takes effect at once (Forget); this bounds a
// change made any other way.
const settingsTTL = 30 * time.Second

// Upstreams are the plan's upstream clients as the saved settings
// define them: the planner reads Open-Meteo through it, the API the
// n8n flow.
type Upstreams struct {
	store SettingsStore

	mu sync.Mutex
	// live is reused for settingsTTL. revision grows on every Forget, so
	// a read that raced a save doesn't cache the clients it replaced.
	live     *upstreamClients
	liveAt   time.Time
	revision int
}

type upstreamClients struct {
	settings Settings
	key      string
	weather  *WeatherClient
	n8n      *Client
}

// NewUpstreams builds clients from what store holds.
func NewUpstreams(store SettingsStore) *Upstreams {
	return &Upstreams{store: store}
}

// Forget drops the clients after a save, so the next request is built
// from the new settings.
func (u *Upstreams) Forget() {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.revision++
	u.live = nil
}

func (u *Upstreams) clients(ctx context.Context) (*upstreamClients, error) {
	u.mu.Lock()
	if u.live != nil && time.Since(u.liveAt) < settingsTTL {
		live := u.live
		u.mu.Unlock()
		return live, nil
	}
	rev, prev := u.revision, u.live
	u.mu.Unlock()

	s, keySet, _, err := u.store.Settings(ctx)
	if err != nil {
		return nil, fmt.Errorf("не вдалося прочитати налаштування прогнозу СЕС: %w", err)
	}
	key := ""
	if keySet {
		if key, err = u.store.OpenMeteoKey(ctx); err != nil {
			return nil, fmt.Errorf("не вдалося прочитати ключ Open-Meteo: %w", err)
		}
	}
	live := prev
	if live == nil || live.settings != s || live.key != key {
		live = &upstreamClients{
			settings: s,
			key:      key,
			weather:  NewWeatherClient(s.ForecastURL, s.PreviousRunsURL, key, nil),
			n8n:      NewClient(s.N8NWebhookURL, nil),
		}
	}
	u.mu.Lock()
	if u.revision == rev {
		u.live, u.liveAt = live, time.Now()
	}
	u.mu.Unlock()
	return live, nil
}

// Forecast is WeatherClient.Forecast on the saved settings.
func (u *Upstreams) Forecast(ctx context.Context, p Plane) ([]HourInput, error) {
	c, err := u.clients(ctx)
	if err != nil {
		return nil, err
	}
	return c.weather.Forecast(ctx, p)
}

// PreviousDay1 is WeatherClient.PreviousDay1 on the saved settings.
func (u *Upstreams) PreviousDay1(ctx context.Context, p Plane, fromDay, toDay string) ([]HourInput, error) {
	c, err := u.clients(ctx)
	if err != nil {
		return nil, err
	}
	return c.weather.PreviousDay1(ctx, p, fromDay, toDay)
}

// DayHourly is Client.DayHourly on the saved webhook.
func (u *Upstreams) DayHourly(ctx context.Context, elevatorCode string, day time.Time) (map[int]float64, error) {
	c, err := u.clients(ctx)
	if err != nil {
		return nil, err
	}
	return c.n8n.DayHourly(ctx, elevatorCode, day)
}

// DayTotal is Client.DayTotal on the saved webhook.
func (u *Upstreams) DayTotal(ctx context.Context, elevatorCode string, day time.Time) (float64, bool, error) {
	c, err := u.clients(ctx)
	if err != nil {
		return 0, false, err
	}
	return c.n8n.DayTotal(ctx, elevatorCode, day)
}
