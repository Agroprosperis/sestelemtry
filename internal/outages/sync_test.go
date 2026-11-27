package outages

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

type replaced struct {
	from time.Time
	n    int
}

// fakeStore is the sync's Store in memory.
type fakeStore struct {
	accounts  []AccountRow
	passwords map[string]string
	replaced  map[string]replaced
	marked    map[string]Read
}

func (f *fakeStore) Accounts(context.Context) ([]AccountRow, error) { return f.accounts, nil }
func (f *fakeStore) Password(_ context.Context, org string) (string, error) {
	return f.passwords[org], nil
}
func (f *fakeStore) ReplaceIntervals(_ context.Context, org string, from time.Time, list []Interval, _ Read) error {
	f.replaced[org] = replaced{from: from, n: len(list)}
	return nil
}
func (f *fakeStore) MarkRead(_ context.Context, org string, read Read) error {
	f.marked[org] = read
	return nil
}

// cabinetServer is one legal account's cabinet that lets in ok@x / pw
// and answers realLegalAnswer; signIns counts the sign-ins it took.
func cabinetServer(t *testing.T, signIns *atomic.Int32) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<input name="password">`))
	})
	mux.HandleFunc("/registr_all_user/login_all_user", func(w http.ResponseWriter, r *http.Request) {
		signIns.Add(1)
		if r.FormValue("email") != "ok@x" || r.FormValue("password") != "pw" {
			w.Write([]byte(`<div class="w2p_flash">Невірний пароль</div><input name="password">`))
			return
		}
		w.Write([]byte(`<a href="/user/logout">Вихід</a>`))
	})
	mux.HandleFunc("/account_legal", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<a href="/user/logout">Вихід</a><a href="/account_legal/view_main_details?a=1">Перегляд</a>`))
	})
	mux.HandleFunc(outagesPath, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(realLegalAnswer))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestSyncReadsEachStationSigningInOncePerCabinet(t *testing.T) {
	var signIns atomic.Int32
	srv := cabinetServer(t, &signIns)
	loc := time.FixedZone("Kyiv", 2*3600)
	vn := "vn.e-svitlo.com.ua"
	store := &fakeStore{
		accounts: []AccountRow{
			{Account: Account{OrganizationID: "je", Host: vn, Login: "ok@x", EIC: "62Z0000000000011"}, PasswordConfigured: true},
			{Account: Account{OrganizationID: "je2", Host: vn, Login: "ok@x", EIC: "62Z0000000000012"}, PasswordConfigured: true},
			{Account: Account{OrganizationID: "bad", Host: vn, Login: "ok@x", EIC: "62Z0000000000011"}, PasswordConfigured: true},
			// No password yet: not read at all.
			{Account: Account{OrganizationID: "draft", Host: vn, Login: "ok@x", EIC: "62Z0000000000011"}},
		},
		passwords: map[string]string{"je": "pw", "je2": "pw", "bad": "wrong"},
		replaced:  map[string]replaced{},
		marked:    map[string]Read{},
	}
	s := NewSyncer(store, loc, nil)
	s.newClient = func(_, login, password string, loc *time.Location, _ *http.Client) *Client {
		return NewClient(srv.URL, login, password, loc, srv.Client())
	}
	s.now = func() time.Time { return time.Date(2026, 11, 20, 17, 46, 0, 0, time.UTC) }

	err := s.SyncOnce(context.Background())
	if err == nil {
		t.Fatal("want the wrong password's error")
	}
	if got := signIns.Load(); got != 2 {
		t.Fatalf("sign-ins = %d, want 2: one per credentials", got)
	}
	dayStart := time.Date(2026, 11, 20, 0, 0, 0, 0, loc)
	for _, org := range []string{"je", "je2"} {
		r, ok := store.replaced[org]
		if !ok || !r.from.Equal(dayStart) || r.n != 2 {
			t.Fatalf("%s replaced = %+v (ok %v), want 2 intervals from %v", org, r, ok, dayStart)
		}
	}
	if read, ok := store.marked["bad"]; !ok || read.Error == "" {
		t.Fatalf("bad station's read = %+v", read)
	}
	if _, ok := store.replaced["draft"]; ok {
		t.Fatal("a station without a password was read")
	}
	if st := s.Status(); !st.Configured || st.Stations != 3 || st.LastError == "" {
		t.Fatalf("status = %+v", st)
	}
}

func TestSyncIdlesWithoutAccounts(t *testing.T) {
	store := &fakeStore{replaced: map[string]replaced{}, marked: map[string]Read{}}
	s := NewSyncer(store, time.UTC, nil)
	if err := s.SyncOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.Status().Configured {
		t.Fatal("configured without accounts")
	}
}
