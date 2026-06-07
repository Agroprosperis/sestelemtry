package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestFusionImportRejectsNonPost(t *testing.T) {
	h := NewHandlers(&mockStore{}, "*")
	h.SetFusionSolarImporter(func(context.Context, string, string, string, time.Time, time.Time, FusionProgressFunc) (any, error) {
		t.Fatal("importer should not run on GET")
		return nil, nil
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/fusionsolar/import?organization_id=ab&from=2026-06-02T00:00:00Z&to=2026-06-03T00:00:00Z", nil)
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("want 405 got %d", rec.Code)
	}
}

func TestFusionImportUnconfigured(t *testing.T) {
	h := NewHandlers(&mockStore{}, "*")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/fusionsolar/import?organization_id=ab&from=2026-06-02T00:00:00Z&to=2026-06-03T00:00:00Z", strings.NewReader(`{"access_token":"t"}`))
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503 got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestFusionImportRequiresOrg(t *testing.T) {
	h := NewHandlers(&mockStore{}, "*")
	h.SetFusionSolarImporter(func(context.Context, string, string, string, time.Time, time.Time, FusionProgressFunc) (any, error) {
		t.Fatal("importer should not run without org")
		return nil, nil
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/fusionsolar/import?from=2026-06-02T00:00:00Z&to=2026-06-03T00:00:00Z", strings.NewReader(`{"access_token":"t"}`))
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400 got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestFusionImportRequiresDates(t *testing.T) {
	h := NewHandlers(&mockStore{}, "*")
	h.SetFusionSolarImporter(func(context.Context, string, string, string, time.Time, time.Time, FusionProgressFunc) (any, error) {
		t.Fatal("importer should not run without dates")
		return nil, nil
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/fusionsolar/import?organization_id=ab", strings.NewReader(`{"access_token":"t"}`))
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400 got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestFusionImportRejectsBackwardsRange(t *testing.T) {
	h := NewHandlers(&mockStore{}, "*")
	h.SetFusionSolarImporter(func(context.Context, string, string, string, time.Time, time.Time, FusionProgressFunc) (any, error) {
		t.Fatal("importer should not run on inverted range")
		return nil, nil
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/fusionsolar/import?organization_id=ab&from=2026-06-03T00:00:00Z&to=2026-06-02T00:00:00Z", strings.NewReader(`{"access_token":"t"}`))
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400 got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestFusionImportRequiresToken(t *testing.T) {
	h := NewHandlers(&mockStore{}, "*")
	h.SetFusionSolarImporter(func(context.Context, string, string, string, time.Time, time.Time, FusionProgressFunc) (any, error) {
		t.Fatal("importer should not run without a token")
		return nil, nil
	})
	// Valid query params but empty body — token missing.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/fusionsolar/import?organization_id=ab&from=2026-04-29T00:00:00Z&to=2026-04-30T00:00:00Z", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400 got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "access_token") || !strings.Contains(rec.Body.String(), "refresh_token") {
		t.Fatalf("expected token hint, got %q", rec.Body.String())
	}
}

// TestFusionImportRejectsAfterCutoff guards the live-data boundary: a
// window whose `to` crosses the archive cutoff (2026-05-01) must be
// rejected with 400 before the importer runs, so real data can never
// be overwritten.
func TestFusionImportRejectsAfterCutoff(t *testing.T) {
	h := NewHandlers(&mockStore{}, "*")
	h.SetFusionSolarImporter(func(context.Context, string, string, string, time.Time, time.Time, FusionProgressFunc) (any, error) {
		t.Fatal("importer must not run for a post-cutoff window")
		return nil, nil
	})
	// to = 2026-05-02 is past the cutoff -> forbidden.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/fusionsolar/import?organization_id=ab&from=2026-04-30T00:00:00Z&to=2026-05-02T00:00:00Z", strings.NewReader(`{"access_token":"t"}`))
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400 got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "archive import forbidden") {
		t.Fatalf("expected cutoff hint, got %q", rec.Body.String())
	}
}

// TestFusionImportAllowsToEqualCutoff verifies the half-open boundary:
// to == cutoff covers up to but excluding the live region and is
// allowed.
func TestFusionImportAllowsToEqualCutoff(t *testing.T) {
	ran := false
	h := NewHandlers(&mockStore{}, "*")
	h.SetFusionSolarImporter(func(context.Context, string, string, string, time.Time, time.Time, FusionProgressFunc) (any, error) {
		ran = true
		return map[string]any{"rows_written": 0}, nil
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/fusionsolar/import?organization_id=ab&from=2026-04-30T00:00:00Z&to=2026-05-01T00:00:00Z", strings.NewReader(`{"access_token":"t"}`))
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 got %d body=%s", rec.Code, rec.Body.String())
	}
	if !ran {
		t.Fatal("importer should have run for to == cutoff")
	}
}

func TestFusionImportUpstreamFailure(t *testing.T) {
	h := NewHandlers(&mockStore{}, "*")
	h.SetFusionSolarImporter(func(context.Context, string, string, string, time.Time, time.Time, FusionProgressFunc) (any, error) {
		return nil, errors.New("fusionsolar: device/history failCode=305 token expired")
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/fusionsolar/import?organization_id=ab&from=2026-04-29T00:00:00Z&to=2026-04-30T00:00:00Z", strings.NewReader(`{"access_token":"t"}`))
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	// Validation/auth passed, so the handler has switched to the NDJSON
	// stream (HTTP 200); an upstream failure surfaces as an "error"
	// event carrying the verbatim cause, not a non-200 status.
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 (streaming) got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"type":"error"`) ||
		!strings.Contains(rec.Body.String(), "failCode=305") {
		t.Fatalf("expected an error event with the upstream cause, got %q", rec.Body.String())
	}
}

func TestFusionImportSuccessPassesParams(t *testing.T) {
	var gotOrg, gotToken, gotBase string
	var gotFrom, gotTo time.Time
	h := NewHandlers(&mockStore{}, "*")
	h.SetFusionSolarImporter(func(_ context.Context, org, token, base string, from, to time.Time, _ FusionProgressFunc) (any, error) {
		gotOrg, gotToken, gotBase, gotFrom, gotTo = org, token, base, from, to
		return map[string]any{"organization_id": org, "rows_written": 42}, nil
	})
	body := `{"access_token":"secret-token","api_base":"https://eu5.fusionsolar.huawei.com"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/fusionsolar/import?organization_id=sm&from=2026-04-29T00:00:00Z&to=2026-04-30T00:00:00Z", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 got %d body=%s", rec.Code, rec.Body.String())
	}
	if gotOrg != "sm" {
		t.Errorf("org = %q, want sm", gotOrg)
	}
	if gotToken != "secret-token" {
		t.Errorf("token = %q, want secret-token", gotToken)
	}
	if gotBase != "https://eu5.fusionsolar.huawei.com" {
		t.Errorf("api_base = %q", gotBase)
	}
	wantFrom := time.Date(2026, 4, 29, 0, 0, 0, 0, time.UTC)
	wantTo := time.Date(2026, 4, 30, 0, 0, 0, 0, time.UTC)
	if !gotFrom.Equal(wantFrom) || !gotTo.Equal(wantTo) {
		t.Errorf("range from=%v to=%v, want %v..%v", gotFrom, gotTo, wantFrom, wantTo)
	}
	// The handler streams NDJSON; the final "done" line carries the result.
	resp := lastResultFromStream(t, rec.Body.Bytes())
	if resp["organization_id"] != "sm" {
		t.Errorf("response org = %v", resp["organization_id"])
	}
}

// lastResultFromStream parses an NDJSON import stream and returns the
// `result` object of the terminating "done" event (or fails the test).
func lastResultFromStream(t *testing.T, body []byte) map[string]any {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(string(body)))
	for {
		var ev struct {
			Type   string         `json:"type"`
			Error  string         `json:"error"`
			Result map[string]any `json:"result"`
		}
		if err := dec.Decode(&ev); err != nil {
			break
		}
		if ev.Type == "error" {
			t.Fatalf("stream error event: %s", ev.Error)
		}
		if ev.Type == "done" {
			return ev.Result
		}
	}
	t.Fatalf("no done event in stream: %s", body)
	return nil
}

// TestFusionImportTokenNotInQuery is a guard that the handler reads the
// token from the body, not the query string (so it can't leak into
// access logs). A token passed only in the query must be rejected.
func TestFusionImportTokenNotInQuery(t *testing.T) {
	h := NewHandlers(&mockStore{}, "*")
	h.SetFusionSolarImporter(func(context.Context, string, string, string, time.Time, time.Time, FusionProgressFunc) (any, error) {
		t.Fatal("importer should not run when token only in query")
		return nil, nil
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/fusionsolar/import?organization_id=ab&from=2026-06-02T00:00:00Z&to=2026-06-03T00:00:00Z&access_token=leaked", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400 got %d body=%s", rec.Code, rec.Body.String())
	}
}
