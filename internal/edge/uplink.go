package edge

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// BatchRequest is the edge→cloud payload of POST /api/v1/edge/batch.
// One endpoint carries all three streams; the server treats the whole
// batch as one idempotent unit keyed by batch_id.
type BatchRequest struct {
	BatchID        string            `json:"batch_id"`
	SiteID         string            `json:"site_id"`
	EdgeID         string            `json:"edge_id"`
	SentAt         time.Time         `json:"sent_at"`
	Records        []json.RawMessage `json:"records,omitempty"`
	ControlRecords []json.RawMessage `json:"control_records,omitempty"`
	Events         []json.RawMessage `json:"events,omitempty"`
}

// BatchResponse reports what the cloud accepted. Duplicate batches
// (batch_id already processed) return Duplicate=true and are treated
// as success by the edge — the rows were delivered earlier.
type BatchResponse struct {
	Duplicate bool `json:"duplicate"`
	Accepted  struct {
		Records        int `json:"records"`
		ControlRecords int `json:"control_records"`
		Events         int `json:"events"`
	} `json:"accepted"`
}

// Heartbeat is POST /api/v1/edge/heartbeat (spec §7.3). Health carries
// the diagnostics snapshot (§8.3); old clouds ignore the extra field.
type Heartbeat struct {
	SiteID          string          `json:"site_id"`
	EdgeID          string          `json:"edge_id"`
	Status          string          `json:"status"`
	BufferPending   int64           `json:"buffer_pending"`
	LastSLPollOK    *time.Time      `json:"last_sl_poll_ok,omitempty"`
	FirmwareVersion string          `json:"firmware_version"`
	Health          json.RawMessage `json:"health,omitempty"`
}

// UplinkClient talks to the sestelemetry cloud API with a per-site
// Bearer token. Retry pacing lives in the caller (Service) so the
// backoff state is shared across batch attempts.
type UplinkClient struct {
	baseURL       string
	batchPath     string
	heartbeatPath string
	manifestPath  string
	token         string
	http          *http.Client
}

func NewUplinkClient(cfg UplinkConfig) *UplinkClient {
	return &UplinkClient{
		baseURL:       strings.TrimRight(cfg.BaseURL, "/"),
		batchPath:     cfg.BatchPath,
		heartbeatPath: cfg.HeartbeatPath,
		manifestPath:  cfg.ManifestPath,
		token:         cfg.Token(),
		http:          &http.Client{Timeout: cfg.HTTPTimeout},
	}
}

func (c *UplinkClient) SendBatch(ctx context.Context, b BatchRequest) (BatchResponse, error) {
	var resp BatchResponse
	err := c.postJSON(ctx, c.batchPath, b, &resp)
	return resp, err
}

func (c *UplinkClient) SendHeartbeat(ctx context.Context, hb Heartbeat) error {
	return c.postJSON(ctx, c.heartbeatPath, hb, nil)
}

// FetchManifest polls the cloud manifest. etag carries the currently
// applied manifest_id; the server answers 304 when nothing newer
// exists and 404 when no manifest was ever published for the site.
func (c *UplinkClient) FetchManifest(ctx context.Context, siteID, etag string) (*Manifest, error) {
	u := c.baseURL + c.manifestPath + "?site_id=" + url.QueryEscape(siteID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	c.auth(req)
	if etag != "" {
		req.Header.Set("If-None-Match", `"`+etag+`"`)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, res.Body)
		_ = res.Body.Close()
	}()
	switch res.StatusCode {
	case http.StatusOK:
		var m Manifest
		if err := json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(&m); err != nil {
			return nil, fmt.Errorf("manifest decode: %w", err)
		}
		return &m, nil
	case http.StatusNotModified, http.StatusNotFound:
		return nil, nil
	default:
		return nil, fmt.Errorf("manifest fetch: HTTP %d", res.StatusCode)
	}
}

func (c *UplinkClient) postJSON(ctx context.Context, path string, body any, out any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	c.auth(req)
	res, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, res.Body)
		_ = res.Body.Close()
	}()
	if res.StatusCode < 200 || res.StatusCode > 299 {
		msg, _ := io.ReadAll(io.LimitReader(res.Body, 512))
		return fmt.Errorf("%s: HTTP %d: %s", path, res.StatusCode, strings.TrimSpace(string(msg)))
	}
	if out != nil {
		if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(out); err != nil {
			return fmt.Errorf("%s: decode response: %w", path, err)
		}
	}
	return nil
}

func (c *UplinkClient) auth(req *http.Request) {
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
}

// newBatchID returns a random 128-bit hex id.
func newBatchID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("t-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

// uplinkBackoff is the spec's retry ladder (§7.2).
var uplinkBackoff = []time.Duration{10 * time.Second, 30 * time.Second, time.Minute, 5 * time.Minute}

func backoffFor(failures int) time.Duration {
	if failures <= 0 {
		return 0
	}
	idx := failures - 1
	if idx >= len(uplinkBackoff) {
		idx = len(uplinkBackoff) - 1
	}
	return uplinkBackoff[idx]
}
