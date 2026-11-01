package aichat

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/nesh/sestelemetry/internal/openai"
)

const (
	// FollowUpCount is how many next questions a turn suggests.
	FollowUpCount = 3
	// maxFollowUpRunes caps one suggestion; it is a button label.
	maxFollowUpRunes = 100
	// The digest the suggestions are picked from: the last few messages,
	// each cut short, and the answer just given.
	followUpMessages      = 4
	followUpMessageRunes  = 1500
	followUpAnswerRunes   = 3000
	followUpToolLineRunes = 140
)

var followUpFormat = &openai.TextFormat{
	Name:   "follow_ups",
	Schema: json.RawMessage(`{"type":"object","properties":{"questions":{"type":"array","items":{"type":"string"}}},"required":["questions"],"additionalProperties":false}`),
}

// FollowUps asks model for the questions the user may want to ask next,
// once answer has been given to the last message of history. The model
// gets no tools, only a list of what the assistant can read, so a
// suggestion is one the next turn can answer.
func FollowUps(ctx context.Context, model Model, history []Message, answer string, tools []Tool) ([]string, openai.Usage, error) {
	res, err := model.Respond(ctx, openai.Request{
		Instructions: followUpInstructions(tools),
		Input:        []json.RawMessage{openai.UserMessage(followUpDigest(history, answer))},
		Format:       followUpFormat,
	}, nil)
	if err != nil {
		return nil, res.Usage, err
	}
	var out struct {
		Questions []string `json:"questions"`
	}
	if err := json.Unmarshal([]byte(res.Text), &out); err != nil {
		return nil, res.Usage, fmt.Errorf("aichat: follow-ups: %w", err)
	}
	return cleanFollowUps(out.Questions, history), res.Usage, nil
}

func followUpInstructions(tools []Tool) string {
	var b strings.Builder
	fmt.Fprintf(&b, `Ти добираєш наступні питання, які користувач диспетчерської панелі SESTelemetry може поставити ШІ-асистенту про той самий об'єкт.
Дай %d короткі питання українською, від імені користувача, до 80 символів кожне.
- Вони продовжують розмову: деталь щойно сказаного, причина, порівняння з іншим періодом, що робити далі. Кожне — про інше.
- Лише про те, що асистент уміє прочитати:
`, FollowUpCount)
	for _, t := range tools {
		fmt.Fprintf(&b, "  - %s\n", toolSummary(t.Description))
	}
	b.WriteString(`- Не повторюй питань, які вже ставили, і не питай про інші об'єкти.
- Без нумерації і лапок; кожне питання закінчується знаком питання.
`)
	return b.String()
}

// toolSummary is the opening clause of a tool description.
func toolSummary(desc string) string {
	if i := strings.IndexAny(desc, ":."); i > 0 {
		desc = desc[:i]
	}
	return cutRunes(strings.TrimSpace(desc), followUpToolLineRunes)
}

func followUpDigest(history []Message, answer string) string {
	var b strings.Builder
	b.WriteString("Розмова:\n")
	start := max(0, len(history)-followUpMessages)
	for _, m := range history[start:] {
		who := "Користувач"
		if m.Role == RoleAssistant {
			who = "Асистент"
		}
		fmt.Fprintf(&b, "%s: %s\n", who, cutRunes(m.Text, followUpMessageRunes))
	}
	fmt.Fprintf(&b, "Асистент щойно відповів: %s\n", cutRunes(answer, followUpAnswerRunes))
	return b.String()
}

var followUpMarks = regexp.MustCompile(`^(?:\d+[.)]\s*|[-•*]\s*)+`)

// cleanFollowUps keeps up to FollowUpCount distinct questions that were
// not asked already, with list marks and quotes stripped.
func cleanFollowUps(questions []string, history []Message) []string {
	seen := map[string]bool{}
	for _, m := range history {
		if m.Role == RoleUser {
			seen[strings.ToLower(strings.TrimSpace(m.Text))] = true
		}
	}
	out := make([]string, 0, FollowUpCount)
	for _, q := range questions {
		q = strings.TrimSpace(followUpMarks.ReplaceAllString(strings.TrimSpace(q), ""))
		q = strings.TrimSpace(strings.Trim(q, `"'«»“”`))
		key := strings.ToLower(q)
		if q == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, cutRunes(q, maxFollowUpRunes))
		if len(out) == FollowUpCount {
			break
		}
	}
	return out
}

func cutRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "…"
}
