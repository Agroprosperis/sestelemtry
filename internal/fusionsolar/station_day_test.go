package fusionsolar

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestClientStationDailyParse(t *testing.T) {
	var gotPath, gotAuth string
	var gotBody stationDayRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		// Two days; second uses a quoted-number field and a stationCode
		// string that must be ignored. collectTime is local-midnight.
		_, _ = io.WriteString(w, `{
			"success": true,
			"failCode": 0,
			"data": [
				{"collectTime": 1775001600000, "stationCode": "NE=1", "dataItemMap": {
					"PVYield": 429.51, "use_power": 2483.0, "buyPower": 2166.5,
					"ongrid_power": 76.93, "chargeCap": 422.57, "dischargeCap": 386.67,
					"selfUsePower": 352.58}},
				{"collectTime": 1775088000000, "dataItemMap": {
					"PVYield": "100.5", "use_power": 200.0}}
			]
		}`)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok-xyz", 5*time.Second)
	loc, err := time.LoadLocation("Europe/Kyiv")
	if err != nil {
		t.Skip("tzdata unavailable")
	}
	days, err := c.StationDaily(context.Background(), "NE=179121693", time.UnixMilli(1775001600000).UTC(), loc)
	if err != nil {
		t.Fatalf("StationDaily: %v", err)
	}

	if gotPath != stationDayPath {
		t.Errorf("path = %q, want %q", gotPath, stationDayPath)
	}
	if gotAuth != "Bearer tok-xyz" {
		t.Errorf("auth = %q", gotAuth)
	}
	if gotBody.StationCodes != "NE=179121693" {
		t.Errorf("stationCodes = %q", gotBody.StationCodes)
	}
	if len(days) != 2 {
		t.Fatalf("got %d days, want 2", len(days))
	}
	d0 := days[0]
	if d0.PVYield != 429.51 || d0.UsePower != 2483.0 || d0.BuyPower != 2166.5 ||
		d0.OnGridPower != 76.93 || d0.ChargeCap != 422.57 || d0.DischargeCap != 386.67 ||
		d0.SelfUsePower != 352.58 {
		t.Errorf("day0 = %+v", d0)
	}
	// Quoted numeric string parses to a float.
	if days[1].PVYield != 100.5 || days[1].UsePower != 200.0 {
		t.Errorf("day1 = %+v", days[1])
	}
	// Day key is the Kyiv civil date as UTC midnight.
	wantDay := time.UnixMilli(1775001600000).In(loc)
	if d0.Day.Year() != wantDay.Year() || d0.Day.Month() != wantDay.Month() || d0.Day.Day() != wantDay.Day() {
		t.Errorf("day0.Day = %v, want civil %v", d0.Day, wantDay)
	}
	if d0.Day.Location() != time.UTC {
		t.Errorf("day key should be UTC-anchored, got %v", d0.Day.Location())
	}
}

func TestClientStationDailyFailCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"success": false, "failCode": 305, "message": "USER_MUST_RELOGIN"}`)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok", 5*time.Second)
	_, err := c.StationDaily(context.Background(), "NE=1", time.Now().UTC(), time.UTC)
	if err == nil {
		t.Fatal("expected failCode error")
	}
}

func TestClientStationDailyRequiresToken(t *testing.T) {
	c := NewClient("http://example.invalid", "", 5*time.Second)
	if _, err := c.StationDaily(context.Background(), "NE=1", time.Now(), time.UTC); err == nil {
		t.Fatal("expected missing-token error")
	}
}
