package fusionsolar

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestClientDeviceHistoryRequestAndParse(t *testing.T) {
	var gotPath, gotAuth string
	var gotBody historyRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"success": true,
			"failCode": 0,
			"data": [
				{"collectTime": 1780411800000, "dataItemMap": {"total_yield": 36842.65, "total_charge": null}},
				{"collectTime": 1780412100000, "dataItemMap": {"total_yield": 36850.10}}
			]
		}`)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok-123", 5*time.Second)
	start := time.UnixMilli(1780411800000).UTC()
	end := start.Add(time.Hour)

	samples, err := c.DeviceHistory(context.Background(), "NE=247434106", devTypeSmartLogger, start, end)
	if err != nil {
		t.Fatalf("DeviceHistory: %v", err)
	}

	if gotPath != deviceHistoryPath {
		t.Errorf("path = %q, want %q", gotPath, deviceHistoryPath)
	}
	if gotAuth != "Bearer tok-123" {
		t.Errorf("auth = %q, want Bearer tok-123", gotAuth)
	}
	if gotBody.DevDn != "NE=247434106" || gotBody.DevTypeID != devTypeSmartLogger {
		t.Errorf("body dev = %+v", gotBody)
	}
	if gotBody.StartTime != start.UnixMilli() || gotBody.EndTime != end.UnixMilli() {
		t.Errorf("body window = %d..%d, want %d..%d", gotBody.StartTime, gotBody.EndTime, start.UnixMilli(), end.UnixMilli())
	}

	if len(samples) != 2 {
		t.Fatalf("got %d samples, want 2", len(samples))
	}
	if samples[0].Fields["total_yield"] != 36842.65 {
		t.Errorf("sample0 total_yield = %v", samples[0].Fields["total_yield"])
	}
	if _, present := samples[0].Fields["total_charge"]; present {
		t.Errorf("null total_charge should be dropped")
	}
	if !samples[0].Time.Equal(time.UnixMilli(1780411800000).UTC()) {
		t.Errorf("sample0 time = %v", samples[0].Time)
	}
}

func TestClientDeviceHistoryFailCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"success": false, "failCode": 305, "message": "USG.0005 token expired"}`)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok", 5*time.Second)
	start := time.Now().Add(-time.Hour).UTC()
	_, err := c.DeviceHistory(context.Background(), "NE=1", devTypeSmartLogger, start, start.Add(time.Hour))
	if err == nil {
		t.Fatal("expected error on failCode != 0")
	}
	if got := err.Error(); !strings.Contains(got, "failCode=305") {
		t.Errorf("err = %q, want failCode=305", got)
	}
}

func TestClientRejectsWideWindow(t *testing.T) {
	c := NewClient("", "tok", time.Second)
	start := time.Now().Add(-48 * time.Hour).UTC()
	_, err := c.DeviceHistory(context.Background(), "NE=1", devTypeSmartLogger, start, start.Add(25*time.Hour))
	if err == nil {
		t.Fatal("expected error for window > 24h")
	}
}

func TestClientRequiresToken(t *testing.T) {
	c := NewClient("", "", time.Second)
	start := time.Now().Add(-time.Hour).UTC()
	_, err := c.DeviceHistory(context.Background(), "NE=1", devTypeSmartLogger, start, start.Add(time.Hour))
	if err == nil {
		t.Fatal("expected error for missing token")
	}
}
