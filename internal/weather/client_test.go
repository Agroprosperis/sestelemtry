package weather

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func mustNewClient(t *testing.T, baseURL string) *Client {
	t.Helper()
	c, err := NewClient(baseURL, time.Second, "", 7)
	if err != nil {
		t.Fatalf("NewClient(%q): %v", baseURL, err)
	}
	return c
}

func TestNewClientRejectsBadBaseURL(t *testing.T) {
	cases := []string{
		"://bad",
		"not-an-absolute-url",
	}
	for _, s := range cases {
		if _, err := NewClient(s, 0, "", 0); err == nil {
			t.Fatalf("NewClient(%q): expected error", s)
		}
	}
}

func TestBuildURLIncludesAllParams(t *testing.T) {
	c := mustNewClient(t, "")
	got := c.BuildURL(49.234, 28.456)
	if !strings.HasPrefix(got, OpenMeteoBaseURL+"?") {
		t.Fatalf("BuildURL: unexpected prefix %q", got)
	}
	for _, want := range []string{
		"latitude=49.234",
		"longitude=28.456",
		"timezone=auto",
		"daily=sunrise%2Csunset%2Cdaylight_duration%2Csunshine_duration%2Cshortwave_radiation_sum",
		"hourly=temperature_2m",
		"global_tilted_irradiance_instant",
		"past_days=7",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("BuildURL: missing %q in %q", want, got)
		}
	}
}

func TestFetchRetriesOn5xx(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls < 3 {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"latitude":49,"longitude":28,"utc_offset_seconds":10800,"hourly":{"time":[]},"daily":{"time":[]}}`))
	}))
	t.Cleanup(srv.Close)

	c := mustNewClient(t, srv.URL)
	f, gotURL, err := c.Fetch(context.Background(), 49, 28, 5, 0)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if f == nil {
		t.Fatal("Fetch: nil forecast")
	}
	if f.UTCOffsetSeconds != 10800 {
		t.Fatalf("UTCOffsetSeconds: got %d, want 10800", f.UTCOffsetSeconds)
	}
	if !strings.HasPrefix(gotURL, srv.URL) {
		t.Fatalf("returned url should start with server url, got %q", gotURL)
	}
	if calls != 3 {
		t.Fatalf("expected 3 attempts, got %d", calls)
	}
}

func TestFetchDoesNotRetryOn4xx(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		http.Error(w, "bad request", http.StatusBadRequest)
	}))
	t.Cleanup(srv.Close)

	c := mustNewClient(t, srv.URL)
	_, _, err := c.Fetch(context.Background(), 49, 28, 5, 0)
	if err == nil {
		t.Fatal("expected error for 400 response")
	}
	if calls != 1 {
		t.Fatalf("expected 1 attempt for 4xx, got %d", calls)
	}
}

func TestFetchReturnsParseErrorWithoutRetry(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`not json at all`))
	}))
	t.Cleanup(srv.Close)

	c := mustNewClient(t, srv.URL)
	_, _, err := c.Fetch(context.Background(), 49, 28, 5, 0)
	if err == nil {
		t.Fatal("expected decode error")
	}
	if calls != 1 {
		t.Fatalf("expected 1 attempt for parse failure, got %d", calls)
	}
}
