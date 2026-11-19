package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/nesh/sestelemetry/internal/outages"
)

// --- Planned grid outages: /api/v1/outage-settings, /api/v1/outage-point,
// /api/v1/outages ---
//
// The day chart shades the hours a station's operator plans to cut it off.
// The company's e-svitlo cabinets (one per operator) are set once on the
// settings page; each station's EIC code on its passport. A background
// sync (outages.Syncer) keeps outage_intervals filled, and request paths
// only ever read that table.

const (
	maxOutageSettingsBody = 4 << 10
	maxOutagePointBody    = 4 << 10
	outageTestTimeout     = 30 * time.Second
	// maxOutagesRange bounds GET /outages: the cabinet only lists today
	// and the days just ahead, so the chart never needs more.
	maxOutagesRange = 3
)

// outagesStore is the outages' Postgres side as the handlers see it.
type outagesStore interface {
	Accounts(ctx context.Context) ([]outages.AccountRow, error)
	Password(ctx context.Context, host string) (string, error)
	SaveAccount(ctx context.Context, a outages.Account, password *string) error
	Point(ctx context.Context, organizationID string) (outages.Point, outages.Read, bool, error)
	SavePoint(ctx context.Context, p outages.Point) error
	Intervals(ctx context.Context, organizationID string, from, to time.Time) ([]outages.Interval, error)
}

// outagesSyncer is the background sync as the handlers see it.
type outagesSyncer interface {
	Wake()
	Status() outages.Status
}

// SetOutages installs the outages store and its sync. Without them the
// outage routes answer 503.
func (h *Handlers) SetOutages(store outagesStore, sync outagesSyncer) {
	h.outages = store
	h.outagesSync = sync
}

// outagesLocation is the zone the cabinet's days and the query dates are in.
func (h *Handlers) outagesLocation() *time.Location {
	loc, err := loadLocation("Europe/Kyiv")
	if err != nil {
		return time.UTC
	}
	return loc
}

// outagesTestError words a failed cabinet test for the settings page.
func outagesTestError(err error) string {
	switch {
	case errors.Is(err, outages.ErrUnauthorized):
		if msg := outages.Refusal(err); msg != "" {
			return "Кабінет не впустив: " + msg
		}
		return "Невірний логін або пароль"
	case errors.Is(err, outages.ErrBlocked):
		return "Кабінет закрив доступ перевіркою на бота"
	case errors.Is(err, outages.ErrUnreachable), errors.Is(err, context.DeadlineExceeded):
		return "Немає з'єднання з кабінетом"
	case errors.Is(err, outages.ErrNoData):
		// The wrapped detail says what the cabinet sent instead.
		return "Кабінет відповів не даними про відключення: " + err.Error()
	default:
		return err.Error()
	}
}

// --- Cabinet accounts: /api/v1/outage-settings ---

// OutageAccountJSON is one operator's cabinet as the settings page sees
// it: the password itself never leaves the server.
type OutageAccountJSON struct {
	Host               string `json:"host"`
	Name               string `json:"name"`
	Login              string `json:"login"`
	PasswordConfigured bool   `json:"password_configured"`
}

// OutageSettingsResponse is the payload of GET /api/v1/outage-settings.
type OutageSettingsResponse struct {
	Accounts []OutageAccountJSON `json:"accounts"`
	Sync     outages.Status      `json:"sync"`
}

// OutageAccountRequest is one cabinet in PUT /api/v1/outage-settings.
type OutageAccountRequest struct {
	Host  string `json:"host"`
	Login string `json:"login"`
	// Password omitted or null keeps the stored one, so editing the
	// login never requires retyping it. An empty string clears it.
	Password *string `json:"password"`
}

// OutageSettingsRequest is the body of PUT /api/v1/outage-settings.
type OutageSettingsRequest struct {
	Accounts []OutageAccountRequest `json:"accounts"`
}

// OutageTestRequest is the body of POST /api/v1/outage-settings/test.
type OutageTestRequest struct {
	Host     string  `json:"host"`
	Login    string  `json:"login"`
	Password *string `json:"password"`
	// EIC, when given, also reads that point's outages.
	EIC string `json:"eic"`
}

// OutageTestResponse reports the cabinet test.
type OutageTestResponse struct {
	OK       bool               `json:"ok"`
	At       string             `json:"at"`
	SignedIn bool               `json:"signed_in"`
	Landing  string             `json:"landing,omitempty"`
	Path     string             `json:"path,omitempty"`
	Outages  []outages.Interval `json:"outages"`
	Error    string             `json:"error,omitempty"`
}

func (h *Handlers) outageSettings(w http.ResponseWriter, r *http.Request) {
	if h.outages == nil {
		http.Error(w, "outages not configured", http.StatusServiceUnavailable)
		return
	}
	switch r.Method {
	case http.MethodGet:
		h.getOutageSettings(w, r)
	case http.MethodPut:
		h.putOutageSettings(w, r)
	default:
		w.Header().Set("Allow", "GET, PUT")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handlers) getOutageSettings(w http.ResponseWriter, r *http.Request) {
	rows, err := h.outages.Accounts(r.Context())
	if err != nil {
		h.log.Error("api_outage_settings_get", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	// The store lists every operator, saved or not; keep that order and
	// never trust it to be complete.
	byHost := make(map[string]outages.AccountRow, len(rows))
	for _, a := range rows {
		byHost[a.Host] = a
	}
	resp := OutageSettingsResponse{Accounts: make([]OutageAccountJSON, 0, len(outages.Operators))}
	for _, op := range outages.Operators {
		a := byHost[op.Host]
		resp.Accounts = append(resp.Accounts, OutageAccountJSON{
			Host:               op.Host,
			Name:               op.Name,
			Login:              a.Login,
			PasswordConfigured: a.PasswordConfigured,
		})
	}
	if h.outagesSync != nil {
		resp.Sync = h.outagesSync.Status()
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handlers) putOutageSettings(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxOutageSettingsBody)
	defer r.Body.Close()
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var req OutageSettingsRequest
	if err := dec.Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("invalid JSON body: %v", err), http.StatusBadRequest)
		return
	}
	// Check every row first so a bad one saves nothing.
	seen := make(map[string]bool, len(req.Accounts))
	for i := range req.Accounts {
		a := &req.Accounts[i]
		a.Host = strings.TrimSpace(a.Host)
		a.Login = strings.TrimSpace(a.Login)
		if _, ok := outages.OperatorFor(a.Host); !ok {
			http.Error(w, fmt.Sprintf("невідомий кабінет %q", a.Host), http.StatusBadRequest)
			return
		}
		if seen[a.Host] {
			http.Error(w, fmt.Sprintf("кабінет %q вказано двічі", a.Host), http.StatusBadRequest)
			return
		}
		seen[a.Host] = true
		if a.Password != nil {
			// A password pasted from a password manager often carries
			// a trailing newline.
			trimmed := strings.TrimSpace(*a.Password)
			a.Password = &trimmed
		}
	}
	ctx := r.Context()
	for _, a := range req.Accounts {
		if err := h.outages.SaveAccount(ctx, outages.Account{Host: a.Host, Login: a.Login}, a.Password); err != nil {
			h.log.Error("api_outage_settings_put", "host", a.Host, "err", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		h.log.Info("api_outage_settings_saved",
			"by", principalLabel(r),
			"host", a.Host,
			"login", a.Login,
			"password_changed", a.Password != nil,
		)
	}
	if h.outagesSync != nil {
		h.outagesSync.Wake()
	}
	w.WriteHeader(http.StatusNoContent)
}

// outageSettingsTest signs in to a cabinet with the account in the body —
// typed on the page, not necessarily saved — and, given an EIC, reads that
// point's outages. Without a password in the body it uses the stored one.
//
//	POST /api/v1/outage-settings/test
func (h *Handlers) outageSettingsTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.outages == nil {
		http.Error(w, "outages not configured", http.StatusServiceUnavailable)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxOutageSettingsBody)
	defer r.Body.Close()
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var req OutageTestRequest
	if err := dec.Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("invalid JSON body: %v", err), http.StatusBadRequest)
		return
	}
	req.Host = strings.TrimSpace(req.Host)
	req.Login = strings.TrimSpace(req.Login)
	req.EIC = outages.NormalizeEIC(req.EIC)
	if _, ok := outages.OperatorFor(req.Host); !ok {
		http.Error(w, fmt.Sprintf("невідомий кабінет %q", req.Host), http.StatusBadRequest)
		return
	}
	if req.Login == "" {
		http.Error(w, "вкажіть логін кабінету", http.StatusBadRequest)
		return
	}
	if req.EIC != "" {
		if err := (outages.Point{EIC: req.EIC, Host: req.Host}).Validate(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	ctx := r.Context()
	var password string
	if req.Password != nil {
		password = strings.TrimSpace(*req.Password)
	} else {
		stored, err := h.outages.Password(ctx, req.Host)
		if err != nil {
			h.log.Error("api_outage_settings_test", "err", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		password = stored
	}
	if password == "" {
		http.Error(w, "вкажіть пароль кабінету", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(ctx, outageTestTimeout)
	defer cancel()
	loc := h.outagesLocation()
	resp := OutageTestResponse{At: time.Now().In(loc).Format(time.RFC3339), Outages: []outages.Interval{}}
	probe, err := outages.NewClient(outages.BaseURL(req.Host), req.Login, password, loc, nil).Probe(ctx, req.EIC)
	resp.SignedIn, resp.Landing, resp.Path = probe.SignedIn, probe.Landing, probe.Path
	if probe.Outages != nil {
		resp.Outages = probe.Outages
	}
	if err != nil {
		resp.Error = outagesTestError(err)
	} else {
		resp.OK = true
	}
	h.log.Info("api_outage_settings_test", "host", req.Host, "ok", resp.OK)
	writeJSON(w, http.StatusOK, resp)
}

// --- Station metering point: /api/v1/outage-point ---

// OutageReadJSON is the outcome of the last read of a point's schedule.
// Times are empty until the first attempt or success.
type OutageReadJSON struct {
	AttemptedAt string `json:"attempted_at,omitempty"`
	SucceededAt string `json:"succeeded_at,omitempty"`
	Error       string `json:"error,omitempty"`
}

// OutagePointResponse is the body of GET/PUT /api/v1/outage-point.
type OutagePointResponse struct {
	OrganizationID string `json:"organization_id"`
	EIC            string `json:"eic"`
	Host           string `json:"host"`
	// Saved is false until the passport has stored a point.
	Saved     bool               `json:"saved"`
	Read      OutageReadJSON     `json:"read"`
	Operators []outages.Operator `json:"operators"`
}

// OutagePointRequest is the body of PUT /api/v1/outage-point. An empty eic
// and host clear the point.
type OutagePointRequest struct {
	EIC  string `json:"eic"`
	Host string `json:"host"`
}

func outageReadToJSON(read outages.Read) OutageReadJSON {
	out := OutageReadJSON{Error: read.Error}
	if !read.AttemptedAt.IsZero() {
		out.AttemptedAt = read.AttemptedAt.UTC().Format(time.RFC3339)
	}
	if !read.SucceededAt.IsZero() {
		out.SucceededAt = read.SucceededAt.UTC().Format(time.RFC3339)
	}
	return out
}

// outagePoint serves a station's metering point: the passport page reads
// and saves it here.
//
//	GET|PUT /api/v1/outage-point?organization_id=
func (h *Handlers) outagePoint(w http.ResponseWriter, r *http.Request) {
	orgID := strings.TrimSpace(r.URL.Query().Get("organization_id"))
	if orgID == "" {
		http.Error(w, "organization_id is required", http.StatusBadRequest)
		return
	}
	if h.outages == nil {
		http.Error(w, "outages not configured", http.StatusServiceUnavailable)
		return
	}
	switch r.Method {
	case http.MethodGet:
		h.getOutagePoint(w, r, orgID)
	case http.MethodPut:
		h.putOutagePoint(w, r, orgID)
	default:
		w.Header().Set("Allow", "GET, PUT")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handlers) getOutagePoint(w http.ResponseWriter, r *http.Request, orgID string) {
	p, read, saved, err := h.outages.Point(r.Context(), orgID)
	if err != nil {
		h.log.Error("api_outage_point_get", "organization_id", orgID, "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, OutagePointResponse{
		OrganizationID: orgID,
		EIC:            p.EIC,
		Host:           p.Host,
		Saved:          saved,
		Read:           outageReadToJSON(read),
		Operators:      outages.Operators,
	})
}

func (h *Handlers) putOutagePoint(w http.ResponseWriter, r *http.Request, orgID string) {
	var req OutagePointRequest
	// Unknown fields are allowed: the page may send back what GET gave it.
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxOutagePointBody)).Decode(&req); err != nil {
		http.Error(w, "не вдалося прочитати дані точки обліку: "+err.Error(), http.StatusBadRequest)
		return
	}
	p := outages.Point{
		OrganizationID: orgID,
		EIC:            outages.NormalizeEIC(req.EIC),
		Host:           strings.TrimSpace(req.Host),
	}
	// Both empty clears the point; anything else must be askable.
	if p.EIC != "" || p.Host != "" {
		if err := p.Validate(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	if err := h.outages.SavePoint(r.Context(), p); err != nil {
		h.log.Error("api_outage_point_save", "organization_id", orgID, "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if h.outagesSync != nil {
		h.outagesSync.Wake()
	}
	h.log.Info("api_outage_point_saved",
		"by", principalLabel(r),
		"organization_id", orgID,
		"host", p.Host,
		"eic", p.EIC,
	)
	h.getOutagePoint(w, r, orgID)
}

// --- Intervals: /api/v1/outages ---

// OutagesResponse is the body of GET /api/v1/outages.
type OutagesResponse struct {
	OrganizationID string `json:"organization_id"`
	From           string `json:"from"`
	To             string `json:"to"`
	// Intervals overlap [from 00:00, to+1 day 00:00) in Kyiv time.
	Intervals []outages.Interval `json:"intervals"`
}

// outagesList serves a station's planned outages overlapping a range of
// Kyiv civil days, both ends included.
//
//	GET /api/v1/outages?organization_id=&from=YYYY-MM-DD&to=YYYY-MM-DD
func (h *Handlers) outagesList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	orgID := strings.TrimSpace(r.URL.Query().Get("organization_id"))
	if orgID == "" {
		http.Error(w, "organization_id is required", http.StatusBadRequest)
		return
	}
	loc := h.outagesLocation()
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
	// Days, not hours: a DST change makes a day 23 or 25 hours long.
	if to.After(from.AddDate(0, 0, maxOutagesRange)) {
		http.Error(w, fmt.Sprintf("range must be at most %d days", maxOutagesRange), http.StatusBadRequest)
		return
	}
	if h.outages == nil {
		http.Error(w, "outages not configured", http.StatusServiceUnavailable)
		return
	}
	list, err := h.outages.Intervals(r.Context(), orgID, from, to.AddDate(0, 0, 1))
	if err != nil {
		h.log.Error("api_outages", "organization_id", orgID, "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	resp := OutagesResponse{
		OrganizationID: orgID,
		From:           from.Format("2006-01-02"),
		To:             to.Format("2006-01-02"),
		Intervals:      make([]outages.Interval, 0, len(list)),
	}
	for _, iv := range list {
		iv.Start, iv.End = iv.Start.In(loc), iv.End.In(loc)
		resp.Intervals = append(resp.Intervals, iv)
	}
	writeJSON(w, http.StatusOK, resp)
}
