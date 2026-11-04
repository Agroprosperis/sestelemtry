package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/nesh/sestelemetry/internal/economics"
	"github.com/nesh/sestelemetry/internal/fxrates"
	"github.com/nesh/sestelemetry/internal/storage"
)

// --- Exchange rates: /api/v1/fx-rates, /api/v1/fx-settings ---
//
// The economics page can show money in USD or EUR. Rates come from the
// ERP «apbank» service set on the «Курси валют ERP» page; a background sync
// (fxrates.Syncer) keeps one rate per civil day in fx_rates, and request
// paths only ever read that table.

const (
	maxFxSettingsBody = 4 << 10
	fxTestTimeout     = 20 * time.Second
	// maxFxRatesRange bounds GET /fx-rates: the economics reach back to
	// 2024 and CAPEX versions sit inside that span.
	maxFxRatesRange = 5 * 366 * 24 * time.Hour
)

// fxStore is the rates' Postgres side as the handlers see it.
type fxStore interface {
	Settings(ctx context.Context) (fxrates.Settings, bool, bool, error)
	Password(ctx context.Context) (string, error)
	SaveSettings(ctx context.Context, settings fxrates.Settings, password *string) error
	Rates(ctx context.Context, currency string) (map[string]float64, error)
	Coverage(ctx context.Context) ([]storage.FxCoverage, error)
}

// fxSyncer is the background sync as the handlers see it.
type fxSyncer interface {
	Wake()
	Status() fxrates.Status
}

// SetFxRates installs the rates store and its sync. Without them the
// economics routes serve UAH only and the fx routes answer 503.
func (h *Handlers) SetFxRates(store fxStore, sync fxSyncer) {
	h.fx = store
	h.fxSync = sync
}

// errFxNoRates is a currency asked for before any of its rates is stored.
var errFxNoRates = errors.New("курсів цієї валюти ще немає: заповніть сторінку «Курси валют ERP»")

// fxView is one request's display currency: the rates it converts by.
// The zero value is UAH, which converts nothing.
type fxView struct {
	currency string
	table    fxrates.Table
	loc      *time.Location
}

// uah reports whether the view leaves money in UAH.
func (v fxView) uah() bool { return v.currency == "" || v.currency == "UAH" }

// code is the currency the response is in.
func (v fxView) code() string {
	if v.uah() {
		return "UAH"
	}
	return v.currency
}

// rate is UAH per unit on the civil day containing t, and whether the day
// borrowed a neighbour's rate.
func (v fxView) rate(t time.Time) (float64, bool) {
	if v.uah() {
		return 1, false
	}
	r, carried, ok := v.table.Rate(t.In(v.loc).Format("2006-01-02"))
	if !ok || r <= 0 {
		return 1, true
	}
	return r, carried
}

// scale is the factor that turns the UAH of the civil day containing t
// into the view's currency.
func (v fxView) scale(t time.Time) float64 {
	r, _ := v.rate(t)
	return 1 / r
}

// window is the mean rate and the count of borrowed days over [from, to].
func (v fxView) window(from, to time.Time) (float64, int) {
	if v.uah() {
		return 0, 0
	}
	return v.table.Window(from, to, v.loc, time.Now())
}

// parseCurrency reads ?currency=; empty means UAH.
func parseCurrency(r *http.Request) (string, error) {
	c := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("currency")))
	if c == "" || c == "UAH" {
		return "UAH", nil
	}
	if !fxrates.IsCurrency(c) {
		return "", fmt.Errorf("currency must be one of UAH, %s", strings.Join(fxrates.Currencies, ", "))
	}
	return c, nil
}

// fxViewFor loads the rates a request in currency needs. UAH needs none.
func (h *Handlers) fxViewFor(ctx context.Context, currency string, loc *time.Location) (fxView, error) {
	if currency == "" || currency == "UAH" {
		return fxView{currency: "UAH", loc: loc}, nil
	}
	if h.fx == nil {
		return fxView{}, errFxNoRates
	}
	rates, err := h.fx.Rates(ctx, currency)
	if err != nil {
		return fxView{}, err
	}
	table := fxrates.NewTable(rates)
	if table.Len() == 0 {
		return fxView{}, errFxNoRates
	}
	return fxView{currency: currency, table: table, loc: loc}, nil
}

// economicsIn is the economics service the view reads through: the
// service itself in UAH, else a read-only copy that converts every stored
// day at its own rate. Only rollups that never compute may use the copy.
func (h *Handlers) economicsIn(view fxView) *economics.Service {
	if view.uah() {
		return h.economics
	}
	return h.economics.InCurrency(view.scale)
}

// writeFxViewError answers a request whose currency could not be served.
func (h *Handlers) writeFxViewError(w http.ResponseWriter, route string, err error) {
	if errors.Is(err, errFxNoRates) {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	h.log.Error(route+"_fx", "err", err)
	http.Error(w, "internal server error", http.StatusInternalServerError)
}

// FxRateDay is one day of GET /api/v1/fx-rates.
type FxRateDay struct {
	Date       string  `json:"date"`
	UahPerUnit float64 `json:"uah_per_unit"`
	// Carried is true when the day has no rate of its own and borrows the
	// nearest one.
	Carried bool `json:"carried,omitempty"`
}

// FxRatesResponse is the body of GET /api/v1/fx-rates.
type FxRatesResponse struct {
	Currency string      `json:"currency"`
	Days     []FxRateDay `json:"days"`
}

// fxRates serves one currency's rates by day. Days after today are left
// out; a day without its own rate borrows the nearest stored one.
//
//	GET /api/v1/fx-rates?currency=USD&from=YYYY-MM-DD&to=YYYY-MM-DD
func (h *Handlers) fxRates(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	currency, err := parseCurrency(r)
	if err != nil || currency == "UAH" {
		http.Error(w, fmt.Sprintf("currency must be one of %s", strings.Join(fxrates.Currencies, ", ")), http.StatusBadRequest)
		return
	}
	loc := h.fxLocation()
	from, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(r.URL.Query().Get("from")), loc)
	if err != nil {
		http.Error(w, "from must be YYYY-MM-DD", http.StatusBadRequest)
		return
	}
	to, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(r.URL.Query().Get("to")), loc)
	if err != nil {
		http.Error(w, "to must be YYYY-MM-DD", http.StatusBadRequest)
		return
	}
	if to.Before(from) {
		http.Error(w, "to must be on or after from", http.StatusBadRequest)
		return
	}
	if to.Sub(from) > maxFxRatesRange {
		http.Error(w, "range must be at most 5 years", http.StatusBadRequest)
		return
	}
	view, err := h.fxViewFor(r.Context(), currency, loc)
	if err != nil {
		h.writeFxViewError(w, "api_fx_rates", err)
		return
	}
	today := time.Now().In(loc).Format("2006-01-02")
	resp := FxRatesResponse{Currency: currency, Days: []FxRateDay{}}
	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		key := d.Format("2006-01-02")
		if key > today {
			break
		}
		rate, carried := view.rate(d)
		resp.Days = append(resp.Days, FxRateDay{Date: key, UahPerUnit: rate, Carried: carried})
	}
	writeJSON(w, http.StatusOK, resp)
}

// fxLocation is the zone the rates' civil days are in.
func (h *Handlers) fxLocation() *time.Location {
	loc, err := loadLocation("Europe/Kyiv")
	if err != nil {
		return time.UTC
	}
	return loc
}

// --- «Курси валют ERP» settings ---

// errFxPasswordElsewhere refuses the stored password to an address it
// wasn't saved with: otherwise one edit could quietly send it to any
// server.
var errFxPasswordElsewhere = errors.New("адресу сервісу змінено: введіть пароль ще раз, щоб він не пішов на іншу адресу")

// FxCoverageJSON is how far one currency's rates reach.
type FxCoverageJSON struct {
	Currency string `json:"currency"`
	FirstDay string `json:"first_day"`
	LastDay  string `json:"last_day"`
	Days     int    `json:"days"`
	// MissingDays counts the days from fxrates.FirstDay to today that
	// have no rate yet.
	MissingDays int    `json:"missing_days"`
	LastFetched string `json:"last_fetched"`
}

// FxSettingsResponse is the payload of GET /api/v1/fx-settings: the
// password itself never leaves the server, only whether one is stored.
type FxSettingsResponse struct {
	fxrates.Settings
	PasswordConfigured bool             `json:"password_configured"`
	Saved              bool             `json:"saved"`
	DefaultURL         string           `json:"default_url"`
	FirstDay           string           `json:"first_day"`
	Coverage           []FxCoverageJSON `json:"coverage"`
	Sync               fxrates.Status   `json:"sync"`
}

// FxSettingsRequest is the body of PUT /api/v1/fx-settings and of the
// connection test.
type FxSettingsRequest struct {
	fxrates.Settings
	// Password omitted or null keeps the stored one, so editing the
	// login never requires retyping it. An empty string clears it.
	Password *string `json:"password"`
}

// FxSettingsTestResponse reports the connection test.
type FxSettingsTestResponse struct {
	OK    bool               `json:"ok"`
	At    string             `json:"at"`
	Rates map[string]float64 `json:"rates,omitempty"`
	Error string             `json:"error,omitempty"`
}

func (h *Handlers) fxSettings(w http.ResponseWriter, r *http.Request) {
	if h.fx == nil {
		http.Error(w, "exchange rates not configured", http.StatusServiceUnavailable)
		return
	}
	switch r.Method {
	case http.MethodGet:
		h.getFxSettings(w, r)
	case http.MethodPut:
		h.putFxSettings(w, r)
	default:
		w.Header().Set("Allow", "GET, PUT")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handlers) getFxSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	settings, passwordSet, saved, err := h.fx.Settings(ctx)
	if err != nil {
		h.log.Error("api_fx_settings_get", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	coverage, err := h.fx.Coverage(ctx)
	if err != nil {
		h.log.Error("api_fx_settings_coverage", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	resp := FxSettingsResponse{
		Settings:           settings,
		PasswordConfigured: passwordSet,
		Saved:              saved,
		DefaultURL:         fxrates.DefaultURL,
		FirstDay:           fxrates.FirstDay,
		Coverage:           fxCoverageToJSON(coverage, fxrates.Currencies, h.fxLocation(), time.Now()),
	}
	if h.fxSync != nil {
		resp.Sync = h.fxSync.Status()
	}
	writeJSON(w, http.StatusOK, resp)
}

// fxCoverageToJSON lists every currency, with or without stored rates.
func fxCoverageToJSON(rows []storage.FxCoverage, currencies []string, loc *time.Location, now time.Time) []FxCoverageJSON {
	first, _ := time.ParseInLocation("2006-01-02", fxrates.FirstDay, loc)
	local := now.In(loc)
	today := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	expected := int(today.Sub(first).Hours()/24+0.5) + 1
	byCurrency := make(map[string]storage.FxCoverage, len(rows))
	for _, c := range rows {
		byCurrency[c.Currency] = c
	}
	out := make([]FxCoverageJSON, 0, len(currencies))
	for _, cur := range currencies {
		c, ok := byCurrency[cur]
		row := FxCoverageJSON{Currency: cur, MissingDays: expected}
		if ok {
			row.FirstDay = c.FirstDay.Format("2006-01-02")
			row.LastDay = c.LastDay.Format("2006-01-02")
			row.Days = c.Days
			row.MissingDays = max(expected-c.Days, 0)
			row.LastFetched = c.LastFetched.UTC().Format(time.RFC3339)
		}
		out = append(out, row)
	}
	return out
}

func decodeFxSettingsRequest(w http.ResponseWriter, r *http.Request) (FxSettingsRequest, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFxSettingsBody)
	defer r.Body.Close()
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var req FxSettingsRequest
	if err := dec.Decode(&req); err != nil {
		return FxSettingsRequest{}, fmt.Errorf("invalid JSON body: %v", err)
	}
	req.Normalize()
	if req.Password != nil {
		// A password pasted from a password manager often carries a
		// trailing newline, and the refusal it causes says nothing
		// about it.
		trimmed := strings.TrimSpace(*req.Password)
		req.Password = &trimmed
	}
	return req, req.Validate()
}

func (h *Handlers) putFxSettings(w http.ResponseWriter, r *http.Request) {
	req, err := decodeFxSettingsRequest(w, r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	stored, passwordSet, _, err := h.fx.Settings(ctx)
	if err != nil {
		h.log.Error("api_fx_settings_put", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if req.Password == nil && passwordSet && req.Endpoint() != stored.Endpoint() {
		http.Error(w, errFxPasswordElsewhere.Error(), http.StatusBadRequest)
		return
	}
	if err := h.fx.SaveSettings(ctx, req.Settings, req.Password); err != nil {
		h.log.Error("api_fx_settings_put", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if h.fxSync != nil {
		h.fxSync.Wake()
	}
	h.log.Info("api_fx_settings_saved",
		"by", principalLabel(r),
		"url", req.Endpoint(),
		"username", req.Username,
		"password_changed", req.Password != nil,
	)
	w.WriteHeader(http.StatusNoContent)
}

// fxSettingsTest asks ERP for today's rates with the settings in the body
// — typed on the page, not necessarily saved. Without a password in the
// body it uses the stored one, at the stored address only.
//
//	POST /api/v1/fx-settings/test
func (h *Handlers) fxSettingsTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.fx == nil {
		http.Error(w, "exchange rates not configured", http.StatusServiceUnavailable)
		return
	}
	req, err := decodeFxSettingsRequest(w, r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.Username == "" {
		http.Error(w, "вкажіть логін ERP", http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	password, err := h.fxTestPassword(ctx, req)
	switch {
	case errors.Is(err, errFxPasswordElsewhere):
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	case err != nil:
		h.log.Error("api_fx_settings_test", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if password == "" {
		http.Error(w, "вкажіть пароль ERP", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(ctx, fxTestTimeout)
	defer cancel()
	at := time.Now().In(h.fxLocation())
	resp := FxSettingsTestResponse{At: at.Format(time.RFC3339)}
	rates, err := fxrates.NewClient(req.Settings, password, nil).Rates(ctx, at, fxrates.Currencies)
	if err != nil {
		resp.Error = fxTestError(err)
	} else {
		resp.OK = true
		resp.Rates = rates
	}
	h.log.Info("api_fx_settings_test", "url", req.Endpoint(), "ok", resp.OK)
	writeJSON(w, http.StatusOK, resp)
}

// fxTestPassword is the password the test runs with: the typed one,
// else the stored one if the address is the one it was saved with.
func (h *Handlers) fxTestPassword(ctx context.Context, req FxSettingsRequest) (string, error) {
	if req.Password != nil {
		return *req.Password, nil
	}
	stored, passwordSet, _, err := h.fx.Settings(ctx)
	if err != nil {
		return "", err
	}
	if !passwordSet {
		return "", nil
	}
	if req.Endpoint() != stored.Endpoint() {
		return "", errFxPasswordElsewhere
	}
	return h.fx.Password(ctx)
}

// fxTestError words a failed test for the settings page.
func fxTestError(err error) string {
	switch {
	case errors.Is(err, fxrates.ErrUnauthorized):
		return "Невірний логін або пароль"
	case errors.Is(err, fxrates.ErrNotFound):
		return "За цією адресою сервіс курсів не знайдено"
	case errors.Is(err, fxrates.ErrUnreachable), errors.Is(err, context.DeadlineExceeded):
		return "Немає з'єднання з сервером ERP"
	default:
		return err.Error()
	}
}
