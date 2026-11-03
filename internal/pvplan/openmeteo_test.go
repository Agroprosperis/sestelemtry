package pvplan

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

func newTestWeather(t *testing.T, handler http.HandlerFunc) (*WeatherClient, *url.Values) {
	t.Helper()
	var last url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		last = r.URL.Query()
		handler(w, r)
	}))
	t.Cleanup(srv.Close)
	c := NewWeatherClient(srv.URL, srv.URL, "", srv.Client())
	c.retryDelays = []time.Duration{0, 0}
	return c, &last
}

// Open-Meteo stamps irradiance at the end of the hour it averages, so
// the value stamped 12:00 belongs to the 11:00–12:00 hour. Reading it
// as 12:00–13:00 (as the n8n flow did) plans every hour an hour late.
func TestForecastAssignsIrradianceToThePrecedingHour(t *testing.T) {
	stamp := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	c, query := newTestWeather(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"hourly":{"time":[` + itoa(stamp.Unix()) + `,` + itoa(stamp.Add(time.Hour).Unix()) + `],
			"temperature_2m":[10.5,null],"global_tilted_irradiance":[640,700]}}`))
	})
	hours, err := c.Forecast(context.Background(), Plane{Latitude: 49.04, Longitude: 27.74, TiltDeg: 30, AzimuthDeg: -92.5})
	if err != nil {
		t.Fatalf("Forecast: %v", err)
	}
	if len(hours) != 1 {
		t.Fatalf("hours = %+v, want one: the hour with a null temperature is unknown, not dark", hours)
	}
	if want := stamp.Add(-time.Hour); !hours[0].Start.Equal(want) {
		t.Fatalf("start = %v, want %v", hours[0].Start, want)
	}
	if hours[0].GtiWm2 != 640 || hours[0].TempC != 10.5 {
		t.Fatalf("hour = %+v", hours[0])
	}
	q := *query
	if q.Get("tilt") != "30" || q.Get("azimuth") != "-92.5" || q.Get("timeformat") != "unixtime" {
		t.Fatalf("query = %v, want the plane's tilt/azimuth and unix times", q)
	}
}

func TestPreviousDay1AsksForTheDayAheadValues(t *testing.T) {
	c, query := newTestWeather(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"hourly":{"time":[1762171200],
			"temperature_2m_previous_day1":[3],"global_tilted_irradiance_previous_day1":[120]}}`))
	})
	hours, err := c.PreviousDay1(context.Background(), Plane{Latitude: 49, Longitude: 27}, "2026-11-01", "2026-11-02")
	if err != nil {
		t.Fatalf("PreviousDay1: %v", err)
	}
	if len(hours) != 1 || hours[0].GtiWm2 != 120 {
		t.Fatalf("hours = %+v", hours)
	}
	q := *query
	if q.Get("start_date") != "2026-11-01" || q.Get("end_date") != "2026-11-02" {
		t.Fatalf("query = %v", q)
	}
	if q.Get("hourly") != "temperature_2m_previous_day1,global_tilted_irradiance_previous_day1" {
		t.Fatalf("hourly = %q", q.Get("hourly"))
	}
}

// A bad request won't get better by retrying; an outage might.
func TestWeatherRetriesOnlyWhatMightRecover(t *testing.T) {
	var calls atomic.Int32
	c, _ := newTestWeather(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":true,"reason":"Parameter 'tilt' must be between 0 and 90"}`))
	})
	if _, err := c.Forecast(context.Background(), Plane{}); err == nil {
		t.Fatal("expected the refusal")
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("a 400 was tried %d times, want once", n)
	}

	calls.Store(0)
	c2, _ := newTestWeather(t, func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) < 3 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte(`{"hourly":{"time":[1762171200],"temperature_2m":[3],"global_tilted_irradiance":[120]}}`))
	})
	hours, err := c2.Forecast(context.Background(), Plane{})
	if err != nil || len(hours) != 1 {
		t.Fatalf("after two 502s: hours=%v err=%v, want the third answer", hours, err)
	}
}

func TestWeatherSendsTheAPIKeyWhenSet(t *testing.T) {
	var key string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key = r.URL.Query().Get("apikey")
		_, _ = w.Write([]byte(`{"hourly":{"time":[],"temperature_2m":[],"global_tilted_irradiance":[]}}`))
	}))
	defer srv.Close()
	c := NewWeatherClient(srv.URL, srv.URL, "secret", srv.Client())
	if _, err := c.Forecast(context.Background(), Plane{}); err != nil {
		t.Fatalf("Forecast: %v", err)
	}
	if key != "secret" {
		t.Fatalf("apikey = %q", key)
	}
}

func itoa(v int64) string {
	return strconv.FormatInt(v, 10)
}
