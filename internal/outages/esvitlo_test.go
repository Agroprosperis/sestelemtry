package outages

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestParseOutagesScheduleAndOther(t *testing.T) {
	loc := time.FixedZone("Kyiv", 2*3600)
	body := []byte(`{
		"planned_list_cab": [
			{"accidentid": 0, "acc_begin": "2026-11-19T06:00:00", "accend_plan": "2026-11-19T10:00:00"},
			{"accidentid": 42, "acc_begin": "2026-11-19T12:00:00", "accend_plan": "2026-11-19T14:00:00"},
			{"accidentid": 0, "acc_begin": "2026-11-19T09:30:00", "accend_plan": "2026-11-19T11:00:00"}
		]
	}`)
	got, err := parseOutages(body, loc)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d intervals, want 2 (schedule merged): %+v", len(got), got)
	}
	if got[0].Kind != KindSchedule || !got[0].Start.Equal(time.Date(2026, 11, 19, 6, 0, 0, 0, loc)) || !got[0].End.Equal(time.Date(2026, 11, 19, 11, 0, 0, 0, loc)) {
		t.Fatalf("schedule merge: %+v", got[0])
	}
	if got[1].Kind != KindOther {
		t.Fatalf("other: %+v", got[1])
	}
}

func TestParseOutagesRefusesUnknownShape(t *testing.T) {
	_, err := parseOutages([]byte(`{"oops":[]}`), time.UTC)
	if err == nil || !strings.Contains(err.Error(), "planned_list_cab") {
		t.Fatalf("want ErrNoData about planned_list_cab, got %v", err)
	}
}

func TestParseOutagesEmptyList(t *testing.T) {
	got, err := parseOutages([]byte(`{"planned_list_cab":[]}`), time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("got %+v", got)
	}
}

func TestClientSignInAndOutages(t *testing.T) {
	loc := time.FixedZone("Kyiv", 2*3600)
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "sess", Value: "1"})
		w.Write([]byte(`<html>login</html>`))
	})
	mux.HandleFunc("/registr_all_user/login_all_user", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if r.Form.Get("email") != "a@b.c" || r.Form.Get("password") != "secret" {
			http.Error(w, "bad", http.StatusUnauthorized)
			return
		}
		w.Write([]byte(`<a href="/account_household/show_only_disconnections">Вихід</a>`))
	})
	mux.HandleFunc("/account_household/show_only_disconnections", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("eic") != "62Z1234567890123" {
			http.Error(w, "bad eic", 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"planned_list_cab":[{"accidentid":0,"acc_begin":"2026-11-19T06:00:00","accend_plan":"2026-11-19T07:00:00"}]}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	c := NewClient(srv.URL, "a@b.c", "secret", loc, srv.Client())
	list, err := c.Outages(context.Background(), "62Z1234567890123")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Kind != KindSchedule {
		t.Fatalf("got %+v", list)
	}
}

func TestClientUnauthorized(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})
	mux.HandleFunc("/registr_all_user/login_all_user", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<form><input name="password"></form>`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	c := NewClient(srv.URL, "x", "y", time.UTC, srv.Client())
	err := c.SignIn(context.Background())
	if !errors.Is(err, ErrUnauthorized) || Refusal(err) != "" {
		t.Fatalf("got %v", err)
	}
}

func TestClientRefusalInTheCabinetsWords(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<div class="w2p_flash alert alert-dismissable"></div><input name="password">`))
	})
	mux.HandleFunc("/registr_all_user/login_all_user", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<div class="w2p_flash alert alert-dismissable">Невірна адреса ел.пошти </div><input name="password">`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	err := NewClient(srv.URL, "x", "y", time.UTC, srv.Client()).SignIn(context.Background())
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("got %v, want ErrUnauthorized", err)
	}
	if got := Refusal(err); got != "Невірна адреса ел.пошти" {
		t.Fatalf("Refusal = %q", got)
	}
}

func TestClientCloudflareBlocked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cf-Mitigated", "challenge")
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte("Just a moment..."))
	}))
	t.Cleanup(srv.Close)

	c := NewClient(srv.URL, "x", "y", time.UTC, srv.Client())
	err := c.SignIn(context.Background())
	if !errors.Is(err, ErrBlocked) {
		t.Fatalf("got %v", err)
	}
}

// legalAnswer is a legal account's outages as the cabinet answers them;
// the points and addresses are made up.
const legalAnswer = `{
	"current_list_cab": [
		{"name_to": "Площадка", "typeid": "Аварійне відключення", "acc_begin": "2026-11-19T08:15:00", "accend_plan": "", "filiya": "СО", "eic": "62Z0000000000001", "address": "вул. Тестова, 1"}
	],
	"planned_list_cab": [
		{"name_to": "Площадка", "typeid": "ГПВ", "acc_begin": "\"2026-11-20 06:00:00\"", "accend_plan": "2026-11-20 10:00:00", "filiya": "СО", "eic": "62Z0000000000001", "address": "вул. Тестова, 1"},
		{"name_to": "Склад", "typeid": "Планові ремонтні роботи", "acc_begin": "2026-11-21T09:00:00", "accend_plan": "2026-11-21T17:00:00", "filiya": "СО", "eic": "62z0000000000002", "address": "вул. Тестова, 2"}
	],
	"fields": ["Назва точки", "Причина відключення", "Дата/час відключення", "Орієнтовна дата/час відновлення", "Структурна одиниця", "EIC код", "Адреса"],
	"keys_s": ["name_to", "typeid", "acc_begin", "accend_plan", "filiya", "eic", "address"]
}`

func TestParseOutagesLegalAccount(t *testing.T) {
	loc := time.FixedZone("Kyiv", 2*3600)
	got, err := parseOutages([]byte(legalAnswer), loc)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %+v, want the two planned ones (the one under way has no end)", got)
	}
	if got[0].Kind != KindSchedule || got[0].EIC != "62Z0000000000001" ||
		!got[0].Start.Equal(time.Date(2026, 11, 20, 6, 0, 0, 0, loc)) || !got[0].End.Equal(time.Date(2026, 11, 20, 10, 0, 0, 0, loc)) {
		t.Fatalf("schedule: %+v", got[0])
	}
	if got[1].Kind != KindOther || got[1].EIC != "62Z0000000000002" {
		t.Fatalf("works: %+v", got[1])
	}
}

func TestClientReadsALegalAccountByItsID(t *testing.T) {
	loc := time.FixedZone("Kyiv", 2*3600)
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<input name="password">`))
	})
	mux.HandleFunc("/registr_all_user/login_all_user", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<a href="/user/logout">Вихід</a>`))
	})
	mux.HandleFunc("/account_legal", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<a href="/user/logout">Вихід</a><a href="/account_legal/view_main_details?a=12345&amp;highlight_main=account_legal">Перегляд</a>`))
	})
	mux.HandleFunc(outagesPath, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("type_user") != "2" || q.Get("a") != "12345" || !q.Has("eic") || q.Get("eic") != "" {
			http.Error(w, "bad query "+r.URL.RawQuery, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(legalAnswer))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	c := NewClient(srv.URL, "a@b.c", "secret", loc, srv.Client())
	p, err := c.Probe(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Accounts) != 1 || p.Accounts[0] != "12345" || len(p.Outages) != 2 {
		t.Fatalf("probe: %+v", p)
	}
	one, err := c.Outages(context.Background(), "62Z0000000000002")
	if err != nil {
		t.Fatal(err)
	}
	if len(one) != 1 || one[0].Kind != KindOther {
		t.Fatalf("kept to the point: %+v", one)
	}
}

// realLegalAnswer has the shape a legal account answered with on
// 20.11.2026: Ukrainian dates, the day's last outage closed at 23:59, the
// same schedule under each of the site's metering points. The EICs are
// made up.
const realLegalAnswer = `{"current_list_cab": [], "planned_list_cab": [
	{"accidentid": 0, "typeid": "Заходи обмеження", "acc_begin": "20 листопада 2026, 21:00", "accend_plan": "20 листопада 2026, 23:59", "eic": "62Z0000000000011", "name_to": "Площадка  елеватор"},
	{"accidentid": 0, "typeid": "Заходи обмеження", "acc_begin": "21 листопада 2026, 04:00", "accend_plan": "21 листопада 2026, 06:00", "eic": "62Z0000000000011", "name_to": "Площадка  елеватор"},
	{"accidentid": 0, "typeid": "Заходи обмеження", "acc_begin": "20 листопада 2026, 21:00", "accend_plan": "20 листопада 2026, 23:59", "eic": "62Z0000000000012", "name_to": "Площадка  елеватор"},
	{"accidentid": 0, "typeid": "Заходи обмеження", "acc_begin": "21 листопада 2026, 04:00", "accend_plan": "21 листопада 2026, 06:00", "eic": "62Z0000000000012", "name_to": "Площадка  елеватор"}
], "fields": [], "keys_s": []}`

func TestParseOutagesUkrainianDates(t *testing.T) {
	loc := time.FixedZone("Kyiv", 2*3600)
	got, err := parseOutages([]byte(realLegalAnswer), loc)
	if err != nil {
		t.Fatal(err)
	}
	// The two points share the schedule, so the station has two stretches.
	if len(got) != 2 {
		t.Fatalf("got %+v, want 2 merged intervals", got)
	}
	evening := got[0]
	if evening.Kind != KindSchedule ||
		!evening.Start.Equal(time.Date(2026, 11, 20, 21, 0, 0, 0, loc)) ||
		!evening.End.Equal(time.Date(2026, 11, 21, 0, 0, 0, 0, loc)) {
		t.Fatalf("evening: %+v, want 20.11 21:00 – 21.11 00:00 schedule", evening)
	}
	if !got[1].Start.Equal(time.Date(2026, 11, 21, 4, 0, 0, 0, loc)) || !got[1].End.Equal(time.Date(2026, 11, 21, 6, 0, 0, 0, loc)) {
		t.Fatalf("night: %+v", got[1])
	}
}

func TestParseUkrainianRefusesNonsense(t *testing.T) {
	for _, s := range []string{"31 лютого 2026, 10:00", "21 листопадаа 2026, 10:00", "21 листопада 2026, 25:00"} {
		if _, ok := parseUkrainian(s, time.UTC); ok {
			t.Errorf("parseUkrainian(%q) accepted", s)
		}
	}
}

// A passport holding the site's EIC, which no item names, still gets the
// account's outages; one holding a point's EIC gets that point's.
func TestClientKeepsTheAccountsOutagesForASiteEIC(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<input name="password">`))
	})
	mux.HandleFunc("/registr_all_user/login_all_user", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<a href="/user/logout">Вихід</a>`))
	})
	mux.HandleFunc("/account_legal", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<a href="/user/logout">Вихід</a><a href="/account_legal/view_main_details?a=777">Перегляд</a>`))
	})
	mux.HandleFunc(outagesPath, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(realLegalAnswer))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	c := NewClient(srv.URL, "a@b.c", "secret", time.UTC, srv.Client())
	site, err := c.Outages(context.Background(), "62Z0000000000099")
	if err != nil {
		t.Fatal(err)
	}
	if len(site) != 2 {
		t.Fatalf("site EIC: %+v, want the account's 2 intervals", site)
	}
	point, err := c.Outages(context.Background(), "62Z0000000000011")
	if err != nil {
		t.Fatal(err)
	}
	if len(point) != 2 || point[0].EIC != "62Z0000000000011" {
		t.Fatalf("point EIC: %+v", point)
	}
}

func TestValidateAccount(t *testing.T) {
	a := Account{OrganizationID: "ab", Host: "vn.e-svitlo.com.ua", Login: "a@b.c", EIC: NormalizeEIC("62z1234567890123")}
	if err := a.Validate(); err != nil {
		t.Fatal(err)
	}
	// The EIC may stay empty: the cabinet's whole account is the station's.
	a.EIC = ""
	if err := a.Validate(); err != nil {
		t.Fatal(err)
	}
	a.EIC = "короткий"
	if err := a.Validate(); err == nil {
		t.Fatal("want eic error")
	}
	a.EIC, a.Login = "", ""
	if err := a.Validate(); err == nil {
		t.Fatal("want login error")
	}
	a.Login, a.Host = "a@b.c", "example.com"
	if err := a.Validate(); err == nil {
		t.Fatal("want host error")
	}
}
