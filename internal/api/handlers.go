package api

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/nesh/sestelemetry/internal/fusionsolar"
)

type Handlers struct {
	store          storeReader
	allowOrigin    string
	log            *slog.Logger
	organizations  []OrganizationInfo
	energyFlowOrgs map[string]EnergyFlowOrg
	// damFetcher backs POST /api/v1/dam-prices/refresh. nil when the
	// API process was started without an `oree:` config block — the
	// handler responds 503 in that case so operators get a clear
	// "service not configured" instead of a confusing 500.
	damFetcher DAMFetcher
	// damDefaultZone is the zone the refresh handler uses when the
	// request omits `zone=`. Mirrors the GET /api/v1/dam-prices
	// default (2 = unified UA grid) but kept as a config-derived
	// field so a deployment with only zone=1 can refresh without
	// passing the param every time.
	damDefaultZone int
	// fusionImporter backs POST /api/v1/fusionsolar/import. Always
	// installed at startup — the operator supplies the FusionSolar
	// access token (or refresh token) and optional API base in the
	// request body from the import page.
	fusionImporter FusionSolarImporter
	// fusionClientID / fusionClientSecret / fusionOAuthBase are the
	// server-side OAuth client used to exchange a refresh token for an
	// access token. They let the import page ask for ONLY the refresh
	// token; the fixed app secret never leaves the server. A value in
	// the request body still overrides these.
	fusionClientID     string
	fusionClientSecret string
	fusionOAuthBase    string
	fusionOAuthResolve string
	fusionRefreshToken string
	fusionAPIBase      string
	// fusionOAuthClient optionally pins the OAuth host to a specific
	// IP (DNS-misroute workaround); nil uses the default client.
	fusionOAuthClient *http.Client
}

// FusionSolarImporter synchronously pulls historical device data from
// the FusionSolar Northbound API for one organization + window using
// the per-request access token / API base, normalizes the cumulative
// counters into telemetry_samples, and returns a JSON-serializable
// summary. The closure is constructed in cmd/api/main.go from
// internal/fusionsolar so the handler stays decoupled from the HTTP
// client / pgxpool wiring (and tests can inject a fake). Errors are
// surfaced verbatim to the operator.
type FusionSolarImporter func(ctx context.Context, organizationID, accessToken, apiBase string, from, to time.Time, onProgress FusionProgressFunc) (any, error)

// FusionProgressFunc is invoked by the importer after each 24h window so
// the handler can stream a progress feed. nil is passed when no client
// is listening for progress.
type FusionProgressFunc func(done, total int)

// progressEvent is one line of the NDJSON stream the long-running import
// handlers emit (Content-Type: application/x-ndjson). Type is one of
// "progress" | "done" | "error"; the frontend updates a progress bar on
// "progress", renders the summary on "done", and shows the message on
// "error". Streaming lets the operator watch a month/year backfill and
// keeps the connection from idling out behind a proxy.
type progressEvent struct {
	Type   string `json:"type"`
	Done   int    `json:"done,omitempty"`
	Total  int    `json:"total,omitempty"`
	Label  string `json:"label,omitempty"`
	Error  string `json:"error,omitempty"`
	Result any    `json:"result,omitempty"`
}

// DAMFetcher synchronously pulls one day's DAM XLS, parses it, and
// upserts the resulting rows into market_dam_prices. The closure is
// constructed in cmd/api/main.go from internal/dam.FetchAndStore so
// the handler stays decoupled from oree.Client / pgxpool wiring (the
// tests just inject a fake function). The returned int is the number
// of rows written; errors are surfaced verbatim to the operator via
// the response body so they know which OREE attempt blew up.
type DAMFetcher func(ctx context.Context, deliveryDate time.Time, zone int) (int, error)

type storeReader interface {
	Current(ctx context.Context, organizationID string, metricKeys []string, at time.Time) (CurrentResponse, error)
	Timeseries(ctx context.Context, organizationID string, metricKeys []string, from, to time.Time, bucket, tz string, aggregation TimeseriesAggregation) (TimeseriesResponse, error)
	EnergySummary(ctx context.Context, organizationID string, metricKeys []string, from, to time.Time) (EnergySummaryResponse, error)
	DAMPrices(ctx context.Context, zone int, from, to time.Time) (DAMPricesResponse, error)
	WeatherForecast(ctx context.Context, organizationID string, from, to time.Time) (WeatherForecastResponse, error)
	Samples(ctx context.Context, organizationID string, metricKeys []string, from, to time.Time, limit int, emit func(SampleRow) error) (int, bool, error)
	EnergyFlowSources(ctx context.Context, organizationID string, from, to time.Time, lookback time.Duration) ([]EnergyFlowRawRow, error)
	// GetOrgTariffs reports the persisted economics tariff bundle for
	// `organizationID`. The bool is false when the org has never saved
	// tariffs (the handler maps that to 404 so the frontend can fall
	// back to bundled defaults).
	GetOrgTariffs(ctx context.Context, organizationID string) (OrgTariffs, bool, error)
	// UpsertOrgTariffs replaces the persisted tariff bundle for
	// `organizationID` (last-writer-wins).
	UpsertOrgTariffs(ctx context.Context, organizationID string, tariffs OrgTariffs) error
	Ready(ctx context.Context) error
}

// Limits for the raw-samples export. These are deliberately on the
// generous side — most analyst use cases sit well under them — but
// they exist to keep a misclick on a multi-month range from streaming
// gigabytes through the API server. The handler returns 400 when a
// request would exceed them so the user fixes their query rather than
// silently receiving truncated data.
const (
	defaultSamplesLimit = 100_000
	// maxSamplesLimit caps the rows we'll stream for a single export
	// to keep memory + bandwidth bounded. The 5M cap pairs with the
	// matching RAW_SAMPLES_LIMIT in web/src/dashboard/customExport.ts
	// and survives a typical "all columns × 2 devices × 7-day window
	// at 1 s polling" pull (~13M rows worst case still truncates,
	// but the cap now buys ~2.5 days of full-fidelity data instead
	// of the previous ~12-15 hours).
	maxSamplesLimit      = 5_000_000
	maxSamplesRange      = 31 * 24 * time.Hour
	maxSamplesMetricKeys = 20
	// maxTimeseriesRange caps the explicit window for the bucketed
	// timeseries endpoint. Year presets need a generous bound, but an
	// open-ended multi-year span would scan the raw hypertable for any
	// sub-day bucket. 366 days covers the widest dashboard preset.
	maxTimeseriesRange = 366 * 24 * time.Hour
	// maxDAMRange caps the DAM price date span; mirrors the spirit of
	// the weather endpoint's bound to keep result sets sane.
	maxDAMRange = 366 * 24 * time.Hour
	// maxFusionImportRange caps a single archive-import request. The
	// importer loops one FusionSolar device/history call per device per
	// 24h window, so a year is ~365 windows × ~5 devices of sequential
	// upstream calls — bounded here to keep one click from launching an
	// unbounded multi-hour job inside a synchronous request.
	maxFusionImportRange = 366 * 24 * time.Hour
)

func NewHandlers(store storeReader, allowOrigin string) *Handlers {
	return &Handlers{
		store:       store,
		allowOrigin: allowOrigin,
		log:         slog.Default(),
	}
}

// SetEnergyFlowOrgs registers the per-org device→role mapping the
// on-the-fly energy-flow summary uses to classify raw rows from
// telemetry_samples back into PV / ESS / single buckets.
// cmd/api/main.go calls this after loading the YAML config so the
// API server can match each row's device_host label to a configured
// Modbus endpoint and apply the same DetectRole rule the collector
// used at poll time.
//
// Orgs missing from the map fall through to "no device map" which
// the on-the-fly compute treats as a single SmartLogger — rows with
// an empty device_host label still pass through as RoleSingle so
// legacy single-device deployments keep working without a config.
func (h *Handlers) SetEnergyFlowOrgs(orgs []EnergyFlowOrg) {
	if len(orgs) == 0 {
		h.energyFlowOrgs = nil
		return
	}
	out := make(map[string]EnergyFlowOrg, len(orgs))
	for _, o := range orgs {
		out[o.ID] = o
	}
	h.energyFlowOrgs = out
}

// SetDAMFetcher installs the on-demand DAM refresh closure that
// POST /api/v1/dam-prices/refresh dispatches to. `defaultZone` is
// the zone used when a refresh request omits `zone=`; pass the
// same value the dam-collector daemon uses (typically 2 = unified
// UA grid) so manual refreshes line up with the scheduled fetch.
//
// Calling with a nil fetcher removes a previously-installed one,
// which is what cmd/api/main.go does when the loaded config no
// longer has `oree.enabled: true`. The route stays registered
// either way — the handler checks for a nil fetcher per-request
// and returns 503.
func (h *Handlers) SetDAMFetcher(fetcher DAMFetcher, defaultZone int) {
	h.damFetcher = fetcher
	if defaultZone > 0 {
		h.damDefaultZone = defaultZone
	}
}

// SetFusionSolarImporter installs the on-demand archive-import closure
// that POST /api/v1/fusionsolar/import dispatches to. Calling with a
// nil importer removes a previously-installed one; the route stays
// registered either way and the handler checks for a nil importer
// per-request and returns 503.
func (h *Handlers) SetFusionSolarImporter(importer FusionSolarImporter) {
	h.fusionImporter = importer
}

// SetFusionSolarOAuth configures the server-side OAuth client used to
// exchange a refresh token for an access token, so the import page can
// collect only the refresh token. clientID and oauthBase fall back to
// the package defaults when empty; an empty clientSecret simply means
// the operator must supply one in the request body.
// firstNonEmpty returns the first trimmed-non-empty string, or "".
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

// FusionSolarDefaults are the server-side connection defaults (from the
// separate fusionsolar.yaml). Any field the import request body sets
// overrides the matching default; secrets are never echoed back to the
// client.
type FusionSolarDefaults struct {
	RefreshToken string
	ClientID     string
	ClientSecret string
	OAuthBase    string
	OAuthResolve string
	APIBase      string
}

// SetFusionSolarDefaults installs the server-side connection defaults so
// the import page can leave credential fields blank. When OAuthResolve
// is set it pins the OAuth host IP (DNS-misroute workaround) for imports
// that don't override it in the body.
func (h *Handlers) SetFusionSolarDefaults(d FusionSolarDefaults) {
	h.fusionClientID = strings.TrimSpace(d.ClientID)
	h.fusionClientSecret = strings.TrimSpace(d.ClientSecret)
	h.fusionOAuthBase = strings.TrimSpace(d.OAuthBase)
	h.fusionOAuthResolve = strings.TrimSpace(d.OAuthResolve)
	h.fusionRefreshToken = strings.TrimSpace(d.RefreshToken)
	h.fusionAPIBase = strings.TrimSpace(d.APIBase)
	if h.fusionOAuthResolve != "" {
		h.fusionOAuthClient = fusionsolar.NewResolvingHTTPClient(h.fusionOAuthResolve, 30*time.Second)
	} else {
		h.fusionOAuthClient = nil
	}
}

// SetOrganizations replaces the organization metadata served by
// /api/v1/organizations. The slice is shallow-copied so the caller
// can mutate the source without affecting in-flight requests; nil /
// empty input means the endpoint returns `{"organizations": []}`.
func (h *Handlers) SetOrganizations(orgs []OrganizationInfo) {
	if len(orgs) == 0 {
		h.organizations = nil
		return
	}
	out := make([]OrganizationInfo, len(orgs))
	copy(out, orgs)
	h.organizations = out
}

func (h *Handlers) Router() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", h.healthz)
	mux.HandleFunc("/readyz", h.readyz)
	mux.HandleFunc("/api/v1/dashboard-config", h.dashboardConfig)
	mux.HandleFunc("/api/v1/organizations", h.organizationsList)
	mux.HandleFunc("/api/v1/current", h.current)
	mux.HandleFunc("/api/v1/timeseries", h.timeseries)
	mux.HandleFunc("/api/v1/samples", h.samples)
	mux.HandleFunc("/api/v1/registers", h.registers)
	mux.HandleFunc("/api/v1/energy-summary", h.energySummary)
	mux.HandleFunc("/api/v1/energy-flow-hourly", h.energyFlowHourly)
	mux.HandleFunc("/api/v1/dam-prices", h.damPrices)
	mux.HandleFunc("/api/v1/dam-prices/refresh", h.damPricesRefresh)
	mux.HandleFunc("/api/v1/dam-prices/refresh-range", h.damPricesRefreshRange)
	mux.HandleFunc("/api/v1/fusionsolar/import", h.fusionSolarImport)
	mux.HandleFunc("/api/v1/fusionsolar/config", h.fusionSolarConfig)
	mux.HandleFunc("/api/v1/weather-forecast", h.weatherForecast)
	mux.HandleFunc("/api/v1/organization-tariffs", h.organizationTariffs)
	mux.HandleFunc("/swagger", h.swaggerUI)
	mux.HandleFunc("/swagger/", h.swaggerUI)
	mux.HandleFunc("/swagger/openapi.yaml", h.swaggerSpec)
	return h.withSecurityHeaders(h.withCORS(mux))
}

func (h *Handlers) healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handlers) readyz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := h.store.Ready(ctx); err != nil {
		h.log.Error("api_readyz", "err", err)
		http.Error(w, "not ready", http.StatusServiceUnavailable)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (h *Handlers) dashboardConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, DefaultDashboardConfig)
}

// organizationsList returns the public metadata for every configured
// organization (id, display name, optional location). The dashboard
// uses this to populate the org switcher and to look up coordinates
// for per-site features (e.g. the weather widget) without hard-coding
// them in the frontend.
func (h *Handlers) organizationsList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	resp := OrganizationsResponse{Organizations: h.organizations}
	if resp.Organizations == nil {
		// Always emit an explicit empty array so JSON consumers can
		// iterate without a nil-check.
		resp.Organizations = []OrganizationInfo{}
	}
	writeJSON(w, http.StatusOK, resp)
}

// registers serves the static metric_key → Modbus register map used
// by the dashboard's export dialog to annotate CSV headers (e.g.
// `active_pv_power_kw_40388`) and by analysts cross-referencing the
// vendor datasheet. The body is small (~20 entries) so we ship it as
// one JSON object rather than paginating.
func (h *Handlers) registers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, RegistersResponse{Metadata: ModbusRegisterMetadata})
}

func (h *Handlers) current(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	orgID := strings.TrimSpace(r.URL.Query().Get("organization_id"))
	if orgID == "" {
		http.Error(w, "organization_id is required", http.StatusBadRequest)
		return
	}
	metricKeys := ParseCSV(r.URL.Query().Get("metric_keys"))
	at, err := parseAt(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	resp, err := h.store.Current(r.Context(), orgID, metricKeys, at)
	if err != nil {
		h.log.Error("api_current", "organization_id", orgID, "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// parseAt reads the optional `at` query param (RFC3339). When omitted, the
// caller receives the zero time which signals "latest" to the store.
func parseAt(r *http.Request) (time.Time, error) {
	raw := strings.TrimSpace(r.URL.Query().Get("at"))
	if raw == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("at must be RFC3339 timestamp")
	}
	return t, nil
}

func (h *Handlers) timeseries(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	orgID := strings.TrimSpace(r.URL.Query().Get("organization_id"))
	if orgID == "" {
		http.Error(w, "organization_id is required", http.StatusBadRequest)
		return
	}
	metricKeys := ParseCSV(r.URL.Query().Get("metric_key"))
	if len(metricKeys) == 0 {
		metricKeys = ParseCSV(r.URL.Query().Get("metric_keys"))
	}
	if len(metricKeys) == 0 {
		http.Error(w, "metric_key or metric_keys is required", http.StatusBadRequest)
		return
	}

	from, to, bucket, tz, err := parseRange(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// Validate tz here (like /samples) so an unknown zone yields a clean
	// 400 instead of leaking a Postgres time_bucket error as a 500.
	if _, err := loadLocation(tz); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// When both bounds are explicit, reject inverted/oversized windows up
	// front. Omitted bounds fall back to the store's default window.
	if !from.IsZero() && !to.IsZero() {
		if !to.After(from) {
			http.Error(w, "to must be after from", http.StatusBadRequest)
			return
		}
		if to.Sub(from) > maxTimeseriesRange {
			http.Error(w, fmt.Sprintf("range must be <= %s", maxTimeseriesRange), http.StatusBadRequest)
			return
		}
	}
	aggregation, err := parseAggregation(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	start := time.Now()
	resp, err := h.store.Timeseries(r.Context(), orgID, metricKeys, from, to, bucket, tz, aggregation)
	dur := time.Since(start)
	if err != nil {
		h.log.Error("api_timeseries",
			"organization_id", orgID,
			"metric_keys", metricKeys,
			"bucket", bucket,
			"aggregation", string(aggregation),
			"duration_ms", dur.Milliseconds(),
			"err", err,
		)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	h.log.Info("api_timeseries_ok",
		"organization_id", orgID,
		"metric_keys", metricKeys,
		"bucket", bucket,
		"aggregation", string(aggregation),
		"points", len(resp.Points),
		"duration_ms", dur.Milliseconds(),
	)
	writeJSON(w, http.StatusOK, resp)
}

// samples streams the raw `telemetry_samples` rows that match the
// request as a CSV download. Used by the dashboard's "Експорт даних →
// Сирі дані" mode when the analyst wants per-poll values rather than
// the bucketed aggregates the timeseries endpoint produces.
//
// The body is a single CSV with header row
// `time,metric_key,value,labels` followed by one row per sample. We
// emit a UTF-8 BOM so Excel auto-detects the encoding (Cyrillic-only
// labels otherwise show as mojibake under a CP-1251 default install).
//
// On truncation a sentinel row prefixed with `__TRUNCATED__` is
// appended. This is the only signaling channel that survives
// `Content-Length: chunked` + browser `fetch()` (HTTP trailers are
// not exposed via the Fetch API), so the dashboard scans the last
// non-empty line of the downloaded blob to decide whether to warn
// the user.
func (h *Handlers) samples(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	orgID := strings.TrimSpace(r.URL.Query().Get("organization_id"))
	if orgID == "" {
		http.Error(w, "organization_id is required", http.StatusBadRequest)
		return
	}
	metricKeys := ParseCSV(r.URL.Query().Get("metric_key"))
	if len(metricKeys) == 0 {
		metricKeys = ParseCSV(r.URL.Query().Get("metric_keys"))
	}
	if len(metricKeys) == 0 {
		http.Error(w, "metric_key or metric_keys is required", http.StatusBadRequest)
		return
	}
	if len(metricKeys) > maxSamplesMetricKeys {
		http.Error(w, fmt.Sprintf("at most %d metric_keys are allowed", maxSamplesMetricKeys), http.StatusBadRequest)
		return
	}
	from, to, _, tz, err := parseRange(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if from.IsZero() || to.IsZero() {
		http.Error(w, "from and to are required", http.StatusBadRequest)
		return
	}
	if !to.After(from) {
		http.Error(w, "to must be after from", http.StatusBadRequest)
		return
	}
	if to.Sub(from) > maxSamplesRange {
		http.Error(w, fmt.Sprintf("range must be <= %s", maxSamplesRange), http.StatusBadRequest)
		return
	}
	// Streaming CSV responses can run for minutes on large multi-day
	// pulls; the server-wide WriteTimeout (configured in cmd/api/main.go
	// to keep idle/slow connections in check) would otherwise sever
	// the response mid-stream once it expires. The cut hits a row
	// boundary often enough that the client sees a clean-looking CSV
	// without our `__TRUNCATED__` sentinel, making the loss invisible.
	// We clear the deadline only for this handler; the request context
	// (`r.Context()`) still cancels the query when the client
	// disconnects so a hung browser doesn't tie up the cursor forever.
	if rc := http.NewResponseController(w); rc != nil {
		_ = rc.SetWriteDeadline(time.Time{})
	}
	// tz controls how each row's `time` column is rendered in the CSV.
	// The DB stores everything in UTC; analysts almost always want
	// human-local timestamps so date pickers like "2026-05-09" line up
	// with what they see in their tools. We default to UTC for
	// backwards compatibility but accept any IANA name the runtime
	// knows (Europe/Kyiv, America/New_York, …).
	loc, err := loadLocation(tz)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	limit, err := parseLimit(r, defaultSamplesLimit, maxSamplesLimit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	filename := fmt.Sprintf("samples_%s_%s_%s.csv",
		sanitizeFilenameSegment(orgID),
		from.UTC().Format("20060102T150405Z"),
		to.UTC().Format("20060102T150405Z"),
	)
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Sample-Limit", strconv.Itoa(limit))
	// Chunked encoding kicks in implicitly because we don't set
	// Content-Length; that lets us stream the rows out of the cursor
	// without buffering the entire result set in memory.
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte{0xEF, 0xBB, 0xBF})

	cw := csv.NewWriter(w)
	cw.UseCRLF = true
	// Column layout exposes the vendor metadata (modbus_register,
	// data_type, gain) right next to metric_key so analysts can spot
	// which physical register backed each sample without flipping
	// between the CSV and registers/huawei_smartlogger.yaml. Synthetic
	// metrics (none today, but reserved for future use) leave the meta
	// columns blank rather than guessing.
	_ = cw.Write([]string{
		"time",
		"metric_key",
		"modbus_register",
		"data_type",
		"gain",
		"value",
		"labels",
	})

	start := time.Now()
	rowsEmitted, truncated, err := h.store.Samples(
		r.Context(),
		orgID,
		metricKeys,
		from,
		to,
		limit,
		func(row SampleRow) error {
			var labels string
			if len(row.Labels) > 0 {
				if b, mErr := json.Marshal(row.Labels); mErr == nil {
					labels = string(b)
				}
			}
			meta, hasMeta := ModbusRegisterMetadata[row.MetricKey]
			var modbusReg, dataType, gain string
			// valuePrec defaults to -1 (shortest round-trip
			// representation) so synthetic / unknown metrics keep
			// the previous behavior. For known Modbus metrics we
			// switch to fixed-precision formatting derived from
			// the register's gain — see decimalsForGain — so float
			// noise like "15.600000000000001" doesn't leak into
			// the CSV. The displayed precision still covers the
			// real signal: gain=0.01 → 2 decimals (the smallest
			// raw-counter step is 0.01), gain=0.001 → 3, and so on.
			valuePrec := -1
			if hasMeta {
				modbusReg = strconv.Itoa(meta.Address)
				dataType = meta.DataType
				gain = strconv.FormatFloat(meta.Gain, 'f', -1, 64)
				if d, ok := decimalsForGain(meta.Gain); ok {
					valuePrec = d
				}
			}
			return cw.Write([]string{
				row.Time.In(loc).Format(time.RFC3339Nano),
				row.MetricKey,
				modbusReg,
				dataType,
				gain,
				strconv.FormatFloat(row.Value, 'f', valuePrec, 64),
				labels,
			})
		},
	)
	if err != nil {
		// We've already written the 200 + header row, so the response
		// can't be replaced with a clean 500. Log and stop; the client
		// will see a truncated download and either retry or report it.
		h.log.Error("api_samples",
			"organization_id", orgID,
			"metric_keys", metricKeys,
			"limit", limit,
			"rows", rowsEmitted,
			"err", err,
		)
		cw.Flush()
		return
	}
	if truncated {
		_ = cw.Write([]string{
			"__TRUNCATED__",
			"",
			"",
			"",
			"",
			strconv.Itoa(limit),
			fmt.Sprintf(`{"reason":"row_limit","limit":%d}`, limit),
		})
	}
	cw.Flush()

	dur := time.Since(start)
	h.log.Info("api_samples_ok",
		"organization_id", orgID,
		"metric_keys", metricKeys,
		"limit", limit,
		"rows", rowsEmitted,
		"truncated", truncated,
		"duration_ms", dur.Milliseconds(),
	)
}

// parseLimit reads the optional `limit` query param. Returns def when
// omitted; rejects non-positive values or anything above max.
func parseLimit(r *http.Request, def, max int) (int, error) {
	raw := strings.TrimSpace(r.URL.Query().Get("limit"))
	if raw == "" {
		return def, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return 0, fmt.Errorf("limit must be a positive integer")
	}
	if n > max {
		return 0, fmt.Errorf("limit must be <= %d", max)
	}
	return n, nil
}

// sanitizeFilenameSegment keeps the Content-Disposition filename safe
// to interpolate into both shells and Windows Explorer when the user
// saves the file. Anything outside `[A-Za-z0-9_-]` is replaced with an
// underscore so a hostile organization_id can't smuggle in a quote
// character that breaks the header.
func sanitizeFilenameSegment(s string) string {
	if s == "" {
		return "_"
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}

func (h *Handlers) energySummary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	orgID := strings.TrimSpace(r.URL.Query().Get("organization_id"))
	if orgID == "" {
		http.Error(w, "organization_id is required", http.StatusBadRequest)
		return
	}
	metricKeys := ParseCSV(r.URL.Query().Get("metric_key"))
	if len(metricKeys) == 0 {
		metricKeys = ParseCSV(r.URL.Query().Get("metric_keys"))
	}
	from, to, _, _, err := parseRange(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if from.IsZero() || to.IsZero() {
		http.Error(w, "from and to are required", http.StatusBadRequest)
		return
	}
	start := time.Now()
	resp, err := h.store.EnergySummary(r.Context(), orgID, metricKeys, from, to)
	if err != nil {
		h.log.Error("api_energy_summary",
			"organization_id", orgID,
			"metric_keys", metricKeys,
			"duration_ms", time.Since(start).Milliseconds(),
			"err", err,
		)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	// Effective set of metrics the caller asked for. The store
	// substitutes the default catalog for an empty list, but we
	// still need to know whether any synthetic flow key is in scope
	// to decide whether to populate resp.Flows.
	effectiveKeys := metricKeys
	if len(effectiveKeys) == 0 {
		effectiveKeys = EnergySummaryAccumulators
	}
	h.maybeAttachEnergyFlow(r.Context(), &resp, orgID, from, to, effectiveKeys)
	h.log.Info("api_energy_summary_ok",
		"organization_id", orgID,
		"metric_keys", metricKeys,
		"totals", len(resp.Totals),
		"flows", resp.Flows != nil,
		"duration_ms", time.Since(start).Milliseconds(),
	)
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handlers) damPrices(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	zone, err := parseZone(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	from, to, err := parseDateRange(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// Cap the span so a misclick on a multi-year range can't trigger an
	// unbounded scan of market_dam_prices.
	if to.Sub(from) > maxDAMRange {
		http.Error(w, fmt.Sprintf("range must be <= %s", maxDAMRange), http.StatusBadRequest)
		return
	}
	start := time.Now()
	resp, err := h.store.DAMPrices(r.Context(), zone, from, to)
	dur := time.Since(start)
	if err != nil {
		h.log.Error("api_dam_prices",
			"zone", zone,
			"from", from,
			"to", to,
			"duration_ms", dur.Milliseconds(),
			"err", err,
		)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	h.log.Info("api_dam_prices_ok",
		"zone", zone,
		"from", from,
		"to", to,
		"prices", len(resp.Prices),
		"duration_ms", dur.Milliseconds(),
	)
	writeJSON(w, http.StatusOK, resp)
}

// damPricesRefresh synchronously pulls one day's DAM prices from
// OREE and upserts them. Operator-driven escape hatch when the
// scheduled dam-collector either hasn't run yet or fetched too
// early (OREE published the file late, network blip, etc).
//
// The fetcher is invoked with a single attempt and no backoff so
// the operator sees the result within an HTTP timeout's worth of
// time; the dam-collector daemon already burns the multi-attempt
// retry budget on its own schedule. After a successful upsert the
// handler re-reads the date through the store and returns the same
// shape as GET /api/v1/dam-prices so the frontend can drop the
// response straight into its existing price-map plumbing without
// a second round-trip.
func (h *Handlers) damPricesRefresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.damFetcher == nil {
		h.log.Warn("api_dam_refresh_unconfigured")
		http.Error(w, "dam refresh not configured (oree section missing or disabled)", http.StatusServiceUnavailable)
		return
	}
	dateStr := strings.TrimSpace(r.URL.Query().Get("date"))
	if dateStr == "" {
		http.Error(w, "date is required (YYYY-MM-DD)", http.StatusBadRequest)
		return
	}
	date, err := time.Parse("2006-01-02", dateStr)
	if err != nil {
		http.Error(w, "date must be YYYY-MM-DD", http.StatusBadRequest)
		return
	}
	zone, err := parseRefreshZone(r, h.damDefaultZone)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	start := time.Now()
	rows, err := h.damFetcher(r.Context(), date, zone)
	dur := time.Since(start)
	if err != nil {
		h.log.Error("api_dam_refresh",
			"date", dateStr,
			"zone", zone,
			"duration_ms", dur.Milliseconds(),
			"err", err,
		)
		// 502 because the failure originated from upstream OREE
		// (download, parse) or the storage layer — the API itself
		// is healthy. Body carries the underlying err.Error() so
		// the operator can see "status 404" / "OLE2 magic check
		// failed" without grepping API logs.
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	resp, err := h.store.DAMPrices(r.Context(), zone, date, date)
	if err != nil {
		h.log.Error("api_dam_refresh_readback",
			"date", dateStr,
			"zone", zone,
			"duration_ms", dur.Milliseconds(),
			"err", err,
		)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	h.log.Info("api_dam_refresh_ok",
		"date", dateStr,
		"zone", zone,
		"rows_written", rows,
		"prices", len(resp.Prices),
		"duration_ms", dur.Milliseconds(),
	)
	writeJSON(w, http.StatusOK, resp)
}

// DAMDayError records a single day that failed during a range refresh
// so the operator can see which deliveries are missing (e.g. OREE never
// published that date) without the whole import aborting.
type DAMDayError struct {
	Date  string `json:"date"`
	Error string `json:"error"`
}

// DAMRefreshRangeResult summarizes a bulk DAM price import over a
// [from, to] inclusive date span. Per-day failures are tolerated and
// collected in Errors (capped) so a year-long backfill doesn't abort on
// a single missing publication.
type DAMRefreshRangeResult struct {
	From        string        `json:"from"`
	To          string        `json:"to"`
	Zone        int           `json:"zone"`
	Days        int           `json:"days"`
	DaysOK      int           `json:"days_ok"`
	DaysFailed  int           `json:"days_failed"`
	RowsWritten int           `json:"rows_written"`
	Errors      []DAMDayError `json:"errors,omitempty"`
}

// maxDAMRangeErrors caps the per-day error list in a range refresh so a
// pathological run (e.g. wrong zone → every day 404s) returns a bounded
// response instead of thousands of lines.
const maxDAMRangeErrors = 50

// damPricesRefreshRange pulls DAM prices for every delivery date in the
// inclusive [from, to] span and upserts them, looping day-by-day over
// the same single-attempt fetcher damPricesRefresh uses. Built for the
// import page so an operator can backfill a month or a year of РДН
// prices in one call. Per-day failures (a date OREE never published)
// are tolerated: they're counted and listed, but the run continues.
//
//	POST /api/v1/dam-prices/refresh-range?from=YYYY-MM-DD&to=YYYY-MM-DD&zone=2
func (h *Handlers) damPricesRefreshRange(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.damFetcher == nil {
		h.log.Warn("api_dam_refresh_range_unconfigured")
		http.Error(w, "dam refresh not configured (oree section missing or disabled)", http.StatusServiceUnavailable)
		return
	}
	fromStr := strings.TrimSpace(r.URL.Query().Get("from"))
	toStr := strings.TrimSpace(r.URL.Query().Get("to"))
	if fromStr == "" || toStr == "" {
		http.Error(w, "from and to are required (YYYY-MM-DD)", http.StatusBadRequest)
		return
	}
	from, err := time.Parse("2006-01-02", fromStr)
	if err != nil {
		http.Error(w, "from must be YYYY-MM-DD", http.StatusBadRequest)
		return
	}
	to, err := time.Parse("2006-01-02", toStr)
	if err != nil {
		http.Error(w, "to must be YYYY-MM-DD", http.StatusBadRequest)
		return
	}
	if to.Before(from) {
		http.Error(w, "to must be on or after from", http.StatusBadRequest)
		return
	}
	if to.Sub(from) > maxDAMRange {
		http.Error(w, fmt.Sprintf("range must be <= %s", maxDAMRange), http.StatusBadRequest)
		return
	}
	zone, err := parseRefreshZone(r, h.damDefaultZone)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// From here we stream NDJSON progress (one "progress" line per day,
	// a final "done"). A multi-day pull issues one sequential OREE
	// download per day, easily exceeding the 30s WriteTimeout; drop the
	// write deadline so the stream isn't truncated. The request context
	// bounds the work and is cancelled when the client aborts (cancel).
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

	// Total days in the inclusive [from, to] span, for the progress bar.
	totalDays := int(to.Sub(from)/(24*time.Hour)) + 1

	start := time.Now()
	result := DAMRefreshRangeResult{From: fromStr, To: toStr, Zone: zone}
	cancelled := false
	for day := from; !day.After(to); day = day.AddDate(0, 0, 1) {
		if err := r.Context().Err(); err != nil {
			cancelled = true
			break
		}
		result.Days++
		rows, ferr := h.damFetcher(r.Context(), day, zone)
		if ferr != nil {
			// A cancelled fetch isn't a "missing publication" — stop.
			if r.Context().Err() != nil {
				cancelled = true
				break
			}
			result.DaysFailed++
			if len(result.Errors) < maxDAMRangeErrors {
				result.Errors = append(result.Errors, DAMDayError{
					Date:  day.Format("2006-01-02"),
					Error: ferr.Error(),
				})
			}
		} else {
			result.DaysOK++
			result.RowsWritten += rows
		}
		emit(progressEvent{Type: "progress", Done: result.Days, Total: totalDays, Label: day.Format("2006-01-02")})
	}
	if cancelled {
		h.log.Info("api_dam_refresh_range_cancelled", "from", fromStr, "to", toStr, "zone", zone, "days", result.Days)
		emit(progressEvent{Type: "error", Error: "import cancelled"})
		return
	}
	h.log.Info("api_dam_refresh_range_ok",
		"from", fromStr,
		"to", toStr,
		"zone", zone,
		"days", result.Days,
		"days_ok", result.DaysOK,
		"days_failed", result.DaysFailed,
		"rows_written", result.RowsWritten,
		"duration_ms", time.Since(start).Milliseconds(),
	)
	emit(progressEvent{Type: "done", Result: result})
}

// fusionSolarConfig returns the non-secret server-side FusionSolar
// defaults so the import page can prefill its form and leave credential
// fields blank when the server already holds them. Secrets (refresh
// token, client secret) are never returned — only booleans indicating
// whether they're configured.
//
//	GET /api/v1/fusionsolar/config
func (h *Handlers) fusionSolarConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	resp := map[string]any{
		"client_id":                h.fusionClientID,
		"api_base":                 h.fusionAPIBase,
		"oauth_base":               h.fusionOAuthBase,
		"oauth_resolve":            h.fusionOAuthResolve,
		"refresh_token_configured": h.fusionRefreshToken != "",
		"client_secret_configured": h.fusionClientSecret != "",
	}
	writeJSON(w, http.StatusOK, resp)
}

// fusionSolarImport backfills historical telemetry for one
// organization from the FusionSolar Northbound API. It mirrors the
// damPricesRefresh shape: POST, query params, a structured JSON result,
// and upstream failures surfaced as 502 with the cause in the body.
//
//	POST /api/v1/fusionsolar/import?organization_id=ab&from=...&to=...
//	body: {"access_token": "...", "api_base": "https://eu5..."}
//
// `from` / `to` are RFC3339 timestamps. The access token (and optional
// API base) travel in the JSON body, not the query string, so they
// never land in access logs. A backfill can fetch many days of
// 5-minute data sequentially, so we clear the response write deadline
// (the server's 30s WriteTimeout would otherwise truncate a long
// import) — the request context still bounds the work if the client
// disconnects.
func (h *Handlers) fusionSolarImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.fusionImporter == nil {
		h.log.Warn("api_fusionsolar_import_unconfigured")
		http.Error(w, "fusionsolar import not available", http.StatusServiceUnavailable)
		return
	}
	orgID := strings.TrimSpace(r.URL.Query().Get("organization_id"))
	if orgID == "" {
		http.Error(w, "organization_id is required", http.StatusBadRequest)
		return
	}
	fromStr := strings.TrimSpace(r.URL.Query().Get("from"))
	toStr := strings.TrimSpace(r.URL.Query().Get("to"))
	if fromStr == "" || toStr == "" {
		http.Error(w, "from and to are required (RFC3339)", http.StatusBadRequest)
		return
	}
	from, err := time.Parse(time.RFC3339, fromStr)
	if err != nil {
		http.Error(w, "from must be an RFC3339 timestamp", http.StatusBadRequest)
		return
	}
	to, err := time.Parse(time.RFC3339, toStr)
	if err != nil {
		http.Error(w, "to must be an RFC3339 timestamp", http.StatusBadRequest)
		return
	}
	if !to.After(from) {
		http.Error(w, "to must be after from", http.StatusBadRequest)
		return
	}
	if to.Sub(from) > maxFusionImportRange {
		http.Error(w, fmt.Sprintf("range too wide: max %s per import", maxFusionImportRange), http.StatusBadRequest)
		return
	}
	// Safety guard: archive imports may not reach the live-data region.
	// The window is half-open [from, to), so to == cutoff is allowed.
	if to.After(fusionsolar.ArchiveCutoff) {
		http.Error(w, fmt.Sprintf("archive import forbidden on/after %s (live data) — set `to` no later than the cutoff", fusionsolar.ArchiveCutoff.Format(time.RFC3339)), http.StatusBadRequest)
		return
	}

	var body struct {
		AccessToken  string `json:"access_token"`
		APIBase      string `json:"api_base"`
		RefreshToken string `json:"refresh_token"`
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
		OAuthBase    string `json:"oauth_base"`
		OAuthResolve string `json:"oauth_resolve"`
	}
	if r.Body != nil {
		// Cap the body: a handful of short credential strings.
		dec := json.NewDecoder(io.LimitReader(r.Body, 1<<16))
		if err := dec.Decode(&body); err != nil && err != io.EOF {
			http.Error(w, "invalid JSON body", http.StatusBadRequest)
			return
		}
	}
	accessToken := strings.TrimSpace(body.AccessToken)
	// Request body overrides the server-side defaults (fusionsolar.yaml).
	refreshToken := firstNonEmpty(body.RefreshToken, h.fusionRefreshToken)
	apiBase := firstNonEmpty(body.APIBase, h.fusionAPIBase)

	// Two supported auth styles (per the FusionSolar handoff): a ready
	// access token, or a long-lived refresh token that we exchange for
	// one via the OAuth server. The data API accepts access tokens
	// only, so a refresh token must always go through this exchange.
	if accessToken == "" {
		if refreshToken == "" {
			http.Error(w, "provide a refresh_token (or an access_token)", http.StatusBadRequest)
			return
		}
		clientID := firstNonEmpty(body.ClientID, h.fusionClientID)
		clientSecret := firstNonEmpty(body.ClientSecret, h.fusionClientSecret)
		oauthBase := firstNonEmpty(body.OAuthBase, h.fusionOAuthBase)
		if clientSecret == "" {
			http.Error(w, "client_secret is not configured; set it in fusionsolar.yaml or pass client_secret", http.StatusBadRequest)
			return
		}
		// A resolve IP in the request body pins the OAuth host for this
		// import (DNS-misroute workaround), overriding the server-wide
		// configured client.
		oauthClient := h.fusionOAuthClient
		if rv := strings.TrimSpace(body.OAuthResolve); rv != "" {
			oauthClient = fusionsolar.NewResolvingHTTPClient(rv, 30*time.Second)
		}
		tok, err := fusionsolar.RefreshAccessToken(r.Context(), oauthClient, oauthBase, clientID, clientSecret, refreshToken)
		if err != nil {
			h.log.Error("api_fusionsolar_oauth", "organization_id", orgID, "err", err)
			// Upstream OAuth failure (bad/expired refresh token, wrong
			// secret) — surface verbatim so the operator sees the cause.
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		accessToken = tok.AccessToken
		h.log.Info("api_fusionsolar_oauth_ok", "organization_id", orgID, "expires_in", tok.ExpiresIn, "rotated_refresh", tok.RefreshToken != "")
	}

	// Everything above could still fail validation/auth with a proper
	// HTTP status. From here we stream NDJSON progress (status is 200
	// once the first byte is written), so the import itself runs under a
	// 200 with per-window "progress" lines and a final "done"/"error".
	// A multi-day import issues many sequential upstream requests well
	// past the 30s WriteTimeout; drop the write deadline so the stream
	// isn't truncated. The request context still bounds the work and is
	// cancelled when the client aborts (the operator's cancel button).
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

	onProgress := func(done, total int) {
		emit(progressEvent{Type: "progress", Done: done, Total: total})
	}

	start := time.Now()
	result, err := h.fusionImporter(r.Context(), orgID, accessToken, apiBase, from, to, onProgress)
	dur := time.Since(start)
	if err != nil {
		// Client cancellation is expected (operator hit cancel) — log it
		// at info, not error, but still surface it on the stream.
		if errors.Is(err, context.Canceled) || r.Context().Err() != nil {
			h.log.Info("api_fusionsolar_import_cancelled", "organization_id", orgID, "duration_ms", dur.Milliseconds())
			emit(progressEvent{Type: "error", Error: "import cancelled"})
			return
		}
		h.log.Error("api_fusionsolar_import",
			"organization_id", orgID,
			"from", fromStr,
			"to", toStr,
			"duration_ms", dur.Milliseconds(),
			"err", err,
		)
		emit(progressEvent{Type: "error", Error: err.Error()})
		return
	}
	h.log.Info("api_fusionsolar_import_ok",
		"organization_id", orgID,
		"from", fromStr,
		"to", toStr,
		"duration_ms", dur.Milliseconds(),
	)
	emit(progressEvent{Type: "done", Result: result})
}

// parseRefreshZone reads the optional `zone=` query param for the
// refresh handler. Defaults to `defaultZone` (configured via
// SetDAMFetcher) when omitted; falls back to 2 if no default was
// set so a misconfigured deployment still produces a valid zone
// instead of zero. Validation matches parseZone (1..99 inclusive).
func parseRefreshZone(r *http.Request, defaultZone int) (int, error) {
	raw := strings.TrimSpace(r.URL.Query().Get("zone"))
	if raw == "" {
		if defaultZone > 0 {
			return defaultZone, nil
		}
		return 2, nil
	}
	return parseZone(r)
}

// weatherForecast returns the cached Open-Meteo forecast for an
// organization in [from, to]. Range bounds are inclusive date values
// (YYYY-MM-DD); omitting them defaults to today..today+2d (today and
// the next two days, which spans the WeatherCard's "yesterday/today/
// tomorrow" anchor selector without needing a wide window).
//
// Empty hourly/daily arrays in the response mean "the collector
// hasn't populated this org / range yet" — the frontend treats it as
// a cue to fall back to Open-Meteo directly.
func (h *Handlers) weatherForecast(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	orgID := strings.TrimSpace(r.URL.Query().Get("organization_id"))
	if orgID == "" {
		http.Error(w, "organization_id is required", http.StatusBadRequest)
		return
	}
	from, to, err := parseWeatherDateRange(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// Expand `to` to the end of day so an "inclusive" date range
	// like from=2026-05-15&to=2026-05-15 returns all 24 hours of
	// 2026-05-15 (stored as `timestamptz` at hourly granularity).
	hourTo := to.Add(24*time.Hour - time.Nanosecond)

	start := time.Now()
	resp, err := h.store.WeatherForecast(r.Context(), orgID, from, hourTo)
	dur := time.Since(start)
	if err != nil {
		h.log.Error("api_weather_forecast",
			"organization_id", orgID,
			"from", from,
			"to", to,
			"duration_ms", dur.Milliseconds(),
			"err", err,
		)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	// Keep the response-level bounds at the original day-precision
	// values the caller asked for so clients can echo them back
	// verbatim. The hour expansion above is purely a SQL-side detail.
	resp.From = from.UTC()
	resp.To = to.UTC()
	h.log.Info("api_weather_forecast_ok",
		"organization_id", orgID,
		"from", from,
		"to", to,
		"hourly", len(resp.Hourly),
		"daily", len(resp.Daily),
		"duration_ms", dur.Milliseconds(),
	)
	writeJSON(w, http.StatusOK, resp)
}

// organizationTariffs serves GET / accepts PUT for the per-org
// economics-page tariff bundle. We use a single handler for both
// methods (rather than splitting into /org-tariffs and
// /org-tariffs/save) so the URL contract stays small and matches
// REST conventions: GET reads the resource, PUT replaces it.
//
// Reads return 404 when the org has never persisted a row so the
// frontend can deliberately fall back to bundled defaults instead of
// silently receiving an all-zeros struct (which would be a valid but
// nonsensical tariff). Writes are all-or-nothing: every field must
// pass validation or the row is left untouched and the caller gets a
// 400 explaining which field failed.
func (h *Handlers) organizationTariffs(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.getOrganizationTariffs(w, r)
	case http.MethodPut:
		h.putOrganizationTariffs(w, r)
	default:
		w.Header().Set("Allow", "GET, PUT")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handlers) getOrganizationTariffs(w http.ResponseWriter, r *http.Request) {
	orgID := strings.TrimSpace(r.URL.Query().Get("organization_id"))
	if orgID == "" {
		http.Error(w, "organization_id is required", http.StatusBadRequest)
		return
	}
	tariffs, ok, err := h.store.GetOrgTariffs(r.Context(), orgID)
	if err != nil {
		h.log.Error("api_org_tariffs_get", "organization_id", orgID, "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, "tariffs not found", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, tariffs)
}

func (h *Handlers) putOrganizationTariffs(w http.ResponseWriter, r *http.Request) {
	orgID := strings.TrimSpace(r.URL.Query().Get("organization_id"))
	if orgID == "" {
		http.Error(w, "organization_id is required", http.StatusBadRequest)
		return
	}
	// Cap the body so a malicious caller can't hand us a multi-MB
	// JSON blob just to trip validation. The actual payload is
	// ~9 small numbers, so 64 KiB is generous headroom.
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	defer r.Body.Close()
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var payload OrgTariffs
	if err := dec.Decode(&payload); err != nil {
		http.Error(w, fmt.Sprintf("invalid JSON body: %v", err), http.StatusBadRequest)
		return
	}
	if err := validateOrgTariffs(payload); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := h.store.UpsertOrgTariffs(r.Context(), orgID, payload); err != nil {
		h.log.Error("api_org_tariffs_put", "organization_id", orgID, "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	h.log.Info("api_org_tariffs_put_ok", "organization_id", orgID)
	w.WriteHeader(http.StatusNoContent)
}

// validateOrgTariffs checks each numeric field is finite and inside
// the documented physical range. We reject NaN / ±Inf explicitly
// because PostgreSQL's `jsonb` accepts them but the dashboard's
// formatters render them as "NaN UAH" and downstream calc panics on
// division. Bounds match the frontend's input guards so the API can
// never end up holding a tariff the UI itself wouldn't accept.
func validateOrgTariffs(t OrgTariffs) error {
	pairs := []struct {
		name string
		val  float64
	}{
		{"distribution_uah_per_kwh", t.DistributionUahPerKwh},
		{"transmission_uah_per_kwh", t.TransmissionUahPerKwh},
		{"supplier_margin_uah_per_kwh", t.SupplierMarginUahPerKwh},
		{"other_fees_uah_per_kwh", t.OtherFeesUahPerKwh},
		{"degradation_uah_per_kwh", t.DegradationUahPerKwh},
	}
	for _, p := range pairs {
		if math.IsNaN(p.val) || math.IsInf(p.val, 0) {
			return fmt.Errorf("%s must be a finite number", p.name)
		}
		if p.val < 0 {
			return fmt.Errorf("%s must be >= 0", p.name)
		}
	}
	if math.IsNaN(t.ExportDiscount) || math.IsInf(t.ExportDiscount, 0) ||
		t.ExportDiscount < 0 || t.ExportDiscount > 1 {
		return fmt.Errorf("export_discount must be in [0, 1]")
	}
	if math.IsNaN(t.VatRate) || math.IsInf(t.VatRate, 0) ||
		t.VatRate < 0 || t.VatRate > 1 {
		return fmt.Errorf("vat_rate must be in [0, 1]")
	}
	if math.IsNaN(t.EssCapacityKwh) || math.IsInf(t.EssCapacityKwh, 0) ||
		t.EssCapacityKwh <= 0 {
		return fmt.Errorf("ess_capacity_kwh must be > 0")
	}
	return nil
}

// parseWeatherDateRange reads `from` and `to` (YYYY-MM-DD) query
// params, defaulting to today..today+2d in UTC. The default window
// pairs with the WeatherCard's anchor selector (yesterday/today/
// tomorrow) and the typical Open-Meteo model horizon.
func parseWeatherDateRange(r *http.Request) (from, to time.Time, err error) {
	fromStr := strings.TrimSpace(r.URL.Query().Get("from"))
	toStr := strings.TrimSpace(r.URL.Query().Get("to"))
	now := time.Now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	if fromStr == "" {
		from = today
	} else {
		from, err = time.Parse("2006-01-02", fromStr)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("from must be YYYY-MM-DD")
		}
	}
	if toStr == "" {
		to = from.AddDate(0, 0, 2)
	} else {
		to, err = time.Parse("2006-01-02", toStr)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("to must be YYYY-MM-DD")
		}
	}
	if to.Before(from) {
		return time.Time{}, time.Time{}, fmt.Errorf("to must be on or after from")
	}
	const maxSpan = 31 * 24 * time.Hour
	if to.Sub(from) > maxSpan {
		return time.Time{}, time.Time{}, fmt.Errorf("date range must be <= 31 days")
	}
	return from, to, nil
}

// parseZone reads `zone` query param (1..99). Defaults to 2 (unified UA grid).
func parseZone(r *http.Request) (int, error) {
	raw := strings.TrimSpace(r.URL.Query().Get("zone"))
	if raw == "" {
		return 2, nil
	}
	var n int
	for _, ch := range raw {
		if ch < '0' || ch > '9' {
			return 0, fmt.Errorf("zone must be a positive integer")
		}
		n = n*10 + int(ch-'0')
		if n > 99 {
			return 0, fmt.Errorf("zone out of range")
		}
	}
	if n < 1 {
		return 0, fmt.Errorf("zone must be >= 1")
	}
	return n, nil
}

// parseDateRange reads `from` and `to` (YYYY-MM-DD) query params; both default to today UTC
// when omitted, returning a single-day window.
func parseDateRange(r *http.Request) (from, to time.Time, err error) {
	fromStr := strings.TrimSpace(r.URL.Query().Get("from"))
	toStr := strings.TrimSpace(r.URL.Query().Get("to"))
	now := time.Now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	if fromStr == "" {
		from = today
	} else {
		from, err = time.Parse("2006-01-02", fromStr)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("from must be YYYY-MM-DD")
		}
	}
	if toStr == "" {
		to = from
	} else {
		to, err = time.Parse("2006-01-02", toStr)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("to must be YYYY-MM-DD")
		}
	}
	if to.Before(from) {
		return time.Time{}, time.Time{}, fmt.Errorf("to must be on or after from")
	}
	return from, to, nil
}

// decimalsForGain returns the number of decimal places needed to
// represent the smallest meaningful step of a Modbus register
// scaled by `gain`. Since raw register values are integers, the
// scaled output's true precision is exactly -log10(gain). We use
// this on the raw CSV path to print "120098.54" instead of
// "120098.54000000001" — the extra trailing digits are float64
// noise from `int * 0.01`, not actual signal.
//
// Returns (decimals, true) for "nice" gains we recognize
// (0.0001, 0.001, 0.01, 0.1, 1, 10, ...). Falls back to false for
// anything weird (negative, zero, NaN, non-power-of-ten) so the
// caller drops back to the shortest-round-trip representation.
func decimalsForGain(gain float64) (int, bool) {
	if gain <= 0 || math.IsNaN(gain) || math.IsInf(gain, 0) {
		return 0, false
	}
	// We only special-case integer powers of ten between 10^-6 and
	// 10^3 — that covers every gain in the Huawei catalog today
	// (1, 0.1, 0.01, 0.001) and leaves headroom for a future map.
	// Tolerate float jitter on the catalog side: the gain column
	// is read from YAML as float64, so 0.01 in the file may not
	// survive the round-trip to the exact double-precision 1e-2.
	for d := 0; d <= 6; d++ {
		step := math.Pow10(-d)
		if math.Abs(gain-step)/step < 1e-9 {
			return d, true
		}
	}
	for d := 1; d <= 3; d++ {
		step := math.Pow10(d)
		if math.Abs(gain-step)/step < 1e-9 {
			return 0, true
		}
	}
	return 0, false
}

// loadLocation resolves an IANA timezone name to a *time.Location.
// Empty / "UTC" / "Z" all collapse to UTC so callers don't have to
// special-case the zero value. Unknown zone names produce a 400-style
// error rather than silently falling back, because a typo here would
// otherwise silently render every timestamp in UTC and the analyst
// would chase a phantom drift.
func loadLocation(name string) (*time.Location, error) {
	name = strings.TrimSpace(name)
	if name == "" || strings.EqualFold(name, "UTC") || name == "Z" {
		return time.UTC, nil
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("tz must be a valid IANA timezone (got %q)", name)
	}
	return loc, nil
}

func parseAggregation(r *http.Request) (TimeseriesAggregation, error) {
	raw := strings.TrimSpace(r.URL.Query().Get("aggregation"))
	if raw == "" {
		return AggregationDelta, nil
	}
	switch TimeseriesAggregation(raw) {
	case AggregationDelta, AggregationAvg, AggregationLast:
		return TimeseriesAggregation(raw), nil
	}
	return "", fmt.Errorf("aggregation must be one of delta, avg, last")
}

func parseRange(r *http.Request) (from time.Time, to time.Time, bucket, tz string, err error) {
	fromStr := strings.TrimSpace(r.URL.Query().Get("from"))
	toStr := strings.TrimSpace(r.URL.Query().Get("to"))
	bucket = strings.TrimSpace(r.URL.Query().Get("bucket"))
	tz = strings.TrimSpace(r.URL.Query().Get("tz"))
	if bucket == "" {
		bucket = "15 minutes"
	}
	if tz == "" {
		tz = "UTC"
	}
	if fromStr != "" {
		from, err = time.Parse(time.RFC3339, fromStr)
		if err != nil {
			return time.Time{}, time.Time{}, "", "", fmt.Errorf("from must be an RFC3339 timestamp")
		}
	}
	if toStr != "" {
		to, err = time.Parse(time.RFC3339, toStr)
		if err != nil {
			return time.Time{}, time.Time{}, "", "", fmt.Errorf("to must be an RFC3339 timestamp")
		}
	}
	return from, to, bucket, tz, nil
}

func (h *Handlers) withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := h.allowOrigin
		if origin == "" {
			origin = "*"
		}
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (h *Handlers) withSecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	// Encode into a buffer first. encoding/json rejects NaN/±Inf in
	// float64 fields with an error; if we encoded straight to the
	// ResponseWriter we'd have already written the status header and
	// would emit a truncated body with Content-Type: application/json.
	// Buffering lets us fall back to a clean 500 instead of corrupt JSON.
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(v); err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

func (h *Handlers) swaggerUI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if r.URL.Path != "/swagger" && r.URL.Path != "/swagger/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(swaggerHTML))
}

func (h *Handlers) swaggerSpec(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(openAPISpecYAML))
}
