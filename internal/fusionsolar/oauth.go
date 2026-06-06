package fusionsolar

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultOAuthBase is the FusionSolar OAuth server (see the handoff
// doc). Huawei flagged a DNS quirk: oauth2.fusionsolar.huawei.com must
// resolve to the European OAuth IP — keep the hostname in the URL so
// TLS/host routing stay correct.
const DefaultOAuthBase = "https://oauth2.fusionsolar.huawei.com"

// oauthTokenPath is Huawei's OAuth2 token endpoint (Northbound
// Interface Reference). It handles both the authorization-code and
// refresh-token grants.
const oauthTokenPath = "/rest/dp/uidm/oauth2/v1/token"

// DefaultClientID is the OAuth client registered for this deployment
// (handoff doc). Overridable per request.
const DefaultClientID = "602196255"

// TokenResult is the subset of the OAuth token response we care about.
// FusionSolar usually returns a rotated RefreshToken alongside the new
// access token — surface it so the operator can persist it.
type TokenResult struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    int
	Scope        string
}

// RefreshAccessToken exchanges a long-lived refresh token for a
// short-lived bearer access token via the FusionSolar OAuth server,
// exactly as the handoff describes (grant_type=refresh_token with
// client_id + client_secret). This is the only supported way to use a
// refresh token: the data API (device/history) accepts access tokens
// only.
//
// clientID falls back to DefaultClientID and oauthBase to
// DefaultOAuthBase when empty; clientSecret and refreshToken are
// required.
func RefreshAccessToken(
	ctx context.Context,
	httpClient *http.Client,
	oauthBase, clientID, clientSecret, refreshToken string,
) (TokenResult, error) {
	refreshToken = strings.TrimSpace(refreshToken)
	if refreshToken == "" {
		return TokenResult{}, fmt.Errorf("fusionsolar: missing refresh_token")
	}
	clientSecret = strings.TrimSpace(clientSecret)
	if clientSecret == "" {
		return TokenResult{}, fmt.Errorf("fusionsolar: missing client_secret")
	}
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		clientID = DefaultClientID
	}
	base := strings.TrimRight(strings.TrimSpace(oauthBase), "/")
	if base == "" {
		base = DefaultOAuthBase
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}

	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)
	form.Set("client_id", clientID)
	form.Set("client_secret", clientSecret)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+oauthTokenPath, strings.NewReader(form.Encode()))
	if err != nil {
		return TokenResult{}, fmt.Errorf("fusionsolar: build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return TokenResult{}, fmt.Errorf("fusionsolar: token request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return TokenResult{}, fmt.Errorf("fusionsolar: read token response: %w", err)
	}

	var parsed struct {
		AccessToken      string `json:"access_token"`
		RefreshToken     string `json:"refresh_token"`
		ExpiresIn        int    `json:"expires_in"`
		Scope            string `json:"scope"`
		TokenType        string `json:"token_type"`
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
		FailCode         int    `json:"failCode"`
		Message          string `json:"message"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return TokenResult{}, fmt.Errorf("fusionsolar: decode token response: %w (body: %s)", err, snippet(body))
	}
	if resp.StatusCode != http.StatusOK || parsed.AccessToken == "" {
		msg := firstNonEmpty(parsed.ErrorDescription, parsed.Error, parsed.Message, snippet(body))
		return TokenResult{}, fmt.Errorf("fusionsolar: token refresh failed (HTTP %d failCode=%d): %s", resp.StatusCode, parsed.FailCode, msg)
	}
	return TokenResult{
		AccessToken:  parsed.AccessToken,
		RefreshToken: parsed.RefreshToken,
		ExpiresIn:    parsed.ExpiresIn,
		Scope:        parsed.Scope,
	}, nil
}

// NewResolvingHTTPClient returns an HTTP client dedicated to the OAuth
// token call that connects to a fixed IP while leaving the TLS SNI /
// certificate hostname intact (net/http derives ServerName from the
// request URL, not from the dialed address). Use it when DNS routes
// oauth2.fusionsolar.huawei.com to the wrong regional cluster (which
// answers invalid_client); pin Huawei's European OAuth IP instead.
//
// When pinnedIP is empty it returns a plain client (no pinning), so
// callers can pass the env value straight through.
func NewResolvingHTTPClient(pinnedIP string, timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	pinnedIP = strings.TrimSpace(pinnedIP)
	if pinnedIP == "" {
		return &http.Client{Timeout: timeout}
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				_, port, err := net.SplitHostPort(addr)
				if err != nil {
					port = "443"
				}
				return dialer.DialContext(ctx, network, net.JoinHostPort(pinnedIP, port))
			},
			ForceAttemptHTTP2:   true,
			TLSHandshakeTimeout: 10 * time.Second,
		},
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}
