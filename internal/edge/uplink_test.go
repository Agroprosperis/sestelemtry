package edge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func uplinkCfg(baseURL string) UplinkConfig {
	t := UplinkConfig{
		Enabled:      true,
		BaseURL:      baseURL,
		SiteTokenEnv: "EMS_TEST_TOKEN",
	}
	// Apply the same defaults LoadConfig would.
	t.BatchPath = "/api/v1/edge/batch"
	t.HeartbeatPath = "/api/v1/edge/heartbeat"
	t.ManifestPath = "/api/v1/edge/manifest"
	t.HTTPTimeout = 5 * time.Second
	return t
}

func TestUplinkSendBatchAuthAndAck(t *testing.T) {
	t.Setenv("EMS_TEST_TOKEN", "tok-123")
	var gotAuth string
	var gotReq BatchRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&gotReq); err != nil {
			t.Errorf("decode: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"duplicate":false,"accepted":{"records":2,"control_records":1,"events":1}}`))
	}))
	defer srv.Close()

	c := NewUplinkClient(uplinkCfg(srv.URL))
	resp, err := c.SendBatch(context.Background(), BatchRequest{
		BatchID: "b-1", SiteID: "ab", EdgeID: "iot2050-test", SentAt: time.Now(),
		Records:        []json.RawMessage{[]byte(`{"a":1}`), []byte(`{"a":2}`)},
		ControlRecords: []json.RawMessage{[]byte(`{"c":1}`)},
		Events:         []json.RawMessage{[]byte(`{"e":1}`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer tok-123" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	if gotReq.BatchID != "b-1" || len(gotReq.Records) != 2 {
		t.Fatalf("server saw %+v", gotReq)
	}
	if resp.Accepted.Records != 2 || resp.Duplicate {
		t.Fatalf("resp = %+v", resp)
	}
}

func TestUplinkSendBatchErrorStatus(t *testing.T) {
	t.Setenv("EMS_TEST_TOKEN", "tok-123")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := NewUplinkClient(uplinkCfg(srv.URL))
	if _, err := c.SendBatch(context.Background(), BatchRequest{BatchID: "b", SiteID: "ab"}); err == nil {
		t.Fatal("expected error on HTTP 500")
	}
}

func TestFetchManifestETagFlow(t *testing.T) {
	t.Setenv("EMS_TEST_TOKEN", "tok-123")
	manifest := `{"schema_version":"lite-1","manifest_id":"ab-1","site_id":"ab","mode":"shadow","preset":"self_consumption"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("site_id") != "ab" {
			http.Error(w, "bad site", http.StatusBadRequest)
			return
		}
		if r.Header.Get("If-None-Match") == `"ab-1"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"ab-1"`)
		_, _ = w.Write([]byte(manifest))
	}))
	defer srv.Close()

	c := NewUplinkClient(uplinkCfg(srv.URL))
	m, err := c.FetchManifest(context.Background(), "ab", "")
	if err != nil || m == nil {
		t.Fatalf("first fetch: %v %v", m, err)
	}
	if m.ManifestID != "ab-1" {
		t.Fatalf("manifest_id = %s", m.ManifestID)
	}
	m2, err := c.FetchManifest(context.Background(), "ab", "ab-1")
	if err != nil || m2 != nil {
		t.Fatalf("304 fetch must return nil: %v %v", m2, err)
	}
}

func TestBackoffLadder(t *testing.T) {
	want := []time.Duration{
		10 * time.Second, 30 * time.Second, time.Minute,
		5 * time.Minute, 5 * time.Minute,
	}
	for i, w := range want {
		if got := backoffFor(i + 1); got != w {
			t.Fatalf("backoffFor(%d) = %s, want %s", i+1, got, w)
		}
	}
	if backoffFor(0) != 0 {
		t.Fatal("no failures → no backoff")
	}
}
