package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/nesh/sestelemetry/internal/askoe"
)

const maxAskoeUploadBytes = 32 << 20

// askoeImport handles POST /api/v1/askoe/import?organization_id=
// with a multipart file field named "file" (.xls, .zip, or .7z).
// Live Modbus and FusionSolar days are never overwritten. After a
// successful write the handler recomputes economics for the imported
// span so month/year/payback pick the new days up.
func (h *Handlers) askoeImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.askoeImporter == nil {
		h.log.Warn("api_askoe_import_unconfigured")
		http.Error(w, "askoe import not available", http.StatusServiceUnavailable)
		return
	}
	orgID := strings.TrimSpace(r.URL.Query().Get("organization_id"))
	if orgID == "" {
		http.Error(w, "organization_id is required", http.StatusBadRequest)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxAskoeUploadBytes+4096)
	if err := r.ParseMultipartForm(maxAskoeUploadBytes); err != nil {
		http.Error(w, "file is required (.xls, .zip or .7z, max 32 MiB)", http.StatusBadRequest)
		return
	}
	file, hdr, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "file is required (.xls, .zip or .7z)", http.StatusBadRequest)
		return
	}
	defer file.Close()
	payload, err := io.ReadAll(io.LimitReader(file, maxAskoeUploadBytes+1))
	if err != nil {
		http.Error(w, "failed to read upload", http.StatusBadRequest)
		return
	}
	if len(payload) > maxAskoeUploadBytes {
		http.Error(w, "upload exceeds 32 MiB", http.StatusRequestEntityTooLarge)
		return
	}
	filename := filepath.Base(hdr.Filename)
	files, err := askoe.ExtractWorkbooks(filename, payload)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if rc := http.NewResponseController(w); rc != nil {
		_ = rc.SetWriteDeadline(time.Time{})
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	enc := json.NewEncoder(w)
	flusher, _ := w.(http.Flusher)
	emit := func(ev progressEvent) {
		_ = enc.Encode(ev)
		if flusher != nil {
			flusher.Flush()
		}
	}

	onProgress := func(done, total int, label string) {
		emit(progressEvent{Type: "progress", Done: done, Total: total, Label: label})
	}

	start := time.Now()
	result, err := h.askoeImporter(r.Context(), orgID, files, onProgress)
	dur := time.Since(start)
	if err != nil {
		if errors.Is(err, context.Canceled) || r.Context().Err() != nil {
			h.log.Info("api_askoe_import_cancelled", "organization_id", orgID, "duration_ms", dur.Milliseconds())
			emit(progressEvent{Type: "error", Error: "import cancelled"})
			return
		}
		h.log.Error("api_askoe_import", "organization_id", orgID, "file", filename, "duration_ms", dur.Milliseconds(), "err", err)
		emit(progressEvent{Type: "error", Error: err.Error()})
		return
	}

	if ir, ok := result.(*askoe.ImportResult); ok && ir.DaysWritten > 0 && h.economics != nil && ir.From != "" && ir.To != "" {
		econ, eerr := h.economics.RecomputeRange(r.Context(), orgID, ir.From, ir.To, "Europe/Kyiv", func(done, total int, label string) {
			emit(progressEvent{Type: "progress", Done: done, Total: total, Label: "економіка " + label})
		})
		if eerr != nil {
			if r.Context().Err() != nil {
				h.log.Info("api_askoe_import_cancelled", "organization_id", orgID, "phase", "economics")
				emit(progressEvent{Type: "error", Error: "import cancelled"})
				return
			}
			ir.Warnings = append(ir.Warnings, fmt.Sprintf("economics recompute: %s", eerr.Error()))
		} else {
			ir.EconomicsDaysOK = econ.DaysOK
			ir.EconomicsDaysFailed = econ.DaysFailed
			for _, de := range econ.Errors {
				ir.Warnings = append(ir.Warnings, fmt.Sprintf("%s: %s", de.Date, de.Error))
			}
		}
	}

	h.log.Info("api_askoe_import_ok", "organization_id", orgID, "file", filename, "duration_ms", dur.Milliseconds())
	emit(progressEvent{Type: "done", Result: result})
}
