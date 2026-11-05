package fxrates

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func kyiv(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Kyiv")
	if err != nil {
		t.Skip("tzdata unavailable")
	}
	return loc
}

func TestClientReadsRatesWithBasicAuth(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "BankRates" || pass != "secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		gotQuery = r.URL.RawQuery
		fmt.Fprint(w, `{"success":true,"data":[{"currency":"USD","exchange_rate":45.35},{"currency":"EUR","exchange_rate":50.78}],"errors":[]}`)
	}))
	defer srv.Close()

	at := time.Date(2026, 10, 30, 12, 0, 0, 0, kyiv(t))
	rates, err := NewClient(Settings{URL: srv.URL, Username: "BankRates"}, "secret", nil).Rates(context.Background(), at, Currencies)
	if err != nil {
		t.Fatalf("Rates: %v", err)
	}
	if rates["USD"] != 45.35 || rates["EUR"] != 50.78 {
		t.Fatalf("rates = %v", rates)
	}
	// The form the live service was checked with.
	if want := "date=2026-10-30T12:00:00&currencies=USD,EUR"; gotQuery != want {
		t.Errorf("query = %q, want %q", gotQuery, want)
	}
}

// The ERP's web server renegotiates TLS in the middle of a request; the
// default client must go along with it, which Go only does on HTTP/1.1.
func TestDefaultClientAcceptsRenegotiation(t *testing.T) {
	tr, ok := defaultHTTPClient().Transport.(*http.Transport)
	if !ok || tr.TLSClientConfig == nil || tr.TLSClientConfig.Renegotiation != tls.RenegotiateFreelyAsClient {
		t.Fatal("the default client refuses TLS renegotiation")
	}
	if tr.ForceAttemptHTTP2 || tr.TLSNextProto == nil || len(tr.TLSNextProto) != 0 {
		t.Error("the default client may switch to HTTP/2, where renegotiation does not exist")
	}
}

func TestClientWordsTheServiceRefusals(t *testing.T) {
	for _, tc := range []struct {
		status int
		want   error
	}{
		{http.StatusUnauthorized, ErrUnauthorized},
		{http.StatusNotFound, ErrNotFound},
	} {
		var calls atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			calls.Add(1)
			w.WriteHeader(tc.status)
		}))
		_, err := NewClient(Settings{URL: srv.URL, Username: "u"}, "p", nil).Rates(context.Background(), time.Now(), Currencies)
		srv.Close()
		if !errors.Is(err, tc.want) {
			t.Errorf("status %d: err = %v, want %v", tc.status, err, tc.want)
		}
		if calls.Load() != 1 {
			t.Errorf("status %d: %d calls, a refusal must not be retried", tc.status, calls.Load())
		}
	}
}

func TestClientRetriesServerErrors(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		fmt.Fprint(w, `{"success":true,"data":[{"currency":"USD","exchange_rate":41},{"currency":"EUR","exchange_rate":44}]}`)
	}))
	defer srv.Close()
	rates, err := NewClient(Settings{URL: srv.URL, Username: "u"}, "p", nil).Rates(context.Background(), time.Now(), Currencies)
	if err != nil || rates["USD"] != 41 {
		t.Fatalf("rates = %v, err = %v", rates, err)
	}
	if calls.Load() != 2 {
		t.Errorf("calls = %d, want a retry after the 502", calls.Load())
	}
}

func TestClientRefusesAnAnswerWithoutTheRate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"success":false,"data":[],"errors":["немає курсу"]}`)
	}))
	defer srv.Close()
	if _, err := NewClient(Settings{URL: srv.URL, Username: "u"}, "p", nil).Rates(context.Background(), time.Now(), Currencies); err == nil {
		t.Fatal("a success:false answer must be an error")
	}
}

func TestSettingsValidate(t *testing.T) {
	for _, bad := range []string{"web1c.agroprosperis.com/apbank", "ftp://host/x", "https://host/x?date=1"} {
		if err := (Settings{URL: bad}).Validate(); err == nil {
			t.Errorf("%q: want a validation error", bad)
		}
	}
	if err := (Settings{}).Validate(); err != nil {
		t.Errorf("empty URL stands for the default: %v", err)
	}
	if got := (Settings{}).Endpoint(); got != DefaultURL {
		t.Errorf("Endpoint() = %q, want the default", got)
	}
}

func TestTableBorrowsTheNearestRate(t *testing.T) {
	tbl := NewTable(map[string]float64{"2026-10-02": 41, "2026-10-05": 42})
	cases := []struct {
		day     string
		rate    float64
		carried bool
	}{
		{"2026-10-02", 41, false},
		{"2026-10-04", 41, true},
		{"2026-10-05", 42, false},
		{"2026-10-09", 42, true},
		{"2026-10-01", 41, true},
	}
	for _, c := range cases {
		r, carried, ok := tbl.Rate(c.day)
		if !ok || r != c.rate || carried != c.carried {
			t.Errorf("Rate(%s) = %v, %v, %v; want %v, %v", c.day, r, carried, ok, c.rate, c.carried)
		}
	}
	if _, _, ok := NewTable(nil).Rate("2026-10-01"); ok {
		t.Error("an empty table has no rate")
	}
}

func TestTableWindow(t *testing.T) {
	loc := kyiv(t)
	tbl := NewTable(map[string]float64{"2026-10-01": 40, "2026-10-02": 44})
	from := time.Date(2026, 10, 1, 0, 0, 0, 0, loc)
	to := time.Date(2026, 10, 4, 0, 0, 0, 0, loc)
	// Today is the 3rd: the 4th has no rate yet and stays out.
	now := time.Date(2026, 10, 3, 9, 0, 0, 0, loc)
	avg, carried := tbl.Window(from, to, loc, now)
	if want := (40 + 44 + 44) / 3.0; avg != want || carried != 1 {
		t.Errorf("Window = %v, %d; want %v, 1", avg, carried, want)
	}
}

// fakeStore keeps the rates in memory.
type fakeStore struct {
	mu        sync.Mutex
	settings  Settings
	password  string
	rates     map[string]map[string]float64
	fetchedAt map[string]time.Time
	now       func() time.Time
	savedDays []string
}

func (f *fakeStore) Settings(context.Context) (Settings, bool, bool, error) {
	return f.settings, f.password != "", true, nil
}

func (f *fakeStore) Password(context.Context) (string, error) { return f.password, nil }

func (f *fakeStore) FetchTimes(_ context.Context, from, to time.Time) (map[string]time.Time, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]time.Time{}
	for day, at := range f.fetchedAt {
		if day >= from.Format("2006-01-02") && day <= to.Format("2006-01-02") && len(f.rates[day]) == len(Currencies) {
			out[day] = at
		}
	}
	return out, nil
}

func (f *fakeStore) SaveRates(_ context.Context, rows []Rate) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.rates == nil {
		f.rates = map[string]map[string]float64{}
		f.fetchedAt = map[string]time.Time{}
	}
	for _, r := range rows {
		day := r.Day.Format("2006-01-02")
		if f.rates[day] == nil {
			f.rates[day] = map[string]float64{}
		}
		f.rates[day][r.Currency] = r.UahPerUnit
		f.fetchedAt[day] = f.now()
	}
	if len(rows) > 0 {
		f.savedDays = append(f.savedDays, rows[0].Day.Format("2006-01-02"))
	}
	return nil
}

// rateServer answers with a rate derived from the requested date, and
// records which moments it was asked about.
func rateServer(t *testing.T) (*httptest.Server, *[]string) {
	t.Helper()
	var mu sync.Mutex
	asked := []string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		asked = append(asked, r.URL.Query().Get("date"))
		mu.Unlock()
		fmt.Fprint(w, `{"success":true,"data":[{"currency":"USD","exchange_rate":41.5},{"currency":"EUR","exchange_rate":48.2}]}`)
	}))
	t.Cleanup(srv.Close)
	return srv, &asked
}

func newTestSyncer(store *fakeStore, loc *time.Location, from, now time.Time) *Syncer {
	s := NewSyncer(store, loc, nil)
	s.from = from
	s.pause = 0
	s.now = func() time.Time { return now }
	store.now = s.now
	return s
}

func TestSyncFillsMissingDaysAtNoonAndTodayNow(t *testing.T) {
	loc := kyiv(t)
	srv, asked := rateServer(t)
	store := &fakeStore{settings: Settings{URL: srv.URL, Username: "u"}, password: "p"}
	from := time.Date(2026, 10, 1, 0, 0, 0, 0, loc)
	now := time.Date(2026, 10, 3, 9, 30, 0, 0, loc)
	s := newTestSyncer(store, loc, from, now)

	if err := s.SyncOnce(context.Background()); err != nil {
		t.Fatalf("SyncOnce: %v", err)
	}
	want := []string{"2026-10-01T12:00:00", "2026-10-02T12:00:00", "2026-10-03T09:30:00"}
	if fmt.Sprint(*asked) != fmt.Sprint(want) {
		t.Fatalf("asked %v, want %v", *asked, want)
	}
	if st := s.Status(); !st.Configured || st.LastError != "" || st.LastSuccess.IsZero() {
		t.Errorf("status = %+v", st)
	}

	// A second pass the same morning refreshes only today.
	*asked = (*asked)[:0]
	if err := s.SyncOnce(context.Background()); err != nil {
		t.Fatalf("SyncOnce: %v", err)
	}
	if fmt.Sprint(*asked) != fmt.Sprint([]string{"2026-10-03T09:30:00"}) {
		t.Errorf("second pass asked %v, want only today", *asked)
	}

	// Next day: yesterday was only fetched before its noon, so it is
	// fetched again at noon.
	*asked = (*asked)[:0]
	next := time.Date(2026, 10, 4, 13, 0, 0, 0, loc)
	s.now = func() time.Time { return next }
	store.now = s.now
	if err := s.SyncOnce(context.Background()); err != nil {
		t.Fatalf("SyncOnce: %v", err)
	}
	want = []string{"2026-10-03T12:00:00", "2026-10-04T12:00:00"}
	if fmt.Sprint(*asked) != fmt.Sprint(want) {
		t.Errorf("next day asked %v, want %v", *asked, want)
	}
}

func TestSyncIdlesWithoutAnAccount(t *testing.T) {
	loc := kyiv(t)
	srv, asked := rateServer(t)
	store := &fakeStore{settings: Settings{URL: srv.URL}}
	s := newTestSyncer(store, loc, time.Date(2026, 10, 1, 0, 0, 0, 0, loc), time.Date(2026, 10, 3, 9, 0, 0, 0, loc))
	if err := s.SyncOnce(context.Background()); err != nil {
		t.Fatalf("SyncOnce: %v", err)
	}
	if len(*asked) != 0 {
		t.Errorf("asked ERP %v without an account", *asked)
	}
	if s.Status().Configured {
		t.Error("status says configured")
	}
}

func TestSyncStopsOnARefusal(t *testing.T) {
	loc := kyiv(t)
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	store := &fakeStore{settings: Settings{URL: srv.URL, Username: "u"}, password: "bad"}
	s := newTestSyncer(store, loc, time.Date(2026, 10, 1, 0, 0, 0, 0, loc), time.Date(2026, 10, 5, 9, 0, 0, 0, loc))
	err := s.SyncOnce(context.Background())
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
	if calls.Load() != 1 {
		t.Errorf("calls = %d: a refused login must stop the pass", calls.Load())
	}
	if s.Status().LastError == "" {
		t.Error("status keeps no error")
	}
}
