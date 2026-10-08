package api

// Manual-control desk API (ems-spec docs/specs/ems_manual_control_mvp.md):
//
//	GET  /api/v1/dispatch/state?site_id=    now, envelope, 8 h fact + 24 h forecast, applied plan
//	POST /api/v1/dispatch/preview?site_id=  LP on a draft, nothing stored
//	POST /api/v1/dispatch/confirm?site_id=  store a new version and publish its plan
//
// The desk works on arrays aligned to start_hour (index 0 = the current
// hour); stored versions are keyed by UTC hour so they survive time
// moving on.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"time"

	"github.com/nesh/sestelemetry/internal/dispatch"
	"github.com/nesh/sestelemetry/internal/economics"
	"github.com/nesh/sestelemetry/internal/storage"
)

const (
	dispatchHistoryHours = 8
	dispatchFutureHours  = 24
	// dispatchUncappedImportKw stands in for a site without a saved
	// import limit: the LP needs a number, the UI shows it as not set.
	dispatchUncappedImportKw = 10000
	// The passport capacity is the usable 10–90 % SOC window
	// (resolveEdgeRatings), so it bounds the desk reserve; the
	// «Обмеження» reserve is only the default for a new draft.
	passportSocMinPct = 10
	passportSocMaxPct = 90
)

// dispatchEnv is everything a run needs besides the operator's model.
type dispatchEnv struct {
	siteID        string
	loc           *time.Location
	tz            string
	now           time.Time
	start         time.Time // current hour, UTC
	params        edgeSiteParams
	site          dispatch.Site
	importSet     bool
	exportRegime  string
	exportCeiling *float64
	defaults      dispatch.Constraints
	startSoc      *float64 // measured; nil = unknown
	inputs        dispatch.Inputs
	rdn           map[time.Time]float64 // UTC hour → RDN, history + future
	pv            map[time.Time]float64 // forecast
	weather       map[time.Time]edgeHourWeather
}

func (h *Handlers) loadDispatchEnv(ctx context.Context, siteID string) (*dispatchEnv, error) {
	e := h.edge
	tz := e.PlannerTimezone
	if tz == "" {
		tz = "Europe/Kyiv"
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return nil, err
	}
	zone := e.PlannerZone
	if zone == 0 {
		zone = 2
	}
	now := time.Now().In(loc)
	env := &dispatchEnv{siteID: siteID, loc: loc, tz: tz, now: now.UTC(), start: now.Truncate(time.Hour).UTC()}
	env.params = edgeSiteParams{Now: now}
	if err := h.applyEdgeSiteParams(ctx, siteID, &env.params); err != nil {
		return nil, err
	}
	settings, _ := h.loadEdgeSiteSettings(ctx, siteID)
	importKw := env.params.GridImportKw
	env.importSet = importKw > 0
	if !env.importSet {
		importKw = dispatchUncappedImportKw
	}
	env.site = dispatch.Site{
		CapacityKwh: env.params.CapacityKwh,
		ChargeKw:    env.params.ChargeMaxKw,
		DischargeKw: env.params.DischargeMaxKw,
		ImportKw:    importKw,
		SocMinPct:   passportSocMinPct,
		SocMaxPct:   math.Max(passportSocMinPct, math.Min(passportSocMaxPct, env.params.SocMax)),
		Tariffs:     env.params.Tariffs,
	}
	if settings != nil {
		env.exportRegime = settings.ExportRegime
		env.exportCeiling = settings.exportCeilingKw()
	}
	env.defaults = dispatch.Constraints{
		ReservePct:  math.Min(math.Max(env.params.SocMin, env.site.SocMinPct), env.site.SocMaxPct),
		GridCharge:  true,
		EssSale:     env.exportCeiling != nil,
		ImportCapKw: importKw,
		ExportCapKw: copyKw(env.exportCeiling),
	}

	to := env.start.Add(dispatchFutureHours * time.Hour)
	env.rdn, err = h.damPricesBetween(ctx, zone, env.start.Add(-dispatchHistoryHours*time.Hour), to, loc)
	if err != nil {
		return nil, fmt.Errorf("dam prices: %w", err)
	}
	env.pv, env.weather, err = h.edgePvForecast(ctx, siteID, env.start, to, env.params.PvRatedKw)
	if err != nil {
		return nil, fmt.Errorf("pv forecast: %w", err)
	}
	if plan, ok := h.edgePvPlanForecast(ctx, siteID, loc, env.start, to); ok {
		for k, v := range plan {
			env.pv[k] = v
		}
	}
	if soc := h.edgeLatestSoc(ctx, siteID); soc > 0 {
		env.startSoc = &soc
	}
	startPct := (env.site.SocMinPct + env.site.SocMaxPct) / 2
	if env.startSoc != nil {
		startPct = *env.startSoc
	}
	env.inputs = dispatch.Inputs{
		Site:     env.site,
		StartKwh: startPct / 100 * env.site.CapacityKwh,
		PV:       make([]float64, dispatchFutureHours),
		Rdn:      make([]*float64, dispatchFutureHours),
	}
	for i := 0; i < dispatchFutureHours; i++ {
		ts := env.start.Add(time.Duration(i) * time.Hour)
		env.inputs.PV[i] = env.pv[ts]
		if p, ok := env.rdn[ts]; ok {
			v := p
			env.inputs.Rdn[i] = &v
		}
	}
	return env, nil
}

func copyKw(v *float64) *float64 {
	if v == nil {
		return nil
	}
	c := *v
	return &c
}

// damPricesBetween loads hourly RDN (UAH/kWh) for the local days that
// cover [from, to), keyed by UTC hour start.
func (h *Handlers) damPricesBetween(ctx context.Context, zone int, from, to time.Time, loc *time.Location) (map[time.Time]float64, error) {
	var dates []string
	f := from.In(loc)
	for d := time.Date(f.Year(), f.Month(), f.Day(), 0, 0, 0, 0, loc); d.Before(to); d = d.AddDate(0, 0, 1) {
		dates = append(dates, d.Format("2006-01-02"))
	}
	return h.damPricesForDates(ctx, zone, dates, loc)
}

// --- stored model -------------------------------------------------------

// dispatchStoredModel is a confirmed plan keyed by UTC hour.
type dispatchStoredModel struct {
	Loads  []dispatchStoredLoad  `json:"loads"`
	Blocks []dispatchStoredBlock `json:"blocks"`
	Cfg    dispatch.Constraints  `json:"cfg"`
}

type dispatchStoredLoad struct {
	TS time.Time `json:"ts"`
	Kw float64   `json:"kw"`
}

type dispatchStoredBlock struct {
	ID        string               `json:"id"`
	From      time.Time            `json:"from"`
	Until     time.Time            `json:"until"`
	Type      dispatch.CommandType `json:"type"`
	Value     float64              `json:"value"`
	Direction string               `json:"direction,omitempty"`
}

// storedFromModel keys an aligned model by UTC hour; consecutive hours
// of one block (same ID and command) become one stored block.
func storedFromModel(m dispatch.Model, start time.Time) dispatchStoredModel {
	sm := dispatchStoredModel{Loads: []dispatchStoredLoad{}, Blocks: []dispatchStoredBlock{}, Cfg: m.Cfg}
	for i, v := range m.Loads {
		if v != nil {
			sm.Loads = append(sm.Loads, dispatchStoredLoad{TS: start.Add(time.Duration(i) * time.Hour), Kw: *v})
		}
	}
	for i := 0; i < len(m.Commands); i++ {
		c := m.Commands[i]
		if c == nil || c.Type == dispatch.CmdAuto {
			continue
		}
		j := i + 1
		for j < len(m.Commands) && m.Commands[j] != nil && *m.Commands[j] == *c {
			j++
		}
		sm.Blocks = append(sm.Blocks, dispatchStoredBlock{
			ID: c.ID, From: start.Add(time.Duration(i) * time.Hour), Until: start.Add(time.Duration(j) * time.Hour),
			Type: c.Type, Value: c.Value, Direction: c.Direction,
		})
		i = j - 1
	}
	return sm
}

// modelFromStored aligns a stored plan to `start` over n hours. Hours
// already gone are dropped; a block in progress keeps its remainder.
func modelFromStored(sm dispatchStoredModel, start time.Time, n int) dispatch.Model {
	m := dispatch.Model{Loads: make([]*float64, n), Commands: make([]*dispatch.Command, n), Cfg: sm.Cfg}
	index := func(ts time.Time) int { return int(math.Round(ts.Sub(start).Hours())) }
	for _, l := range sm.Loads {
		if i := index(l.TS); i >= 0 && i < n {
			v := l.Kw
			m.Loads[i] = &v
		}
	}
	for _, b := range sm.Blocks {
		cmd := dispatch.Command{Type: b.Type, Value: b.Value, Direction: b.Direction, ID: b.ID}
		for i := index(b.From); i < index(b.Until); i++ {
			if i >= 0 && i < n {
				c := cmd
				m.Commands[i] = &c
			}
		}
	}
	return m
}

// sanitizeConstraints fits a stored or defaulted cfg into the current
// envelope (limits may have changed in «Обмеження» since it was saved).
func sanitizeConstraints(env *dispatchEnv, cfg dispatch.Constraints) dispatch.Constraints {
	site := env.site
	cfg.ReservePct = math.Min(math.Max(cfg.ReservePct, site.SocMinPct), site.SocMaxPct)
	if cfg.ImportCapKw <= 0 || cfg.ImportCapKw > site.ImportKw {
		cfg.ImportCapKw = site.ImportKw
	}
	// A cleared cap is the operator's «експорт вимкнено», not «take the
	// ceiling»; only a value above the ceiling is cut down.
	switch {
	case env.exportCeiling == nil:
		cfg.ExportCapKw = nil
	case cfg.ExportCapKw != nil && *cfg.ExportCapKw > *env.exportCeiling:
		cfg.ExportCapKw = copyKw(env.exportCeiling)
	}
	if cfg.ExportCapKw == nil {
		cfg.EssSale = false
	}
	return cfg
}

// appliedModel is the latest confirmed plan aligned to env.start, or an
// empty AUTO model with the default limits.
func (h *Handlers) appliedModel(ctx context.Context, env *dispatchEnv) (dispatch.Model, storage.DispatchVersion, error) {
	ver, has, err := storage.LatestDispatchVersion(ctx, h.edge.Pool, env.siteID)
	if err != nil {
		return dispatch.Model{}, ver, err
	}
	sm := dispatchStoredModel{Cfg: env.defaults}
	if has {
		if err := json.Unmarshal(ver.Model, &sm); err != nil {
			return dispatch.Model{}, ver, fmt.Errorf("dispatch version %d: %w", ver.Version, err)
		}
	}
	m := modelFromStored(sm, env.start, dispatchFutureHours)
	m.Cfg = sanitizeConstraints(env, m.Cfg)
	return m, ver, nil
}

// --- request / response --------------------------------------------------

type dispatchDraftRequest struct {
	StartHour   time.Time            `json:"start_hour"`
	BaseVersion int                  `json:"base_version"`
	Loads       []*float64           `json:"loads"`
	Commands    []*dispatch.Command  `json:"commands"`
	Cfg         dispatch.Constraints `json:"cfg"`
}

// model aligns the draft to the server's current hour: hours that went
// by since the client loaded the page are dropped.
func (r dispatchDraftRequest) model(start time.Time) (dispatch.Model, error) {
	shift := 0
	if !r.StartHour.IsZero() {
		shift = int(math.Round(start.Sub(r.StartHour.UTC()).Hours()))
	}
	if shift < 0 {
		return dispatch.Model{}, fmt.Errorf("start_hour is in the future")
	}
	m := dispatch.Model{Loads: make([]*float64, dispatchFutureHours), Commands: make([]*dispatch.Command, dispatchFutureHours), Cfg: r.Cfg}
	for i := 0; i < dispatchFutureHours; i++ {
		src := i + shift
		if src < len(r.Loads) {
			m.Loads[i] = r.Loads[src]
		}
		if src < len(r.Commands) {
			if c := r.Commands[src]; c != nil && c.Type != dispatch.CmdAuto {
				m.Commands[i] = c
			}
		}
	}
	return m, nil
}

// validateModel runs the mockup's input checks on every command and on
// the horizon-wide limits. "" = valid.
func validateModel(env *dispatchEnv, m dispatch.Model) string {
	if msg := dispatch.ValidateConstraints(env.site, m.Cfg, env.exportCeiling); msg != "" {
		return msg
	}
	for i, c := range m.Commands {
		if c == nil {
			continue
		}
		if msg := dispatch.ValidateCommand(env.site, m.Cfg, *c); msg != "" {
			return hourLabel(env, i) + ": " + msg
		}
	}
	return ""
}

func hourLabel(env *dispatchEnv, i int) string {
	t := env.start.Add(time.Duration(i) * time.Hour).In(env.loc)
	return t.Format("15:04") + "–" + t.Add(time.Hour).Format("15:04")
}

type dispatchSiteInfo struct {
	CapacityKwh     float64  `json:"capacity_kwh"`
	ChargeKw        float64  `json:"charge_kw"`
	DischargeKw     float64  `json:"discharge_kw"`
	ImportKw        float64  `json:"import_kw"`
	ImportSet       bool     `json:"import_set"`
	SocMinPct       float64  `json:"soc_min_pct"`
	SocMaxPct       float64  `json:"soc_max_pct"`
	Eta             float64  `json:"eta"`
	PvRatedKw       float64  `json:"pv_rated_kw"`
	ExportRegime    string   `json:"export_regime"`
	ExportCeilingKw *float64 `json:"export_ceiling_kw"`
}

type dispatchTariffs struct {
	DistributionUahPerKwh float64 `json:"distribution_uah_per_kwh"`
	TransmissionUahPerKwh float64 `json:"transmission_uah_per_kwh"`
	SupplierMarginUah     float64 `json:"supplier_margin_uah_per_kwh"`
	SupplierMarginMode    string  `json:"supplier_margin_mode"`
	SupplierMarginPct     float64 `json:"supplier_margin_pct"`
	OtherFeesUahPerKwh    float64 `json:"other_fees_uah_per_kwh"`
	ExportDiscount        float64 `json:"export_discount"`
	DegradationUahPerKwh  float64 `json:"degradation_uah_per_kwh"`
	IncludeVat            bool    `json:"include_vat"`
	VatRate               float64 `json:"vat_rate"`
	RoundtripEfficiency   float64 `json:"roundtrip_efficiency"`
}

// dispatchFact is one measured hour (history): hourly means, SOC at the
// end of the hour, load from the node balance (grid + PV + BESS).
type dispatchFact struct {
	PvKw   *float64 `json:"pv_kw"`
	LoadKw *float64 `json:"load_kw"`
	GridKw *float64 `json:"grid_kw"` // + import / − export
	EssKw  *float64 `json:"ess_kw"`  // + discharge / − charge
	SocPct *float64 `json:"soc_pct"`
}

type dispatchHour struct {
	TS      time.Time        `json:"ts"`
	Offset  int              `json:"offset"` // hours from start_hour (history < 0)
	Rdn     *float64         `json:"rdn_uah_per_kwh"`
	Buy     *float64         `json:"buy_uah_per_kwh"`
	Sell    *float64         `json:"sell_uah_per_kwh"`
	PvKw    float64          `json:"pv_kw"` // forecast (future)
	Weather *edgeHourWeather `json:"weather,omitempty"`
	Fact    *dispatchFact    `json:"fact,omitempty"`
	// Plan is what the edge was following in a history hour.
	Plan *dispatchRunHour `json:"plan,omitempty"`
}

type dispatchModelDTO struct {
	Loads    []*float64           `json:"loads"`
	Commands []*dispatch.Command  `json:"commands"`
	Cfg      dispatch.Constraints `json:"cfg"`
}

type dispatchApplied struct {
	Version     int              `json:"version"`
	ConfirmedAt *time.Time       `json:"confirmed_at"`
	ConfirmedBy string           `json:"confirmed_by"`
	Model       dispatchModelDTO `json:"model"`
}

type dispatchStateResponse struct {
	SiteID        string               `json:"site_id"`
	Timezone      string               `json:"timezone"`
	Now           time.Time            `json:"now"`
	StartHour     time.Time            `json:"start_hour"`
	HistoryHours  int                  `json:"history_hours"`
	FutureHours   int                  `json:"future_hours"`
	Site          dispatchSiteInfo     `json:"site"`
	Tariffs       dispatchTariffs      `json:"tariffs"`
	StartSocPct   float64              `json:"start_soc_pct"`
	StartSocKnown bool                 `json:"start_soc_known"`
	Hours         []dispatchHour       `json:"hours"`
	Defaults      dispatch.Constraints `json:"defaults"`
	Applied       dispatchApplied      `json:"applied"`
	Result        dispatch.Result      `json:"result"`
}

type dispatchPreviewResponse struct {
	StartHour time.Time        `json:"start_hour"`
	Invalid   string           `json:"invalid,omitempty"`
	Result    *dispatch.Result `json:"result,omitempty"`
}

type dispatchConfirmResponse struct {
	Version    int               `json:"version"`
	Publish    EdgePublishResult `json:"publish"`
	Invalid    string            `json:"invalid,omitempty"`
	Issues     []dispatch.Issue  `json:"issues,omitempty"`
	Conflict   bool              `json:"conflict,omitempty"`
	ResultHint string            `json:"message,omitempty"`
}

// --- handlers ------------------------------------------------------------

func (h *Handlers) dispatchState(w http.ResponseWriter, r *http.Request) {
	siteID, ok := h.requireEdge(w, r, http.MethodGet)
	if !ok {
		return
	}
	env, err := h.loadDispatchEnv(r.Context(), siteID)
	if err != nil {
		h.edge.Log.Error("dispatch_state", "site_id", siteID, "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	m, ver, err := h.appliedModel(r.Context(), env)
	if err != nil {
		h.edge.Log.Error("dispatch_state_applied", "site_id", siteID, "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	res, err := dispatch.Simulate(env.inputs, m)
	if err != nil {
		h.edge.Log.Error("dispatch_state_lp", "site_id", siteID, "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	histFrom := env.start.Add(-dispatchHistoryHours * time.Hour)
	facts, err := h.dispatchFacts(r.Context(), env, histFrom, env.start)
	if err != nil {
		h.edge.Log.Warn("dispatch_state_facts", "site_id", siteID, "err", err)
	}
	var histHours []time.Time
	for ts := histFrom; ts.Before(env.start); ts = ts.Add(time.Hour) {
		histHours = append(histHours, ts)
	}
	runs, err := storage.DispatchRunsBetween(r.Context(), h.edge.Pool, siteID, histFrom, env.start)
	if err != nil {
		h.edge.Log.Warn("dispatch_state_runs", "site_id", siteID, "err", err)
	}
	plans := plansInForce(runs, histHours)

	resp := dispatchStateResponse{
		SiteID: siteID, Timezone: env.tz, Now: env.now, StartHour: env.start,
		HistoryHours: dispatchHistoryHours, FutureHours: dispatchFutureHours,
		Site:          env.siteInfo(),
		Tariffs:       tariffsDTO(env.site.Tariffs),
		StartSocPct:   env.inputs.StartKwh / env.site.CapacityKwh * 100,
		StartSocKnown: env.startSoc != nil,
		Defaults:      env.defaults,
		Applied: dispatchApplied{
			Version: ver.Version, ConfirmedBy: ver.ConfirmedBy,
			Model: dispatchModelDTO{Loads: m.Loads, Commands: m.Commands, Cfg: m.Cfg},
		},
		Result: res,
	}
	if ver.Version > 0 {
		t := ver.ConfirmedAt
		resp.Applied.ConfirmedAt = &t
	}
	for off := -dispatchHistoryHours; off < dispatchFutureHours; off++ {
		ts := env.start.Add(time.Duration(off) * time.Hour)
		hr := dispatchHour{TS: ts, Offset: off}
		if p, ok := env.rdn[ts]; ok {
			buy, sell := env.site.Prices(p)
			hr.Rdn, hr.Buy, hr.Sell = f64p(round3(p)), f64p(round3(buy)), f64p(round3(sell))
		}
		if off >= 0 {
			hr.PvKw = round1(env.pv[ts])
			if wx, ok := env.weather[ts]; ok {
				w := wx
				hr.Weather = &w
			}
		} else {
			hr.Fact = facts[ts]
			if p, ok := plans[ts]; ok {
				pl := p
				hr.Plan = &pl
			}
		}
		resp.Hours = append(resp.Hours, hr)
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handlers) dispatchPreview(w http.ResponseWriter, r *http.Request) {
	siteID, ok := h.requireEdge(w, r, http.MethodPost)
	if !ok {
		return
	}
	var req dispatchDraftRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	env, err := h.loadDispatchEnv(r.Context(), siteID)
	if err != nil {
		h.edge.Log.Error("dispatch_preview", "site_id", siteID, "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	m, err := req.model(env.start)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	resp := dispatchPreviewResponse{StartHour: env.start}
	if msg := validateModel(env, m); msg != "" {
		resp.Invalid = msg
		writeJSON(w, http.StatusOK, resp)
		return
	}
	res, err := dispatch.Simulate(env.inputs, m)
	if err != nil {
		h.edge.Log.Error("dispatch_preview_lp", "site_id", siteID, "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	resp.Result = &res
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handlers) dispatchConfirm(w http.ResponseWriter, r *http.Request) {
	siteID, ok := h.requireEdge(w, r, http.MethodPost)
	if !ok {
		return
	}
	var req dispatchDraftRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	env, err := h.loadDispatchEnv(ctx, siteID)
	if err != nil {
		h.edge.Log.Error("dispatch_confirm", "site_id", siteID, "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	m, err := req.model(env.start)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if msg := validateModel(env, m); msg != "" {
		writeJSON(w, http.StatusUnprocessableEntity, dispatchConfirmResponse{Invalid: msg})
		return
	}
	res, err := dispatch.Simulate(env.inputs, m)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Known shortfalls must be fixed before the plan is applied (guide
	// §6); hours past the known horizon are saved as intent only.
	if len(res.Issues) > 0 {
		writeJSON(w, http.StatusUnprocessableEntity, dispatchConfirmResponse{
			Issues: res.Issues, ResultHint: "Перед застосуванням виправте позначені інтервали.",
		})
		return
	}
	payload, err := json.Marshal(storedFromModel(m, env.start))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	by := ""
	if p := principalFrom(ctx); p != nil {
		by = p.Email
	}
	ver, err := storage.InsertDispatchVersion(ctx, h.edge.Pool, siteID, req.BaseVersion, by, payload)
	if errors.Is(err, storage.ErrDispatchVersionConflict) {
		writeJSON(w, http.StatusConflict, dispatchConfirmResponse{
			Conflict: true, ResultHint: "План змінив інший користувач. Оновіть сторінку й перенесіть свої зміни.",
		})
		return
	}
	if err != nil {
		h.edge.Log.Error("dispatch_confirm_store", "site_id", siteID, "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	pub, err := h.publishDispatch(ctx, siteID)
	if err != nil {
		h.edge.Log.Error("dispatch_confirm_publish", "site_id", siteID, "version", ver.Version, "err", err)
		http.Error(w, "план збережено, але публікація не вдалася: "+err.Error(), http.StatusBadGateway)
		return
	}
	h.edge.Log.Info("dispatch_confirmed", "site_id", siteID, "version", ver.Version, "by", by, "manifest_id", pub.ManifestID)
	writeJSON(w, http.StatusOK, dispatchConfirmResponse{Version: ver.Version, Publish: pub})
}

// --- publication ---------------------------------------------------------

// dispatchRunHour is one planned hour as stored with a publication.
type dispatchRunHour struct {
	TS        time.Time         `json:"ts"`
	LoadKw    float64           `json:"load_kw"`
	PvKw      float64           `json:"pv_kw"`
	EssKw     float64           `json:"ess_kw"`
	SocPct    float64           `json:"soc_pct"`
	ImportKw  float64           `json:"import_kw"`
	ExportKw  float64           `json:"export_kw"`
	Curtailed float64           `json:"curtailed_kw"`
	Command   *dispatch.Command `json:"command,omitempty"`
}

// publishDispatch republishes a site's plan: the latest confirmed
// version from the current hour, priced by the LP over the known
// horizon. Without known load the plan is empty and the edge runs
// self-consumption (no_plan_*).
func (h *Handlers) publishDispatch(ctx context.Context, siteID string) (EdgePublishResult, error) {
	e := h.edge
	env, err := h.loadDispatchEnv(ctx, siteID)
	if err != nil {
		return EdgePublishResult{SiteID: siteID}, err
	}
	m, ver, err := h.appliedModel(ctx, env)
	if err != nil {
		return EdgePublishResult{SiteID: siteID}, err
	}
	res, err := dispatch.Simulate(env.inputs, m)
	if err != nil {
		return EdgePublishResult{SiteID: siteID}, err
	}
	doc, run := buildDispatchManifest(env, m, res, ver.Version)

	out := EdgePublishResult{
		SiteID: siteID, ManifestID: doc.ManifestID, Source: doc.Source,
		ValidUntil: doc.ValidUntil.Format(time.RFC3339), LoadSource: "operator",
	}
	if doc.Plan != nil {
		out.Intervals = len(doc.Plan.Intervals)
	} else {
		out.LoadSource = "none"
	}
	_, latestID, hasLatest, err := storage.LatestEdgeManifest(ctx, e.Pool, siteID)
	if err != nil {
		return out, err
	}
	if !hasLatest || latestID != doc.ManifestID {
		payload, err := json.Marshal(doc)
		if err != nil {
			return out, err
		}
		if err := storage.UpsertEdgeManifest(ctx, e.Pool, siteID, doc.ManifestID, payload, doc.ValidFrom, doc.ValidUntil); err != nil {
			return out, err
		}
		out.Published = true
		e.Log.Info("dispatch_manifest_published", "site_id", siteID, "manifest_id", doc.ManifestID,
			"version", ver.Version, "intervals", out.Intervals)
	}
	hours, err := json.Marshal(run)
	if err != nil {
		return out, err
	}
	if err := storage.InsertDispatchRun(ctx, e.Pool, storage.DispatchRun{
		SiteID: siteID, StartHour: env.start, ManifestID: doc.ManifestID, Version: ver.Version, Hours: hours,
	}); err != nil {
		e.Log.Warn("dispatch_run_store", "site_id", siteID, "err", err)
	}
	return out, nil
}

// buildDispatchManifest turns a simulated plan into manifest-lite (the
// edge follows plan.intervals[].ess_kw) and the run record. Pure.
func buildDispatchManifest(env *dispatchEnv, m dispatch.Model, res dispatch.Result, version int) (edgeManifestDoc, []dispatchRunHour) {
	cfg := m.Cfg
	doc := edgeManifestDoc{
		SchemaVersion: "lite-1",
		SiteID:        env.siteID,
		IssuedAt:      env.now,
		ValidFrom:     env.now,
		ValidUntil:    env.start.Add(dispatchFutureHours * time.Hour),
		Mode:          "shadow",
		WriteEnabled:  false,
		Preset:        "economic_arbitrage",
		ExportAllowed: cfg.EssSale && !cfg.BlockExport && cfg.EffectiveExportCap() > 0,
		Source:        "dispatch",
		Note:          fmt.Sprintf("dispatch v%d", version),
	}
	doc.Limits.EssChargeMaxKw = env.site.ChargeKw
	doc.Limits.EssDischargeMaxKw = env.site.DischargeKw
	if env.importSet {
		doc.GridLimits.ImportLimitKw = cfg.ImportCapKw
	}
	doc.GridLimits.TargetImportKw = env.params.GridTargetKw
	doc.GridLimits.PvRatedKw = env.params.PvRatedKw
	doc.SocPolicy.MinEconomicPct = cfg.ReservePct
	doc.SocPolicy.MaxEconomicPct = env.site.SocMaxPct

	var intervals []edgePlanInterval
	run := []dispatchRunHour{}
	for i := 0; i < res.KnownHours; i++ {
		hr := res.Hours[i]
		ts := env.start.Add(time.Duration(i) * time.Hour)
		load, pv := *m.Loads[i], env.inputs.PV[i]
		iv := edgePlanInterval{
			TS:           ts,
			EssKw:        round1(*hr.P),
			SocTargetPct: round1(*hr.Soc),
			Action:       dispatchAction(*hr.P, *hr.Export, load, pv),
		}
		if env.inputs.Rdn[i] != nil {
			iv.PriceUah = round3(*env.inputs.Rdn[i])
		}
		intervals = append(intervals, iv)
		run = append(run, dispatchRunHour{
			TS: ts, LoadKw: round1(load), PvKw: round1(pv), EssKw: round1(*hr.P), SocPct: round1(*hr.Soc),
			ImportKw: round1(*hr.Import), ExportKw: round1(*hr.Export), Curtailed: round1(*hr.Curtailed),
			Command: m.Commands[i],
		})
	}
	if len(intervals) > 0 {
		doc.Plan = &edgePlanDoc{Granularity: "1h", LoadSource: "operator", Intervals: intervals}
	}
	doc.ManifestID = edgeManifestID(env.siteID, doc.ValidUntil, doc)
	return doc, run
}

// dispatchAction labels a planned hour for the edge console.
func dispatchAction(p, exportKw, load, pv float64) string {
	switch {
	case p > 0.5:
		for _, part := range dispatch.SplitFlow(p, pv, load) {
			if part.Key == "export" && part.Power > 0.5 && exportKw > 0.05 {
				return "export"
			}
		}
		return "discharge"
	case p < -0.5:
		return "charge"
	default:
		return "hold"
	}
}

// --- facts ---------------------------------------------------------------

// dispatchFacts reads hourly means of the measured series in [from, to):
// PV, PCC (40505, + import) and BESS (sign per org config), SOC at the
// end of each hour. Load is the node balance — on a dual-logger site
// load_power_kw (PV logger 40503) does not see the BESS.
func (h *Handlers) dispatchFacts(ctx context.Context, env *dispatchEnv, from, to time.Time) (map[time.Time]*dispatchFact, error) {
	out := map[time.Time]*dispatchFact{}
	if h.store == nil {
		return out, nil
	}
	avg, err := h.store.Timeseries(ctx, env.siteID,
		[]string{"active_pv_power_kw", "grid_connected_active_power_kw", "active_ess_power_kw"},
		from, to.Add(-time.Second), "1 hour", env.tz, AggregationAvg)
	if err != nil {
		return out, err
	}
	last, err := h.store.Timeseries(ctx, env.siteID, []string{"soc_percent"}, from, to.Add(-time.Second), "1 hour", env.tz, AggregationLast)
	if err != nil {
		return out, err
	}
	sign := 1.0
	if cfg, ok := h.energyFlowOrgs[env.siteID]; ok && cfg.EssDischargeSign == -1 {
		sign = -1
	}
	get := func(ts time.Time) *dispatchFact {
		key := ts.UTC().Truncate(time.Hour)
		f, ok := out[key]
		if !ok {
			f = &dispatchFact{}
			out[key] = f
		}
		return f
	}
	for _, p := range avg.Points {
		v := round1(p.Value)
		f := get(p.Time)
		switch p.MetricKey {
		case "active_pv_power_kw":
			f.PvKw = &v
		case "grid_connected_active_power_kw":
			f.GridKw = &v
		case "active_ess_power_kw":
			s := round1(p.Value * sign)
			f.EssKw = &s
		}
	}
	for _, p := range last.Points {
		v := round1(p.Value)
		get(p.Time).SocPct = &v
	}
	for _, f := range out {
		if f.PvKw != nil && f.GridKw != nil && f.EssKw != nil {
			load := round1(math.Max(0, *f.GridKw+*f.PvKw+*f.EssKw))
			f.LoadKw = &load
		}
	}
	return out, nil
}

// --- history: plan in force -------------------------------------------

// plansInForce maps each hour to the planned hour of the newest run
// published before that hour started (what the edge was following).
// runs must be oldest first (DispatchRunsBetween order).
func plansInForce(runs []storage.DispatchRun, hours []time.Time) map[time.Time]dispatchRunHour {
	out := map[time.Time]dispatchRunHour{}
	decoded := make([][]dispatchRunHour, len(runs))
	for i, r := range runs {
		_ = json.Unmarshal(r.Hours, &decoded[i])
	}
	for _, ts := range hours {
		for i := len(runs) - 1; i >= 0; i-- {
			if runs[i].RunAt.After(ts) {
				continue
			}
			found := false
			for _, hr := range decoded[i] {
				if hr.TS.Equal(ts) {
					out[ts] = hr
					found = true
					break
				}
			}
			if found {
				break
			}
			// The newest run before ts does not cover it (no known load
			// that far): the edge had no plan for the hour.
			if !runs[i].StartHour.After(ts) {
				break
			}
		}
	}
	return out
}

type dispatchDayHour struct {
	TS   time.Time        `json:"ts"`
	Rdn  *float64         `json:"rdn_uah_per_kwh"`
	Buy  *float64         `json:"buy_uah_per_kwh"`
	Sell *float64         `json:"sell_uah_per_kwh"`
	Fact *dispatchFact    `json:"fact,omitempty"`
	Plan *dispatchRunHour `json:"plan,omitempty"`
}

type dispatchDayResponse struct {
	SiteID   string            `json:"site_id"`
	Timezone string            `json:"timezone"`
	Date     string            `json:"date"`
	Site     dispatchSiteInfo  `json:"site"`
	Hours    []dispatchDayHour `json:"hours"`
}

// dispatchDay handles GET /api/v1/dispatch/day?site_id=&date=YYYY-MM-DD:
// a past day for read-only review — measured fact next to the plan the
// edge was following.
func (h *Handlers) dispatchDay(w http.ResponseWriter, r *http.Request) {
	siteID, ok := h.requireEdge(w, r, http.MethodGet)
	if !ok {
		return
	}
	ctx := r.Context()
	env, err := h.loadDispatchEnv(ctx, siteID)
	if err != nil {
		h.edge.Log.Error("dispatch_day", "site_id", siteID, "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	date := r.URL.Query().Get("date")
	day, err := time.ParseInLocation("2006-01-02", date, env.loc)
	if err != nil {
		http.Error(w, "date must be YYYY-MM-DD", http.StatusBadRequest)
		return
	}
	end := day.AddDate(0, 0, 1)
	if !day.Before(env.start) {
		http.Error(w, "the day must be in the past", http.StatusBadRequest)
		return
	}
	if end.After(env.start) {
		end = env.start
	}
	zone := h.edge.PlannerZone
	if zone == 0 {
		zone = 2
	}
	prices, err := h.damPricesForDates(ctx, zone, []string{date}, env.loc)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	facts, err := h.dispatchFacts(ctx, env, day.UTC(), end.UTC())
	if err != nil {
		h.edge.Log.Warn("dispatch_day_facts", "site_id", siteID, "err", err)
	}
	var hours []time.Time
	for ts := day.UTC(); ts.Before(end.UTC()); ts = ts.Add(time.Hour) {
		hours = append(hours, ts)
	}
	runs, err := storage.DispatchRunsBetween(ctx, h.edge.Pool, siteID, day.UTC(), end.UTC())
	if err != nil {
		h.edge.Log.Warn("dispatch_day_runs", "site_id", siteID, "err", err)
	}
	plans := plansInForce(runs, hours)

	resp := dispatchDayResponse{SiteID: siteID, Timezone: env.tz, Date: date, Site: env.siteInfo()}
	for _, ts := range hours {
		hr := dispatchDayHour{TS: ts, Fact: facts[ts]}
		if p, ok := prices[ts]; ok {
			buy, sell := env.site.Prices(p)
			hr.Rdn, hr.Buy, hr.Sell = f64p(round3(p)), f64p(round3(buy)), f64p(round3(sell))
		}
		if p, ok := plans[ts]; ok {
			pl := p
			hr.Plan = &pl
		}
		resp.Hours = append(resp.Hours, hr)
	}
	writeJSON(w, http.StatusOK, resp)
}

func (env *dispatchEnv) siteInfo() dispatchSiteInfo {
	return dispatchSiteInfo{
		CapacityKwh: env.site.CapacityKwh, ChargeKw: env.site.ChargeKw, DischargeKw: env.site.DischargeKw,
		ImportKw: env.site.ImportKw, ImportSet: env.importSet,
		SocMinPct: env.site.SocMinPct, SocMaxPct: env.site.SocMaxPct, Eta: env.site.Eta(),
		PvRatedKw: env.params.PvRatedKw, ExportRegime: env.exportRegime, ExportCeilingKw: env.exportCeiling,
	}
}

func tariffsDTO(t economics.Tariffs) dispatchTariffs {
	return dispatchTariffs{
		DistributionUahPerKwh: t.DistributionUahPerKwh, TransmissionUahPerKwh: t.TransmissionUahPerKwh,
		SupplierMarginUah: t.SupplierMarginUahPerKwh, SupplierMarginMode: t.SupplierMarginMode,
		SupplierMarginPct: t.SupplierMarginPct, OtherFeesUahPerKwh: t.OtherFeesUahPerKwh,
		ExportDiscount: t.ExportDiscount, DegradationUahPerKwh: t.DegradationUahPerKwh,
		IncludeVat: t.IncludeVat, VatRate: t.VatRate, RoundtripEfficiency: t.RoundtripEfficiency,
	}
}

func f64p(v float64) *float64 { return &v }
