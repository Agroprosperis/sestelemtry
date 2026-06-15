package api

import (
	"compress/gzip"
	"io"
	"net/http"
	"strings"
	"sync"
)

// gzipWriterPool recycles gzip.Writers across requests so a busy export
// endpoint doesn't allocate (and GC) a fresh ~64 KiB compressor per
// response. Writers are Reset onto the real ResponseWriter on borrow
// and returned on Close.
var gzipWriterPool = sync.Pool{
	New: func() any {
		// BestSpeed keeps the streaming CSV/JSON path CPU-cheap: telemetry
		// exports are highly repetitive (repeated metric_keys, metadata,
		// labels JSON) so even level 1 compresses ~10x, and we'd rather
		// not burn CPU buffering a multi-MB download at higher levels.
		gw, _ := gzip.NewWriterLevel(io.Discard, gzip.BestSpeed)
		return gw
	},
}

// isCompressibleContentType reports whether a response body of the given
// Content-Type benefits from gzip and is safe to buffer through the
// compressor. We intentionally exclude:
//   - application/x-ndjson and text/event-stream: these are live
//     progress streams (DAM/FusionSolar/economics) that must flush
//     unbuffered, line by line; compressing them adds latency for no
//     real size win.
//   - already-compressed binaries (images, archives, gzip): re-gzipping
//     wastes CPU and usually grows the payload.
//
// An empty Content-Type is treated as non-compressible: handlers in this
// package always set the type before writing, so an empty value means an
// implicit body we'd rather pass through untouched than risk corrupting
// net/http's content sniffing.
func isCompressibleContentType(ct string) bool {
	ct = strings.ToLower(strings.TrimSpace(ct))
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = strings.TrimSpace(ct[:i])
	}
	switch ct {
	case "":
		return false
	case "text/event-stream", "application/x-ndjson":
		return false
	case "application/json",
		"application/javascript",
		"application/xml",
		"image/svg+xml",
		"application/yaml",
		"application/x-yaml":
		return true
	}
	return strings.HasPrefix(ct, "text/")
}

// gzipResponseWriter lazily compresses the response. It defers the
// compress-or-passthrough decision until WriteHeader (or the first
// Write, which triggers an implicit WriteHeader) so the handler's
// Content-Type — set just before it starts writing — drives the choice.
//
// It implements http.Flusher so streaming handlers keep flushing (gzip
// flush point + underlying flush) and Unwrap so http.ResponseController
// can still reach the underlying writer for SetWriteDeadline on the
// long-running export streams.
type gzipResponseWriter struct {
	http.ResponseWriter
	gz          *gzip.Writer
	wroteHeader bool
	compress    bool
}

func (w *gzipResponseWriter) WriteHeader(status int) {
	if w.wroteHeader {
		w.ResponseWriter.WriteHeader(status)
		return
	}
	w.wroteHeader = true

	header := w.ResponseWriter.Header()
	// Bodyless / informational responses never carry content worth
	// compressing, and a Content-Encoding on a 204/304 would be wrong.
	bodyless := status < http.StatusOK ||
		status == http.StatusNoContent ||
		status == http.StatusNotModified
	if !bodyless &&
		header.Get("Content-Encoding") == "" &&
		isCompressibleContentType(header.Get("Content-Type")) {
		w.compress = true
		// Content-Length no longer matches the compressed body; drop it so
		// net/http falls back to chunked transfer encoding.
		header.Del("Content-Length")
		header.Set("Content-Encoding", "gzip")
		header.Add("Vary", "Accept-Encoding")
		gz := gzipWriterPool.Get().(*gzip.Writer)
		gz.Reset(w.ResponseWriter)
		w.gz = gz
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *gzipResponseWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if w.compress {
		return w.gz.Write(b)
	}
	return w.ResponseWriter.Write(b)
}

func (w *gzipResponseWriter) Flush() {
	if w.compress && w.gz != nil {
		// Flush the compressor first so its buffered bytes reach the
		// underlying writer, then flush that to the client.
		_ = w.gz.Flush()
	}
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap exposes the underlying writer to http.ResponseController so the
// streaming export handlers can still clear their write deadline.
func (w *gzipResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// close flushes and returns the gzip.Writer to the pool. Safe to call
// when compression was never activated.
func (w *gzipResponseWriter) close() {
	if w.gz == nil {
		return
	}
	_ = w.gz.Close()
	gzipWriterPool.Put(w.gz)
	w.gz = nil
}

// withGzip compresses eligible responses when the client advertises
// gzip support. HEAD and OPTIONS carry no body, so they bypass the
// wrapper entirely.
func (h *Handlers) withGzip(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead ||
			r.Method == http.MethodOptions ||
			!strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			next.ServeHTTP(w, r)
			return
		}
		gw := &gzipResponseWriter{ResponseWriter: w}
		defer gw.close()
		next.ServeHTTP(gw, r)
	})
}
