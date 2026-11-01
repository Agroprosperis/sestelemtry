// Package openai is a small client for the OpenAI Responses API: one
// streamed model turn with function tools, which is all the dashboard's
// assistant needs. It speaks plain net/http and Server-Sent Events
// instead of pulling an SDK into the module.
//
// Every request is sent with store:false, so the API keeps nothing
// between turns: the caller resends the conversation each time and
// hands a turn's output items back (see Replay) when the model asked
// for function calls.
package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DefaultBaseURL is the public API host; OPENAI_BASE_URL points the
// client at a compatible gateway instead.
const DefaultBaseURL = "https://api.openai.com"

const (
	defaultTimeout         = 90 * time.Second
	defaultMaxOutputTokens = 4096
	// maxEventBytes bounds one SSE line. Encrypted reasoning items are
	// the largest thing the stream carries.
	maxEventBytes = 8 << 20
	// maxErrorBody bounds how much of a failed response is read for its
	// error message.
	maxErrorBody = 64 << 10
)

// Config configures a Client. APIKey and Model are required.
type Config struct {
	APIKey  string
	Model   string
	BaseURL string
	// ReasoningEffort ("low", "medium", "high") turns reasoning on and
	// asks for the encrypted reasoning items a store:false conversation
	// has to hand back. Leave it empty for models without reasoning:
	// they reject that include.
	ReasoningEffort string
	MaxOutputTokens int
	// HTTPClient's Timeout bounds one whole streamed turn. nil gets a
	// client with a 90 s timeout.
	HTTPClient *http.Client
}

// Client runs model turns against the Responses API.
type Client struct {
	cfg  Config
	http *http.Client
}

// NewClient returns a client for cfg, filling in the defaults.
func NewClient(cfg Config) *Client {
	cfg.BaseURL = strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultBaseURL
	}
	if cfg.MaxOutputTokens <= 0 {
		cfg.MaxOutputTokens = defaultMaxOutputTokens
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultTimeout}
	}
	return &Client{cfg: cfg, http: httpClient}
}

// Model names the model every turn runs on.
func (c *Client) Model() string { return c.cfg.Model }

// Tool is a function the model may call. Parameters is a JSON Schema
// that satisfies strict mode: every property listed in required, no
// additional properties, optional values typed as a union with null.
type Tool struct {
	Name        string
	Description string
	Parameters  json.RawMessage
}

// Request is one model turn. Input holds Responses API items: messages
// built with UserMessage / AssistantMessage, a previous turn's output
// passed through Replay, and FunctionOutput results.
type Request struct {
	Instructions string
	Input        []json.RawMessage
	Tools        []Tool
	// ToolChoice "none" forbids function calls for this turn; empty
	// leaves the choice to the model.
	ToolChoice string
}

// FunctionCall is a tool call the model asked for.
type FunctionCall struct {
	CallID    string
	Name      string
	Arguments string
}

// Usage is the token count of one or more turns.
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// Result is what one turn produced. Output carries every output item
// in order, so a turn that ended in function calls can be continued.
type Result struct {
	Text   string
	Calls  []FunctionCall
	Output []json.RawMessage
	Usage  Usage
	// Incomplete names why the model stopped early
	// ("max_output_tokens", "content_filter"); empty for a finished
	// turn.
	Incomplete string
}

// APIError is a failure the API itself reported: an HTTP status, or an
// error event inside the stream (Status 0).
type APIError struct {
	Status  int
	Code    string
	Message string
}

func (e *APIError) Error() string {
	var b strings.Builder
	b.WriteString("openai")
	if e.Status != 0 {
		fmt.Fprintf(&b, ": status %d", e.Status)
	}
	if e.Code != "" {
		fmt.Fprintf(&b, ": %s", e.Code)
	}
	if e.Message != "" {
		fmt.Fprintf(&b, ": %s", e.Message)
	}
	return b.String()
}

// ErrStreamEnded reports a stream that closed before the response
// completed (a dropped connection or a gateway timeout).
var ErrStreamEnded = errors.New("openai: stream ended before the response completed")

// UserMessage is a user turn of the conversation.
func UserMessage(text string) json.RawMessage {
	return messageItem("user", text)
}

// AssistantMessage is an earlier answer of the assistant.
func AssistantMessage(text string) json.RawMessage {
	return messageItem("assistant", text)
}

func messageItem(role, text string) json.RawMessage {
	b, _ := json.Marshal(struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}{role, text})
	return b
}

// FunctionOutput answers the function call callID.
func FunctionOutput(callID, output string) json.RawMessage {
	b, _ := json.Marshal(struct {
		Type   string `json:"type"`
		CallID string `json:"call_id"`
		Output string `json:"output"`
	}{"function_call_output", callID, output})
	return b
}

// Replay prepares a turn's output items for the next turn's input.
// Nothing is stored server-side, so a reasoning item can only come back
// with its encrypted content. When one lacks it (reasoning was not
// requested), reasoning items are dropped and the remaining items lose
// their ids, which would otherwise refer to the dropped reasoning.
func Replay(output []json.RawMessage) []json.RawMessage {
	complete := true
	for _, raw := range output {
		var it struct {
			Type             string `json:"type"`
			EncryptedContent string `json:"encrypted_content"`
		}
		if json.Unmarshal(raw, &it) == nil && it.Type == "reasoning" && it.EncryptedContent == "" {
			complete = false
			break
		}
	}
	if complete {
		return append([]json.RawMessage(nil), output...)
	}
	out := make([]json.RawMessage, 0, len(output))
	for _, raw := range output {
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) != nil {
			continue
		}
		var typ string
		_ = json.Unmarshal(fields["type"], &typ)
		if typ == "reasoning" {
			continue
		}
		delete(fields, "id")
		b, err := json.Marshal(fields)
		if err != nil {
			continue
		}
		out = append(out, b)
	}
	return out
}

type toolJSON struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
	Strict      bool            `json:"strict"`
}

type reasoningJSON struct {
	Effort string `json:"effort"`
}

type requestJSON struct {
	Model           string            `json:"model"`
	Instructions    string            `json:"instructions,omitempty"`
	Input           []json.RawMessage `json:"input"`
	Tools           []toolJSON        `json:"tools,omitempty"`
	ToolChoice      string            `json:"tool_choice,omitempty"`
	Stream          bool              `json:"stream"`
	Store           bool              `json:"store"`
	MaxOutputTokens int               `json:"max_output_tokens,omitempty"`
	Reasoning       *reasoningJSON    `json:"reasoning,omitempty"`
	Include         []string          `json:"include,omitempty"`
}

func (c *Client) body(req Request) ([]byte, error) {
	body := requestJSON{
		Model:           c.cfg.Model,
		Instructions:    req.Instructions,
		Input:           req.Input,
		Stream:          true,
		Store:           false,
		MaxOutputTokens: c.cfg.MaxOutputTokens,
	}
	if body.Input == nil {
		body.Input = []json.RawMessage{}
	}
	for _, t := range req.Tools {
		body.Tools = append(body.Tools, toolJSON{
			Type:        "function",
			Name:        t.Name,
			Description: t.Description,
			Parameters:  t.Parameters,
			Strict:      true,
		})
	}
	if len(body.Tools) > 0 {
		body.ToolChoice = req.ToolChoice
	}
	if effort := strings.TrimSpace(c.cfg.ReasoningEffort); effort != "" {
		body.Reasoning = &reasoningJSON{Effort: effort}
		body.Include = []string{"reasoning.encrypted_content"}
	}
	return json.Marshal(body)
}

// Respond runs one streamed turn. onDelta, when not nil, receives the
// answer text as it arrives.
func (c *Client) Respond(ctx context.Context, req Request, onDelta func(string)) (Result, error) {
	payload, err := c.body(req)
	if err != nil {
		return Result{}, fmt.Errorf("openai: encode request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.BaseURL+"/v1/responses", bytes.NewReader(payload))
	if err != nil {
		return Result{}, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	res, err := c.http.Do(httpReq)
	if err != nil {
		return Result{}, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return Result{}, statusError(res)
	}
	return readStream(res.Body, onDelta)
}

func statusError(res *http.Response) error {
	raw, _ := io.ReadAll(io.LimitReader(res.Body, maxErrorBody))
	apiErr := &APIError{Status: res.StatusCode}
	var body struct {
		Error *struct {
			Code    any    `json:"code"`
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &body) == nil && body.Error != nil {
		apiErr.Message = body.Error.Message
		apiErr.Code = codeString(body.Error.Code)
		if apiErr.Code == "" {
			apiErr.Code = body.Error.Type
		}
	} else {
		apiErr.Message = strings.TrimSpace(string(raw))
	}
	return apiErr
}

// codeString renders an error code the API sends as a string or, on
// some gateways, as a number.
func codeString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return fmt.Sprintf("%.0f", t)
	}
	return ""
}

type errorJSON struct {
	Code    any    `json:"code"`
	Message string `json:"message"`
}

type responseJSON struct {
	Output            []json.RawMessage `json:"output"`
	Usage             *Usage            `json:"usage"`
	Error             *errorJSON        `json:"error"`
	IncompleteDetails *struct {
		Reason string `json:"reason"`
	} `json:"incomplete_details"`
}

type eventJSON struct {
	Type        string          `json:"type"`
	Delta       string          `json:"delta"`
	OutputIndex int             `json:"output_index"`
	Item        json.RawMessage `json:"item"`
	Response    *responseJSON   `json:"response"`
	Code        any             `json:"code"`
	Message     string          `json:"message"`
	Error       *errorJSON      `json:"error"`
}

type itemJSON struct {
	Type      string `json:"type"`
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
	Content   []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

// readStream folds the event stream of one turn into a Result.
func readStream(r io.Reader, onDelta func(string)) (Result, error) {
	var (
		res      Result
		streamed strings.Builder
		items    = map[int]json.RawMessage{}
		maxIndex = -1
		finished bool
	)
	err := readEvents(r, func(data []byte) error {
		if bytes.Equal(data, []byte("[DONE]")) {
			return nil
		}
		var ev eventJSON
		if err := json.Unmarshal(data, &ev); err != nil {
			return fmt.Errorf("openai: decode event: %w", err)
		}
		switch ev.Type {
		case "response.output_text.delta":
			streamed.WriteString(ev.Delta)
			if onDelta != nil && ev.Delta != "" {
				onDelta(ev.Delta)
			}
		case "response.output_item.done":
			if len(ev.Item) > 0 {
				items[ev.OutputIndex] = append(json.RawMessage(nil), ev.Item...)
				maxIndex = max(maxIndex, ev.OutputIndex)
			}
		case "response.completed", "response.incomplete":
			finished = true
			if ev.Response == nil {
				return nil
			}
			if ev.Response.Usage != nil {
				res.Usage = *ev.Response.Usage
			}
			if ev.Response.IncompleteDetails != nil {
				res.Incomplete = ev.Response.IncompleteDetails.Reason
			} else if ev.Type == "response.incomplete" {
				res.Incomplete = "incomplete"
			}
			if len(items) == 0 {
				for i, item := range ev.Response.Output {
					items[i] = item
					maxIndex = max(maxIndex, i)
				}
			}
		case "response.failed":
			apiErr := &APIError{Message: "response failed"}
			if ev.Response != nil && ev.Response.Error != nil {
				apiErr.Code = codeString(ev.Response.Error.Code)
				apiErr.Message = ev.Response.Error.Message
			}
			return apiErr
		case "error":
			apiErr := &APIError{Code: codeString(ev.Code), Message: ev.Message}
			if ev.Error != nil {
				apiErr.Code = codeString(ev.Error.Code)
				apiErr.Message = ev.Error.Message
			}
			return apiErr
		}
		return nil
	})
	if err != nil {
		return res, err
	}
	if !finished {
		return res, ErrStreamEnded
	}

	var text strings.Builder
	for i := 0; i <= maxIndex; i++ {
		raw, ok := items[i]
		if !ok {
			continue
		}
		res.Output = append(res.Output, raw)
		var it itemJSON
		if json.Unmarshal(raw, &it) != nil {
			continue
		}
		switch it.Type {
		case "function_call":
			res.Calls = append(res.Calls, FunctionCall{CallID: it.CallID, Name: it.Name, Arguments: it.Arguments})
		case "message":
			for _, part := range it.Content {
				if part.Type == "output_text" {
					text.WriteString(part.Text)
				}
			}
		}
	}
	res.Text = text.String()
	if res.Text == "" {
		res.Text = streamed.String()
	}
	return res, nil
}

// readEvents splits a Server-Sent Events body into the data payload of
// each event. Event names are not needed: every payload carries its
// own "type".
func readEvents(r io.Reader, handle func(data []byte) error) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), maxEventBytes)
	var data []byte
	dispatch := func() error {
		if len(data) == 0 {
			return nil
		}
		d := data
		data = nil
		return handle(d)
	}
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			if err := dispatch(); err != nil {
				return err
			}
			continue
		}
		if line[0] == ':' {
			continue
		}
		field, value, _ := bytes.Cut(line, []byte(":"))
		if string(field) != "data" {
			continue
		}
		value = bytes.TrimPrefix(value, []byte(" "))
		if len(data) > 0 {
			data = append(data, '\n')
		}
		data = append(data, value...)
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("openai: read stream: %w", err)
	}
	return dispatch()
}
