package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/nesh/sestelemetry/internal/aichat"
	"github.com/nesh/sestelemetry/internal/auth"
	"github.com/nesh/sestelemetry/internal/openai"
)

func aiSettingsRequest(s AISettings, key *string) map[string]any {
	body := map[string]any{
		"enabled":          s.Enabled,
		"model":            s.Model,
		"base_url":         s.BaseURL,
		"reasoning_effort": s.ReasoningEffort,
		"daily_limit":      s.DailyLimit,
	}
	if key != nil {
		body["api_key"] = *key
	}
	return body
}

func getAISettingsResponse(t *testing.T, h http.Handler, cookie *http.Cookie) (AISettingsResponse, string) {
	t.Helper()
	rec := serve(h, http.MethodGet, "/api/v1/ai-settings", cookie, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET: status = %d (%s)", rec.Code, rec.Body.String())
	}
	var out AISettingsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out, rec.Body.String()
}

func TestAISettingsKeepTheKeyOnTheServer(t *testing.T) {
	store := &mockStore{}
	h, accounts := authTestHandlers(t, store)
	router := h.Router()
	cookie := sessionCookie(t, accounts, "root@example.com", auth.Grant{Role: auth.RoleAdmin})
	put := func(s AISettings, key *string) (int, string) {
		t.Helper()
		rec := serve(router, http.MethodPut, "/api/v1/ai-settings", cookie, aiSettingsRequest(s, key))
		return rec.Code, rec.Body.String()
	}

	got, _ := getAISettingsResponse(t, router, cookie)
	if got.Saved || got.Enabled || got.APIKeyConfigured || got.DailyLimit != DefaultChatDailyLimit {
		t.Fatalf("before the first save = %+v", got)
	}

	on := AISettings{Enabled: true, Model: "m1"}
	if code, body := put(on, nil); code != http.StatusBadRequest || !strings.Contains(body, "ключ API") {
		t.Errorf("enable without a key: %d %s", code, body)
	}
	if code, body := put(on, strPtr(" sk-secret-1\n")); code != http.StatusNoContent {
		t.Fatalf("save: %d %s", code, body)
	}
	if store.aiKey != "sk-secret-1" {
		t.Errorf("stored key = %q, want it trimmed", store.aiKey)
	}
	got, raw := getAISettingsResponse(t, router, cookie)
	if !got.Saved || !got.Enabled || !got.APIKeyConfigured || got.Model != "m1" || got.DailyLimit != DefaultChatDailyLimit {
		t.Errorf("after save = %+v", got)
	}
	if strings.Contains(raw, "sk-secret") {
		t.Fatalf("GET leaked the key: %s", raw)
	}

	// Editing the rest keeps the key.
	if code, body := put(AISettings{Enabled: true, Model: "m2", DailyLimit: 20}, nil); code != http.StatusNoContent || store.aiKey != "sk-secret-1" {
		t.Errorf("edit without the key: %d %s, key %q", code, body, store.aiKey)
	}
	// The stored key never follows the assistant to another address.
	moved := AISettings{Enabled: true, Model: "m2", BaseURL: "https://gateway.example.com/"}
	if code, body := put(moved, nil); code != http.StatusBadRequest || !strings.Contains(body, "адресу API змінено") {
		t.Errorf("new address with the stored key: %d %s", code, body)
	}
	if code, body := put(moved, strPtr("sk-gateway")); code != http.StatusNoContent || store.aiSettings.BaseURL != "https://gateway.example.com" {
		t.Errorf("new address with a new key: %d %s, stored %+v", code, body, store.aiSettings)
	}

	if code, _ := put(on, strPtr("")); code != http.StatusBadRequest {
		t.Errorf("clearing the key of a running assistant: status = %d", code)
	}
	if code, _ := put(AISettings{Model: "m2", BaseURL: "https://gateway.example.com"}, strPtr("")); code != http.StatusNoContent || store.aiKey != "" {
		t.Errorf("switch off and clear the key: status = %d, key %q", code, store.aiKey)
	}

	for name, body := range map[string]map[string]any{
		"effort":  aiSettingsRequest(AISettings{Model: "m", ReasoningEffort: "extreme"}, nil),
		"url":     aiSettingsRequest(AISettings{Model: "m", BaseURL: "ftp://example.com"}, nil),
		"limit":   aiSettingsRequest(AISettings{Model: "m", DailyLimit: maxChatDailyLimit + 1}, nil),
		"unknown": {"model": "m", "temperature": 2},
	} {
		if rec := serve(router, http.MethodPut, "/api/v1/ai-settings", cookie, body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d", name, rec.Code)
		}
	}
}

// A save takes effect at once: the assistant is rebuilt from the new
// settings without a restart.
func TestAssistantFollowsTheSavedSettings(t *testing.T) {
	store := &mockStore{}
	h, accounts := authTestHandlers(t, store)
	router := h.Router()
	var built []openai.Config
	h.SetAssistant(AssistantOptions{Usage: &memChatUsage{}, NewModel: func(cfg openai.Config) aichat.Model {
		built = append(built, cfg)
		return &scriptedModel{turns: []openai.Result{{Text: "Працюю."}}}
	}})
	admin := sessionCookie(t, accounts, "root@example.com", auth.Grant{Role: auth.RoleAdmin})
	user := sessionCookie(t, accounts, "eng@example.com", auth.Grant{Role: auth.RoleEngineer, OrganizationID: "ze"})
	aiChatOn := func() bool {
		t.Helper()
		var me AuthMeResponse
		_ = json.Unmarshal(serve(router, http.MethodGet, "/api/v1/auth/me", user, nil).Body.Bytes(), &me)
		return me.AIChat
	}
	chat := func() int {
		t.Helper()
		return serve(router, http.MethodPost, "/api/v1/chat?organization_id=ze", user, chatBody("?")).Code
	}

	if aiChatOn() || chat() != http.StatusServiceUnavailable {
		t.Fatal("assistant on before anything was saved")
	}
	settings := AISettings{Enabled: true, Model: "m1", ReasoningEffort: "low", DailyLimit: 5}
	if rec := serve(router, http.MethodPut, "/api/v1/ai-settings", admin, aiSettingsRequest(settings, strPtr("sk-1"))); rec.Code != http.StatusNoContent {
		t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
	}
	if !aiChatOn() || chat() != http.StatusOK {
		t.Fatal("assistant still off right after it was switched on")
	}
	if len(built) == 0 || built[0] != (openai.Config{APIKey: "sk-1", Model: "m1", ReasoningEffort: "low"}) {
		t.Errorf("client built from %+v", built)
	}

	settings.Enabled = false
	if rec := serve(router, http.MethodPut, "/api/v1/ai-settings", admin, aiSettingsRequest(settings, nil)); rec.Code != http.StatusNoContent {
		t.Fatalf("switch off: %d", rec.Code)
	}
	if aiChatOn() || chat() != http.StatusServiceUnavailable {
		t.Error("assistant still on right after it was switched off")
	}
}

func TestAISettingsConnectionTest(t *testing.T) {
	store := &mockStore{aiSettings: &AISettings{Model: "m1", DailyLimit: 10}, aiKey: "sk-stored"}
	h, accounts := authTestHandlers(t, store)
	router := h.Router()
	var used []openai.Config
	model := &scriptedModel{turns: []openai.Result{{Text: "  Так, на зв'язку.  ", Usage: openai.Usage{InputTokens: 30, OutputTokens: 6}}}}
	h.SetAssistant(AssistantOptions{Usage: &memChatUsage{}, NewModel: func(cfg openai.Config) aichat.Model {
		used = append(used, cfg)
		return model
	}})
	cookie := sessionCookie(t, accounts, "root@example.com", auth.Grant{Role: auth.RoleAdmin})
	test := func(s AISettings, key *string) (int, string) {
		t.Helper()
		rec := serve(router, http.MethodPost, "/api/v1/ai-settings/test", cookie, aiSettingsRequest(s, key))
		return rec.Code, rec.Body.String()
	}

	// Typed, not saved: the model on the form with the stored key.
	code, body := test(AISettings{Model: "m-new"}, nil)
	if code != http.StatusOK || !strings.Contains(body, `"reply":"Так, на зв'язку."`) || !strings.Contains(body, `"input_tokens":30`) {
		t.Fatalf("test with the stored key: %d %s", code, body)
	}
	if used[0].APIKey != "sk-stored" || used[0].Model != "m-new" {
		t.Errorf("test ran on %+v", used[0])
	}
	if store.aiWrites != 0 {
		t.Error("the connection test saved the settings")
	}
	if code, body := test(AISettings{Model: "m1", BaseURL: "https://elsewhere.example.com"}, nil); code != http.StatusBadRequest || !strings.Contains(body, "адресу API змінено") {
		t.Errorf("stored key to another address: %d %s", code, body)
	}
	if code, _ := test(AISettings{Model: "m1", BaseURL: "https://elsewhere.example.com"}, strPtr("sk-typed")); code != http.StatusOK || used[len(used)-1].APIKey != "sk-typed" {
		t.Errorf("typed key: status %d, ran on %+v", code, used[len(used)-1])
	}
	if code, _ := test(AISettings{}, nil); code != http.StatusBadRequest {
		t.Errorf("no model: status = %d", code)
	}

	model.err = &openai.APIError{Status: http.StatusUnauthorized, Code: "invalid_api_key", Message: "Incorrect API key provided"}
	if code, body := test(AISettings{Model: "m1"}, nil); code != http.StatusBadGateway || !strings.Contains(body, "API відповіло 401: invalid_api_key Incorrect API key provided") {
		t.Errorf("rejected key: %d %s", code, body)
	}
}
