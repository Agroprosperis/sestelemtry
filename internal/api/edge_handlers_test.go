package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func edgeTestHandlers(t *testing.T, e *EdgeIngest) *Handlers {
	t.Helper()
	h := NewHandlers(nil, "*")
	if e != nil {
		e.Log = slog.Default()
		h.SetEdgeIngest(e)
	}
	return h
}

func TestEdgeEndpointsUnconfigured(t *testing.T) {
	h := edgeTestHandlers(t, nil)
	for _, path := range []string{"/api/v1/edge/batch", "/api/v1/edge/heartbeat"} {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader("{}"))
		rec := httptest.NewRecorder()
		h.Router().ServeHTTP(rec, req)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s: status = %d, want 503", path, rec.Code)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/edge/manifest?site_id=ab", nil)
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("manifest: status = %d, want 503", rec.Code)
	}
}

func TestEdgeHeartbeatRequestKeepsHealthRaw(t *testing.T) {
	// The §8.3 snapshot must pass through to storage byte-identical —
	// the cloud never re-interprets edge diagnostics.
	raw := `{"site_id":"ze","edge_id":"iot2050-ze-001","status":"online",
		"health":{"ts":"2026-09-01T12:00:00Z","ok":false,"checks":[{"id":"pcs","ok":false,"severity":"alarm"}]}}`
	var req edgeHeartbeatRequest
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		t.Fatal(err)
	}
	if len(req.Health) == 0 {
		t.Fatal("health field lost during decode")
	}
	var h struct {
		OK     bool `json:"ok"`
		Checks []struct {
			ID string `json:"id"`
		} `json:"checks"`
	}
	if err := json.Unmarshal(req.Health, &h); err != nil {
		t.Fatal(err)
	}
	if h.OK || len(h.Checks) != 1 || h.Checks[0].ID != "pcs" {
		t.Fatalf("health round-trip broken: %s", req.Health)
	}
	// Old builds without the field → empty raw, nothing stored.
	var old edgeHeartbeatRequest
	if err := json.Unmarshal([]byte(`{"site_id":"ze"}`), &old); err != nil {
		t.Fatal(err)
	}
	if len(old.Health) != 0 {
		t.Fatalf("health = %q, want empty for old heartbeats", old.Health)
	}
}

func TestEdgeBatchRejectsBadAuth(t *testing.T) {
	h := edgeTestHandlers(t, &EdgeIngest{Tokens: map[string]string{"ab": "secret-token"}})

	// Wrong method.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/edge/batch", nil)
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET batch: status = %d, want 405", rec.Code)
	}

	// Missing site_id/batch_id.
	req = httptest.NewRequest(http.MethodPost, "/api/v1/edge/batch", strings.NewReader(`{}`))
	rec = httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty body: status = %d, want 400", rec.Code)
	}

	body := `{"batch_id":"b1","site_id":"ab"}`

	// No token.
	req = httptest.NewRequest(http.MethodPost, "/api/v1/edge/batch", strings.NewReader(body))
	rec = httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token: status = %d, want 401", rec.Code)
	}

	// Wrong token.
	req = httptest.NewRequest(http.MethodPost, "/api/v1/edge/batch", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer wrong")
	rec = httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token: status = %d, want 401", rec.Code)
	}

	// Token of another site.
	body = `{"batch_id":"b1","site_id":"ze"}`
	req = httptest.NewRequest(http.MethodPost, "/api/v1/edge/batch", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer secret-token")
	rec = httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("foreign site: status = %d, want 401", rec.Code)
	}
}

func TestEdgeManifestValidation(t *testing.T) {
	h := edgeTestHandlers(t, &EdgeIngest{Tokens: map[string]string{"ab": "secret-token"}})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/edge/manifest", nil)
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing site_id: status = %d, want 400", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/edge/manifest?site_id=ab", nil)
	req.Header.Set("Authorization", "Bearer nope")
	rec = httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("bad token: status = %d, want 401", rec.Code)
	}
}

func TestParseEdgeTokens(t *testing.T) {
	got := ParseEdgeTokens("ab=tok1, ze:tok2 ,bad,=x,y=")
	if len(got) != 2 || got["ab"] != "tok1" || got["ze"] != "tok2" {
		t.Fatalf("ParseEdgeTokens = %v", got)
	}
	if len(ParseEdgeTokens("")) != 0 {
		t.Fatal("empty input must produce no tokens")
	}
}
