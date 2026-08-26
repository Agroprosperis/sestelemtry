package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nesh/sestelemetry/internal/economics"
	"github.com/nesh/sestelemetry/internal/storage"
)

// Edge manifest publisher: turns DAM prices + the PV forecast + a
// heuristic load profile into a manifest-lite document with
// plan.intervals[] (the level-A plan the shadow engine follows) and
// stores it in edge_manifests, where GET /api/v1/edge/manifest serves
// it to the device.

// pvPerformanceRatio derates the irradiance→AC conversion (soiling,
// temperature, inverter losses) for the PV forecast.
const pvPerformanceRatio = 0.8

// edgeManifestDoc mirrors internal/edge.Manifest (manifest-lite).
type edgeManifestDoc struct {
	SchemaVersion string    `json:"schema_version"`
	ManifestID    string    `json:"manifest_id"`
	SiteID        string    `json:"site_id"`
	IssuedAt      time.Time `json:"issued_at"`
	ValidFrom     time.Time `json:"valid_from"`
	ValidUntil    time.Time `json:"valid_until"`

	Mode         string `json:"mode"`
	WriteEnabled bool   `json:"write_enabled"`
	Preset       string `json:"preset"`

	// Source distinguishes planner output ("" / "auto") from operator
	// publications ("manual"). The edge ignores unknown fields; the
	// cloud uses it to keep the rolling planner from overwriting a
	// still-valid manual manifest.
	Source string `json:"source,omitempty"`
	Note   string `json:"note,omitempty"`

	Limits struct {
		EssChargeMaxKw    float64 `json:"ess_charge_max_kw,omitempty"`
		EssDischargeMaxKw float64 `json:"ess_discharge_max_kw,omitempty"`
	} `json:"limits"`
	GridLimits struct {
		ImportLimitKw  float64 `json:"import_limit_kw,omitempty"`
		TargetImportKw float64 `json:"target_import_kw,omitempty"`
		PvRatedKw      float64 `json:"pv_rated_kw,omitempty"`
	} `json:"grid_limits"`
	SocPolicy struct {
		MinEconomicPct float64 `json:"min_economic_pct,omitempty"`
		MaxEconomicPct float64 `json:"max_economic_pct,omitempty"`
	} `json:"soc_policy"`

	Plan *edgePlanDoc `json:"plan,omitempty"`
}

type edgePlanDoc struct {
	Granularity string             `json:"granularity"`
	LoadSource  string             `json:"load_source,omitempty"`
	Intervals   []edgePlanInterval `json:"intervals"`
}

type edgePlanInterval struct {
	TS           time.Time `json:"ts"`
	EssKw        float64   `json:"ess_kw"`
	SocTargetPct float64   `json:"soc_target_pct,omitempty"`
	Action       string    `json:"action,omitempty"`
	PriceUah     float64   `json:"rdn_uah_per_kwh,omitempty"`
}

// EdgePublishResult is the response of the publish endpoints.
type EdgePublishResult struct {
	SiteID     string `json:"site_id"`
	ManifestID string `json:"manifest_id"`
	Published  bool   `json:"published"` // false = unchanged plan, nothing new stored
	Intervals  int    `json:"intervals"`
	LoadSource string `json:"load_source"`
	ValidUntil string `json:"valid_until"`
	// Skipped explains why nothing was published (e.g. an operator's
	// manual manifest is still valid and blocks the rolling planner).
	Skipped string `json:"skipped,omitempty"`
	Source  string `json:"source,omitempty"`
}

// edgeManifestPublish handles POST /api/v1/edge/manifest/publish?site_id=.
// Operator-facing (no edge token): recomputes the forward plan and
// publishes a new manifest version if it changed.
func (h *Handlers) edgeManifestPublish(w http.ResponseWriter, r *http.Request) {
	e := h.edge
	if e == nil {
		http.Error(w, "edge ingest not configured", http.StatusServiceUnavailable)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	siteID := strings.TrimSpace(r.URL.Query().Get("site_id"))
	if siteID == "" {
		http.Error(w, "site_id is required", http.StatusBadRequest)
		return
	}
	if _, ok := e.Tokens[siteID]; !ok {
		http.Error(w, "unknown edge site", http.StatusNotFound)
		return
	}
	res, err := h.PublishEdgeManifest(r.Context(), siteID)
	if err != nil {
		e.Log.Error("edge_manifest_publish", "site_id", siteID, "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// --- manual manifest (console «Ручний режим») ---

// edgeManualInterval is one operator-entered dispatch hour.
type edgeManualInterval struct {
	TS           time.Time `json:"ts"`
	EssKw        float64   `json:"ess_kw"` // + discharge / − charge
	SocTargetPct float64   `json:"soc_target_pct,omitempty"`
}

// edgeManualPublishRequest is the POST /api/v1/edge/manifest/publish-manual
// body. Intervals may be empty — a preset-only manual manifest (e.g.
// «потримай self_consumption_safe 4 години») is legitimate. Cancel=true
// discards the manual manifest by republishing the rolling plan.
type edgeManualPublishRequest struct {
	TTLHours  float64              `json:"ttl_hours"`
	Preset    string               `json:"preset"`
	Note      string               `json:"note"`
	Cancel    bool                 `json:"cancel"`
	Intervals []edgeManualInterval `json:"intervals"`
}

const (
	edgeManualTTLDefault = 4 * time.Hour
	edgeManualTTLMax     = 48 * time.Hour
	// edgeManualEssMaxKw is a sanity bound, not a site limit — the real
	// per-site caps still apply on the edge via manifest limits.
	edgeManualEssMaxKw = 20000.0
)

func (r *edgeManualPublishRequest) validate() error {
	if r.Cancel {
		return nil
	}
	if r.TTLHours != 0 && (r.TTLHours < 0.5 || r.TTLHours > edgeManualTTLMax.Hours()) {
		return fmt.Errorf("ttl_hours має бути в межах 0.5..%v", edgeManualTTLMax.Hours())
	}
	switch r.Preset {
	case "", "economic_arbitrage", "self_consumption", "self_consumption_safe":
	default:
		return fmt.Errorf("невідомий preset %q", r.Preset)
	}
	seen := map[time.Time]bool{}
	for i, iv := range r.Intervals {
		if iv.TS.IsZero() {
			return fmt.Errorf("intervals[%d]: ts обов'язковий", i)
		}
		if math.IsNaN(iv.EssKw) || math.IsInf(iv.EssKw, 0) || math.Abs(iv.EssKw) > edgeManualEssMaxKw {
			return fmt.Errorf("intervals[%d]: ess_kw поза межами", i)
		}
		if iv.SocTargetPct < 0 || iv.SocTargetPct > 100 {
			return fmt.Errorf("intervals[%d]: soc_target_pct має бути 0..100", i)
		}
		key := iv.TS.UTC().Truncate(time.Hour)
		if seen[key] {
			return fmt.Errorf("intervals[%d]: дубльована година %s", i, key.Format(time.RFC3339))
		}
		seen[key] = true
	}
	return nil
}

func (r *edgeManualPublishRequest) ttl() time.Duration {
	if r.TTLHours <= 0 {
		return edgeManualTTLDefault
	}
	return time.Duration(r.TTLHours * float64(time.Hour))
}

// edgeManifestPublishManual handles
// POST /api/v1/edge/manifest/publish-manual?site_id=.
func (h *Handlers) edgeManifestPublishManual(w http.ResponseWriter, r *http.Request) {
	siteID, ok := h.requireEdge(w, r, http.MethodPost)
	if !ok {
		return
	}
	var req edgeManualPublishRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := req.validate(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if req.Cancel {
		// Back to the rolling planner: force one auto publication past
		// the manual guard so the edge picks a fresh plan immediately.
		res, err := h.publishEdgeManifest(r.Context(), siteID, true)
		if err != nil {
			h.edge.Log.Error("edge_manifest_manual_cancel", "site_id", siteID, "err", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, res)
		return
	}

	res, err := h.publishManualEdgeManifest(r.Context(), siteID, req)
	if err != nil {
		h.edge.Log.Error("edge_manifest_manual", "site_id", siteID, "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// publishManualEdgeManifest stores an operator manifest: the requested
// intervals with the site's limits/SOC policy, valid for the TTL. While
// it is valid the rolling planner leaves it alone.
func (h *Handlers) publishManualEdgeManifest(ctx context.Context, siteID string, req edgeManualPublishRequest) (EdgePublishResult, error) {
	e := h.edge
	now := time.Now().UTC()

	in := edgePlanInputs{Now: now}
	if err := h.applyEdgeSiteParams(ctx, siteID, &in); err != nil {
		return EdgePublishResult{SiteID: siteID}, err
	}

	preset := req.Preset
	if preset == "" {
		preset = "economic_arbitrage"
	}

	doc := edgeManifestDoc{
		SchemaVersion: "lite-1",
		SiteID:        siteID,
		IssuedAt:      now,
		ValidFrom:     now,
		ValidUntil:    now.Add(req.ttl()),
		Mode:          "shadow",
		WriteEnabled:  false,
		Preset:        preset,
		Source:        "manual",
		Note:          strings.TrimSpace(req.Note),
	}
	doc.Limits.EssChargeMaxKw = in.ChargeMaxKw
	doc.Limits.EssDischargeMaxKw = in.DischargeMaxKw
	doc.GridLimits.ImportLimitKw = in.GridImportKw
	doc.GridLimits.TargetImportKw = in.GridTargetKw
	doc.GridLimits.PvRatedKw = in.PvRatedKw
	doc.SocPolicy.MinEconomicPct = in.SocMin
	doc.SocPolicy.MaxEconomicPct = in.SocMax

	if len(req.Intervals) > 0 {
		ivs := make([]edgePlanInterval, 0, len(req.Intervals))
		for _, iv := range req.Intervals {
			ivs = append(ivs, edgePlanInterval{
				TS:           iv.TS.UTC().Truncate(time.Hour),
				EssKw:        round1(iv.EssKw),
				SocTargetPct: round1(iv.SocTargetPct),
				Action:       "manual",
			})
		}
		sort.Slice(ivs, func(i, j int) bool { return ivs[i].TS.Before(ivs[j].TS) })
		doc.Plan = &edgePlanDoc{Granularity: "1h", LoadSource: "manual", Intervals: ivs}
	}
	doc.ManifestID = edgeManualManifestID(siteID, doc)

	res := EdgePublishResult{
		SiteID:     siteID,
		ManifestID: doc.ManifestID,
		Intervals:  len(req.Intervals),
		LoadSource: "manual",
		ValidUntil: doc.ValidUntil.Format(time.RFC3339),
		Source:     "manual",
	}

	_, latestID, hasLatest, err := storage.LatestEdgeManifest(ctx, e.Pool, siteID)
	if err != nil {
		return res, err
	}
	if hasLatest && latestID == doc.ManifestID {
		return res, nil
	}

	payload, err := json.Marshal(doc)
	if err != nil {
		return res, err
	}
	if err := storage.UpsertEdgeManifest(ctx, e.Pool, siteID, doc.ManifestID, payload, doc.ValidFrom, doc.ValidUntil); err != nil {
		return res, err
	}
	res.Published = true
	e.Log.Info("edge_manifest_manual_published",
		"site_id", siteID, "manifest_id", doc.ManifestID,
		"intervals", len(req.Intervals), "valid_until", res.ValidUntil)
	return res, nil
}

// manualManifestActive reports whether payload is an operator manifest
// that is still within its validity window.
func manualManifestActive(payload []byte, now time.Time) bool {
	var m struct {
		Source     string    `json:"source"`
		ValidUntil time.Time `json:"valid_until"`
	}
	if json.Unmarshal(payload, &m) != nil {
		return false
	}
	return m.Source == "manual" && m.ValidUntil.After(now)
}

// edgeManualManifestID hashes the manual content *including* the
// validity end: re-publishing the same hours with a longer TTL must
// yield a new version (the auto id deliberately ignores valid_until).
func edgeManualManifestID(siteID string, doc edgeManifestDoc) string {
	hashable := struct {
		SiteID     string       `json:"site_id"`
		Preset     string       `json:"preset"`
		Note       string       `json:"note"`
		ValidUntil time.Time    `json:"valid_until"`
		Plan       *edgePlanDoc `json:"plan"`
		Limits     any          `json:"limits"`
		SocPolicy  any          `json:"soc_policy"`
	}{siteID, doc.Preset, doc.Note, doc.ValidUntil, doc.Plan, doc.Limits, doc.SocPolicy}
	raw, _ := json.Marshal(hashable)
	sum := sha256.Sum256(raw)
	return fmt.Sprintf("%s-manual-%s-%s", siteID, doc.ValidUntil.Format("20060102T1504"), hex.EncodeToString(sum[:])[:8])
}

// edgePlanInputs bundles everything the forward DP needs for one site:
// horizon, ratings, tariffs and the merged hourly forecast series.
type edgePlanInputs struct {
	Loc        *time.Location
	Timezone   string
	Now        time.Time
	Start, End time.Time

	Tariffs     economics.Tariffs
	CapacityKwh float64
	PowerKw     float64 // DP charge/discharge cap (min of the two limits)
	PvRatedKw   float64
	SocMin      float64
	SocMax      float64
	StartSoc    float64

	// Manifest-facing limits (may differ per direction when the
	// console settings say so; default both to PowerKw).
	ChargeMaxKw    float64
	DischargeMaxKw float64
	GridImportKw   float64
	GridTargetKw   float64

	Hours      []economics.ForwardHour
	LoadSource string
	// PvSource names the PV forecast origin: "generation_forecast"
	// (the n8n per-orientation product the dashboard uses) or
	// "gti_estimate" (irradiance × rated × PR fallback).
	PvSource string
	// OperatorHour marks hours (UTC hour start) whose load came from
	// the operator plan (or the preview draft) rather than the
	// heuristic profile.
	OperatorHour map[time.Time]bool
	// Weather carries the display forecast (temp/clouds) for the UI.
	Weather map[time.Time]edgeHourWeather
}

// gatherEdgePlanInputs assembles the DP inputs for a site. draftLoad
// (UTC hour start → kW) lets the planner UI preview unsaved edits: a
// draft hour overrides both the stored operator plan and the heuristic
// profile.
func (h *Handlers) gatherEdgePlanInputs(ctx context.Context, siteID string, draftLoad map[time.Time]float64) (edgePlanInputs, error) {
	e := h.edge
	if e == nil {
		return edgePlanInputs{}, fmt.Errorf("edge ingest not configured")
	}
	tzName := e.PlannerTimezone
	if tzName == "" {
		tzName = "Europe/Kyiv"
	}
	loc, err := time.LoadLocation(tzName)
	if err != nil {
		return edgePlanInputs{}, err
	}
	zone := e.PlannerZone
	if zone == 0 {
		zone = 2
	}
	now := time.Now().In(loc)

	in := edgePlanInputs{Loc: loc, Timezone: tzName, Now: now}
	if err := h.applyEdgeSiteParams(ctx, siteID, &in); err != nil {
		return edgePlanInputs{}, err
	}

	// Horizon: the current hour → the end of tomorrow (local).
	in.Start = now.Truncate(time.Hour)
	in.End = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, 2)

	prices, err := h.edgeDAMPrices(ctx, zone, now, loc)
	if err != nil {
		return edgePlanInputs{}, fmt.Errorf("dam prices: %w", err)
	}
	pv, weather, err := h.edgePvForecast(ctx, siteID, in.Start, in.End, in.PvRatedKw)
	if err != nil {
		return edgePlanInputs{}, fmt.Errorf("pv forecast: %w", err)
	}
	in.Weather = weather
	in.PvSource = "gti_estimate"
	// Prefer the real generation forecast (same n8n product the
	// dashboard's day chart shows); the GTI estimate stays as the
	// per-hour fallback for hours the product does not cover.
	if plan, ok := h.edgePvPlanForecast(ctx, siteID, loc, in.Start, in.End); ok {
		in.PvSource = "generation_forecast"
		for key, kw := range plan {
			pv[key] = kw
		}
	}
	heuristic, heuristicSource, err := h.edgeLoadProfile(ctx, siteID, tzName)
	if err != nil {
		return edgePlanInputs{}, fmt.Errorf("load profile: %w", err)
	}
	operator, err := storage.GetEdgeLoadPlan(ctx, e.Pool, siteID, in.Start, in.End)
	if err != nil {
		return edgePlanInputs{}, fmt.Errorf("operator load plan: %w", err)
	}

	in.StartSoc = h.edgeLatestSoc(ctx, siteID)
	if in.StartSoc == 0 {
		in.StartSoc = (in.SocMin + in.SocMax) / 2
	}

	in.OperatorHour = map[time.Time]bool{}
	totalHours, operatorHours := 0, 0
	for ts := in.Start; ts.Before(in.End); ts = ts.Add(time.Hour) {
		key := ts.UTC()
		fh := economics.ForwardHour{TS: ts, PvKw: pv[key]}
		switch {
		case draftLoad != nil && hasHour(draftLoad, key):
			fh.LoadKw = draftLoad[key]
			in.OperatorHour[key] = true
			operatorHours++
		case hasHour(operator, key):
			fh.LoadKw = operator[key]
			in.OperatorHour[key] = true
			operatorHours++
		default:
			fh.LoadKw = heuristic[ts.In(loc).Hour()]
		}
		if price, ok := prices[key]; ok {
			p := price
			fh.RdnUahPerKwh = &p
		}
		in.Hours = append(in.Hours, fh)
		totalHours++
	}

	switch {
	case operatorHours == totalHours && totalHours > 0:
		in.LoadSource = "operator"
	case operatorHours > 0:
		in.LoadSource = "operator_partial"
	default:
		in.LoadSource = heuristicSource
	}
	return in, nil
}

func hasHour(m map[time.Time]float64, ts time.Time) bool {
	_, ok := m[ts]
	return ok
}

// applyEdgeSiteParams fills tariffs, ratings, per-direction limits and
// the SOC policy into in — passport/inventory numbers first, saved
// console settings on top (mockup panel-settings). in.Now must be set.
func (h *Handlers) applyEdgeSiteParams(ctx context.Context, siteID string, in *edgePlanInputs) error {
	var err error
	in.Tariffs = h.resolveEdgeTariffs(ctx, siteID, in.Now)
	in.CapacityKwh, in.PowerKw, in.PvRatedKw, err = h.resolveEdgeRatings(ctx, siteID, in.Tariffs)
	if err != nil {
		return err
	}
	in.ChargeMaxKw, in.DischargeMaxKw = in.PowerKw, in.PowerKw
	in.SocMin, in.SocMax = 20.0, 90.0

	if s, saved := h.loadEdgeSiteSettings(ctx, siteID); saved && s != nil {
		if s.PvRatedKw > 0 {
			in.PvRatedKw = s.PvRatedKw
		}
		if s.AutoChargeMaxKw > 0 {
			in.ChargeMaxKw = s.AutoChargeMaxKw
		}
		if s.AutoDischargeMaxKw > 0 {
			in.DischargeMaxKw = s.AutoDischargeMaxKw
		}
		if s.AutoChargeMaxKw > 0 || s.AutoDischargeMaxKw > 0 {
			// The DP has a single symmetric cap — take the stricter of
			// the two so it never plans past either manifest limit.
			in.PowerKw = math.Min(in.ChargeMaxKw, in.DischargeMaxKw)
		}
		if s.SocReservePct > 0 {
			in.SocMin = s.SocReservePct
		}
		if s.SocTargetPct > 0 {
			in.SocMax = s.SocTargetPct
		}
		in.GridImportKw = s.GridImportKw
		in.GridTargetKw = s.GridTargetKw
	}
	return nil
}

// PublishEdgeManifest builds the forward plan for one site and stores
// it as a manifest-lite version. The manifest_id is a content hash, so
// republishing an unchanged plan is a no-op (the edge keeps its cached
// copy via ETag). A still-valid manual manifest blocks this auto path —
// cancel it via publish-manual {"cancel":true}.
func (h *Handlers) PublishEdgeManifest(ctx context.Context, siteID string) (EdgePublishResult, error) {
	return h.publishEdgeManifest(ctx, siteID, false)
}

func (h *Handlers) publishEdgeManifest(ctx context.Context, siteID string, overrideManual bool) (EdgePublishResult, error) {
	e := h.edge
	if e == nil {
		return EdgePublishResult{}, fmt.Errorf("edge ingest not configured")
	}

	latestPayload, latestID, hasLatest, err := storage.LatestEdgeManifest(ctx, e.Pool, siteID)
	if err != nil {
		return EdgePublishResult{SiteID: siteID}, err
	}
	if hasLatest && !overrideManual && manualManifestActive(latestPayload, time.Now().UTC()) {
		return EdgePublishResult{
			SiteID: siteID, ManifestID: latestID, Source: "manual",
			Skipped: "manual manifest active",
		}, nil
	}

	in, err := h.gatherEdgePlanInputs(ctx, siteID, nil)
	if err != nil {
		return EdgePublishResult{}, err
	}
	now, end, loadSource := in.Now, in.End, in.LoadSource

	steps, err := economics.BuildForwardPlan(in.Hours, economics.ForwardParams{
		Tariffs:     in.Tariffs,
		CapacityKwh: in.CapacityKwh,
		PowerKw:     in.PowerKw,
		SocMinPct:   in.SocMin,
		SocMaxPct:   in.SocMax,
		StartSocPct: in.StartSoc,
	})
	if err != nil {
		return EdgePublishResult{}, err
	}

	intervals := make([]edgePlanInterval, 0, len(steps))
	for _, s := range steps {
		if !s.Tradable {
			continue // no DAM price — the edge preset rules own the hour
		}
		intervals = append(intervals, edgePlanInterval{
			TS:           s.TS.UTC(),
			EssKw:        round1(s.EssKw),
			SocTargetPct: round1(s.SocEndPct),
			Action:       s.Action,
			PriceUah:     round3(s.RdnUahPerKwh),
		})
	}

	doc := edgeManifestDoc{
		SchemaVersion: "lite-1",
		SiteID:        siteID,
		IssuedAt:      now.UTC(),
		ValidFrom:     now.UTC(),
		ValidUntil:    end.UTC(),
		Mode:          "shadow",
		WriteEnabled:  false,
		Preset:        "economic_arbitrage",
		Source:        "auto",
	}
	doc.Limits.EssChargeMaxKw = in.ChargeMaxKw
	doc.Limits.EssDischargeMaxKw = in.DischargeMaxKw
	doc.GridLimits.ImportLimitKw = in.GridImportKw
	doc.GridLimits.TargetImportKw = in.GridTargetKw
	doc.GridLimits.PvRatedKw = in.PvRatedKw
	doc.SocPolicy.MinEconomicPct = in.SocMin
	doc.SocPolicy.MaxEconomicPct = in.SocMax
	if len(intervals) > 0 {
		doc.Plan = &edgePlanDoc{
			Granularity: "1h",
			LoadSource:  loadSource,
			Intervals:   intervals,
		}
	}
	doc.ManifestID = edgeManifestID(siteID, end, doc)

	res := EdgePublishResult{
		SiteID:     siteID,
		ManifestID: doc.ManifestID,
		Intervals:  len(intervals),
		LoadSource: loadSource,
		ValidUntil: doc.ValidUntil.Format(time.RFC3339),
		Source:     doc.Source,
	}

	// Unchanged content → same id → nothing to publish.
	if hasLatest && latestID == doc.ManifestID {
		return res, nil
	}

	payload, err := json.Marshal(doc)
	if err != nil {
		return res, err
	}
	if err := storage.UpsertEdgeManifest(ctx, e.Pool, siteID, doc.ManifestID, payload, doc.ValidFrom, doc.ValidUntil); err != nil {
		return res, err
	}
	res.Published = true
	e.Log.Info("edge_manifest_published",
		"site_id", siteID, "manifest_id", doc.ManifestID,
		"intervals", len(intervals), "valid_until", res.ValidUntil)
	return res, nil
}

// edgeManifestID derives a deterministic content id: same plan → same
// id → the edge's ETag poll sees 304 and nothing is re-applied.
func edgeManifestID(siteID string, horizonEnd time.Time, doc edgeManifestDoc) string {
	hashable := struct {
		SiteID    string       `json:"site_id"`
		Preset    string       `json:"preset"`
		Plan      *edgePlanDoc `json:"plan"`
		Limits    any          `json:"limits"`
		SocPolicy any          `json:"soc_policy"`
	}{siteID, doc.Preset, doc.Plan, doc.Limits, doc.SocPolicy}
	raw, _ := json.Marshal(hashable)
	sum := sha256.Sum256(raw)
	return fmt.Sprintf("%s-%s-%s", siteID, horizonEnd.Format("20060102"), hex.EncodeToString(sum[:])[:8])
}

// resolveEdgeTariffs picks the tariff bundle in effect today: the
// date-versioned schedule first, the legacy single blob second, zero
// tariffs (pure RDN prices) as the last resort.
func (h *Handlers) resolveEdgeTariffs(ctx context.Context, orgID string, day time.Time) economics.Tariffs {
	if h.store != nil {
		if versions, err := h.store.GetTariffScheduleVersions(ctx, orgID); err == nil && len(versions) > 0 {
			sched := make(economics.Schedule, 0, len(versions))
			for _, v := range versions {
				ef, err := time.Parse("2006-01-02", v.EffectiveFrom)
				if err != nil {
					continue
				}
				sched = append(sched, economics.ScheduleEntry{
					EffectiveFrom: ef,
					Tariffs:       orgTariffsToEconomics(v.Tariffs),
				})
			}
			if t, ok := sched.ResolveForDay(day); ok {
				return t
			}
		}
		if t, ok, err := h.store.GetOrgTariffs(ctx, orgID); err == nil && ok {
			return orgTariffsToEconomics(t)
		}
	}
	h.edge.Log.Warn("edge_planner_no_tariffs", "site_id", orgID)
	return economics.Tariffs{}
}

// resolveEdgeRatings finds the ESS/PV passport numbers: the live plant
// inventory (Modbus 40396/40398/40484) first, tariff metadata second.
func (h *Handlers) resolveEdgeRatings(ctx context.Context, orgID string, t economics.Tariffs) (capacityKwh, powerKw, pvRatedKw float64, err error) {
	if h.store != nil {
		if inv, ok, err := h.store.LatestPlantInventory(ctx, orgID); err == nil && ok {
			if inv.ESSRatedKwh != nil {
				capacityKwh = *inv.ESSRatedKwh
			}
			if inv.ESSRatedKw != nil {
				powerKw = *inv.ESSRatedKw
			}
			if inv.PVRatedKw != nil {
				pvRatedKw = *inv.PVRatedKw
			}
		}
	}
	if capacityKwh <= 0 && t.EssCapacityKwh > 0 {
		// Tariffs store the usable 10–90% window; scale to nameplate.
		capacityKwh = t.EssCapacityKwh / 0.8
	}
	if powerKw <= 0 {
		powerKw = t.EssPowerLimitKw
	}
	if capacityKwh <= 0 || powerKw <= 0 {
		return 0, 0, 0, fmt.Errorf("no ESS ratings for %s: need plant inventory or tariffs (ess_capacity_kwh / ess_power_limit_kw)", orgID)
	}
	return capacityKwh, powerKw, pvRatedKw, nil
}

// edgeDAMPrices loads today's and tomorrow's hourly RDN prices
// (UAH/kWh) keyed by UTC hour start.
func (h *Handlers) edgeDAMPrices(ctx context.Context, zone int, now time.Time, loc *time.Location) (map[time.Time]float64, error) {
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	dates := []string{today.Format("2006-01-02"), today.AddDate(0, 0, 1).Format("2006-01-02")}
	rows, err := h.edge.Pool.Query(ctx, `
		SELECT delivery_date, hour, price_uah_per_mwh
		FROM market_dam_prices
		WHERE zone = $1 AND delivery_date = ANY($2::date[]) AND price_uah_per_mwh IS NOT NULL`,
		zone, dates)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[time.Time]float64{}
	for rows.Next() {
		var d time.Time
		var hour int
		var priceMwh float64
		if err := rows.Scan(&d, &hour, &priceMwh); err != nil {
			return nil, err
		}
		local := time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, loc).Add(time.Duration(hour-1) * time.Hour)
		out[local.UTC()] = priceMwh / 1000
	}
	return out, rows.Err()
}

// --- PV generation forecast (the same n8n source the dashboard uses) ---

// edgePvForecastURL is the n8n webhook that serves the per-orientation
// hourly generation forecast (elevator_code + forecast_day → rows of
// {hour_ending, orientation_idx, planned_kwh}). Same endpoint the
// dashboard's day chart calls; overridable via PV_FORECAST_WEBHOOK_URL.
const edgePvForecastURL = "https://granary.app.n8n.cloud/webhook/96bac28d-5020-48b3-8f23-0bc189029c00"

// edgePvElevatorCode maps organization ids to the n8n flow's elevator
// codes (mirrors web/src/dashboard/transforms/pvForecast.ts).
var edgePvElevatorCode = map[string]string{
	"ze": "JE", "pe": "RE", "pde": "PE", "ab": "AB", "ke": "KE", "de": "DE", "sm": "SM",
}

// edgePvPlanCache caches one site-day of the n8n forecast (the flow
// recomputes it a few times a day; refetching on every 15-minute
// rolling pass would hammer it for nothing).
type edgePvPlanCache struct {
	mu sync.Mutex
	m  map[string]cachedPvPlan // key: site|YYYY-MM-DD
}

type cachedPvPlan struct {
	byHour map[int]float64 // local hour start 0..23 → avg kW
	at     time.Time
}

const pvPlanTTL = 30 * time.Minute

// edgePvPlanForecast fetches the generation forecast for the local days
// covering [start, end) and returns kW keyed by UTC hour start. ok is
// false when the site has no elevator code or every fetch failed — the
// caller falls back to the GTI estimate.
func (h *Handlers) edgePvPlanForecast(ctx context.Context, siteID string, loc *time.Location, start, end time.Time) (map[time.Time]float64, bool) {
	code, known := edgePvElevatorCode[siteID]
	if !known {
		return nil, false
	}
	out := map[time.Time]float64{}
	got := false
	for day := time.Date(start.In(loc).Year(), start.In(loc).Month(), start.In(loc).Day(), 0, 0, 0, 0, loc); day.Before(end); day = day.AddDate(0, 0, 1) {
		byHour, err := h.pvPlanForDay(ctx, siteID, code, day)
		if err != nil {
			h.edge.Log.Warn("edge_pv_forecast", "site_id", siteID, "day", day.Format("2006-01-02"), "err", err)
			continue
		}
		if len(byHour) == 0 {
			continue
		}
		got = true
		for hour, kw := range byHour {
			out[day.Add(time.Duration(hour)*time.Hour).UTC()] = kw
		}
	}
	return out, got
}

func (h *Handlers) pvPlanForDay(ctx context.Context, siteID, code string, day time.Time) (map[int]float64, error) {
	key := siteID + "|" + day.Format("2006-01-02")
	cache := &h.edge.pvPlans
	cache.mu.Lock()
	if ent, ok := cache.m[key]; ok && time.Since(ent.at) < pvPlanTTL {
		cache.mu.Unlock()
		return ent.byHour, nil
	}
	cache.mu.Unlock()

	base := edgePvForecastURL
	if v := strings.TrimSpace(os.Getenv("PV_FORECAST_WEBHOOK_URL")); v != "" {
		base = v
	}
	reqURL := base + "?elevator_code=" + url.QueryEscape(code) + "&forecast_day=" + day.Format("2006-01-02")

	reqCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("pv forecast: %s", res.Status)
	}
	var rows []map[string]any
	if err := json.NewDecoder(res.Body).Decode(&rows); err != nil {
		return nil, fmt.Errorf("pv forecast decode: %w", err)
	}
	byHour := aggregatePvPlanRows(rows)

	cache.mu.Lock()
	if cache.m == nil {
		cache.m = map[string]cachedPvPlan{}
	}
	cache.m[key] = cachedPvPlan{byHour: byHour, at: time.Now()}
	cache.mu.Unlock()
	return byHour, nil
}

// aggregatePvPlanRows sums planned_kwh across panel orientations per
// hour, deduplicating repeated (hour_ending, orientation_idx) pairs —
// the exact logic of the dashboard's aggregatePvForecastHourly.
// hour_ending is 1..24 local Kyiv; the result keys are hour starts.
func aggregatePvPlanRows(rows []map[string]any) map[int]float64 {
	byHour := map[int]map[int]float64{}
	for _, r := range rows {
		hourEnding := int(anyToFloat(r["hour_ending"]))
		if hourEnding < 1 || hourEnding > 24 {
			continue
		}
		kwh := anyToFloat(r["planned_kwh"])
		if math.IsNaN(kwh) {
			continue
		}
		orientation := int(anyToFloat(r["orientation_idx"]))
		inner, ok := byHour[hourEnding]
		if !ok {
			inner = map[int]float64{}
			byHour[hourEnding] = inner
		}
		inner[orientation] = kwh
	}
	out := map[int]float64{}
	for hourEnding, inner := range byHour {
		sum := 0.0
		for _, v := range inner {
			sum += v
		}
		if sum > 0 {
			out[hourEnding-1] = sum // 1 h interval → kWh ≡ avg kW
		}
	}
	return out
}

func anyToFloat(v any) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(t), 64)
		if err != nil {
			return math.NaN()
		}
		return f
	case json.Number:
		f, err := t.Float64()
		if err != nil {
			return math.NaN()
		}
		return f
	default:
		return math.NaN()
	}
}

// edgeHourWeather is one hour of the stored Open-Meteo forecast used by
// the planner UI's weather strip.
type edgeHourWeather struct {
	TempC    *float64 `json:"temp_c,omitempty"`
	CloudPct *float64 `json:"cloud_pct,omitempty"`
	IsDay    bool     `json:"is_day"`
}

// edgePvForecast converts stored irradiance forecasts into AC kW keyed
// by UTC hour start (GTI when available, plane-agnostic shortwave
// otherwise, scaled by the rated PV power and a fixed performance
// ratio) and also returns the display weather for the same hours.
func (h *Handlers) edgePvForecast(ctx context.Context, orgID string, from, to time.Time, pvRatedKw float64) (map[time.Time]float64, map[time.Time]edgeHourWeather, error) {
	out := map[time.Time]float64{}
	weather := map[time.Time]edgeHourWeather{}
	rows, err := h.edge.Pool.Query(ctx, `
		SELECT hour, COALESCE(gti_instant_wm2, shortwave_wm2),
		       temperature_2m_c, cloud_cover_pct, COALESCE(is_day, true)
		FROM weather_forecast_hourly
		WHERE organization_id = $1 AND hour >= $2 AND hour < $3`,
		orgID, from.UTC(), to.UTC())
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var hour time.Time
		var wm2, tempC, cloudPct *float64
		var isDay bool
		if err := rows.Scan(&hour, &wm2, &tempC, &cloudPct, &isDay); err != nil {
			return nil, nil, err
		}
		key := hour.UTC()
		weather[key] = edgeHourWeather{TempC: tempC, CloudPct: cloudPct, IsDay: isDay}
		if wm2 == nil || *wm2 <= 0 || pvRatedKw <= 0 {
			continue
		}
		out[key] = math.Min(pvRatedKw, *wm2/1000*pvRatedKw*pvPerformanceRatio)
	}
	return out, weather, rows.Err()
}

// edgeLoadProfileCache holds the per-site heuristic profile. Zero value
// ready to use; guarded by its own mutex because the planner loop and
// HTTP handlers (publish, preview) share one EdgeIngest.
type edgeLoadProfileCache struct {
	mu         sync.Mutex
	m          map[string]cachedLoadProfile
	inflight   map[string]chan struct{} // cold-start: collapse concurrent scans
	refreshing map[string]bool          // stale: one background refresh at a time
}

type cachedLoadProfile struct {
	byHour map[int]float64
	source string
	at     time.Time
}

// loadProfileTTL is how long a computed heuristic profile counts as
// fresh. The profile moves slowly (a 14-day median), so staleness is
// invisible — while recomputing it on a large site scans tens of
// millions of 1 s samples and takes minutes.
const loadProfileTTL = time.Hour

// edgeLoadProfile builds the heuristic load forecast: the median load
// per local hour over the trailing 14 days (spec: until the operator
// plan enters via the UI, shadow calibration uses this
// marked-as-heuristic profile).
//
// Caching policy keeps interactive callers fast:
//   - fresh entry → return it;
//   - stale entry → return it immediately (stale-while-revalidate) and
//     kick one background refresh;
//   - no entry (first call after boot) → compute synchronously, but
//     concurrent callers share a single scan instead of stacking up.
func (h *Handlers) edgeLoadProfile(ctx context.Context, orgID, tzName string) (map[int]float64, string, error) {
	cache := &h.edge.loadProfiles

	cache.mu.Lock()
	ent, has := cache.m[orgID]
	if has && time.Since(ent.at) < loadProfileTTL {
		cache.mu.Unlock()
		return ent.byHour, ent.source, nil
	}
	if has {
		if cache.refreshing == nil {
			cache.refreshing = map[string]bool{}
		}
		if !cache.refreshing[orgID] {
			cache.refreshing[orgID] = true
			go h.refreshEdgeLoadProfile(orgID, tzName)
		}
		cache.mu.Unlock()
		return ent.byHour, ent.source, nil
	}
	ch, joined := cache.inflight[orgID]
	if !joined {
		if cache.inflight == nil {
			cache.inflight = map[string]chan struct{}{}
		}
		ch = make(chan struct{})
		cache.inflight[orgID] = ch
	}
	cache.mu.Unlock()

	if joined {
		select {
		case <-ch:
			cache.mu.Lock()
			ent, has := cache.m[orgID]
			cache.mu.Unlock()
			if has {
				return ent.byHour, ent.source, nil
			}
			return nil, "", fmt.Errorf("load profile: initial computation failed")
		case <-ctx.Done():
			return nil, "", ctx.Err()
		}
	}

	byHour, source, err := h.queryEdgeLoadProfile(ctx, orgID, tzName)
	cache.mu.Lock()
	if err == nil {
		if cache.m == nil {
			cache.m = map[string]cachedLoadProfile{}
		}
		cache.m[orgID] = cachedLoadProfile{byHour: byHour, source: source, at: time.Now()}
	}
	delete(cache.inflight, orgID)
	close(ch)
	cache.mu.Unlock()
	if err != nil {
		return nil, "", err
	}
	return byHour, source, nil
}

// refreshEdgeLoadProfile recomputes one site's profile detached from
// any request context (the caller already got the stale copy).
func (h *Handlers) refreshEdgeLoadProfile(orgID, tzName string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	byHour, source, err := h.queryEdgeLoadProfile(ctx, orgID, tzName)

	cache := &h.edge.loadProfiles
	cache.mu.Lock()
	defer cache.mu.Unlock()
	delete(cache.refreshing, orgID)
	if err != nil {
		h.edge.Log.Warn("edge_load_profile_refresh", "site_id", orgID, "err", err)
		return
	}
	cache.m[orgID] = cachedLoadProfile{byHour: byHour, source: source, at: time.Now()}
}

func (h *Handlers) queryEdgeLoadProfile(ctx context.Context, orgID, tzName string) (map[int]float64, string, error) {
	rows, err := h.edge.Pool.Query(ctx, `
		SELECT extract(hour FROM bucket AT TIME ZONE $2)::int AS h,
		       percentile_cont(0.5) WITHIN GROUP (ORDER BY v) AS load_kw
		FROM (
			SELECT time_bucket('1 hour', time) AS bucket, avg(value) AS v
			FROM telemetry_samples
			WHERE organization_id = $1
			  AND metric_key = 'load_power_kw'
			  AND time >= now() - interval '14 days'
			GROUP BY 1
		) s
		GROUP BY 1`,
		orgID, tzName)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	out := map[int]float64{}
	for rows.Next() {
		var hour int
		var kw float64
		if err := rows.Scan(&hour, &kw); err != nil {
			return nil, "", err
		}
		out[hour] = kw
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	if len(out) == 0 {
		return out, "none", nil
	}
	return out, "heuristic_median_14d", nil
}

// edgeLatestSoc returns the freshest soc_percent within 2 hours, or 0.
func (h *Handlers) edgeLatestSoc(ctx context.Context, orgID string) float64 {
	var soc float64
	err := h.edge.Pool.QueryRow(ctx, `
		SELECT value FROM telemetry_samples
		WHERE organization_id = $1 AND metric_key = 'soc_percent'
		  AND time >= now() - interval '2 hours'
		ORDER BY time DESC LIMIT 1`, orgID).Scan(&soc)
	if err != nil {
		return 0
	}
	return soc
}

// RunEdgePlannerLoop republishes manifests for every configured site on
// `interval` (content-hash ids make unchanged plans no-ops). Runs until
// ctx is done; call from main in a goroutine.
func (h *Handlers) RunEdgePlannerLoop(ctx context.Context, sites []string, interval time.Duration) {
	if h.edge == nil || len(sites) == 0 {
		return
	}
	if interval <= 0 {
		interval = 30 * time.Minute
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		for _, site := range sites {
			res, err := h.PublishEdgeManifest(ctx, site)
			if err != nil {
				h.edge.Log.Warn("edge_planner", "site_id", site, "err", err)
				continue
			}
			if res.Published {
				h.edge.Log.Info("edge_planner_published", "site_id", site, "manifest_id", res.ManifestID)
			}
			if res.Skipped != "" {
				h.edge.Log.Info("edge_planner_skipped", "site_id", site, "reason", res.Skipped)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }

func round3(v float64) float64 { return math.Round(v*1000) / 1000 }
