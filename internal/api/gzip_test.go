package api

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGzipCompressesJSON(t *testing.T) {
	h := NewHandlers(&mockStore{}, "*")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/organizations", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)

	res := rec.Result()
	if got := res.Header.Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", got)
	}
	if got := res.Header.Get("Content-Length"); got != "" {
		t.Errorf("Content-Length = %q, want empty (chunked)", got)
	}
	if !strings.Contains(res.Header.Get("Vary"), "Accept-Encoding") {
		t.Errorf("Vary = %q, want to contain Accept-Encoding", res.Header.Get("Vary"))
	}
	gr, err := gzip.NewReader(res.Body)
	if err != nil {
		t.Fatalf("gzip.NewReader: %v", err)
	}
	body, err := io.ReadAll(gr)
	if err != nil {
		t.Fatalf("read gzip body: %v", err)
	}
	if !strings.Contains(string(body), "organizations") {
		t.Errorf("decompressed body = %q, want it to contain organizations", string(body))
	}
}

func TestGzipSkippedWithoutAcceptEncoding(t *testing.T) {
	h := NewHandlers(&mockStore{}, "*")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/organizations", nil)
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)

	res := rec.Result()
	if got := res.Header.Get("Content-Encoding"); got != "" {
		t.Fatalf("Content-Encoding = %q, want empty (no gzip requested)", got)
	}
	body, _ := io.ReadAll(res.Body)
	if !strings.Contains(string(body), "organizations") {
		t.Errorf("body = %q, want it to contain organizations", string(body))
	}
}

func TestIsCompressibleContentType(t *testing.T) {
	cases := []struct {
		ct   string
		want bool
	}{
		{"application/json", true},
		{"text/csv; charset=utf-8", true},
		{"text/plain", true},
		{"application/x-ndjson", false},
		{"text/event-stream", false},
		{"image/png", false},
		{"application/gzip", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := isCompressibleContentType(tc.ct); got != tc.want {
			t.Errorf("isCompressibleContentType(%q) = %v, want %v", tc.ct, got, tc.want)
		}
	}
}
