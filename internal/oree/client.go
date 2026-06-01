package oree

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client downloads DAM XLS files from the OREE public website.
type Client struct {
	BaseURL   string
	HTTP      *http.Client
	UserAgent string
}

// NewClient returns a Client with a configured timeout and reasonable defaults.
func NewClient(baseURL string, timeout time.Duration, userAgent string) *Client {
	if baseURL == "" {
		baseURL = "https://www.oree.com.ua"
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if userAgent == "" {
		userAgent = "sestelemetry-dam/1.0"
	}
	return &Client{
		BaseURL:   strings.TrimRight(baseURL, "/"),
		HTTP:      &http.Client{Timeout: timeout},
		UserAgent: userAgent,
	}
}

// BuildDAMURL returns the canonical OREE DAM download URL for the given delivery date and zone.
// The path component is "downloadxlsx" but the response body is legacy OLE2 (.xls).
func (c *Client) BuildDAMURL(day time.Time, zone int) string {
	return fmt.Sprintf("%s/index.php/PXS/downloadxlsx/%s/DAM/%d",
		c.BaseURL, day.Format("02.01.2006"), zone)
}

// DownloadDAM fetches the XLS payload for a given delivery date and zone, with retry.
// Returns the full bytes and the URL that was hit. The response is validated by checking
// for the OLE2 magic header (D0 CF 11 E0). attempts ≤ 0 means a single try; backoff ≤ 0
// means no delay between retries.
func (c *Client) DownloadDAM(ctx context.Context, day time.Time, zone, attempts int, backoff time.Duration) ([]byte, string, error) {
	url := c.BuildDAMURL(day, zone)
	if attempts <= 0 {
		attempts = 1
	}

	var lastErr error
	for i := 0; i < attempts; i++ {
		if i > 0 && backoff > 0 {
			select {
			case <-ctx.Done():
				return nil, url, ctx.Err()
			case <-time.After(backoff):
			}
		}
		body, err := c.tryDownload(ctx, url)
		if err == nil {
			return body, url, nil
		}
		// Context cancellation/deadline is terminal: stop immediately
		// instead of burning the remaining retry budget (which can be
		// minutes of backoff) during shutdown or request timeout.
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, url, err
		}
		// Permanent HTTP failures (4xx other than 429) won't recover by
		// retrying — a bad date/zone would otherwise block the whole
		// attempts*backoff window. 5xx and 429 stay retryable.
		if !isRetryable(err) {
			return nil, url, fmt.Errorf("oree: download %s failed (non-retryable): %w", url, err)
		}
		lastErr = err
	}
	return nil, url, fmt.Errorf("oree: download %s failed after %d attempts: %w", url, attempts, lastErr)
}

func (c *Client) tryDownload(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.UserAgent)
	req.Header.Set("Accept", "application/vnd.ms-excel,application/octet-stream,*/*")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, &statusError{Status: resp.StatusCode}
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	if !looksLikeOLE2(body) {
		return nil, errors.New("response body is not an OLE2 file (wrong magic header)")
	}
	return body, nil
}

// statusError carries the HTTP status of a non-200 OREE response so the
// retry loop can distinguish transient (5xx, 429) from permanent (other
// 4xx) failures.
type statusError struct {
	Status int
}

func (e *statusError) Error() string {
	return fmt.Sprintf("status %d", e.Status)
}

// isRetryable reports whether a failed tryDownload is worth retrying.
// HTTP 4xx (except 429 Too Many Requests) is permanent; everything else
// (5xx, network errors, malformed OLE2 payloads that may be transient
// upstream glitches) is retried.
func isRetryable(err error) bool {
	var se *statusError
	if errors.As(err, &se) {
		if se.Status == http.StatusTooManyRequests {
			return true
		}
		return se.Status < 400 || se.Status >= 500
	}
	return true
}

// looksLikeOLE2 checks for the compound-document magic bytes D0 CF 11 E0 A1 B1 1A E1.
func looksLikeOLE2(b []byte) bool {
	magic := []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}
	return len(b) >= len(magic) && bytes.Equal(b[:len(magic)], magic)
}
