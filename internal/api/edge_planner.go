package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"sync"
	"time"

	"github.com/nesh/sestelemetry/internal/economics"
	"github.com/nesh/sestelemetry/internal/pvplan"
)

// Edge manifest plumbing behind the desk publisher (dispatch_handlers.go):
// the manifest-lite document, the site envelope (passport, inventory,
// console settings), RDN prices, the PV forecast and the republish loop.
// GET /api/v1/edge/manifest serves the stored document to the device.

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
	// ExportAllowed keeps the edge shadow engine from clamping an
	// exporting plan back to the local deficit.
	ExportAllowed bool `json:"export_allowed,omitempty"`

	// Source is "dispatch" for desk plans; older rows carry "auto" or
	// "manual". The edge ignores it.
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
	Source     string `json:"source,omitempty"`
}

// edgeManifestPublish handles POST /api/v1/edge/manifest/publish?site_id=.
// Operator-facing (no edge token): republishes the applied desk plan
// now rather than on the next loop tick (e.g. after «Обмеження» changed).
func (h *Handlers) edgeManifestPublish(w http.ResponseWriter, r *http.Request) {
	siteID, ok := h.requireEdge(w, r, http.MethodPost)
	if !ok {
		return
	}
	res, err := h.publishDispatch(r.Context(), siteID)
	if err != nil {
		h.edge.Log.Error("edge_manifest_publish", "site_id", siteID, "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// edgeSiteParams is a site's planning envelope: tariffs, ratings,
// per-direction power limits, grid limits and the SOC window.
type edgeSiteParams struct {
	Now time.Time

	Tariffs     economics.Tariffs
	CapacityKwh float64
	PvRatedKw   float64
	SocMin      float64
	SocMax      float64

	ChargeMaxKw    float64
	DischargeMaxKw float64
	GridImportKw   float64
	GridTargetKw   float64
}

// applyEdgeSiteParams fills tariffs, ratings, per-direction limits and
// the SOC policy into in — passport/inventory numbers first, saved
// console settings on top (mockup panel-settings). in.Now must be set.
func (h *Handlers) applyEdgeSiteParams(ctx context.Context, siteID string, in *edgeSiteParams) error {
	in.Tariffs = h.resolveEdgeTariffs(ctx, siteID, in.Now)
	capacityKwh, powerKw, pvRatedKw, err := h.resolveEdgeRatings(ctx, siteID, in.Tariffs)
	if err != nil {
		return err
	}
	in.CapacityKwh, in.PvRatedKw = capacityKwh, pvRatedKw
	// Trusted SL PV rating (40396) caps every later override (§4.1);
	// resolveEdgeRatings returns exactly it (0 = not polled yet).
	slPvKw := in.PvRatedKw
	in.ChargeMaxKw, in.DischargeMaxKw = powerKw, powerKw
	in.SocMin, in.SocMax = 20.0, 90.0

	if s, saved := h.loadEdgeSiteSettings(ctx, siteID); saved && s != nil {
		if s.PvRatedKw > 0 {
			in.PvRatedKw = s.PvRatedKw
			if slPvKw > 0 && in.PvRatedKw > slPvKw {
				in.PvRatedKw = slPvKw
			}
		}
		if s.AutoChargeMaxKw > 0 {
			in.ChargeMaxKw = s.AutoChargeMaxKw
		}
		if s.AutoDischargeMaxKw > 0 {
			in.DischargeMaxKw = s.AutoDischargeMaxKw
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

// edgeManifestID derives a deterministic content id: same plan → same
// id → the edge's ETag poll sees 304 and nothing is re-applied.
func edgeManifestID(siteID string, horizonEnd time.Time, doc edgeManifestDoc) string {
	// Every dispatch-relevant field must be here: what the hash misses
	// never reaches the edge (live bug 2026-09-04 — export_allowed
	// flipped, id stayed, publish no-oped until the plan changed).
	hashable := struct {
		SiteID        string       `json:"site_id"`
		Preset        string       `json:"preset"`
		ExportAllowed bool         `json:"export_allowed"`
		Plan          *edgePlanDoc `json:"plan"`
		Limits        any          `json:"limits"`
		GridLimits    any          `json:"grid_limits"`
		SocPolicy     any          `json:"soc_policy"`
	}{siteID, doc.Preset, doc.ExportAllowed, doc.Plan, doc.Limits, doc.GridLimits, doc.SocPolicy}
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
// per diagnostics spec §4.1: the passport (prod tariff metadata) is the
// policy source; trusted SmartLogger registers only cap it. 40398 (ESS
// rated kW) is NOT trusted until its semantics are settled (ze reports
// 1123 vs the 864 passport), so power never comes from the SL.
func (h *Handlers) resolveEdgeRatings(ctx context.Context, orgID string, t economics.Tariffs) (capacityKwh, powerKw, pvRatedKw float64, err error) {
	// Trusted SL inventory (40484 kWh, 40396 PV kW) — hard caps only.
	var slCapacityKwh, slPvKw float64
	if h.store != nil {
		if inv, ok, err := h.store.LatestPlantInventory(ctx, orgID); err == nil && ok {
			if inv.ESSRatedKwh != nil {
				slCapacityKwh = *inv.ESSRatedKwh
			}
			if inv.PVRatedKw != nil {
				slPvKw = *inv.PVRatedKw
			}
		}
	}

	// Passport: tariff metadata maintained in prod (never git YAML).
	if t.EssCapacityKwh > 0 {
		// Tariffs store the usable 10–90% window; scale to nameplate.
		capacityKwh = t.EssCapacityKwh / 0.8
	}
	powerKw = t.EssPowerLimitKw
	pvRatedKw = slPvKw

	// Trusted SL values cap the passport (>0 only; ze had 40488=0).
	if slCapacityKwh > 0 && (capacityKwh <= 0 || slCapacityKwh < capacityKwh) {
		capacityKwh = slCapacityKwh
	}
	if capacityKwh <= 0 || powerKw <= 0 {
		return 0, 0, 0, fmt.Errorf("no ESS ratings for %s: need tariffs passport (ess_capacity_kwh / ess_power_limit_kw) or trusted plant inventory", orgID)
	}
	return capacityKwh, powerKw, pvRatedKw, nil
}

// damPricesForDates loads hourly RDN prices (UAH/kWh) for local
// delivery dates, keyed by UTC hour start.
func (h *Handlers) damPricesForDates(ctx context.Context, zone int, dates []string, loc *time.Location) (map[time.Time]float64, error) {
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

// The forecast flow itself (URL, site codes, row aggregation) lives in
// internal/pvplan, shared with /api/v1/pv-plan-summary so the planner's
// PV curve and the dashboard's plan-vs-actual card read the same
// numbers for a given day.

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
	code, known := pvplan.ElevatorCodeFor(siteID)
	if !known || h.pvPlan == nil {
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

	// A tighter deadline than the client's own: the planner answers a
	// live request and would rather fall back to the GTI estimate than
	// hold the caller for a slow upstream.
	reqCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	byHour, err := h.pvPlan.DayHourly(reqCtx, code, day)
	if err != nil {
		return nil, err
	}

	cache.mu.Lock()
	if cache.m == nil {
		cache.m = map[string]cachedPvPlan{}
	}
	cache.m[key] = cachedPvPlan{byHour: byHour, at: time.Now()}
	cache.mu.Unlock()
	return byHour, nil
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

// RunEdgePlannerLoop re-runs the LP of each site's applied desk version
// from the current SOC on `interval` and republishes the plan
// (content-hash ids make unchanged plans no-ops). Without a version or
// entered load the plan is empty and the edge runs self-consumption.
// Runs until ctx is done; call from main in a goroutine.
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
			res, err := h.publishDispatch(ctx, site)
			if err != nil {
				h.edge.Log.Warn("edge_planner", "site_id", site, "err", err)
				continue
			}
			if res.Published {
				h.edge.Log.Info("edge_planner_published", "site_id", site, "manifest_id", res.ManifestID, "intervals", res.Intervals)
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
