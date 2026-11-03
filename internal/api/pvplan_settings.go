package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/nesh/sestelemetry/internal/pvplan"
)

// --- «Прогноз СЕС» settings: /api/v1/pv-plan-settings ---
//
// Where the PV plan's upstreams are: Open-Meteo (and its key) for the
// model, the n8n webhook for the stations still on the flow. They serve
// every organization, so only administrators of all of them edit them.

const (
	maxPvPlanSettingsBody = 8 << 10
	pvPlanTestTimeout     = 20 * time.Second
)

// pvPlanTestPlane is what the connection test asks Open-Meteo about:
// any plane will do, the test is about the address and the key.
var pvPlanTestPlane = pvplan.Plane{Latitude: 50.45, Longitude: 30.52, TiltDeg: 30}

// errPvKeyElsewhere refuses the stored key to Open-Meteo addresses it
// wasn't saved with: otherwise one edit could quietly send it to any
// server.
var errPvKeyElsewhere = errors.New("адресу Open-Meteo змінено: введіть ключ ще раз, щоб він не пішов на іншу адресу")

// PvPlanSettingsResponse is the payload of GET /api/v1/pv-plan-settings:
// the key itself never leaves the server, only whether one is stored.
type PvPlanSettingsResponse struct {
	pvplan.Settings
	APIKeyConfigured bool `json:"api_key_configured"`
	// Saved is false until the first save.
	Saved bool `json:"saved"`
	// Defaults are the addresses empty fields stand for.
	Defaults pvplan.Settings `json:"defaults"`
}

// PvPlanSettingsRequest is the body of PUT /api/v1/pv-plan-settings and
// of the connection test.
type PvPlanSettingsRequest struct {
	pvplan.Settings
	// APIKey omitted or null keeps the stored Open-Meteo key, so editing
	// an address never requires retyping it. An empty string clears it.
	APIKey *string `json:"api_key"`
}

// PvPlanUpstreamCheck is one upstream's answer to the connection test.
type PvPlanUpstreamCheck struct {
	OK bool `json:"ok"`
	// Hours is how many hours with data came back.
	Hours int    `json:"hours"`
	Error string `json:"error,omitempty"`
}

// PvPlanSettingsTestResponse reports the connection test. N8N asks the
// flow about N8NOrganizationID's today and is absent when no served
// station has an n8n code.
type PvPlanSettingsTestResponse struct {
	Forecast          PvPlanUpstreamCheck  `json:"forecast"`
	PreviousRuns      PvPlanUpstreamCheck  `json:"previous_runs"`
	N8N               *PvPlanUpstreamCheck `json:"n8n,omitempty"`
	N8NOrganizationID string               `json:"n8n_organization_id,omitempty"`
}

func (h *Handlers) pvPlanSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.getPvPlanSettings(w, r)
	case http.MethodPut:
		h.putPvPlanSettings(w, r)
	default:
		w.Header().Set("Allow", "GET, PUT")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handlers) getPvPlanSettings(w http.ResponseWriter, r *http.Request) {
	settings, keySet, saved, err := h.store.PvPlanSettings(r.Context())
	if err != nil {
		h.log.Error("api_pv_plan_settings_get", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, PvPlanSettingsResponse{
		Settings:         settings,
		APIKeyConfigured: keySet,
		Saved:            saved,
		Defaults:         pvplan.DefaultSettings(),
	})
}

func decodePvPlanSettingsRequest(w http.ResponseWriter, r *http.Request) (PvPlanSettingsRequest, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxPvPlanSettingsBody)
	defer r.Body.Close()
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var req PvPlanSettingsRequest
	if err := dec.Decode(&req); err != nil {
		return PvPlanSettingsRequest{}, fmt.Errorf("invalid JSON body: %v", err)
	}
	req.Normalize()
	if req.APIKey != nil {
		// A key pasted from a password manager often carries a
		// trailing newline, and the refusal it causes says nothing
		// about it.
		trimmed := strings.TrimSpace(*req.APIKey)
		req.APIKey = &trimmed
	}
	return req, req.Validate()
}

func (h *Handlers) putPvPlanSettings(w http.ResponseWriter, r *http.Request) {
	req, err := decodePvPlanSettingsRequest(w, r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	stored, keySet, _, err := h.store.PvPlanSettings(ctx)
	if err != nil {
		h.log.Error("api_pv_plan_settings_put", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if req.APIKey == nil && keySet && !pvplan.SameOpenMeteo(req.Settings, stored) {
		http.Error(w, errPvKeyElsewhere.Error(), http.StatusBadRequest)
		return
	}
	if err := h.store.SavePvPlanSettings(ctx, req.Settings, req.APIKey); err != nil {
		h.log.Error("api_pv_plan_settings_put", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if h.pvUpstreams != nil {
		h.pvUpstreams.Forget()
	}
	h.log.Info("api_pv_plan_settings_saved",
		"by", principalLabel(r),
		"forecast_url", req.ForecastURL,
		"previous_runs_url", req.PreviousRunsURL,
		"n8n_webhook_url", req.N8NWebhookURL,
		"key_changed", req.APIKey != nil,
	)
	w.WriteHeader(http.StatusNoContent)
}

// pvPlanSettingsTest asks every upstream once with the settings in the
// body — typed on the page, not necessarily saved — so an administrator
// can check an address or a key before the 06:00 run finds out. Without
// a key in the body it uses the stored one, on the stored Open-Meteo
// addresses only.
//
//	POST /api/v1/pv-plan-settings/test
func (h *Handlers) pvPlanSettingsTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	req, err := decodePvPlanSettingsRequest(w, r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	key, err := h.pvPlanTestKey(ctx, req)
	switch {
	case errors.Is(err, errPvKeyElsewhere):
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	case err != nil:
		h.log.Error("api_pv_plan_settings_test", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	ctx, cancel := context.WithTimeout(ctx, pvPlanTestTimeout)
	defer cancel()
	weather := pvplan.NewWeatherClient(req.ForecastURL, req.PreviousRunsURL, key, nil)
	yesterday := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
	var (
		resp PvPlanSettingsTestResponse
		n8n  PvPlanUpstreamCheck
		wg   sync.WaitGroup
	)
	org, code, hasN8N := h.n8nTestStation()
	wg.Add(2)
	go func() {
		defer wg.Done()
		hours, err := weather.Forecast(ctx, pvPlanTestPlane)
		resp.Forecast = pvUpstreamCheck(len(hours), err)
	}()
	go func() {
		defer wg.Done()
		hours, err := weather.PreviousDay1(ctx, pvPlanTestPlane, yesterday, yesterday)
		resp.PreviousRuns = pvUpstreamCheck(len(hours), err)
	}()
	if hasN8N {
		wg.Add(1)
		go func() {
			defer wg.Done()
			byHour, err := pvplan.NewClient(req.N8NWebhookURL, nil).DayHourly(ctx, code, time.Now().In(h.pvPlanLoc()))
			n8n = pvUpstreamCheck(len(byHour), err)
		}()
	}
	wg.Wait()
	if hasN8N {
		resp.N8N, resp.N8NOrganizationID = &n8n, org
	}
	h.log.Info("api_pv_plan_settings_test",
		"forecast", resp.Forecast.OK, "previous_runs", resp.PreviousRuns.OK, "n8n", n8n.OK, "key", key != "")
	writeJSON(w, http.StatusOK, resp)
}

// pvPlanTestKey is the Open-Meteo key the test runs with: the typed
// one, else the stored one if the addresses are the ones it was saved
// with.
func (h *Handlers) pvPlanTestKey(ctx context.Context, req PvPlanSettingsRequest) (string, error) {
	if req.APIKey != nil {
		return *req.APIKey, nil
	}
	stored, keySet, _, err := h.store.PvPlanSettings(ctx)
	if err != nil || !keySet {
		return "", err
	}
	if !pvplan.SameOpenMeteo(req.Settings, stored) {
		return "", errPvKeyElsewhere
	}
	return h.store.PvPlanOpenMeteoKey(ctx)
}

// n8nTestStation is the station the test asks the flow about: the first
// served one the flow has a code for.
func (h *Handlers) n8nTestStation() (org, code string, ok bool) {
	for _, o := range h.organizations {
		if code, ok := pvplan.ElevatorCodeFor(o.ID); ok {
			return o.ID, code, true
		}
	}
	return "", "", false
}

func pvUpstreamCheck(hours int, err error) PvPlanUpstreamCheck {
	var (
		netErr net.Error
		urlErr *url.Error
	)
	switch {
	case err == nil:
		return PvPlanUpstreamCheck{OK: true, Hours: hours}
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &netErr) && netErr.Timeout():
		return PvPlanUpstreamCheck{Error: fmt.Sprintf("немає відповіді за %s", pvPlanTestTimeout)}
	case errors.As(err, &urlErr):
		return PvPlanUpstreamCheck{Error: "не вдалося зʼєднатися: " + urlErr.Err.Error()}
	}
	return PvPlanUpstreamCheck{Error: err.Error()}
}
