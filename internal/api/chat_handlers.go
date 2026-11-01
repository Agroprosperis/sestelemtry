package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nesh/sestelemetry/internal/aichat"
	"github.com/nesh/sestelemetry/internal/auth"
	"github.com/nesh/sestelemetry/internal/openai"
	"github.com/nesh/sestelemetry/internal/storage"
)

const (
	// DefaultChatDailyLimit is how many assistant turns one user gets
	// per Kyiv day until the settings page sets another limit.
	DefaultChatDailyLimit = 100

	// maxChatBody fits maxChatMessages messages at their rune caps in
	// two-byte Cyrillic.
	maxChatBody     = 256 << 10
	maxChatMessages = 12
	// maxChatQuestionRunes caps the question; longer earlier answers
	// are cut to maxChatHistoryRunes instead of failing the turn.
	maxChatQuestionRunes = 4000
	maxChatHistoryRunes  = 6000
	chatTurnTimeout      = 120 * time.Second
	// chatFollowUpTimeout bounds picking the next questions; the answer
	// is already on screen, so a slow pick just goes without them.
	chatFollowUpTimeout = 20 * time.Second
)

// ChatUsage is the assistant's usage log: the daily limit counts it,
// and every turn lands in it.
type ChatUsage interface {
	CountTurnsSince(ctx context.Context, userID int64, since time.Time) (int, error)
	RecordTurn(ctx context.Context, row storage.AIChatUsageRow) error
}

// ChatUsageStore keeps the usage log in Postgres (ai_chat_usage).
type ChatUsageStore struct {
	pool *pgxpool.Pool
}

func NewChatUsageStore(pool *pgxpool.Pool) *ChatUsageStore {
	return &ChatUsageStore{pool: pool}
}

func (s *ChatUsageStore) CountTurnsSince(ctx context.Context, userID int64, since time.Time) (int, error) {
	return storage.CountAIChatTurnsSince(ctx, s.pool, userID, since)
}

func (s *ChatUsageStore) RecordTurn(ctx context.Context, row storage.AIChatUsageRow) error {
	return storage.InsertAIChatUsage(ctx, s.pool, row)
}

// aiSettingsTTL bounds how long a read of the saved settings is reused;
// a save through this process drops it at once.
const aiSettingsTTL = 30 * time.Second

// AssistantOptions configures SetAssistant.
type AssistantOptions struct {
	Usage ChatUsage
	// NewModel builds the model client for the saved settings; nil uses
	// the OpenAI Responses API client.
	NewModel func(openai.Config) aichat.Model
}

type assistant struct {
	usage    ChatUsage
	newModel func(openai.Config) aichat.Model

	mu sync.Mutex
	// busy holds the users with a turn in flight: one at a time each.
	busy map[int64]bool
	// live is the assistant as the settings last read define it, reused
	// for aiSettingsTTL so /auth/me and every turn don't each query the
	// database. revision grows on every save, so a read that raced one
	// doesn't cache the settings it replaced.
	live     *liveAssistant
	liveAt   time.Time
	revision int
}

// liveAssistant is the assistant under the current settings.
type liveAssistant struct {
	settings AISettings
	// model is nil while the assistant is off or lacks a key or model.
	model aichat.Model
}

// SetAssistant enables POST /api/v1/chat, run on the settings saved
// through /api/v1/ai-settings. A nil Usage leaves it disabled.
func (h *Handlers) SetAssistant(opts AssistantOptions) {
	if opts.Usage == nil {
		h.assistant = nil
		return
	}
	newModel := opts.NewModel
	if newModel == nil {
		newModel = func(cfg openai.Config) aichat.Model { return openai.NewClient(cfg) }
	}
	h.assistant = &assistant{usage: opts.Usage, newModel: newModel, busy: map[int64]bool{}}
}

// newAIModel builds a model client the way the assistant does, also
// for the settings page's connection test.
func (h *Handlers) newAIModel(cfg openai.Config) aichat.Model {
	if h.assistant != nil {
		return h.assistant.newModel(cfg)
	}
	return openai.NewClient(cfg)
}

// liveAssistant returns the assistant as the saved settings define it.
func (h *Handlers) liveAssistant(ctx context.Context) (liveAssistant, error) {
	a := h.assistant
	a.mu.Lock()
	if a.live != nil && time.Since(a.liveAt) < aiSettingsTTL {
		live := *a.live
		a.mu.Unlock()
		return live, nil
	}
	rev := a.revision
	a.mu.Unlock()

	settings, keySet, saved, err := h.store.GetAISettings(ctx)
	if err != nil {
		return liveAssistant{}, err
	}
	settings.Normalize()
	live := liveAssistant{settings: settings}
	if saved && keySet && settings.Enabled && settings.Model != "" {
		key, err := h.store.AIAPIKey(ctx)
		if err != nil {
			return liveAssistant{}, err
		}
		if key != "" {
			live.model = a.newModel(settings.openaiConfig(key))
		}
	}
	a.mu.Lock()
	if a.revision == rev {
		a.live, a.liveAt = &live, time.Now()
	}
	a.mu.Unlock()
	return live, nil
}

// forget drops the cached settings after a save.
func (a *assistant) forget() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.revision++
	a.live = nil
}

// assistantOn reports whether a chat turn would run now; /auth/me
// shows or hides the assistant by it.
func (h *Handlers) assistantOn(ctx context.Context) bool {
	if h.assistant == nil {
		return false
	}
	live, err := h.liveAssistant(ctx)
	if err != nil {
		h.log.Warn("api_ai_settings_read", "err", err)
		return false
	}
	return live.model != nil
}

func (a *assistant) begin(userID int64) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.busy[userID] {
		return false
	}
	a.busy[userID] = true
	return true
}

func (a *assistant) end(userID int64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.busy, userID)
}

type chatRequest struct {
	Messages []aichat.Message `json:"messages"`
	Context  chatContext      `json:"context"`
}

// chatContext is where the user is in the dashboard, read from its
// query string. It only steers the prompt: the organization comes from
// the authorized query parameter, never from here.
type chatContext struct {
	View           string `json:"view"`
	Tab            string `json:"tab"`
	Preset         string `json:"preset"`
	Range          string `json:"range"`
	Anchor         string `json:"anchor"`
	From           string `json:"from"`
	To             string `json:"to"`
	OperationStart string `json:"operation_start"`
}

// readChatRequest decodes and normalizes the body: the last
// maxChatMessages messages, blanks dropped, ending with the question.
func readChatRequest(w http.ResponseWriter, r *http.Request) (chatRequest, error) {
	// Read to EOF before answering: from there net/http watches the
	// connection, so a client that hangs up («Зупинити») cancels
	// r.Context() and with it the model request.
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxChatBody))
	if err != nil {
		return chatRequest{}, errors.New("тіло запиту завелике")
	}
	var req chatRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return chatRequest{}, errors.New("invalid JSON body")
	}
	msgs := req.Messages
	if len(msgs) > maxChatMessages {
		msgs = msgs[len(msgs)-maxChatMessages:]
	}
	clean := make([]aichat.Message, 0, len(msgs))
	for _, m := range msgs {
		if m.Role != aichat.RoleUser && m.Role != aichat.RoleAssistant {
			return chatRequest{}, fmt.Errorf("невідома роль повідомлення %q", m.Role)
		}
		text := strings.TrimSpace(m.Text)
		if text == "" {
			continue
		}
		clean = append(clean, aichat.Message{Role: m.Role, Text: text})
	}
	if len(clean) == 0 || clean[len(clean)-1].Role != aichat.RoleUser {
		return chatRequest{}, errors.New("останнє повідомлення має бути питанням користувача")
	}
	for i := range clean {
		limit := maxChatHistoryRunes
		if i == len(clean)-1 {
			if utf8.RuneCountInString(clean[i].Text) > maxChatQuestionRunes {
				return chatRequest{}, fmt.Errorf("питання задовге: щонайбільше %d символів", maxChatQuestionRunes)
			}
			continue
		}
		if utf8.RuneCountInString(clean[i].Text) > limit {
			clean[i].Text = string([]rune(clean[i].Text)[:limit]) + "…"
		}
	}
	req.Messages = clean
	return req, nil
}

// aiChat answers one question to the assistant about an organization,
// streaming NDJSON events: "tool" when a read-only lookup starts,
// "delta" with answer text, "sources", then "done" or "error".
//
//	POST /api/v1/chat?organization_id=
func (h *Handlers) aiChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	a := h.assistant
	if a == nil {
		http.Error(w, "ШІ-асистент не налаштовано", http.StatusServiceUnavailable)
		return
	}
	orgID := strings.TrimSpace(r.URL.Query().Get("organization_id"))
	if orgID == "" {
		http.Error(w, "organization_id is required", http.StatusBadRequest)
		return
	}
	req, err := readChatRequest(w, r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var userID int64
	if p := principalFrom(r.Context()); p != nil {
		userID = p.UserID
	}
	loc, err := loadLocation(chatTZ)
	if err != nil {
		loc = time.UTC
	}

	live, err := h.liveAssistant(r.Context())
	if err != nil {
		h.log.Error("api_chat_settings", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if live.model == nil {
		http.Error(w, "ШІ-асистент вимкнено", http.StatusServiceUnavailable)
		return
	}

	if !a.begin(userID) {
		http.Error(w, "попередня відповідь ще формується", http.StatusConflict)
		return
	}
	defer a.end(userID)
	now := time.Now().In(loc)
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	used, err := a.usage.CountTurnsSince(r.Context(), userID, dayStart)
	if err != nil {
		h.log.Error("api_chat_usage_count", "user_id", userID, "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if limit := live.settings.DailyLimit; used >= limit {
		http.Error(w, fmt.Sprintf("Денний ліміт ШІ-асистента (%d запитів) вичерпано. Спробуйте завтра.", limit), http.StatusTooManyRequests)
		return
	}

	turn := aichat.Request{
		Instructions: aichat.Instructions(aichat.PromptContext{
			OrganizationID:   orgID,
			OrganizationName: h.organizationName(orgID),
			Now:              now,
			View:             req.Context.View,
			Tab:              req.Context.Tab,
			Preset:           req.Context.Preset,
			Range:            req.Context.Range,
			Anchor:           req.Context.Anchor,
			From:             req.Context.From,
			To:               req.Context.To,
			OperationStart:   req.Context.OperationStart,
			NoMoney:          !h.visibleTo(r, orgID, auth.PermEconomicsRead),
		}),
		Messages: req.Messages,
		Tools:    h.chatTools(r, orgID, loc),
	}

	// A turn with a few tool rounds outlives the server's 30 s
	// WriteTimeout.
	if rc := http.NewResponseController(w); rc != nil {
		_ = rc.SetWriteDeadline(time.Time{})
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	enc := json.NewEncoder(w)
	flusher, _ := w.(http.Flusher)
	emit := func(ev aichat.Event) {
		_ = enc.Encode(ev)
		if flusher != nil {
			flusher.Flush()
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), chatTurnTimeout)
	defer cancel()
	start := time.Now()
	out, runErr := aichat.Run(ctx, live.model, turn, emit)
	status := "ok"
	switch {
	case runErr == nil:
		if out.Incomplete != "" {
			emit(aichat.Event{Type: aichat.EventDelta, Text: "\n\n(Відповідь обірвано: модель досягла ліміту довжини.)"})
		}
		if len(out.Sources) > 0 {
			emit(aichat.Event{Type: aichat.EventSources, Sources: out.Sources})
		}
		if live.settings.FollowUps {
			emit(aichat.Event{Type: aichat.EventStatus, Label: "Добираю наступні питання…"})
			fctx, fcancel := context.WithTimeout(ctx, chatFollowUpTimeout)
			questions, usage, err := aichat.FollowUps(fctx, live.model, req.Messages, out.Text, turn.Tools)
			fcancel()
			out.Usage.InputTokens += usage.InputTokens
			out.Usage.OutputTokens += usage.OutputTokens
			switch {
			case err != nil && r.Context().Err() == nil:
				h.log.Warn("api_chat_follow_ups", "organization_id", orgID, "err", err)
			case len(questions) > 0:
				emit(aichat.Event{Type: aichat.EventSuggestions, Suggestions: questions})
			}
		}
		emit(aichat.Event{Type: aichat.EventDone, Usage: &out.Usage})
	case r.Context().Err() != nil:
		status = "cancelled"
	default:
		status = "error"
		emit(aichat.Event{Type: aichat.EventError, Error: chatErrorMessage(runErr)})
	}
	logArgs := []any{
		"organization_id", orgID,
		"user_id", userID,
		"status", status,
		"tools", strings.Join(out.Tools, ","),
		"input_tokens", out.Usage.InputTokens,
		"output_tokens", out.Usage.OutputTokens,
		"question_runes", utf8.RuneCountInString(req.Messages[len(req.Messages)-1].Text),
		"duration_ms", time.Since(start).Milliseconds(),
	}
	if status == "error" {
		h.log.Error("api_chat", append(logArgs, "err", runErr)...)
	} else {
		h.log.Info("api_chat", logArgs...)
	}
	if err := a.usage.RecordTurn(context.WithoutCancel(r.Context()), storage.AIChatUsageRow{
		UserID:         userID,
		OrganizationID: orgID,
		Model:          live.settings.Model,
		Tools:          out.Tools,
		InputTokens:    out.Usage.InputTokens,
		OutputTokens:   out.Usage.OutputTokens,
		Status:         status,
	}); err != nil {
		h.log.Error("api_chat_usage_record", "user_id", userID, "err", err)
	}
}

// organizationName is the configured display name of org, or "" for
// an organization missing from the config.
func (h *Handlers) organizationName(org string) string {
	for _, o := range h.organizations {
		if o.ID == org {
			return o.Name
		}
	}
	return ""
}

// chatErrorMessage is what the user reads when a turn fails; the cause
// itself goes to the log only.
func chatErrorMessage(err error) string {
	var (
		apiErr *openai.APIError
		netErr net.Error
	)
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &netErr) && netErr.Timeout():
		return "ШІ-асистент не встиг відповісти. Спробуйте поставити коротше питання."
	case errors.Is(err, aichat.ErrNoAnswer):
		return "ШІ-асистент не сформував відповіді. Спробуйте переформулювати питання."
	case errors.As(err, &apiErr) && (apiErr.Status == http.StatusUnauthorized || apiErr.Status == http.StatusForbidden):
		return "Сервіс OpenAI відхилив ключ доступу. Зверніться до адміністратора."
	case errors.As(err, &apiErr) && apiErr.Status == http.StatusTooManyRequests:
		return "Сервіс OpenAI перевантажений або вичерпано квоту. Спробуйте за хвилину."
	}
	return "Сервіс OpenAI тимчасово недоступний. Спробуйте пізніше."
}
