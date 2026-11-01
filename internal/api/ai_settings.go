package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nesh/sestelemetry/internal/openai"
	"github.com/nesh/sestelemetry/internal/storage"
)

// AISettings configures the assistant behind POST /api/v1/chat. It is
// stored in ai_settings and edited on the dashboard's «ШІ-асистент»
// page by administrators of every organization; the API key is kept in
// its own column and never sent back.
type AISettings struct {
	Enabled bool `json:"enabled"`
	// Model is a Responses API model id; it has to support function
	// calling.
	Model string `json:"model"`
	// BaseURL points the client at a compatible gateway; empty means
	// openai.DefaultBaseURL.
	BaseURL string `json:"base_url"`
	// ReasoningEffort is sent to reasoning models only: the others
	// reject it, so it stays empty for them.
	ReasoningEffort string `json:"reasoning_effort"`
	// DailyLimit is how many turns one user gets per Kyiv day.
	DailyLimit int `json:"daily_limit"`
	// FollowUps offers the next questions under every answer, at the
	// cost of one more short model request per turn.
	FollowUps bool `json:"follow_ups"`
}

const (
	maxChatDailyLimit = 10000
	maxAISettingsBody = 16 << 10
	aiTestTimeout     = 30 * time.Second
	aiTestReplyRunes  = 300
)

var reasoningEfforts = []string{"", "minimal", "low", "medium", "high"}

// Normalize trims the fields and fills in the default daily limit.
func (s *AISettings) Normalize() {
	s.Model = strings.TrimSpace(s.Model)
	s.BaseURL = strings.TrimRight(strings.TrimSpace(s.BaseURL), "/")
	s.ReasoningEffort = strings.ToLower(strings.TrimSpace(s.ReasoningEffort))
	if s.DailyLimit <= 0 {
		s.DailyLimit = DefaultChatDailyLimit
	}
}

// Validate reports the first field an administrator has to fix, in
// Ukrainian: the page shows it as is.
func (s AISettings) Validate() error {
	if s.Enabled && s.Model == "" {
		return errors.New("вкажіть модель, щоб увімкнути асистента")
	}
	if !slices.Contains(reasoningEfforts, s.ReasoningEffort) {
		return errors.New("reasoning effort має бути minimal, low, medium, high або порожнім")
	}
	if s.BaseURL != "" {
		u, err := url.Parse(s.BaseURL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			return errors.New("адреса API має бути URL на кшталт https://api.openai.com")
		}
	}
	if s.DailyLimit > maxChatDailyLimit {
		return fmt.Errorf("денний ліміт — щонайбільше %d запитів", maxChatDailyLimit)
	}
	return nil
}

func (s AISettings) openaiConfig(apiKey string) openai.Config {
	return openai.Config{APIKey: apiKey, Model: s.Model, BaseURL: s.BaseURL, ReasoningEffort: s.ReasoningEffort}
}

// AISettingsResponse is the payload of GET /api/v1/ai-settings: the key
// itself never leaves the server, only whether one is stored.
type AISettingsResponse struct {
	AISettings
	APIKeyConfigured bool `json:"api_key_configured"`
	// Saved is false until the first save; the payload then holds the
	// defaults (next questions on).
	Saved bool `json:"saved"`
}

// AISettingsRequest is the body of PUT /api/v1/ai-settings and of the
// connection test.
type AISettingsRequest struct {
	AISettings
	// APIKey omitted or null keeps the stored key, so editing the limit
	// never requires retyping it (and the page never holds it). An empty
	// string clears it.
	APIKey *string `json:"api_key"`
}

// AISettingsTestResponse reports a successful connection test.
type AISettingsTestResponse struct {
	Model string       `json:"model"`
	Reply string       `json:"reply"`
	Usage openai.Usage `json:"usage"`
}

func (s *Store) GetAISettings(ctx context.Context) (AISettings, bool, bool, error) {
	payload, keySet, ok, err := storage.GetAISettings(ctx, s.pool)
	if err != nil || !ok {
		return AISettings{}, false, false, err
	}
	var out AISettings
	if err := json.Unmarshal(payload, &out); err != nil {
		return AISettings{}, false, false, fmt.Errorf("decode ai settings: %w", err)
	}
	return out, keySet, true, nil
}

func (s *Store) AIAPIKey(ctx context.Context) (string, error) {
	return storage.GetAIAPIKey(ctx, s.pool)
}

func (s *Store) UpsertAISettings(ctx context.Context, settings AISettings, apiKey *string) error {
	payload, err := json.Marshal(settings)
	if err != nil {
		return fmt.Errorf("encode ai settings: %w", err)
	}
	return storage.UpsertAISettings(ctx, s.pool, payload, apiKey)
}

func (h *Handlers) aiSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.getAISettings(w, r)
	case http.MethodPut:
		h.putAISettings(w, r)
	default:
		w.Header().Set("Allow", "GET, PUT")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handlers) getAISettings(w http.ResponseWriter, r *http.Request) {
	settings, keySet, saved, err := h.store.GetAISettings(r.Context())
	if err != nil {
		h.log.Error("api_ai_settings_get", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if !saved {
		settings.FollowUps = true
	}
	settings.Normalize()
	writeJSON(w, http.StatusOK, AISettingsResponse{AISettings: settings, APIKeyConfigured: keySet, Saved: saved})
}

func decodeAISettingsRequest(w http.ResponseWriter, r *http.Request) (AISettingsRequest, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxAISettingsBody)
	defer r.Body.Close()
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var req AISettingsRequest
	if err := dec.Decode(&req); err != nil {
		return AISettingsRequest{}, fmt.Errorf("invalid JSON body: %v", err)
	}
	req.Normalize()
	if req.APIKey != nil {
		// A key pasted from a password manager often carries a
		// trailing newline, and the 401 it causes says nothing about it.
		trimmed := strings.TrimSpace(*req.APIKey)
		req.APIKey = &trimmed
	}
	return req, req.Validate()
}

// storedKeyAllowed reports why the stored key may not serve req: it
// only goes to the address it was saved for, so pointing the assistant
// (or a test) at another host takes typing the key again — otherwise
// one edit could quietly send it to any server.
func storedKeyAllowed(req AISettingsRequest, stored AISettings, keySet bool) error {
	if !keySet {
		return errors.New("введіть ключ API")
	}
	if req.BaseURL != stored.BaseURL {
		return errors.New("адресу API змінено: введіть ключ ще раз, щоб він не пішов на іншу адресу")
	}
	return nil
}

func (h *Handlers) putAISettings(w http.ResponseWriter, r *http.Request) {
	req, err := decodeAISettingsRequest(w, r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	stored, keySet, _, err := h.store.GetAISettings(ctx)
	if err != nil {
		h.log.Error("api_ai_settings_put", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	stored.Normalize()
	switch {
	case req.APIKey != nil && *req.APIKey == "":
		if req.Enabled {
			http.Error(w, "без ключа API асистента не ввімкнути", http.StatusBadRequest)
			return
		}
	case req.APIKey == nil && keySet:
		if err := storedKeyAllowed(req, stored, keySet); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	case req.APIKey == nil && req.Enabled:
		http.Error(w, "введіть ключ API, щоб увімкнути асистента", http.StatusBadRequest)
		return
	}
	if err := h.store.UpsertAISettings(ctx, req.AISettings, req.APIKey); err != nil {
		h.log.Error("api_ai_settings_put", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if h.assistant != nil {
		h.assistant.forget()
	}
	h.log.Info("api_ai_settings_put_ok",
		"enabled", req.Enabled,
		"model", req.Model,
		"base_url", req.BaseURL,
		"reasoning_effort", req.ReasoningEffort,
		"daily_limit", req.DailyLimit,
		"key_changed", req.APIKey != nil,
	)
	w.WriteHeader(http.StatusNoContent)
}

// aiSettingsTest runs one tiny turn on the settings in the body — typed
// on the page, not necessarily saved — so an administrator can check
// the key and the model before switching the assistant on. Without a
// key in the body it uses the stored one, on the stored address only.
//
//	POST /api/v1/ai-settings/test
func (h *Handlers) aiSettingsTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	req, err := decodeAISettingsRequest(w, r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.Model == "" {
		http.Error(w, "вкажіть модель", http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	key := ""
	if req.APIKey != nil {
		key = *req.APIKey
	}
	if key == "" {
		stored, keySet, _, err := h.store.GetAISettings(ctx)
		if err == nil {
			stored.Normalize()
			if err := storedKeyAllowed(req, stored, keySet); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			key, err = h.store.AIAPIKey(ctx)
		}
		if err != nil {
			h.log.Error("api_ai_settings_test", "err", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
	}

	ctx, cancel := context.WithTimeout(ctx, aiTestTimeout)
	defer cancel()
	res, err := h.newAIModel(req.openaiConfig(key)).Respond(ctx, openai.Request{
		Instructions: "Відповідай одним коротким реченням українською.",
		Input:        []json.RawMessage{openai.UserMessage("Перевірка зв'язку з консоллю SESTelemetry: підтверди, що ти на зв'язку.")},
	}, nil)
	if err != nil {
		h.log.Error("api_ai_settings_test_failed", "model", req.Model, "base_url", req.BaseURL, "err", err)
		// Unlike a chat user, the administrator needs the API's own
		// complaint ("model not found", "invalid api key") to fix the
		// settings just typed.
		http.Error(w, aiTestFailure(err), http.StatusBadGateway)
		return
	}
	reply := strings.TrimSpace(res.Text)
	if utf8.RuneCountInString(reply) > aiTestReplyRunes {
		reply = string([]rune(reply)[:aiTestReplyRunes]) + "…"
	}
	h.log.Info("api_ai_settings_test_ok", "model", req.Model, "input_tokens", res.Usage.InputTokens, "output_tokens", res.Usage.OutputTokens)
	writeJSON(w, http.StatusOK, AISettingsTestResponse{Model: req.Model, Reply: reply, Usage: res.Usage})
}

func aiTestFailure(err error) string {
	var (
		apiErr *openai.APIError
		netErr net.Error
	)
	switch {
	case errors.As(err, &apiErr) && apiErr.Status != 0:
		return fmt.Sprintf("API відповіло %d: %s", apiErr.Status, strings.TrimSpace(apiErr.Code+" "+apiErr.Message))
	case errors.As(err, &apiErr):
		return "модель повернула помилку: " + strings.TrimSpace(apiErr.Code+" "+apiErr.Message)
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &netErr) && netErr.Timeout():
		return fmt.Sprintf("немає відповіді за %s", aiTestTimeout)
	}
	return "не вдалося зʼєднатися: " + err.Error()
}
