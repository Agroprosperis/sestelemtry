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

// --- Planned grid outages: /api/v1/outage-account, /api/v1/outages ---
//
// The day chart shades the hours a station's operator plans to cut it off.
// Each elevator is its own consumer with its own e-svitlo cabinet: the
// station passport holds the cabinet's login, password and, optionally,
// the station's EIC. A background sync (outages.Syncer) reads every such
// cabinet hourly into outage_intervals, and request paths only ever read
// that table.

const (
	maxOutageAccountBody = 4 << 10
	outageTestTimeout    = 30 * time.Second
	// maxOutagesRange bounds GET /outages: the cabinet only lists today
	// and tomorrow, so the chart never needs more.
	maxOutagesRange = 3
	// outageUpcoming is how far ahead the passport lists outages.
	outageUpcoming = 48 * time.Hour
)

// outagesStore is the outages' Postgres side as the handlers see it.
type outagesStore interface {
	Account(ctx context.Context, organizationID string) (outages.AccountRow, outages.Read, bool, error)
	Password(ctx context.Context, organizationID string) (string, error)
	SaveAccount(ctx context.Context, a outages.Account, password *string) error
	DeleteAccount(ctx context.Context, organizationID string) error
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

// outagesTestError words a failed cabinet test for the passport.
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
	case errors.Is(err, outages.ErrNoAccount):
		return "У кабінеті немає рахунку юридичної особи — вкажіть EIC точки обліку"
	default:
		return err.Error()
	}
}

// errOutagePasswordElsewhere is a test or save that would use the stored
// password with a login or cabinet it was not saved for.
var errOutagePasswordElsewhere = errors.New("збережений пароль — від іншого логіна або кабінету: введіть пароль ще раз")

// --- Station account: /api/v1/outage-account ---

// OutageReadJSON is the outcome of the last read of a station's
// schedule. Times are empty until the first attempt or success.
type OutageReadJSON struct {
	AttemptedAt string `json:"attempted_at,omitempty"`
	SucceededAt string `json:"succeeded_at,omitempty"`
	Error       string `json:"error,omitempty"`
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

// OutageAccountResponse is the body of GET/PUT /api/v1/outage-account:
// the station's cabinet as the passport shows it. The password itself
// never leaves the server, only whether one is stored.
type OutageAccountResponse struct {
	OrganizationID     string `json:"organization_id"`
	Host               string `json:"host"`
	Login              string `json:"login"`
	EIC                string `json:"eic"`
	PasswordConfigured bool   `json:"password_configured"`
	// Saved is false until the passport has stored an account.
	Saved     bool               `json:"saved"`
	Read      OutageReadJSON     `json:"read"`
	Operators []outages.Operator `json:"operators"`
	// Upcoming are the stored outages from now on, for the next 48 hours.
	Upcoming []outages.Interval `json:"upcoming"`
}

// OutageAccountRequest is the body of PUT /api/v1/outage-account and of
// its test. An empty login takes the station off the hourly read.
type OutageAccountRequest struct {
	Host  string `json:"host"`
	Login string `json:"login"`
	EIC   string `json:"eic"`
	// Password omitted or null keeps the stored one, so editing the login
	// or the EIC never requires retyping it. An empty string clears it.
	Password *string `json:"password"`
}

// OutageTestResponse reports the cabinet test.
type OutageTestResponse struct {
	OK       bool   `json:"ok"`
	At       string `json:"at"`
	SignedIn bool   `json:"signed_in"`
	// Accounts is the legal accounts (a=) the cabinet holds.
	Accounts []string           `json:"accounts,omitempty"`
	Outages  []outages.Interval `json:"outages"`
	Error    string             `json:"error,omitempty"`
}

// outageAccount serves a station's cabinet: the passport reads and saves
// it here.
//
//	GET|PUT /api/v1/outage-account?organization_id=
func (h *Handlers) outageAccount(w http.ResponseWriter, r *http.Request) {
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
		h.getOutageAccount(w, r, orgID)
	case http.MethodPut:
		h.putOutageAccount(w, r, orgID)
	default:
		w.Header().Set("Allow", "GET, PUT")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handlers) getOutageAccount(w http.ResponseWriter, r *http.Request, orgID string) {
	ctx := r.Context()
	a, read, saved, err := h.outages.Account(ctx, orgID)
	if err != nil {
		h.log.Error("api_outage_account_get", "organization_id", orgID, "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	loc := h.outagesLocation()
	now := time.Now()
	upcoming, err := h.outages.Intervals(ctx, orgID, now, now.Add(outageUpcoming))
	if err != nil {
		h.log.Error("api_outage_account_upcoming", "organization_id", orgID, "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	resp := OutageAccountResponse{
		OrganizationID:     orgID,
		Host:               a.Host,
		Login:              a.Login,
		EIC:                a.EIC,
		PasswordConfigured: a.PasswordConfigured,
		Saved:              saved,
		Read:               outageReadToJSON(read),
		Operators:          outages.Operators,
		Upcoming:           make([]outages.Interval, 0, len(upcoming)),
	}
	for _, iv := range upcoming {
		iv.Start, iv.End = iv.Start.In(loc), iv.End.In(loc)
		resp.Upcoming = append(resp.Upcoming, iv)
	}
	writeJSON(w, http.StatusOK, resp)
}

func decodeOutageAccount(w http.ResponseWriter, r *http.Request, orgID string) (outages.Account, *string, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxOutageAccountBody)
	defer r.Body.Close()
	var req OutageAccountRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return outages.Account{}, nil, fmt.Errorf("не вдалося прочитати дані кабінету: %v", err)
	}
	a := outages.Account{
		OrganizationID: orgID,
		Host:           strings.TrimSpace(req.Host),
		Login:          strings.TrimSpace(req.Login),
		EIC:            outages.NormalizeEIC(req.EIC),
	}
	password := req.Password
	if password != nil {
		// A password pasted from a password manager often carries a
		// trailing newline, and the refusal it causes says nothing about it.
		trimmed := strings.TrimSpace(*password)
		password = &trimmed
	}
	return a, password, nil
}

func (h *Handlers) putOutageAccount(w http.ResponseWriter, r *http.Request, orgID string) {
	a, password, err := decodeOutageAccount(w, r, orgID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	if a.Login == "" {
		if err := h.outages.DeleteAccount(ctx, orgID); err != nil {
			h.log.Error("api_outage_account_delete", "organization_id", orgID, "err", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		h.log.Info("api_outage_account_deleted", "by", principalLabel(r), "organization_id", orgID)
		h.getOutageAccount(w, r, orgID)
		return
	}
	if err := a.Validate(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if password == nil {
		if err := h.outagePasswordStays(ctx, a); err != nil {
			h.writeOutagePasswordError(w, "api_outage_account_put", orgID, err)
			return
		}
	}
	if err := h.outages.SaveAccount(ctx, a, password); err != nil {
		h.log.Error("api_outage_account_save", "organization_id", orgID, "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if h.outagesSync != nil {
		h.outagesSync.Wake()
	}
	h.log.Info("api_outage_account_saved",
		"by", principalLabel(r),
		"organization_id", orgID,
		"host", a.Host,
		"login", a.Login,
		"eic", a.EIC,
		"password_changed", password != nil,
	)
	h.getOutageAccount(w, r, orgID)
}

// outagePasswordStays refuses to keep a stored password for a login or
// cabinet other than the one it was saved with.
func (h *Handlers) outagePasswordStays(ctx context.Context, a outages.Account) error {
	stored, _, saved, err := h.outages.Account(ctx, a.OrganizationID)
	if err != nil {
		return err
	}
	if saved && stored.PasswordConfigured && (stored.Host != a.Host || stored.Login != a.Login) {
		return errOutagePasswordElsewhere
	}
	return nil
}

func (h *Handlers) writeOutagePasswordError(w http.ResponseWriter, route, orgID string, err error) {
	if errors.Is(err, errOutagePasswordElsewhere) {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	h.log.Error(route, "organization_id", orgID, "err", err)
	http.Error(w, "internal server error", http.StatusInternalServerError)
}

// outageAccountTest signs in to a cabinet with the account in the body —
// typed on the passport, not necessarily saved — and reads its outages.
// Without a password in the body it uses the station's stored one, for
// the login and cabinet it was saved with only.
//
//	POST /api/v1/outage-account/test?organization_id=
func (h *Handlers) outageAccountTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	orgID := strings.TrimSpace(r.URL.Query().Get("organization_id"))
	if orgID == "" {
		http.Error(w, "organization_id is required", http.StatusBadRequest)
		return
	}
	if h.outages == nil {
		http.Error(w, "outages not configured", http.StatusServiceUnavailable)
		return
	}
	a, password, err := decodeOutageAccount(w, r, orgID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := a.Validate(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	var secret string
	if password != nil {
		secret = *password
	} else {
		if err := h.outagePasswordStays(ctx, a); err != nil {
			h.writeOutagePasswordError(w, "api_outage_account_test", orgID, err)
			return
		}
		if secret, err = h.outages.Password(ctx, orgID); err != nil {
			h.log.Error("api_outage_account_test", "organization_id", orgID, "err", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
	}
	if secret == "" {
		http.Error(w, "вкажіть пароль кабінету", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(ctx, outageTestTimeout)
	defer cancel()
	loc := h.outagesLocation()
	resp := OutageTestResponse{At: time.Now().In(loc).Format(time.RFC3339), Outages: []outages.Interval{}}
	probe, err := outages.NewClient(outages.BaseURL(a.Host), a.Login, secret, loc, nil).Probe(ctx, a.EIC)
	resp.SignedIn, resp.Accounts = probe.SignedIn, probe.Accounts
	for _, iv := range probe.Outages {
		iv.Start, iv.End = iv.Start.In(loc), iv.End.In(loc)
		resp.Outages = append(resp.Outages, iv)
	}
	if err != nil {
		resp.Error = outagesTestError(err)
	} else {
		resp.OK = true
	}
	h.log.Info("api_outage_account_test", "organization_id", orgID, "host", a.Host, "ok", resp.OK)
	writeJSON(w, http.StatusOK, resp)
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
