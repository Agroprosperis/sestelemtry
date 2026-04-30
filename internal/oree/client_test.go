package oree

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestBuildDAMURL(t *testing.T) {
	c := NewClient("https://www.oree.com.ua/", 0, "")
	got := c.BuildDAMURL(time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC), 2)
	want := "https://www.oree.com.ua/index.php/PXS/downloadxlsx/01.05.2026/DAM/2"
	if got != want {
		t.Fatalf("BuildDAMURL: got %q, want %q", got, want)
	}
}

func TestDownloadDAM_RetriesOnNonOK(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls < 3 {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/vnd.ms-excel")
		w.Write(oleHeader())
	}))
	t.Cleanup(srv.Close)

	c := NewClient(srv.URL, time.Second, "")
	body, _, err := c.DownloadDAM(context.Background(), time.Date(2026, 4, 29, 0, 0, 0, 0, time.UTC), 2, 5, 0)
	if err != nil {
		t.Fatalf("DownloadDAM: %v", err)
	}
	if !looksLikeOLE2(body) {
		t.Fatalf("response did not pass OLE2 magic check")
	}
	if calls != 3 {
		t.Fatalf("expected 3 attempts, got %d", calls)
	}
}

func TestDownloadDAM_RejectsNonOLE2(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("<html>404 not found</html>"))
	}))
	t.Cleanup(srv.Close)

	c := NewClient(srv.URL, time.Second, "")
	_, _, err := c.DownloadDAM(context.Background(), time.Date(2026, 4, 29, 0, 0, 0, 0, time.UTC), 2, 1, 0)
	if err == nil {
		t.Fatal("expected error for non-OLE2 body, got nil")
	}
}

func oleHeader() []byte {
	b := make([]byte, 64)
	copy(b, []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1})
	return b
}
