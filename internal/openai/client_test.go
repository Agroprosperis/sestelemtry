package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// sse renders events the way the API streams them: an event line, a
// data line with the JSON payload, a blank line.
func sse(events ...string) string {
	var b strings.Builder
	for _, ev := range events {
		var probe struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal([]byte(ev), &probe)
		fmt.Fprintf(&b, "event: %s\ndata: %s\n\n", probe.Type, ev)
	}
	return b.String()
}

func streamServer(t *testing.T, body string, capture func(r *http.Request, payload map[string]any)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var payload map[string]any
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Errorf("request body is not JSON: %v", err)
		}
		if capture != nil {
			capture(r, payload)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

const completedEvent = `{"type":"response.completed","response":{"usage":{"input_tokens":120,"output_tokens":30}}}`

func TestRespondRequestShape(t *testing.T) {
	var (
		path, auth string
		payload    map[string]any
	)
	srv := streamServer(t, sse(completedEvent), func(r *http.Request, p map[string]any) {
		path, auth, payload = r.URL.Path, r.Header.Get("Authorization"), p
	})
	c := NewClient(Config{APIKey: "sk-test", Model: "test-model", BaseURL: srv.URL + "/", HTTPClient: srv.Client()})
	_, err := c.Respond(context.Background(), Request{
		Instructions: "be brief",
		Input:        []json.RawMessage{UserMessage("Привіт")},
		Tools: []Tool{{
			Name:        "current_snapshot",
			Description: "live values",
			Parameters:  json.RawMessage(`{"type":"object","properties":{},"required":[],"additionalProperties":false}`),
		}},
	}, nil)
	if err != nil {
		t.Fatalf("Respond: %v", err)
	}
	if path != "/v1/responses" {
		t.Errorf("path = %q, want /v1/responses", path)
	}
	if auth != "Bearer sk-test" {
		t.Errorf("Authorization = %q", auth)
	}
	if payload["model"] != "test-model" || payload["instructions"] != "be brief" {
		t.Errorf("model/instructions = %v / %v", payload["model"], payload["instructions"])
	}
	// store must be sent explicitly: the API stores responses by default.
	if v, ok := payload["store"]; !ok || v != false {
		t.Errorf("store = %v (present %v), want false", v, ok)
	}
	if payload["stream"] != true {
		t.Errorf("stream = %v, want true", payload["stream"])
	}
	tools, _ := payload["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools = %v", payload["tools"])
	}
	tool := tools[0].(map[string]any)
	if tool["type"] != "function" || tool["strict"] != true || tool["name"] != "current_snapshot" {
		t.Errorf("tool = %v", tool)
	}
	if _, ok := payload["reasoning"]; ok {
		t.Error("reasoning sent without ReasoningEffort")
	}
	if _, ok := payload["include"]; ok {
		t.Error("include sent without ReasoningEffort: models without reasoning reject it")
	}
	input, _ := payload["input"].([]any)
	if len(input) != 1 || input[0].(map[string]any)["role"] != "user" || input[0].(map[string]any)["content"] != "Привіт" {
		t.Errorf("input = %v", payload["input"])
	}
}

func TestRespondAsksForEncryptedReasoningWhenEffortSet(t *testing.T) {
	var payload map[string]any
	srv := streamServer(t, sse(completedEvent), func(_ *http.Request, p map[string]any) { payload = p })
	c := NewClient(Config{APIKey: "k", Model: "m", BaseURL: srv.URL, ReasoningEffort: "low", HTTPClient: srv.Client()})
	if _, err := c.Respond(context.Background(), Request{}, nil); err != nil {
		t.Fatalf("Respond: %v", err)
	}
	reasoning, _ := payload["reasoning"].(map[string]any)
	if reasoning["effort"] != "low" {
		t.Errorf("reasoning = %v", payload["reasoning"])
	}
	include, _ := payload["include"].([]any)
	if len(include) != 1 || include[0] != "reasoning.encrypted_content" {
		t.Errorf("include = %v", payload["include"])
	}
}

func TestRespondAsksForStructuredOutput(t *testing.T) {
	var payload map[string]any
	srv := streamServer(t, sse(completedEvent), func(_ *http.Request, p map[string]any) { payload = p })
	c := NewClient(Config{APIKey: "k", Model: "m", BaseURL: srv.URL, HTTPClient: srv.Client()})
	schema := json.RawMessage(`{"type":"object","properties":{"questions":{"type":"array","items":{"type":"string"}}},"required":["questions"],"additionalProperties":false}`)
	if _, err := c.Respond(context.Background(), Request{Format: &TextFormat{Name: "follow_ups", Schema: schema}}, nil); err != nil {
		t.Fatalf("Respond: %v", err)
	}
	text, _ := payload["text"].(map[string]any)
	format, _ := text["format"].(map[string]any)
	if format["type"] != "json_schema" || format["name"] != "follow_ups" || format["strict"] != true || format["schema"] == nil {
		t.Errorf("text = %v", payload["text"])
	}
	if _, ok := payload["tool_choice"]; ok {
		t.Error("tool_choice sent without tools")
	}
}

func TestRespondStreamsTextAndUsage(t *testing.T) {
	body := sse(
		`{"type":"response.created","response":{}}`,
		`{"type":"response.output_text.delta","output_index":0,"delta":"СЕС дала "}`,
		`{"type":"response.output_text.delta","output_index":0,"delta":"1,2 МВт·год."}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"type":"message","id":"msg_1","role":"assistant","content":[{"type":"output_text","text":"СЕС дала 1,2 МВт·год."}]}}`,
		completedEvent,
	)
	srv := streamServer(t, body, nil)
	c := NewClient(Config{APIKey: "k", Model: "m", BaseURL: srv.URL, HTTPClient: srv.Client()})
	var deltas []string
	res, err := c.Respond(context.Background(), Request{}, func(d string) { deltas = append(deltas, d) })
	if err != nil {
		t.Fatalf("Respond: %v", err)
	}
	if strings.Join(deltas, "|") != "СЕС дала |1,2 МВт·год." {
		t.Errorf("deltas = %q", deltas)
	}
	if res.Text != "СЕС дала 1,2 МВт·год." {
		t.Errorf("text = %q", res.Text)
	}
	if res.Usage.InputTokens != 120 || res.Usage.OutputTokens != 30 {
		t.Errorf("usage = %+v", res.Usage)
	}
	if len(res.Calls) != 0 || len(res.Output) != 1 {
		t.Errorf("calls = %v, output = %d", res.Calls, len(res.Output))
	}
}

// One turn may ask for several tools at once; all of them come back in
// output order so their results can be returned together.
func TestRespondCollectsSeveralFunctionCalls(t *testing.T) {
	body := sse(
		`{"type":"response.output_item.done","output_index":1,"item":{"type":"function_call","id":"fc_2","call_id":"call_b","name":"dam_prices","arguments":"{\"date\":\"2026-10-30\"}"}}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","id":"fc_1","call_id":"call_a","name":"current_snapshot","arguments":"{}"}}`,
		completedEvent,
	)
	srv := streamServer(t, body, nil)
	c := NewClient(Config{APIKey: "k", Model: "m", BaseURL: srv.URL, HTTPClient: srv.Client()})
	res, err := c.Respond(context.Background(), Request{}, nil)
	if err != nil {
		t.Fatalf("Respond: %v", err)
	}
	if len(res.Calls) != 2 {
		t.Fatalf("calls = %+v", res.Calls)
	}
	if res.Calls[0].CallID != "call_a" || res.Calls[1].Name != "dam_prices" || res.Calls[1].Arguments != `{"date":"2026-10-30"}` {
		t.Errorf("calls = %+v", res.Calls)
	}
	if len(res.Output) != 2 {
		t.Errorf("output = %d items, want 2", len(res.Output))
	}
}

func TestRespondReportsIncompleteTurn(t *testing.T) {
	body := sse(`{"type":"response.incomplete","response":{"incomplete_details":{"reason":"max_output_tokens"},"usage":{"input_tokens":5,"output_tokens":4096}}}`)
	srv := streamServer(t, body, nil)
	c := NewClient(Config{APIKey: "k", Model: "m", BaseURL: srv.URL, HTTPClient: srv.Client()})
	res, err := c.Respond(context.Background(), Request{}, nil)
	if err != nil {
		t.Fatalf("Respond: %v", err)
	}
	if res.Incomplete != "max_output_tokens" || res.Usage.OutputTokens != 4096 {
		t.Errorf("incomplete = %q, usage = %+v", res.Incomplete, res.Usage)
	}
}

func TestRespondHTTPErrorCarriesStatusAndMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"error":{"message":"Rate limit reached","type":"requests","code":"rate_limit_exceeded"}}`)
	}))
	defer srv.Close()
	c := NewClient(Config{APIKey: "k", Model: "m", BaseURL: srv.URL, HTTPClient: srv.Client()})
	_, err := c.Respond(context.Background(), Request{}, nil)
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *APIError", err)
	}
	if apiErr.Status != http.StatusTooManyRequests || apiErr.Code != "rate_limit_exceeded" || apiErr.Message != "Rate limit reached" {
		t.Errorf("apiErr = %+v", apiErr)
	}
}

func TestRespondStreamErrorEvents(t *testing.T) {
	cases := map[string]string{
		"error":           `{"type":"error","code":"server_error","message":"boom"}`,
		"response.failed": `{"type":"response.failed","response":{"error":{"code":"server_error","message":"boom"}}}`,
	}
	for name, ev := range cases {
		t.Run(name, func(t *testing.T) {
			srv := streamServer(t, sse(ev), nil)
			c := NewClient(Config{APIKey: "k", Model: "m", BaseURL: srv.URL, HTTPClient: srv.Client()})
			_, err := c.Respond(context.Background(), Request{}, nil)
			var apiErr *APIError
			if !errors.As(err, &apiErr) || apiErr.Code != "server_error" || apiErr.Message != "boom" {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestRespondStreamWithoutCompletionFails(t *testing.T) {
	srv := streamServer(t, sse(`{"type":"response.output_text.delta","delta":"half"}`), nil)
	c := NewClient(Config{APIKey: "k", Model: "m", BaseURL: srv.URL, HTTPClient: srv.Client()})
	_, err := c.Respond(context.Background(), Request{}, nil)
	if !errors.Is(err, ErrStreamEnded) {
		t.Fatalf("err = %v, want ErrStreamEnded", err)
	}
}

func TestReplayKeepsEncryptedReasoning(t *testing.T) {
	output := []json.RawMessage{
		json.RawMessage(`{"type":"reasoning","id":"rs_1","encrypted_content":"gAAA","summary":[]}`),
		json.RawMessage(`{"type":"function_call","id":"fc_1","call_id":"call_a","name":"x","arguments":"{}"}`),
	}
	got := Replay(output)
	if len(got) != 2 || string(got[0]) != string(output[0]) || string(got[1]) != string(output[1]) {
		t.Fatalf("Replay = %s", got)
	}
}

// Without encrypted content a reasoning item can't be resent under
// store:false; the calls go back without ids that would point at it.
func TestReplayDropsReasoningWithoutEncryptedContent(t *testing.T) {
	got := Replay([]json.RawMessage{
		json.RawMessage(`{"type":"reasoning","id":"rs_1","summary":[]}`),
		json.RawMessage(`{"type":"function_call","id":"fc_1","call_id":"call_a","name":"x","arguments":"{}"}`),
	})
	if len(got) != 1 {
		t.Fatalf("Replay = %s, want only the call", got)
	}
	var call map[string]any
	if err := json.Unmarshal(got[0], &call); err != nil {
		t.Fatal(err)
	}
	if _, ok := call["id"]; ok {
		t.Errorf("call kept its id: %s", got[0])
	}
	if call["call_id"] != "call_a" || call["type"] != "function_call" {
		t.Errorf("call = %v", call)
	}
}

func TestFunctionOutputItem(t *testing.T) {
	var item map[string]any
	if err := json.Unmarshal(FunctionOutput("call_a", `{"ok":true}`), &item); err != nil {
		t.Fatal(err)
	}
	if item["type"] != "function_call_output" || item["call_id"] != "call_a" || item["output"] != `{"ok":true}` {
		t.Errorf("item = %v", item)
	}
}
