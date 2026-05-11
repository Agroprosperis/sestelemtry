package api

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/nesh/sestelemetry/internal/energyflow"
)

type Handlers struct {
	store          storeReader
	allowOrigin    string
	log            *slog.Logger
	organizations  []OrganizationInfo
	energyFlowOrgs map[string]EnergyFlowOrg
}

type storeReader interface {
	Current(ctx context.Context, organizationID string, metricKeys []string, at time.Time) (CurrentResponse, error)
	Timeseries(ctx context.Context, organizationID string, metricKeys []string, from, to time.Time, bucket, tz string, aggregation TimeseriesAggregation) (TimeseriesResponse, error)
	EnergySummary(ctx context.Context, organizationID string, metricKeys []string, from, to time.Time) (EnergySummaryResponse, error)
	DAMPrices(ctx context.Context, zone int, from, to time.Time) (DAMPricesResponse, error)
	Samples(ctx context.Context, organizationID string, metricKeys []string, from, to time.Time, limit int, emit func(SampleRow) error) (int, bool, error)
	EnergyFlowSources(ctx context.Context, organizationID string, from, to time.Time, lookback time.Duration) ([]EnergyFlowRawRow, error)
	Ready(ctx context.Context) error
}

// Limits for the raw-samples export. These are deliberately on the
// generous side — most analyst use cases sit well under them — but
// they exist to keep a misclick on a multi-month range from streaming
// gigabytes through the API server. The handler returns 400 when a
// request would exceed them so the user fixes their query rather than
// silently receiving truncated data.
const (
	defaultSamplesLimit  = 100_000
	maxSamplesLimit      = 1_000_000
	maxSamplesRange      = 31 * 24 * time.Hour
	maxSamplesMetricKeys = 20
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
	mux.HandleFunc("/api/v1/dam-prices", h.damPrices)
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
	// substitutes the default catalog for an empty list, but the
	// response only carries totals it actually saw; we still need to
	// know whether any synthetic flow key is in scope so we can
	// compute it on the fly below.
	effectiveKeys := metricKeys
	if len(effectiveKeys) == 0 {
		effectiveKeys = EnergySummaryAccumulators
	}
	if syntheticKeysRequested(effectiveKeys) {
		flowStart := time.Now()
		flows, ferr := h.computeEnergyFlowTotals(r.Context(), orgID, from, to)
		if ferr != nil {
			// On-the-fly compute failures degrade gracefully:
			// the rest of the summary (raw counters) is still
			// useful, and synthetic keys fall back to zero. We
			// log so the operator sees the misconfig but the
			// dashboard does not blank out.
			h.log.Warn("api_energy_summary_flow_compute",
				"organization_id", orgID, "err", ferr,
				"duration_ms", time.Since(flowStart).Milliseconds(),
			)
			if resp.Totals == nil {
				resp.Totals = make(map[string]float64, len(EnergyFlowSyntheticMetrics))
			}
			for _, k := range EnergyFlowSyntheticMetrics {
				if _, ok := resp.Totals[k]; !ok {
					resp.Totals[k] = 0
				}
			}
		} else {
			if resp.Totals == nil {
				resp.Totals = make(map[string]float64, len(flows))
			}
			for k, v := range flows {
				resp.Totals[k] = v
			}
			h.log.Info("api_energy_summary_flow_compute_ok",
				"organization_id", orgID,
				"duration_ms", time.Since(flowStart).Milliseconds(),
			)
		}
	}
	h.log.Info("api_energy_summary_ok",
		"organization_id", orgID,
		"metric_keys", metricKeys,
		"totals", len(resp.Totals),
		"duration_ms", time.Since(start).Milliseconds(),
	)
	writeJSON(w, http.StatusOK, resp)
}

// syntheticKeysRequested reports whether the caller's metric_keys
// list contains at least one of the four synthetic energy-flow
// counters. Used by the EnergySummary handler to decide whether to
// run the on-the-fly allocation pipeline.
func syntheticKeysRequested(keys []string) bool {
	want := make(map[string]struct{}, len(EnergyFlowSyntheticMetrics))
	for _, k := range EnergyFlowSyntheticMetrics {
		want[k] = struct{}{}
	}
	for _, k := range keys {
		if _, ok := want[k]; ok {
			return true
		}
	}
	return false
}

// computeEnergyFlowTotals re-runs the allocation rule against the raw
// Modbus counters stored in telemetry_samples for [from, to] and
// returns the four period flow totals (kWh). It replaces the old
// "live aggregator writes synthetic cumulative samples, dashboard
// reads end-minus-seed" pipeline: synthetic samples are no longer
// persisted, every dashboard read computes from raw on the fly.
//
// The benefit is that there is no shared cumulative state to
// corrupt: rewriting an old period is just "click refresh", and
// missing periods (e.g. before the energy-flow feature shipped) are
// computed identically to fresh ones with no manual ops work.
//
// We accept the modest CPU cost: a day query is ~1.4k 60s buckets,
// a month ~43k, a year ~520k. Each bucket is a handful of float
// multiplications inside energyflow.Allocate, so worst-case (year)
// runs in a few seconds; faster presets are sub-second.
func (h *Handlers) computeEnergyFlowTotals(
	ctx context.Context,
	orgID string,
	from, to time.Time,
) (map[string]float64, error) {
	rows, err := h.store.EnergyFlowSources(ctx, orgID, from, to, 0)
	if err != nil {
		return nil, fmt.Errorf("energy_flow sources: %w", err)
	}
	if len(rows) == 0 {
		return emptyFlowTotals(), nil
	}
	// Org config is optional: legacy single-device deployments do
	// not register a device→role map, and buildRawSamples maps any
	// row with an empty device_host label to RoleSingle in that
	// case. Multi-SmartLogger orgs need the config to attribute
	// rows to the correct role.
	cfg := h.energyFlowOrgs[orgID]
	rawSamples := buildRawSamples(rows, cfg)
	rec := energyflow.Recompute(rawSamples, nil, energyflow.Options{
		EssDischargeSign:        cfg.EssDischargeSign,
		AllocationWindowSeconds: 60,
		MaxGapSeconds:           0, // disabled — same logic as backfill
	})
	out := make(map[string]float64, len(EnergyFlowSyntheticMetrics))
	for _, k := range EnergyFlowSyntheticMetrics {
		out[k] = rec.Totals[k]
	}
	return out, nil
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
			return time.Time{}, time.Time{}, "", "", err
		}
	}
	if toStr != "" {
		to, err = time.Parse(time.RFC3339, toStr)
		if err != nil {
			return time.Time{}, time.Time{}, "", "", err
		}
	}
	return from, to, bucket, tz, nil
}

// buildRawSamples groups telemetry rows into energyflow.RawSample by
// (time, device_host). The device_host label is mapped to a Role via
// the org config; unknown hosts default to RoleSingle which is the
// safest classification for legacy single-device deployments (no
// device_host label) and avoids dropping data when an operator
// renames a SmartLogger between collection and on-the-fly compute.
func buildRawSamples(rows []EnergyFlowRawRow, cfg EnergyFlowOrg) []energyflow.RawSample {
	roleByHost := make(map[string]energyflow.Role, len(cfg.Devices))
	for _, d := range cfg.Devices {
		roleByHost[d.Host] = energyflow.Role(d.Role)
	}
	type key struct {
		t    int64
		host string
	}
	grouped := make(map[key]map[string]float64, len(rows)/3+1)
	order := make([]key, 0, len(rows)/3+1)
	for _, r := range rows {
		k := key{t: r.Time.UnixNano(), host: r.DeviceHost}
		bucket, ok := grouped[k]
		if !ok {
			bucket = make(map[string]float64, 5)
			grouped[k] = bucket
			order = append(order, k)
		}
		bucket[r.MetricKey] = r.Value
	}
	out := make([]energyflow.RawSample, 0, len(order))
	for _, k := range order {
		role, ok := roleByHost[k.host]
		if !ok {
			if len(cfg.Devices) == 0 {
				role = energyflow.RoleSingle
			} else if k.host == "" {
				role = energyflow.RoleSingle
			} else {
				continue
			}
		}
		if role == energyflow.RoleNone {
			continue
		}
		out = append(out, energyflow.RawSample{
			Time:   time.Unix(0, k.t).UTC(),
			Role:   role,
			Values: grouped[k],
		})
	}
	return out
}

func emptyFlowTotals() map[string]float64 {
	out := make(map[string]float64, len(EnergyFlowSyntheticMetrics))
	for _, k := range EnergyFlowSyntheticMetrics {
		out[k] = 0
	}
	return out
}

func (h *Handlers) withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := h.allowOrigin
		if origin == "" {
			origin = "*"
		}
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
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
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
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
