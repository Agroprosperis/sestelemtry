package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strings"
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

// EdgePublishResult is the response of the publish endpoint.
type EdgePublishResult struct {
	SiteID     string `json:"site_id"`
	ManifestID string `json:"manifest_id"`
	Published  bool   `json:"published"` // false = unchanged plan, nothing new stored
	Intervals  int    `json:"intervals"`
	LoadSource string `json:"load_source"`
	ValidUntil string `json:"valid_until"`
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

// PublishEdgeManifest builds the forward plan for one site and stores
// it as a manifest-lite version. The manifest_id is a content hash, so
// republishing an unchanged plan is a no-op (the edge keeps its cached
// copy via ETag).
func (h *Handlers) PublishEdgeManifest(ctx context.Context, siteID string) (EdgePublishResult, error) {
	e := h.edge
	if e == nil {
		return EdgePublishResult{}, fmt.Errorf("edge ingest not configured")
	}
	tzName := e.PlannerTimezone
	if tzName == "" {
		tzName = "Europe/Kyiv"
	}
	loc, err := time.LoadLocation(tzName)
	if err != nil {
		return EdgePublishResult{}, err
	}
	zone := e.PlannerZone
	if zone == 0 {
		zone = 2
	}
	now := time.Now().In(loc)

	tariffs := h.resolveEdgeTariffs(ctx, siteID, now)
	capacityKwh, powerKw, pvRatedKw, err := h.resolveEdgeRatings(ctx, siteID, tariffs)
	if err != nil {
		return EdgePublishResult{}, err
	}

	// Horizon: the current hour → the end of tomorrow (local).
	start := now.Truncate(time.Hour)
	end := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, 2)

	prices, err := h.edgeDAMPrices(ctx, zone, now, loc)
	if err != nil {
		return EdgePublishResult{}, fmt.Errorf("dam prices: %w", err)
	}
	pv, err := h.edgePvForecast(ctx, siteID, start, end, pvRatedKw)
	if err != nil {
		return EdgePublishResult{}, fmt.Errorf("pv forecast: %w", err)
	}
	loadByHour, loadSource, err := h.edgeLoadProfile(ctx, siteID, tzName)
	if err != nil {
		return EdgePublishResult{}, fmt.Errorf("load profile: %w", err)
	}
	startSoc := h.edgeLatestSoc(ctx, siteID)

	socMin, socMax := 20.0, 90.0
	if startSoc == 0 {
		startSoc = (socMin + socMax) / 2
	}

	var hours []economics.ForwardHour
	for ts := start; ts.Before(end); ts = ts.Add(time.Hour) {
		fh := economics.ForwardHour{
			TS:     ts,
			PvKw:   pv[ts.UTC()],
			LoadKw: loadByHour[ts.In(loc).Hour()],
		}
		if price, ok := prices[ts.UTC()]; ok {
			p := price
			fh.RdnUahPerKwh = &p
		}
		hours = append(hours, fh)
	}

	steps, err := economics.BuildForwardPlan(hours, economics.ForwardParams{
		Tariffs:     tariffs,
		CapacityKwh: capacityKwh,
		PowerKw:     powerKw,
		SocMinPct:   socMin,
		SocMaxPct:   socMax,
		StartSocPct: startSoc,
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
	}
	doc.Limits.EssChargeMaxKw = powerKw
	doc.Limits.EssDischargeMaxKw = powerKw
	doc.GridLimits.PvRatedKw = pvRatedKw
	doc.SocPolicy.MinEconomicPct = socMin
	doc.SocPolicy.MaxEconomicPct = socMax
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
	}

	// Unchanged content → same id → nothing to publish.
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

// edgePvForecast converts stored irradiance forecasts into AC kW keyed
// by UTC hour start: GTI when available, plane-agnostic shortwave
// otherwise, scaled by the rated PV power and a fixed performance ratio.
func (h *Handlers) edgePvForecast(ctx context.Context, orgID string, from, to time.Time, pvRatedKw float64) (map[time.Time]float64, error) {
	out := map[time.Time]float64{}
	if pvRatedKw <= 0 {
		return out, nil
	}
	rows, err := h.edge.Pool.Query(ctx, `
		SELECT hour, COALESCE(gti_instant_wm2, shortwave_wm2)
		FROM weather_forecast_hourly
		WHERE organization_id = $1 AND hour >= $2 AND hour < $3`,
		orgID, from.UTC(), to.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var hour time.Time
		var wm2 *float64
		if err := rows.Scan(&hour, &wm2); err != nil {
			return nil, err
		}
		if wm2 == nil || *wm2 <= 0 {
			continue
		}
		kw := math.Min(pvRatedKw, *wm2/1000*pvRatedKw*pvPerformanceRatio)
		out[hour.UTC()] = kw
	}
	return out, rows.Err()
}

// edgeLoadProfile builds the heuristic load forecast: the median load
// per local hour over the trailing 14 days (spec: until the operator
// plan UI exists, shadow calibration uses this marked-as-heuristic
// profile).
func (h *Handlers) edgeLoadProfile(ctx context.Context, orgID, tzName string) (map[int]float64, string, error) {
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
