//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package responses

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/responses"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const orderedOutput = `[
  {"type":"reasoning","id":"rs_1","status":"completed","summary":[{"type":"summary_text","text":"first"}],"encrypted_content":"opaque-1","future_reasoning":"kept"},
  {"type":"function_call","id":"fc_1","status":"completed","call_id":"call_1","name":"calculator","arguments":"{\"a\":1}","future_call":true},
  {"type":"reasoning","id":"rs_2","status":"completed","summary":[],"content":[{"type":"reasoning_text","text":"internal"}],"encrypted_content":"opaque-2"},
  {"type":"message","id":"msg_1","status":"completed","role":"assistant","phase":"commentary","content":[{"type":"output_text","text":"working"}],"future_message":"kept"},
  {"type":"message","id":"msg_2","status":"completed","role":"assistant","phase":"final_answer","content":[{"type":"refusal","refusal":"cannot help"}]}
]`

func TestGenerateContent_NonStreamReplayProjectionAndUsage(t *testing.T) {
	var body map[string]json.RawMessage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body = mustReadJSONBody(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"resp_1","object":"response","created_at":1700000000,"status":"completed","model":"gpt-5-alias","output":`+orderedOutput+`,"usage":{"input_tokens":11,"output_tokens":2,"total_tokens":13,"input_tokens_details":{"cached_tokens":4},"output_tokens_details":{"reasoning_tokens":1}}}`)
	}))
	defer srv.Close()

	m := New("gpt-5", WithAPIKey("test"), WithBaseURL(srv.URL))
	ch, err := m.GenerateContent(context.Background(), &model.Request{Messages: []model.Message{model.NewUserMessage("hi")}})
	require.NoError(t, err)
	result := collect(t, ch)
	require.Len(t, result, 1)
	resp := result[0]
	require.Nil(t, resp.Error)
	require.True(t, resp.Done)
	require.Equal(t, "workingcannot help", resp.Choices[0].Message.Content)
	require.Equal(t, "first", resp.Choices[0].Message.ReasoningContent)
	require.Len(t, resp.Choices[0].Message.ToolCalls, 1)
	require.Equal(t, "call_1", resp.Choices[0].Message.ToolCalls[0].ID)
	require.Equal(t, "calculator", resp.Choices[0].Message.ToolCalls[0].Function.Name)
	require.JSONEq(t, `{"a":1}`, string(resp.Choices[0].Message.ToolCalls[0].Function.Arguments))
	require.NotNil(t, resp.Choices[0].Message.Replay)
	require.Equal(t, 11, resp.Usage.PromptTokens)
	require.Equal(t, 2, resp.Usage.CompletionTokens)
	require.Equal(t, 4, resp.Usage.PromptTokensDetails.CachedTokens)
	require.Equal(t, 1, resp.Usage.CompletionTokensDetails.ReasoningTokens)
	require.Equal(t, "false", string(body["store"]))
	require.JSONEq(t, `["reasoning.encrypted_content"]`, string(body["include"]))
	require.Equal(t, `"gpt-5"`, string(body["model"]))
}

func TestReplayItemsAreSubmittedUnchangedAndInNativeOrder(t *testing.T) {
	var calls int
	var secondInput json.RawMessage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":"resp_1","status":"completed","output":`+orderedOutput+`}`)
			return
		}
		var request struct {
			Input json.RawMessage `json:"input"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
		secondInput = request.Input
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"resp_2","status":"completed","output":[]}`)
	}))
	defer srv.Close()
	m := New("gpt-5", WithAPIKey("test"), WithBaseURL(srv.URL))
	first, err := m.GenerateContent(context.Background(), &model.Request{Messages: []model.Message{model.NewUserMessage("hi")}})
	require.NoError(t, err)
	firstResponses := collect(t, first)
	require.Len(t, firstResponses, 1)
	assistant := firstResponses[0].Choices[0].Message

	encoded, err := json.Marshal(assistant)
	require.NoError(t, err)
	var restored model.Message
	require.NoError(t, json.Unmarshal(encoded, &restored))
	request, err := m.GenerateContent(context.Background(), &model.Request{Messages: []model.Message{
		model.NewUserMessage("hi"),
		restored,
		model.NewToolMessage("call_1", "calculator", "2"),
	}})
	require.NoError(t, err)
	_ = collect(t, request)
	require.Equal(t, 2, calls)
	var got []json.RawMessage
	require.NoError(t, json.Unmarshal(secondInput, &got))
	require.Len(t, got, 7)
	var firstNative []json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(orderedOutput), &firstNative))
	for i := range firstNative {
		require.JSONEq(t, string(firstNative[i]), string(got[i+1]))
	}
	require.JSONEq(t, `{"type":"function_call_output","call_id":"call_1","output":"2"}`, string(got[6]))
}

func TestReplayRejectsProjectionEditsAndUnsupportedItems(t *testing.T) {
	resp := decodeResponse(t, `{"id":"r","status":"completed","output":[`+
		`{"type":"function_call","id":"fc_1","status":"completed","call_id":"call_1","name":"f","arguments":"{}"},`+
		`{"type":"message","id":"msg_1","status":"completed","role":"assistant","phase":"final_answer","content":[{"type":"output_text","text":"ok"}]}]}`)
	projected, err := projectResponse(resp, "gpt-5")
	require.NoError(t, err)
	message := projected.Choices[0].Message
	message.ToolCalls[0].Function.Arguments = []byte(`{"edited":true}`)
	_, err = convertMessages([]model.Message{message, model.NewToolMessage("call_1", "f", "ok")}, "gpt-5")
	require.ErrorContains(t, err, "projection changed")

	message = projected.Choices[0].Message
	message.Content = "edited"
	_, err = convertMessages([]model.Message{message, model.NewToolMessage("call_1", "f", "ok")}, "gpt-5")
	require.ErrorContains(t, err, "projection changed")

	unsupported := decodeResponse(t, `{"id":"r","status":"completed","output":[{"type":"tool_search_call","id":"ts_1","status":"completed"}]}`)
	_, err = projectResponse(unsupported, "gpt-5")
	require.ErrorContains(t, err, "unsupported native item type")
}

func TestOpaqueReasoningAndRefusalAreRetained(t *testing.T) {
	resp := decodeResponse(t, `{"id":"r","status":"completed","output":[{"type":"reasoning","id":"rs_opaque","status":"completed","summary":[],"encrypted_content":"secret"},{"type":"message","id":"msg_refusal","status":"completed","role":"assistant","content":[{"type":"refusal","refusal":"no"}]}]}`)
	projected, err := projectResponse(resp, "gpt-5")
	require.NoError(t, err)
	require.Equal(t, "no", projected.Choices[0].Message.Content)
	require.Empty(t, projected.Choices[0].Message.ReasoningContent)
	raw, err := projected.Choices[0].Message.ReplayDataFor(responseProvider, "gpt-5")
	require.NoError(t, err)
	require.Contains(t, string(raw), `"id":"rs_opaque"`)
	require.Contains(t, string(raw), `"encrypted_content":"secret"`)
}

func TestStreamDeltasArePartialAndFinalReplayIsAuthoritative(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeSSE(w,
			`{"type":"response.created","response":{"id":"resp_s","status":"in_progress","created_at":1700000001}}`,
			`{"type":"keepalive"}`,
			`{"type":"response.output_text.delta","delta":"he"}`,
			`{"type":"response.refusal.delta","delta":"llo"}`,
			`{"type":"response.output_item.done","output_index":0,"item":{"type":"message","id":"msg_1","status":"completed","role":"assistant","content":[{"type":"refusal","refusal":"hello"}]}}`,
			`{"type":"response.completed","response":{"id":"resp_s","status":"completed","created_at":1700000001,"output":[{"type":"message","id":"msg_1","status":"completed","role":"assistant","content":[{"type":"refusal","refusal":"hello"}]}],"usage":{"input_tokens":3,"output_tokens":2,"total_tokens":5}}}`,
		)
	}))
	defer srv.Close()
	m := New("gpt-5", WithAPIKey("test"), WithBaseURL(srv.URL))
	ch, err := m.GenerateContent(context.Background(), &model.Request{
		Messages:         []model.Message{model.NewUserMessage("hi")},
		GenerationConfig: model.GenerationConfig{Stream: true},
	})
	require.NoError(t, err)
	got := collect(t, ch)
	require.Len(t, got, 3)
	for _, partial := range got[:2] {
		require.True(t, partial.IsPartial)
		require.False(t, partial.Done)
		require.Empty(t, partial.Choices[0].Message.ToolCalls)
		require.Nil(t, partial.Choices[0].Message.Replay)
	}
	require.Equal(t, []string{"he", "llo"}, []string{got[0].Choices[0].Delta.Content, got[1].Choices[0].Delta.Content})
	final := got[2]
	require.True(t, final.Done)
	require.False(t, final.IsPartial)
	require.Equal(t, "hello", final.Choices[0].Message.Content)
	require.NotNil(t, final.Choices[0].Message.Replay)
}

func TestStreamFailureIncompleteAndEOFNeverExposeCalls(t *testing.T) {
	cases := []struct {
		name   string
		events []string
	}{
		{name: "eof", events: []string{
			`{"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"fc_1","status":"in_progress","call_id":"call_1","name":"f","arguments":""}}`,
			`{"type":"response.function_call_arguments.done","item_id":"fc_1","arguments":"{}"}`,
		}},
		{name: "failed", events: []string{
			`{"type":"response.failed","response":{"id":"r","status":"failed","error":{"message":"failed"}}}`,
		}},
		{name: "incomplete", events: []string{
			`{"type":"response.incomplete","response":{"id":"r","status":"incomplete","incomplete_details":{"reason":"max_output_tokens"}}}`,
		}},
		{name: "cancelled", events: []string{
			`{"type":"response.cancelled","response":{"id":"r","status":"cancelled"}}`,
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { writeSSE(w, tc.events...) }))
			defer srv.Close()
			m := New("gpt-5", WithAPIKey("test"), WithBaseURL(srv.URL))
			ch, err := m.GenerateContent(context.Background(), &model.Request{
				Messages:         []model.Message{model.NewUserMessage("hi")},
				GenerationConfig: model.GenerationConfig{Stream: true},
			})
			require.NoError(t, err)
			got := collect(t, ch)
			require.NotEmpty(t, got)
			final := got[len(got)-1]
			require.True(t, final.Done)
			require.NotNil(t, final.Error)
			for _, resp := range got {
				if len(resp.Choices) > 0 {
					require.Empty(t, resp.Choices[0].Message.ToolCalls)
					require.Nil(t, resp.Choices[0].Message.Replay)
				}
			}
		})
	}
}

func TestNonStreamingIncompleteDoesNotBecomeSuccessful(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"resp_inc","status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output":[{"type":"function_call","id":"fc_1","status":"incomplete","call_id":"call_1","name":"f","arguments":"{}"}]}`)
	}))
	defer srv.Close()
	m := New("gpt-5", WithAPIKey("test"), WithBaseURL(srv.URL))
	ch, err := m.GenerateContent(context.Background(), &model.Request{Messages: []model.Message{model.NewUserMessage("hi")}})
	require.NoError(t, err)
	got := collect(t, ch)
	require.Len(t, got, 1)
	require.NotNil(t, got[0].Error)
	require.Empty(t, got[0].Choices)
}

func TestExtraFieldsAndCallbackCannotOverrideManagedState(t *testing.T) {
	for _, key := range []string{"model", "input", "stream", "store", "include", "tools.0", "previous_response_id", "conversation.id", "background"} {
		m := New("gpt-5", WithExtraFields(map[string]any{key: "bad"}))
		_, err := m.GenerateContent(context.Background(), &model.Request{Messages: []model.Message{model.NewUserMessage("hi")}})
		require.ErrorContains(t, err, "cannot override managed request state")
	}
	m := New("gpt-5", WithRequestCallback(func(_ context.Context, params *responses.ResponseNewParams) {
		params.PreviousResponseID = openai.String("resp_previous")
	}))
	_, err := m.GenerateContent(context.Background(), &model.Request{Messages: []model.Message{model.NewUserMessage("hi")}})
	require.ErrorContains(t, err, `request callback cannot change managed field "previous_response_id"`)
}

func TestLegacyTextAndLocalFunctionToolHistory(t *testing.T) {
	items, err := convertMessages([]model.Message{
		model.NewUserMessage("calc"),
		{Role: model.RoleAssistant, Content: "calculating", ToolCalls: []model.ToolCall{{
			ID: "call_1", Type: "function", Function: model.FunctionDefinitionParam{Name: "calculator", Arguments: []byte(`{"a":1}`)},
		}}},
		model.NewToolMessage("call_1", "calculator", "2"),
		model.NewAssistantMessage("2"),
	}, "gpt-5")
	require.NoError(t, err)
	raw, err := json.Marshal(items)
	require.NoError(t, err)
	var got []map[string]any
	require.NoError(t, json.Unmarshal(raw, &got))
	require.Equal(t, []any{"user", "assistant", "function_call", "function_call_output", "assistant"}, []any{got[0]["role"], got[1]["role"], got[2]["type"], got[3]["type"], got[4]["role"]})
	require.Equal(t, "call_1", got[3]["call_id"])

	_, err = convertMessages([]model.Message{model.NewToolMessage("missing", "f", "x")}, "gpt-5")
	require.ErrorContains(t, err, "no preceding function call")
	_, err = convertMessages([]model.Message{{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{ID: "", Function: model.FunctionDefinitionParam{Name: "f", Arguments: []byte(`{}`)}}}}}, "gpt-5")
	require.ErrorContains(t, err, "empty call_id")
	_, err = convertMessages([]model.Message{{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{ID: "c", Function: model.FunctionDefinitionParam{Name: "f", Arguments: []byte(`{}`)}}}}}, "gpt-5")
	require.ErrorContains(t, err, "no tool result")
	_, err = convertMessages([]model.Message{{Role: model.RoleAssistant, ReasoningContent: "loose reasoning"}}, "gpt-5")
	require.ErrorContains(t, err, "requires native replay data")
}

func TestReplayPayloadValidatesDependencyIDs(t *testing.T) {
	for _, tc := range []struct {
		name string
		msgs []model.Message
		want string
	}{
		{name: "duplicate call", msgs: []model.Message{
			{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{ID: "c", Function: model.FunctionDefinitionParam{Name: "f", Arguments: []byte(`{}`)}}}},
			model.NewToolMessage("c", "f", "x"),
			{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{ID: "c", Function: model.FunctionDefinitionParam{Name: "f", Arguments: []byte(`{}`)}}}},
			model.NewToolMessage("c", "f", "y"),
		}, want: "duplicate function call"},
		{name: "duplicate result", msgs: []model.Message{
			{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{ID: "c", Function: model.FunctionDefinitionParam{Name: "f", Arguments: []byte(`{}`)}}}},
			model.NewToolMessage("c", "f", "x"), model.NewToolMessage("c", "f", "y"),
		}, want: "duplicate tool result"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := convertMessages(tc.msgs, "gpt-5")
			require.ErrorContains(t, err, tc.want)
		})
	}
}

func TestLegacyCallIDsRemainOpaque(t *testing.T) {
	call := func(id string) model.Message {
		return model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{
			ID: id, Type: "function",
			Function: model.FunctionDefinitionParam{Name: "f", Arguments: []byte(`{}`)},
		}}}
	}
	items, err := convertMessages([]model.Message{
		call(" call_1 "),
		model.NewToolMessage(" call_1 ", "f", "ok"),
	}, "gpt-5")
	require.NoError(t, err)
	raw, err := json.Marshal(items)
	require.NoError(t, err)
	require.JSONEq(t, `[
		{"type":"function_call","call_id":" call_1 ","name":"f","arguments":"{}"},
		{"type":"function_call_output","call_id":" call_1 ","output":"ok"}
	]`, string(raw))

	_, err = convertMessages([]model.Message{
		call("call_1"),
		model.NewToolMessage(" call_1 ", "f", "ok"),
	}, "gpt-5")
	require.ErrorContains(t, err, "no preceding function call")
	_, err = convertMessages([]model.Message{
		call(" \t "),
		model.NewToolMessage(" \t ", "f", "ok"),
	}, "gpt-5")
	require.ErrorContains(t, err, "empty call_id")
}

func TestNativeCallIDsRemainOpaqueAndItemsAreNotRewritten(t *testing.T) {
	nativeMessage := func(t *testing.T, payload string) model.Message {
		t.Helper()
		msg := model.NewAssistantMessage("")
		require.NoError(t, msg.SetReplayData(responseProvider, "gpt-5", json.RawMessage(payload)))
		return msg
	}
	item := `{"type":"function_call","call_id":" call_1 ","name":"f","arguments":"{}","future":true}`
	items, err := convertMessages([]model.Message{
		nativeMessage(t, `[`+item+`]`),
		model.NewToolMessage(" call_1 ", "f", "ok"),
	}, "gpt-5")
	require.NoError(t, err)
	raw, err := json.Marshal(items)
	require.NoError(t, err)
	var got []json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &got))
	require.JSONEq(t, item, string(got[0]), "native function call fields and call_id must be retained")
	require.JSONEq(t, `{"type":"function_call_output","call_id":" call_1 ","output":"ok"}`, string(got[1]))

	_, err = convertMessages([]model.Message{
		nativeMessage(t, `[{"type":"function_call","call_id":"call_1","name":"f","arguments":"{}"}]`),
		model.NewToolMessage(" call_1 ", "f", "ok"),
	}, "gpt-5")
	require.ErrorContains(t, err, "no preceding function call")
	_, err = convertMessages([]model.Message{
		nativeMessage(t, `[{"type":"function_call","call_id":"  ","name":"f","arguments":"{}"}]`),
	}, "gpt-5")
	require.ErrorContains(t, err, "empty call_id")
}

func TestToolsStructuredOutputAndMultimodalInputs(t *testing.T) {
	var body map[string]json.RawMessage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body = mustReadJSONBody(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"r","status":"completed","output":[]}`)
	}))
	defer srv.Close()
	m := New("gpt-5", WithAPIKey("test"), WithBaseURL(srv.URL))
	ch, err := m.GenerateContent(context.Background(), &model.Request{
		Messages: []model.Message{{Role: model.RoleUser, ContentParts: []model.ContentPart{
			{Type: model.ContentTypeImage, Image: &model.Image{URL: "https://example.test/img.png", Detail: "low"}},
			{Type: model.ContentTypeFile, File: &model.File{FileID: "file_1", Name: "notes.txt"}},
		}}},
		Tools: map[string]tool.Tool{"calculator": stubTool{name: "calculator"}},
		StructuredOutput: &model.StructuredOutput{Type: model.StructuredOutputJSONSchema, JSONSchema: &model.JSONSchemaConfig{
			Name: "answer", Strict: true, Schema: map[string]any{"type": "object"},
		}},
	})
	require.NoError(t, err)
	_ = collect(t, ch)
	var decoded map[string]any
	encodedBody, err := json.Marshal(body)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(encodedBody, &decoded))
	require.Len(t, decoded["tools"], 1)
	require.Equal(t, "function", decoded["tools"].([]any)[0].(map[string]any)["type"])
	format := decoded["text"].(map[string]any)["format"].(map[string]any)
	require.Equal(t, "json_schema", format["type"])
	require.Equal(t, "answer", format["name"])
	require.Equal(t, true, format["strict"])
	input := decoded["input"].([]any)[0].(map[string]any)["content"].([]any)
	require.Equal(t, "input_image", input[0].(map[string]any)["type"])
	require.Equal(t, "input_file", input[1].(map[string]any)["type"])
}

func TestMalformedAndUnterminatedStreamAreErrors(t *testing.T) {
	for _, payload := range []string{`not-json`, `{"type":"response.created"}`} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { writeSSE(w, payload) }))
		m := New("gpt-5", WithAPIKey("test"), WithBaseURL(srv.URL))
		ch, err := m.GenerateContent(context.Background(), &model.Request{
			Messages:         []model.Message{model.NewUserMessage("hi")},
			GenerationConfig: model.GenerationConfig{Stream: true},
		})
		require.NoError(t, err)
		got := collect(t, ch)
		require.NotEmpty(t, got)
		require.NotNil(t, got[len(got)-1].Error)
		srv.Close()
	}
}

func TestStreamContextCancellationClosesResponseChannel(t *testing.T) {
	started := make(chan struct{})
	serverClosed := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.created\",\"response\":{\"id\":\"r\",\"status\":\"in_progress\"}}\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		close(started)
		<-r.Context().Done()
		close(serverClosed)
	}))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	m := New("gpt-5", WithAPIKey("test"), WithBaseURL(srv.URL), WithChannelBufferSize(1))
	ch, err := m.GenerateContent(ctx, &model.Request{
		Messages:         []model.Message{model.NewUserMessage("hi")},
		GenerationConfig: model.GenerationConfig{Stream: true},
	})
	require.NoError(t, err)
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("stream request did not start")
	}
	cancel()
	select {
	case _, ok := <-ch:
		if ok {
			for range ch {
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatal("response channel did not close after cancellation")
	}
	select {
	case <-serverClosed:
	case <-time.After(2 * time.Second):
		t.Fatal("HTTP stream was not cancelled")
	}
}

func TestStreamCompletesWithoutWaitingForServerClose(t *testing.T) {
	serverDone := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeSSE(w, `{"type":"response.completed","response":{"id":"r","status":"completed","output":[]}}`)
		<-r.Context().Done()
		close(serverDone)
	}))
	defer srv.Close()
	m := New("gpt-5", WithAPIKey("test"), WithBaseURL(srv.URL))
	ch, err := m.GenerateContent(context.Background(), &model.Request{
		Messages:         []model.Message{model.NewUserMessage("hi")},
		GenerationConfig: model.GenerationConfig{Stream: true},
	})
	require.NoError(t, err)
	select {
	case got := <-ch:
		require.NotNil(t, got)
		require.True(t, got.Done)
	case <-time.After(2 * time.Second):
		t.Fatal("adapter waited for the server to close after response.completed")
	}
	select {
	case <-serverDone:
	case <-time.After(2 * time.Second):
		t.Fatal("adapter did not close the stream after response.completed")
	}
}

func TestInfo(t *testing.T) {
	require.Equal(t, "gpt-5", New("gpt-5").Info().Name)
	require.Equal(t, responseProvider, New("gpt-5").Info().ReplayProtocol)
}

func TestCompletedResponseAcceptsOptionalItemFields(t *testing.T) {
	response := decodeResponse(t, `{"id":"resp_optional","status":"completed","output":[
		{"type":"reasoning","id":"rs_optional","summary":[],"encrypted_content":"opaque"},
		{"type":"function_call","call_id":"call_optional","name":"calculator","arguments":"{}"}
	]}`)
	projected, err := projectResponse(response, "gpt-5")
	require.NoError(t, err)
	require.NotNil(t, projected.Choices[0].Message.Replay)
	payload, err := projected.Choices[0].Message.ReplayDataFor(responseProvider, "gpt-5")
	require.NoError(t, err)
	var native []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(payload, &native))
	require.NotContains(t, native[1], "id", "replay must not fabricate an optional function-call item ID")
	_, err = convertMessages([]model.Message{
		projected.Choices[0].Message,
		{Role: model.RoleTool, ToolID: "call_optional", Content: "42"},
	}, "gpt-5")
	require.NoError(t, err)
}

func TestToolResultPreservesTextAndTextParts(t *testing.T) {
	part := " from part"
	text, err := messageText(model.Message{Content: "from content", ContentParts: []model.ContentPart{
		{Type: model.ContentTypeText, Text: &part},
	}})
	require.NoError(t, err)
	require.Equal(t, "from content from part", text)
}

func TestNonStreamNullResponseReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, "null")
	}))
	defer srv.Close()
	m := New("gpt-5", WithAPIKey("test"), WithBaseURL(srv.URL))
	ch, err := m.GenerateContent(context.Background(), &model.Request{Messages: []model.Message{model.NewUserMessage("hi")}})
	require.NoError(t, err)
	result := collect(t, ch)
	require.Len(t, result, 1)
	require.NotNil(t, result[0].Error)
	require.Empty(t, result[0].Choices)
}

func TestIncompleteResponseRetainsReportedUsage(t *testing.T) {
	resp := decodeResponse(t, `{"id":"resp_incomplete","model":"gpt-5","status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"usage":{"input_tokens":10,"output_tokens":20,"total_tokens":30}}`)
	result := responseStatusError(resp)
	require.NotNil(t, result.Error)
	require.Empty(t, result.Choices)
	require.Equal(t, "resp_incomplete", result.ID)
	require.Equal(t, 30, result.Usage.TotalTokens)
}

type stubTool struct{ name string }

func (s stubTool) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: s.name, Description: "stub", InputSchema: &tool.Schema{Type: "object"}}
}

func decodeResponse(t *testing.T, raw string) *responses.Response {
	t.Helper()
	var resp responses.Response
	require.NoError(t, json.Unmarshal([]byte(raw), &resp))
	return &resp
}

func collect(t *testing.T, ch <-chan *model.Response) []*model.Response {
	t.Helper()
	var out []*model.Response
	for resp := range ch {
		out = append(out, resp)
	}
	return out
}

func mustReadJSONBody(t *testing.T, r *http.Request) map[string]json.RawMessage {
	t.Helper()
	defer r.Body.Close()
	var body map[string]json.RawMessage
	require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
	return body
}

func writeSSE(w http.ResponseWriter, events ...string) {
	w.Header().Set("Content-Type", "text/event-stream")
	flusher, _ := w.(http.Flusher)
	for _, event := range events {
		_, _ = io.WriteString(w, "data: "+event+"\n\n")
		if flusher != nil {
			flusher.Flush()
		}
	}
}

func TestLowLevelSDKOptionsAreAnExplicitEscapeHatch(t *testing.T) {
	var body map[string]json.RawMessage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body = mustReadJSONBody(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"r","status":"completed","output":[]}`)
	}))
	defer srv.Close()
	m := New("gpt-5", WithAPIKey("test"), WithBaseURL(srv.URL), WithOpenAIOptions(option.WithJSONSet("prompt_cache_key", "test")))
	ch, err := m.GenerateContent(context.Background(), &model.Request{Messages: []model.Message{model.NewUserMessage("hi")}})
	require.NoError(t, err)
	_ = collect(t, ch)
	require.Equal(t, `"test"`, string(body["prompt_cache_key"]))
}

func TestReplayModelBindingUsesConfiguredName(t *testing.T) {
	resp := decodeResponse(t, `{"id":"r","status":"completed","model":"resolved-name","output":[{"type":"message","id":"m","status":"completed","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}`)
	projected, err := projectResponse(resp, "configured-name")
	require.NoError(t, err)
	_, err = projected.Choices[0].Message.ReplayDataFor(responseProvider, "configured-name")
	require.NoError(t, err)
	_, err = projected.Choices[0].Message.ReplayDataFor(responseProvider, "resolved-name")
	require.Error(t, err)
}

func TestOpenAIRequestCallbackCanChangeSamplingOnly(t *testing.T) {
	var body map[string]json.RawMessage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body = mustReadJSONBody(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"r","status":"completed","output":[]}`)
	}))
	defer srv.Close()
	m := New("gpt-5", WithAPIKey("test"), WithBaseURL(srv.URL), WithRequestCallback(func(_ context.Context, params *responses.ResponseNewParams) {
		params.Temperature = openai.Float(0.2)
	}))
	ch, err := m.GenerateContent(context.Background(), &model.Request{Messages: []model.Message{model.NewUserMessage("hi")}})
	require.NoError(t, err)
	_ = collect(t, ch)
	require.Equal(t, "0.2", string(body["temperature"]))
}
