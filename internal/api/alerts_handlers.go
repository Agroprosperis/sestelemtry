package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/nesh/sestelemetry/internal/alerts"
)

// maxAlertSettingsBody caps a settings payload. The largest legitimate
// body is a few dozen email addresses.
const maxAlertSettingsBody = 64 * 1024

// AlertSettingsResponse is the payload of GET /api/v1/alert-settings.
//
// The SMTP password is deliberately absent: the storage layer's read
// path never selects that column, so the only thing the dashboard learns
// is whether one is stored.
type AlertSettingsResponse struct {
	alerts.Settings
	SMTPPasswordConfigured bool `json:"smtp_password_configured"`
	// Saved is false when nothing has been stored yet and the payload
	// is the config.yaml fallback the watchdog would currently act on.
	// The form then shows real values instead of blanks, so the first
	// save does not silently downgrade a YAML-configured deployment.
	Saved bool `json:"saved"`
}

// AlertSettingsRequest is the body of PUT /api/v1/alert-settings.
type AlertSettingsRequest struct {
	alerts.Settings
	// SMTPPassword omitted or null keeps the stored password, so
	// editing a recipient list never requires retyping the mail
	// credentials (and the dashboard never has to hold them). An empty
	// string clears it.
	SMTPPassword *string `json:"smtp_password"`
}

// OrgAlertSettingsResponse is the payload of
// GET /api/v1/organization-alert-settings: overrides keyed by
// organization id. Organizations the operator never touched are absent,
// which the dashboard renders as "inherits the default list".
type OrgAlertSettingsResponse struct {
	Organizations map[string]alerts.OrgSettings `json:"organizations"`
}

// AlertTestEmailResponse reports where a test message was delivered.
type AlertTestEmailResponse struct {
	Recipients []string `json:"recipients"`
}

// SetAlertFallback installs the settings the API reports when nothing
// has been saved yet: the `alerts:` block from config.yaml, plus the
// SMTP_PASSWORD environment variable if the deployment uses one.
//
// cmd/api/main.go derives both through alerts.SettingsFromConfig, the
// same conversion the watchdog uses, so the settings page shows exactly
// what the daemon is currently acting on rather than blank defaults.
func (h *Handlers) SetAlertFallback(settings alerts.Settings, password string) {
	settings.Normalize()
	h.alertFallback = settings
	h.alertFallbackPassword = password
}

func (h *Handlers) alertSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.getAlertSettings(w, r)
	case http.MethodPut:
		h.putAlertSettings(w, r)
	default:
		w.Header().Set("Allow", "GET, PUT")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handlers) getAlertSettings(w http.ResponseWriter, r *http.Request) {
	settings, passwordSet, ok, err := h.store.GetAlertSettings(r.Context())
	if err != nil {
		h.log.Error("api_alert_settings_get", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	resp := AlertSettingsResponse{Settings: settings, SMTPPasswordConfigured: passwordSet, Saved: ok}
	if !ok {
		resp.Settings = h.alertFallback
		resp.SMTPPasswordConfigured = h.alertFallbackPassword != ""
	}
	resp.Settings.Normalize()
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handlers) putAlertSettings(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxAlertSettingsBody)
	defer r.Body.Close()
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var payload AlertSettingsRequest
	if err := dec.Decode(&payload); err != nil {
		http.Error(w, fmt.Sprintf("invalid JSON body: %v", err), http.StatusBadRequest)
		return
	}
	payload.Settings.Normalize()
	if err := payload.Settings.Validate(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	password := payload.SMTPPassword
	if password != nil {
		// A password pasted from a manager routinely carries a trailing
		// newline, and the resulting auth failure is opaque.
		trimmed := strings.TrimSpace(*password)
		password = &trimmed
	}
	if err := h.store.UpsertAlertSettings(r.Context(), payload.Settings, password); err != nil {
		h.log.Error("api_alert_settings_put", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	h.log.Info("api_alert_settings_put_ok",
		"enabled", payload.Settings.Enabled,
		"recipients", len(payload.Settings.Recipients),
		"password_changed", password != nil,
	)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) organizationAlertSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.getOrganizationAlertSettings(w, r)
	case http.MethodPut:
		h.putOrganizationAlertSettings(w, r)
	default:
		w.Header().Set("Allow", "GET, PUT")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handlers) getOrganizationAlertSettings(w http.ResponseWriter, r *http.Request) {
	overrides, err := h.store.LoadOrgAlertSettings(r.Context())
	if err != nil {
		h.log.Error("api_org_alert_settings_get", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if overrides == nil {
		overrides = map[string]alerts.OrgSettings{}
	}
	writeJSON(w, http.StatusOK, OrgAlertSettingsResponse{Organizations: overrides})
}

func (h *Handlers) putOrganizationAlertSettings(w http.ResponseWriter, r *http.Request) {
	orgID := strings.TrimSpace(r.URL.Query().Get("organization_id"))
	if orgID == "" {
		http.Error(w, "organization_id is required", http.StatusBadRequest)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxAlertSettingsBody)
	defer r.Body.Close()
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var payload alerts.OrgSettings
	if err := dec.Decode(&payload); err != nil {
		http.Error(w, fmt.Sprintf("invalid JSON body: %v", err), http.StatusBadRequest)
		return
	}
	payload.Normalize()
	if err := payload.Validate(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := h.store.UpsertOrgAlertSettings(r.Context(), orgID, payload); err != nil {
		h.log.Error("api_org_alert_settings_put", "err", err, "organization_id", orgID)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	h.log.Info("api_org_alert_settings_put_ok",
		"organization_id", orgID,
		"enabled", payload.Enabled,
		"recipients", len(payload.Recipients),
	)
	w.WriteHeader(http.StatusNoContent)
}

// alertSettingsTestEmail sends a message to the addresses that would
// actually receive an alert, so the operator can prove the whole chain
// works without waiting for a real outage. With `organization_id` it
// uses that organization's effective list; without it, the default one.
func (h *Handlers) alertSettingsTestEmail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ctx := r.Context()
	orgID := strings.TrimSpace(r.URL.Query().Get("organization_id"))

	settings, _, ok, err := h.store.GetAlertSettings(ctx)
	if err != nil {
		h.log.Error("api_alert_test_email", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	password := ""
	if ok {
		if password, err = h.store.AlertSMTPPassword(ctx); err != nil {
			h.log.Error("api_alert_test_email", "err", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
	} else {
		settings = h.alertFallback
		password = h.alertFallbackPassword
	}
	settings.Normalize()

	recipients, err := h.testEmailRecipients(ctx, settings, orgID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	mailer, err := alerts.NewMailer(settings.SMTP, password)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	msg := alerts.BuildTestMessage(h.alertTestDevices(orgID), time.Now(), time.Local)
	if err := mailer.Send(ctx, recipients, msg); err != nil {
		h.log.Error("api_alert_test_email_failed", "err", err, "organization_id", orgID)
		// The upstream relay is the problem, and the operator needs its
		// verbatim complaint ("535 auth failed", "connection refused")
		// to fix the settings they just typed.
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	h.log.Info("api_alert_test_email_sent", "organization_id", orgID, "recipients", len(recipients))
	writeJSON(w, http.StatusOK, AlertTestEmailResponse{Recipients: recipients})
}

// testEmailRecipients resolves who a test message goes to, ignoring the
// enabled switches: the point of the button is to verify delivery before
// arming the watchdog.
func (h *Handlers) testEmailRecipients(
	ctx context.Context,
	settings alerts.Settings,
	organizationID string,
) ([]string, error) {
	recipients := settings.Recipients
	if organizationID != "" {
		overrides, err := h.store.LoadOrgAlertSettings(ctx)
		if err != nil {
			return nil, fmt.Errorf("could not read organization settings")
		}
		if override, ok := overrides[organizationID]; ok && len(override.Recipients) > 0 {
			recipients = override.Recipients
		}
	}
	recipients = alerts.NormalizeRecipients(recipients)
	if len(recipients) == 0 {
		return nil, fmt.Errorf("no recipients configured")
	}
	return recipients, nil
}

// alertTestDevices lists what the test message enumerates: one
// organization when the operator tested a single row, the whole fleet
// otherwise.
func (h *Handlers) alertTestDevices(organizationID string) []alerts.Device {
	out := make([]alerts.Device, 0, len(h.organizations))
	for _, org := range h.organizations {
		if organizationID != "" && org.ID != organizationID {
			continue
		}
		out = append(out, alerts.Device{OrganizationID: org.ID, OrganizationName: org.Name})
	}
	return out
}
