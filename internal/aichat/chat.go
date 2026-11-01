// Package aichat runs one turn of the dashboard's assistant: it hands
// the conversation and the caller's tools to the model, runs the
// function calls the model asks for, and feeds the results back until
// the model answers in text.
//
// It knows nothing about HTTP routes, permissions or the database:
// internal/api decides which tools a user gets and what they read.
package aichat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/nesh/sestelemetry/internal/openai"
)

// Model is one streamed model turn; *openai.Client implements it.
type Model interface {
	Respond(ctx context.Context, req openai.Request, onDelta func(string)) (openai.Result, error)
}

// Source names the data a tool answered from. The chat lists the
// sources of a turn under its answer.
type Source struct {
	Label string `json:"label"`
}

// Tool is a read-only function the model may call.
type Tool struct {
	Name        string
	Description string
	// Parameters is the strict-mode JSON Schema of the arguments.
	Parameters json.RawMessage
	// Status renders the progress line shown while the tool runs;
	// nil shows the tool name.
	Status func(args json.RawMessage) string
	// Call runs the tool. Its result reaches the model as compact JSON;
	// an error goes back as {"error": ...}, so the model can say what is
	// missing instead of the turn failing.
	Call func(ctx context.Context, args json.RawMessage) (any, Source, error)
}

// Message is one turn of the conversation as the browser keeps it.
type Message struct {
	Role string `json:"role"`
	Text string `json:"text"`
}

const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
)

// Request is one user turn: the conversation so far, ending with the
// question, and the tools the caller may use.
type Request struct {
	Instructions string
	Messages     []Message
	Tools        []Tool
}

// Event is one line of a turn's progress stream.
type Event struct {
	Type        string        `json:"type"`
	Name        string        `json:"name,omitempty"`
	Label       string        `json:"label,omitempty"`
	Text        string        `json:"text,omitempty"`
	Sources     []Source      `json:"sources,omitempty"`
	Suggestions []string      `json:"suggestions,omitempty"`
	Usage       *openai.Usage `json:"usage,omitempty"`
	Error       string        `json:"error,omitempty"`
}

const (
	EventTool    = "tool"
	EventDelta   = "delta"
	EventSources = "sources"
	// EventStatus is a progress line that is not a tool (e.g. picking
	// the next questions).
	EventStatus = "status"
	// EventSuggestions carries the questions the user may ask next.
	EventSuggestions = "suggestions"
	EventDone        = "done"
	EventError       = "error"
)

// Outcome is what a turn did, for the audit record.
type Outcome struct {
	Text    string
	Tools   []string
	Sources []Source
	Usage   openai.Usage
	// Incomplete is why the model cut its answer short, if it did.
	Incomplete string
}

const (
	// MaxToolRounds bounds how many times one turn goes back to the
	// model with tool results. The round after the last one forbids
	// tools, so the model has to answer with what it has.
	MaxToolRounds = 6
	// MaxToolOutputBytes caps one tool result sent to the model.
	MaxToolOutputBytes = 16 << 10
)

// ErrNoAnswer reports a turn that ended without any text.
var ErrNoAnswer = errors.New("aichat: the model returned no answer")

// Run answers the last message of req. emit receives the turn's tool
// and delta events as they happen; the caller adds sources, done and
// error itself.
func Run(ctx context.Context, model Model, req Request, emit func(Event)) (Outcome, error) {
	if emit == nil {
		emit = func(Event) {}
	}
	tools := make(map[string]Tool, len(req.Tools))
	specs := make([]openai.Tool, 0, len(req.Tools))
	for _, t := range req.Tools {
		tools[t.Name] = t
		specs = append(specs, openai.Tool{Name: t.Name, Description: t.Description, Parameters: t.Parameters})
	}
	input := make([]json.RawMessage, 0, len(req.Messages)+8)
	for _, m := range req.Messages {
		if m.Role == RoleAssistant {
			input = append(input, openai.AssistantMessage(m.Text))
		} else {
			input = append(input, openai.UserMessage(m.Text))
		}
	}

	var (
		out    Outcome
		answer strings.Builder
	)
	// say streams answer text. Text from an earlier round (a model that
	// talks before calling tools) is kept apart from the next round's.
	say := func(text string, first bool) {
		if first && answer.Len() > 0 {
			text = "\n\n" + text
		}
		answer.WriteString(text)
		emit(Event{Type: EventDelta, Text: text})
	}
	for round := 0; ; round++ {
		turn := openai.Request{Instructions: req.Instructions, Input: input, Tools: specs}
		if round == MaxToolRounds {
			turn.ToolChoice = "none"
		}
		streamed := false
		res, err := model.Respond(ctx, turn, func(delta string) {
			say(delta, !streamed)
			streamed = true
		})
		out.Usage.InputTokens += res.Usage.InputTokens
		out.Usage.OutputTokens += res.Usage.OutputTokens
		if err != nil {
			out.Text = answer.String()
			return out, err
		}
		if !streamed && res.Text != "" {
			say(res.Text, true)
		}
		if len(res.Calls) == 0 || round == MaxToolRounds {
			out.Text = answer.String()
			out.Incomplete = res.Incomplete
			if strings.TrimSpace(out.Text) == "" {
				return out, ErrNoAnswer
			}
			return out, nil
		}
		input = append(input, openai.Replay(res.Output)...)
		for _, call := range res.Calls {
			input = append(input, openai.FunctionOutput(call.CallID, runTool(ctx, tools, call, emit, &out)))
		}
	}
}

func runTool(ctx context.Context, tools map[string]Tool, call openai.FunctionCall, emit func(Event), out *Outcome) string {
	tool, ok := tools[call.Name]
	if !ok {
		return toolError(fmt.Sprintf("невідомий інструмент %q", call.Name))
	}
	args := json.RawMessage(call.Arguments)
	if len(bytes.TrimSpace(args)) == 0 {
		args = json.RawMessage("{}")
	}
	label := tool.Name
	if tool.Status != nil {
		if s := tool.Status(args); s != "" {
			label = s
		}
	}
	emit(Event{Type: EventTool, Name: tool.Name, Label: label})
	out.Tools = append(out.Tools, tool.Name)
	result, src, err := tool.Call(ctx, args)
	if err != nil {
		return toolError(err.Error())
	}
	if src.Label != "" && !slices.Contains(out.Sources, src) {
		out.Sources = append(out.Sources, src)
	}
	return Compact(result)
}

func toolError(msg string) string {
	b, _ := json.Marshal(map[string]string{"error": msg})
	return string(b)
}

// Compact renders a tool result for the model: JSON with numbers
// rounded (one decimal from 100 up, three below) and cut at
// MaxToolOutputBytes.
func Compact(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return toolError("не вдалося підготувати дані: " + err.Error())
	}
	var tree any
	if json.Unmarshal(raw, &tree) == nil {
		if b, err := json.Marshal(roundTree(tree)); err == nil {
			raw = b
		}
	}
	if len(raw) <= MaxToolOutputBytes {
		return string(raw)
	}
	cut := raw[:MaxToolOutputBytes]
	for len(cut) > 0 && !utf8.Valid(cut) {
		cut = cut[:len(cut)-1]
	}
	return string(cut) + " …[обрізано]"
}

func roundTree(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, x := range t {
			t[k] = roundTree(x)
		}
	case []any:
		for i, x := range t {
			t[i] = roundTree(x)
		}
	case float64:
		scale := 1000.0
		if math.Abs(t) >= 100 {
			scale = 10
		}
		return math.Round(t*scale) / scale
	}
	return v
}
