package api

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nesh/sestelemetry/internal/askoe"
)

func TestAskoeImportRejectsNonPost(t *testing.T) {
	h := NewHandlers(&mockStore{}, "*")
	h.SetAskoeImporter(func(context.Context, string, []askoe.WorkbookFile, func(int, int, string)) (any, error) {
		t.Fatal("importer should not run on GET")
		return nil, nil
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/askoe/import?organization_id=ze", nil)
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("want 405 got %d", rec.Code)
	}
}

func TestAskoeImportUnconfigured(t *testing.T) {
	h := NewHandlers(&mockStore{}, "*")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/askoe/import?organization_id=ze", nil)
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503 got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestAskoeImportRequiresOrg(t *testing.T) {
	h := NewHandlers(&mockStore{}, "*")
	h.SetAskoeImporter(func(context.Context, string, []askoe.WorkbookFile, func(int, int, string)) (any, error) {
		t.Fatal("importer should not run without org")
		return nil, nil
	})
	req := multipartAskoe(t, "/api/v1/askoe/import", "dump.xls", []byte("x"))
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400 got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestAskoeImportRequiresFile(t *testing.T) {
	h := NewHandlers(&mockStore{}, "*")
	h.SetAskoeImporter(func(context.Context, string, []askoe.WorkbookFile, func(int, int, string)) (any, error) {
		t.Fatal("importer should not run without file")
		return nil, nil
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/askoe/import?organization_id=ze", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400 got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestAskoeImportSuccessPassesOrgAndFiles(t *testing.T) {
	xls, err := os.ReadFile(filepath.Join("..", "askoe", "testdata", "aug2024_import.xls"))
	if err != nil {
		t.Fatal(err)
	}
	var gotOrg string
	var gotFiles int
	h := NewHandlers(&mockStore{}, "*")
	h.SetAskoeImporter(func(_ context.Context, org string, files []askoe.WorkbookFile, _ func(int, int, string)) (any, error) {
		gotOrg = org
		gotFiles = len(files)
		return &askoe.ImportResult{OrganizationID: org, FilesRead: len(files), DaysWritten: 0}, nil
	})
	req := multipartAskoe(t, "/api/v1/askoe/import?organization_id=ze", "08 Погодинна А+ ЕЕ РУ-10 Серпень ЖЕ.xls", xls)
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 got %d body=%s", rec.Code, rec.Body.String())
	}
	if gotOrg != "ze" {
		t.Errorf("org = %q, want ze", gotOrg)
	}
	if gotFiles != 1 {
		t.Errorf("files = %d, want 1", gotFiles)
	}
	resp := lastResultFromStream(t, rec.Body.Bytes())
	if resp["organization_id"] != "ze" {
		t.Errorf("response org = %v", resp["organization_id"])
	}
}

func multipartAskoe(t *testing.T, url, filename string, payload []byte) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, url, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req
}
