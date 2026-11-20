package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nesh/sestelemetry/internal/outages"
)

// fakeOutagesStore keeps one account per station in memory.
type fakeOutagesStore struct {
	accounts  map[string]outages.Account
	passwords map[string]string
	intervals []outages.Interval
	deleted   []string
}

func newFakeOutagesStore() *fakeOutagesStore {
	return &fakeOutagesStore{accounts: map[string]outages.Account{}, passwords: map[string]string{}}
}

func (f *fakeOutagesStore) Account(_ context.Context, org string) (outages.AccountRow, outages.Read, bool, error) {
	a, ok := f.accounts[org]
	if !ok {
		return outages.AccountRow{Account: outages.Account{OrganizationID: org}}, outages.Read{}, false, nil
	}
	return outages.AccountRow{Account: a, PasswordConfigured: f.passwords[org] != ""}, outages.Read{}, true, nil
}

func (f *fakeOutagesStore) Password(_ context.Context, org string) (string, error) {
	return f.passwords[org], nil
}

func (f *fakeOutagesStore) SaveAccount(_ context.Context, a outages.Account, password *string) error {
	f.accounts[a.OrganizationID] = a
	if password != nil {
		f.passwords[a.OrganizationID] = *password
	}
	return nil
}

func (f *fakeOutagesStore) DeleteAccount(_ context.Context, org string) error {
	delete(f.accounts, org)
	f.deleted = append(f.deleted, org)
	return nil
}

func (f *fakeOutagesStore) Intervals(context.Context, string, time.Time, time.Time) ([]outages.Interval, error) {
	return f.intervals, nil
}

type fakeOutagesSync struct{ woken int }

func (s *fakeOutagesSync) Wake()                  { s.woken++ }
func (s *fakeOutagesSync) Status() outages.Status { return outages.Status{} }

func outageRequest(t *testing.T, h *Handlers, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, httptest.NewRequest(method, target, strings.NewReader(body)))
	return rec
}

// The password goes in once and never comes back out; saving the login
// again without it keeps it, and wakes the hourly read at once.
func TestOutageAccountKeepsThePasswordOnTheServer(t *testing.T) {
	store, sync := newFakeOutagesStore(), &fakeOutagesSync{}
	h := NewHandlers(&mockStore{}, "*")
	h.SetOutages(store, sync)
	const target = "/api/v1/outage-account?organization_id=je"

	rec := outageRequest(t, h, http.MethodPut, target, `{"host":"vn.e-svitlo.com.ua","login":"jm@ukr.net","eic":"62z5841289870313","password":"s3cret\n"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("put: %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "s3cret") {
		t.Fatalf("the password came back: %s", rec.Body.String())
	}
	var got OutageAccountResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.Saved || !got.PasswordConfigured || got.EIC != "62Z5841289870313" || store.passwords["je"] != "s3cret" {
		t.Fatalf("saved %+v, stored password %q", got, store.passwords["je"])
	}
	if sync.woken != 1 {
		t.Fatalf("woken %d times, want 1", sync.woken)
	}

	// A save without the password keeps it…
	if rec := outageRequest(t, h, http.MethodPut, target, `{"host":"vn.e-svitlo.com.ua","login":"jm@ukr.net","eic":""}`); rec.Code != http.StatusOK || store.passwords["je"] != "s3cret" {
		t.Fatalf("keep: %d, password %q", rec.Code, store.passwords["je"])
	}
	// …but only for the login it was saved with.
	if rec := outageRequest(t, h, http.MethodPut, target, `{"host":"vn.e-svitlo.com.ua","login":"other@ukr.net"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("another login with the stored password: %d", rec.Code)
	}
	// An empty login takes the station off the read.
	if rec := outageRequest(t, h, http.MethodPut, target, `{"host":"","login":""}`); rec.Code != http.StatusOK || len(store.deleted) != 1 {
		t.Fatalf("delete: %d, deleted %v", rec.Code, store.deleted)
	}
}

func TestOutageAccountRefusesWhatTheCabinetCannotTake(t *testing.T) {
	h := NewHandlers(&mockStore{}, "*")
	h.SetOutages(newFakeOutagesStore(), &fakeOutagesSync{})
	for _, body := range []string{
		`{"host":"example.com","login":"a@b.c","password":"x"}`,
		`{"host":"vn.e-svitlo.com.ua","login":"a@b.c","eic":"короткий","password":"x"}`,
		`not json`,
	} {
		if rec := outageRequest(t, h, http.MethodPut, "/api/v1/outage-account?organization_id=je", body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", body, rec.Code)
		}
	}
	// A test needs a password, typed or stored.
	if rec := outageRequest(t, h, http.MethodPost, "/api/v1/outage-account/test?organization_id=je", `{"host":"vn.e-svitlo.com.ua","login":"a@b.c"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("test without a password: %d", rec.Code)
	}
}

func TestOutageAccountListsTheNextOutages(t *testing.T) {
	store := newFakeOutagesStore()
	start := time.Date(2026, 11, 20, 19, 0, 0, 0, time.UTC)
	store.intervals = []outages.Interval{{Start: start, End: start.Add(3 * time.Hour), Kind: outages.KindSchedule}}
	h := NewHandlers(&mockStore{}, "*")
	h.SetOutages(store, &fakeOutagesSync{})
	rec := outageRequest(t, h, http.MethodGet, "/api/v1/outage-account?organization_id=je", "")
	var got OutageAccountResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("%d %v", rec.Code, err)
	}
	if got.Saved || len(got.Upcoming) != 1 || !got.Upcoming[0].Start.Equal(start) || len(got.Operators) != 2 {
		t.Fatalf("got %+v", got)
	}
}
