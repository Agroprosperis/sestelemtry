// Package fxrates reads the exchange rates the economics page converts
// UAH into: the «apbank» HTTP service of the group's ERP. Economics
// stay stored in UAH; a background sync keeps one rate per civil day in
// Postgres, and the read paths divide each day's money by that day's rate.
package fxrates

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultURL is the production ERP base's rates service; an empty URL in
// the settings stands for it.
const DefaultURL = "https://web1c.agroprosperis.com/APGroup/hs/ExchangeRates/apbank"

// Currencies are the currencies the economics page can show besides UAH.
var Currencies = []string{"USD", "EUR"}

// IsCurrency reports whether c is one of Currencies.
func IsCurrency(c string) bool {
	for _, v := range Currencies {
		if v == c {
			return true
		}
	}
	return false
}

// Settings is where the ERP service is and which account it lets in. The
// password is stored and passed separately so every read of it is a call
// a reviewer can grep.
type Settings struct {
	URL      string `json:"url"`
	Username string `json:"username"`
}

// Normalize trims what a paste tends to carry along.
func (s *Settings) Normalize() {
	s.URL = strings.TrimSpace(s.URL)
	s.Username = strings.TrimSpace(s.Username)
}

// Validate refuses an address the client could not call.
func (s Settings) Validate() error {
	if s.URL == "" {
		return nil
	}
	u, err := url.Parse(s.URL)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return fmt.Errorf("адреса сервісу курсів має бути повною адресою http(s)://…")
	}
	if u.RawQuery != "" {
		return fmt.Errorf("адресу сервісу курсів вказують без параметрів: дату й валюти додає сервер")
	}
	return nil
}

// Endpoint is the address requests go to.
func (s Settings) Endpoint() string {
	if s.URL == "" {
		return DefaultURL
	}
	return s.URL
}

// Configured reports whether there is enough to call the service.
func (s Settings) Configured(passwordSet bool) bool {
	return s.Username != "" && passwordSet
}

// Errors the settings page turns into plain words.
var (
	ErrUnauthorized = errors.New("ERP не прийняла логін або пароль")
	ErrNotFound     = errors.New("за цією адресою немає сервісу курсів")
	ErrUnreachable  = errors.New("немає з'єднання з сервером ERP")
)

// retryDelays are the pauses before the second and third attempt of a
// request that failed on the server side or on the way there.
var retryDelays = []time.Duration{300 * time.Millisecond, 900 * time.Millisecond}

// Client asks the ERP service for rates.
type Client struct {
	endpoint string
	username string
	password string
	http     *http.Client
}

// NewClient builds a client for the given settings. A nil httpClient
// gets a 15 s timeout.
func NewClient(s Settings, password string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	return &Client{endpoint: s.Endpoint(), username: s.Username, password: password, http: httpClient}
}

type ratesResponse struct {
	Success bool `json:"success"`
	Data    []struct {
		Currency     string  `json:"currency"`
		ExchangeRate float64 `json:"exchange_rate"`
	} `json:"data"`
	Errors []json.RawMessage `json:"errors"`
}

// Rates returns UAH per unit for each currency at the wall-clock moment
// at, which is sent without a zone offset (the service reads it as Kyiv
// time).
func (c *Client) Rates(ctx context.Context, at time.Time, currencies []string) (map[string]float64, error) {
	// Written out rather than url.Values-encoded: the ERP service was
	// checked with a literal ":" and "," in the query, and both are legal
	// there unescaped. Currency codes are Latin letters only.
	target := c.endpoint + "?date=" + at.Format("2006-01-02T15:04:05") +
		"&currencies=" + strings.Join(currencies, ",")

	var lastErr error
	for attempt := 0; attempt <= len(retryDelays); attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(retryDelays[attempt-1]):
			}
		}
		rates, retry, err := c.fetch(ctx, target, currencies)
		if err == nil {
			return rates, nil
		}
		lastErr = err
		if !retry {
			break
		}
	}
	return nil, lastErr
}

func (c *Client) fetch(ctx context.Context, target string, currencies []string) (map[string]float64, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, false, err
	}
	req.SetBasicAuth(c.username, c.password)
	req.Header.Set("Accept", "application/json")
	res, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, false, ctx.Err()
		}
		return nil, true, fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return nil, true, fmt.Errorf("fxrates: read: %w", err)
	}
	switch {
	case res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden:
		return nil, false, ErrUnauthorized
	case res.StatusCode == http.StatusNotFound:
		return nil, false, ErrNotFound
	case res.StatusCode >= 500:
		return nil, true, fmt.Errorf("fxrates: ERP відповіла %s", res.Status)
	case res.StatusCode != http.StatusOK:
		return nil, false, fmt.Errorf("fxrates: ERP відповіла %s", res.Status)
	}
	var parsed ratesResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, false, fmt.Errorf("fxrates: відповідь ERP не JSON: %w", err)
	}
	if !parsed.Success {
		return nil, false, fmt.Errorf("fxrates: ERP повідомила про помилку: %s", joinErrors(parsed.Errors))
	}
	out := make(map[string]float64, len(currencies))
	for _, d := range parsed.Data {
		cur := strings.ToUpper(strings.TrimSpace(d.Currency))
		if d.ExchangeRate > 0 {
			out[cur] = d.ExchangeRate
		}
	}
	for _, cur := range currencies {
		if _, ok := out[cur]; !ok {
			return nil, false, fmt.Errorf("fxrates: ERP не повернула курс %s", cur)
		}
	}
	return out, false, nil
}

func joinErrors(raw []json.RawMessage) string {
	if len(raw) == 0 {
		return "без пояснення"
	}
	parts := make([]string, 0, len(raw))
	for _, r := range raw {
		var s string
		if json.Unmarshal(r, &s) == nil {
			parts = append(parts, s)
			continue
		}
		parts = append(parts, string(r))
	}
	return strings.Join(parts, "; ")
}
