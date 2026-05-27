package dam

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nesh/sestelemetry/internal/oree"
)

// fakePoolNonNil returns an opaque pgxpool.Pool pointer that is
// non-nil but cannot be operated on. The failure-mode tests below
// short-circuit before any storage call, so the pool is never
// dereferenced — we only need a non-nil pointer to clear the
// guard in FetchAndStore.
func fakePoolNonNil() *pgxpool.Pool {
	return &pgxpool.Pool{}
}

func TestFetchAndStore_NilClient(t *testing.T) {
	_, err := FetchAndStore(context.Background(), nil, nil, nil, time.Now(), 2, 1, 0)
	if err == nil {
		t.Fatal("expected error on nil client, got nil")
	}
	if !strings.Contains(err.Error(), "nil oree client") {
		t.Fatalf("unexpected err: %v", err)
	}
}

func TestFetchAndStore_NilPool(t *testing.T) {
	// Real client but nil pool: we should bail BEFORE touching the
	// network so the operator gets a clear "wiring is broken" error
	// without burning OREE traffic on a config bug.
	client := oree.NewClient("https://example.invalid", time.Second, "")
	_, err := FetchAndStore(context.Background(), nil, client, nil, time.Now(), 2, 1, 0)
	if err == nil {
		t.Fatal("expected error on nil pool, got nil")
	}
	if !strings.Contains(err.Error(), "nil pool") {
		t.Fatalf("unexpected err: %v", err)
	}
}

func TestFetchAndStore_OREE502SurfacesAsDownloadError(t *testing.T) {
	// Upstream returns 5xx — DownloadDAM exhausts its (single)
	// attempt and FetchAndStore wraps the error with a `dam:
	// download:` prefix so the caller can pattern-match the
	// failure stage.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusBadGateway)
	}))
	t.Cleanup(srv.Close)

	client := oree.NewClient(srv.URL, time.Second, "")
	// fakePool isn't dereferenced because we fail before the upsert,
	// but we still need a non-nil pointer to clear the nil-pool guard.
	_, err := FetchAndStore(
		context.Background(),
		nil, // log
		client,
		fakePoolNonNil(),
		time.Date(2026, 5, 25, 0, 0, 0, 0, time.UTC),
		2,
		1, // single attempt — operator-facing endpoint mode
		0,
	)
	if err == nil {
		t.Fatal("expected download error, got nil")
	}
	if !strings.Contains(err.Error(), "dam: download:") {
		t.Fatalf("expected wrapped download error, got: %v", err)
	}
}

func TestFetchAndStore_NonOLE2BodySurfacesAsDownloadError(t *testing.T) {
	// Upstream returns 200 with an HTML error page (a real OREE
	// failure mode when the unit is down for maintenance). The
	// OLE2 magic check inside DownloadDAM catches it, so we
	// surface as `dam: download:` rather than `dam: parse:` —
	// callers can then decide whether to retry.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("<html>service unavailable</html>"))
	}))
	t.Cleanup(srv.Close)

	client := oree.NewClient(srv.URL, time.Second, "")
	_, err := FetchAndStore(
		context.Background(),
		nil,
		client,
		fakePoolNonNil(),
		time.Date(2026, 5, 25, 0, 0, 0, 0, time.UTC),
		2,
		1,
		0,
	)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "dam: download:") {
		t.Fatalf("expected wrapped download error, got: %v", err)
	}
}
