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
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/responses"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type capturedRequest struct {
	body   map[string]any
	header http.Header
}

// captureRequest sends req through a model configured with opts and returns
// the HTTP request the adapter produced.
func captureRequest(t *testing.T, req *model.Request, opts ...Option) capturedRequest {
	t.Helper()
	got := make(chan capturedRequest, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		got <- capturedRequest{body: body, header: r.Header.Clone()}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"r","status":"completed","output":[]}`)
	}))
	defer srv.Close()
	m := New("gpt-5", append([]Option{WithAPIKey("test"), WithBaseURL(srv.URL)}, opts...)...)
	ch, err := m.GenerateContent(context.Background(), req)
	require.NoError(t, err)
	for resp := range ch {
		require.Nil(t, resp.Error)
	}
	return <-got
}

// rejectingServer fails any HTTP call; requests must be rejected before I/O.
func rejectingServer(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		http.Error(w, "unexpected model call", http.StatusBadRequest)
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func requireJSONField(t *testing.T, want string, value any) {
	t.Helper()
	encoded, err := json.Marshal(value)
	require.NoError(t, err)
	require.JSONEq(t, want, string(encoded))
}

type declarationlessTool struct{}

func (declarationlessTool) Declaration() *tool.Declaration { return nil }

func TestUnsupportedRequestsFailBeforeHTTP(t *testing.T) {
	srv, calls := rejectingServer(t)
	user := []model.Message{model.NewUserMessage("hi")}
	with := func(edit func(*model.Request)) *model.Request {
		req := &model.Request{Messages: user}
		edit(req)
		return req
	}
	penalty, logprobs, topLogprobs, effort := 0.5, true, 2, "turbo"
	for _, tc := range []struct {
		name      string
		modelName string
		req       *model.Request
		want      string
	}{
		{name: "nil request", want: "request cannot be nil"},
		{name: "stop", req: with(func(r *model.Request) { r.Stop = []string{"x"} }), want: "stop is not supported"},
		{name: "presence penalty", req: with(func(r *model.Request) { r.PresencePenalty = &penalty }),
			want: "presence_penalty is not supported"},
		{name: "frequency penalty", req: with(func(r *model.Request) { r.FrequencyPenalty = &penalty }),
			want: "frequency_penalty is not supported"},
		{name: "logprobs", req: with(func(r *model.Request) { r.Logprobs = &logprobs }), want: "logprobs is not supported"},
		{name: "top logprobs", req: with(func(r *model.Request) { r.TopLogprobs = &topLogprobs }),
			want: "logprobs is not supported"},
		{name: "reasoning effort", req: with(func(r *model.Request) { r.ReasoningEffort = &effort }),
			want: `unsupported reasoning_effort "turbo"`},
		{name: "managed request field", req: with(func(r *model.Request) { r.ExtraFields = map[string]any{"input": "x"} }),
			want: "cannot override managed request state"},
		{name: "empty model name", modelName: " ", req: with(func(*model.Request) {}), want: "model name cannot be empty"},
		{name: "structured output without schema", req: with(func(r *model.Request) {
			r.StructuredOutput = &model.StructuredOutput{Type: model.StructuredOutputJSONSchema}
		}), want: "unsupported structured output type"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			name := "gpt-5"
			if tc.modelName != "" {
				name = tc.modelName
			}
			m := New(name, WithAPIKey("test"), WithBaseURL(srv.URL))
			_, err := m.GenerateContent(context.Background(), tc.req)
			require.ErrorContains(t, err, tc.want)
		})
	}
	require.Zero(t, calls.Load())
}

func TestExtraFieldPathsCannotEscapeManagedRootValidation(t *testing.T) {
	srv, calls := rejectingServer(t)
	managedRoots := []string{
		"model", "input", "stream", "store", "include", "tools",
		"previous_response_id", "conversation", "background",
	}
	for _, root := range managedRoots {
		paths := []string{
			":" + root,
			"\\" + root,
			root[:1] + "\\" + root[1:],
			":\\" + root,
		}
		for _, path := range paths {
			for _, source := range []string{"model option", "request field"} {
				t.Run(source+"/"+path, func(t *testing.T) {
					modelOpts := []Option{
						WithAPIKey("test"), WithBaseURL(srv.URL),
						WithOpenAIOptions(option.WithMaxRetries(0)),
					}
					req := &model.Request{Messages: []model.Message{model.NewUserMessage("hi")}}
					if source == "model option" {
						modelOpts = append(modelOpts, WithExtraFields(map[string]any{path: "override"}))
					} else {
						req.ExtraFields = map[string]any{path: "override"}
					}
					m := New("gpt-5", modelOpts...)
					ch, err := m.GenerateContent(context.Background(), req)
					if err == nil {
						_ = collect(t, ch)
					}
					require.ErrorContains(t, err, "unsupported sjson path syntax")
				})
			}
		}
	}
	require.Zero(t, calls.Load(), "managed aliases must fail before HTTP")
}

func TestRequestParametersAndOptionsReachTheWire(t *testing.T) {
	maxTokens, temperature, topP, effort := 64, 0.3, 0.9, "low"
	var roundTrips atomic.Int32
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		roundTrips.Add(1)
		return http.DefaultTransport.RoundTrip(r)
	})
	got := captureRequest(t, &model.Request{
		Messages: []model.Message{model.NewSystemMessage("be brief"), model.NewUserMessage("hi")},
		GenerationConfig: model.GenerationConfig{
			MaxTokens:       &maxTokens,
			Temperature:     &temperature,
			TopP:            &topP,
			ReasoningEffort: &effort,
		},
		Tools: map[string]tool.Tool{
			"calculator": stubTool{name: "calculator"},
			"nil":        nil,
			"undeclared": declarationlessTool{},
		},
		ExtraFields: map[string]any{"metadata.source": "request"},
		Headers:     map[string]string{"X-Trace-Id": "trace-1"},
	},
		WithStore(true),
		WithExtraFields(map[string]any{
			"prompt_cache_key":  "cache-1",
			"reasoning.summary": "detailed",
		}),
		WithHTTPClientOptions(model.WithHTTPClientTransport(transport)),
	)
	require.EqualValues(t, 64, got.body["max_output_tokens"])
	require.EqualValues(t, 0.3, got.body["temperature"])
	require.EqualValues(t, 0.9, got.body["top_p"])
	requireJSONField(t, `{"effort":"low","summary":"detailed"}`, got.body["reasoning"])
	require.Equal(t, true, got.body["store"])
	requireJSONField(t, `{"source":"request"}`, got.body["metadata"])
	require.Equal(t, "cache-1", got.body["prompt_cache_key"])
	require.Equal(t, "trace-1", got.header.Get("X-Trace-Id"))
	require.EqualValues(t, 1, roundTrips.Load(), "HTTP client options configure the transport")
	requireJSONField(t, `[
		{"role":"system","content":"be brief"},
		{"role":"user","content":"hi"}
	]`, got.body["input"])
	tools := got.body["tools"].([]any)
	require.Len(t, tools, 1, "nil and undeclared tools are omitted")
	require.Equal(t, "calculator", tools[0].(map[string]any)["name"])

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"r","status":"completed","output":[]}`)
	}))
	defer srv.Close()
	for size, want := range map[int]int{0: defaultChannelBufferSize, 1: 1} {
		m := New("gpt-5", WithAPIKey("test"), WithBaseURL(srv.URL), WithChannelBufferSize(size))
		ch, err := m.GenerateContent(context.Background(), &model.Request{Messages: []model.Message{model.NewUserMessage("hi")}})
		require.NoError(t, err)
		require.Equal(t, want, cap(ch), "buffer size %d", size)
		_ = collect(t, ch)
	}
}

func TestToolChoiceContract(t *testing.T) {
	srv, calls := rejectingServer(t)
	tools := map[string]tool.Tool{"calculator": stubTool{name: "calculator"}}
	for _, tc := range []struct {
		name          string
		modelChoice   any
		requestChoice any
		want          string
		wantErr       string
	}{
		{name: "unset"},
		{name: "model mode", modelChoice: "required", want: `"required"`},
		{name: "request function", requestChoice: map[string]any{"type": "function", "name": "calculator"},
			want: `{"type":"function","name":"calculator"}`},
		{name: "function type is the default", requestChoice: map[string]any{"name": "calculator"},
			want: `{"type":"function","name":"calculator"}`},
		{name: "request overrides model", modelChoice: "none", requestChoice: "auto", want: `"auto"`},
		{name: "empty mode", requestChoice: " ", wantErr: "tool_choice cannot be empty"},
		{name: "function without name", requestChoice: map[string]any{"type": "function"},
			wantErr: "unsupported tool_choice object"},
		{name: "hosted tool", requestChoice: map[string]any{"type": "file_search"},
			wantErr: "unsupported tool_choice object"},
		{name: "number", modelChoice: 1, wantErr: "unsupported tool_choice value int"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var opts []Option
			if tc.modelChoice != nil {
				opts = append(opts, WithExtraFields(map[string]any{"tool_choice": tc.modelChoice}))
			}
			req := &model.Request{Messages: []model.Message{model.NewUserMessage("hi")}, Tools: tools}
			if tc.requestChoice != nil {
				req.ExtraFields = map[string]any{"tool_choice": tc.requestChoice}
			}
			if tc.wantErr != "" {
				m := New("gpt-5", append([]Option{WithAPIKey("test"), WithBaseURL(srv.URL)}, opts...)...)
				_, err := m.GenerateContent(context.Background(), req)
				require.ErrorContains(t, err, tc.wantErr)
				return
			}
			got := captureRequest(t, req, opts...)
			choice, ok := got.body["tool_choice"]
			if tc.want == "" {
				require.False(t, ok, "tool_choice is omitted unless configured")
				return
			}
			requireJSONField(t, tc.want, choice)
		})
	}
	require.Zero(t, calls.Load())
}

func TestContentPartsMapToInputContent(t *testing.T) {
	text := "part"
	data := func(prefix, raw string) string {
		return prefix + base64.StdEncoding.EncodeToString([]byte(raw))
	}
	got := captureRequest(t, &model.Request{Messages: []model.Message{{
		Role:    model.RoleUser,
		Content: "look",
		ContentParts: []model.ContentPart{
			{Type: model.ContentTypeText, Text: &text},
			{Type: model.ContentTypeText},
			{},
			{Type: model.ContentTypeImage, Image: &model.Image{Data: []byte("jpg"), Format: "jpeg", Detail: "HIGH"}},
			{Type: model.ContentTypeImage, Image: &model.Image{Data: []byte("webp"), Format: "image/webp", Detail: "original"}},
			{Type: model.ContentTypeImage, Image: &model.Image{Data: []byte("png")}},
			{Type: model.ContentTypeImage, Image: &model.Image{URL: "https://example.test/a.png"}},
			{Type: model.ContentTypeFile, File: &model.File{Data: []byte("%PDF"), MimeType: "application/pdf", Name: "a.pdf"}},
			{Type: model.ContentTypeFile, File: &model.File{Data: []byte("raw")}},
			{Type: model.ContentTypeFile, File: &model.File{URL: "https://example.test/b.txt"}},
		},
	}}})
	want := []map[string]any{
		{"type": "input_text", "text": "look"},
		{"type": "input_text", "text": "part"},
		{"type": "input_image", "detail": "high", "image_url": data("data:image/jpeg;base64,", "jpg")},
		{"type": "input_image", "detail": "original", "image_url": data("data:image/webp;base64,", "webp")},
		{"type": "input_image", "detail": "auto", "image_url": data("data:image/png;base64,", "png")},
		{"type": "input_image", "detail": "auto", "image_url": "https://example.test/a.png"},
		{"type": "input_file", "filename": "a.pdf", "file_data": data("data:application/pdf;base64,", "%PDF")},
		{"type": "input_file", "file_data": data("data:application/octet-stream;base64,", "raw")},
		{"type": "input_file", "file_url": "https://example.test/b.txt"},
	}
	encoded, err := json.Marshal(want)
	require.NoError(t, err)
	input := got.body["input"].([]any)
	require.Len(t, input, 1)
	requireJSONField(t, string(encoded), input[0].(map[string]any)["content"])

	srv, calls := rejectingServer(t)
	m := New("gpt-5", WithAPIKey("test"), WithBaseURL(srv.URL))
	for _, tc := range []struct {
		name string
		part model.ContentPart
		want string
	}{
		{name: "nil image", part: model.ContentPart{Type: model.ContentTypeImage}, want: "image content part is empty"},
		{name: "empty image", part: model.ContentPart{Type: model.ContentTypeImage, Image: &model.Image{Detail: "low"}},
			want: "image content part has no url or data"},
		{name: "nil file", part: model.ContentPart{Type: model.ContentTypeFile}, want: "file content part is empty"},
		{name: "empty file", part: model.ContentPart{Type: model.ContentTypeFile, File: &model.File{Name: "a.txt"}},
			want: "file content part has no file_id, url, or data"},
		{name: "audio", part: model.ContentPart{Type: model.ContentTypeAudio},
			want: fmt.Sprintf("%s content parts are not supported", model.ContentTypeAudio)},
		{name: "unknown", part: model.ContentPart{Type: "hologram"}, want: `unsupported content part type "hologram"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := m.GenerateContent(context.Background(), &model.Request{Messages: []model.Message{{
				Role:         model.RoleUser,
				ContentParts: []model.ContentPart{tc.part},
			}}})
			require.ErrorContains(t, err, tc.want)
		})
	}
	require.Zero(t, calls.Load())
}

func TestInvalidHistoryFailsBeforeHTTP(t *testing.T) {
	srv, calls := rejectingServer(t)
	m := New("gpt-5", WithAPIKey("test"), WithBaseURL(srv.URL))
	call := func(id, name, arguments string) model.Message {
		return model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{
			Type:     functionToolType,
			ID:       id,
			Function: model.FunctionDefinitionParam{Name: name, Arguments: []byte(arguments)},
		}}}
	}
	native := func(t *testing.T, modelName, payload string) model.Message {
		msg := model.NewAssistantMessage("")
		require.NoError(t, msg.SetReplayData(responseProvider, modelName, json.RawMessage(payload)))
		return msg
	}
	nativeCall := `[{"type":"function_call","call_id":"c","name":"f","arguments":"{}"}]`
	image := model.ContentPart{Type: model.ContentTypeImage, Image: &model.Image{URL: "https://example.test/a.png"}}
	for _, tc := range []struct {
		name     string
		messages func(t *testing.T) []model.Message
		want     string
	}{
		{name: "unsupported role", messages: func(*testing.T) []model.Message {
			return []model.Message{{Role: "developer", Content: "x"}}
		}, want: `unsupported message role "developer" at message 0`},
		{name: "tool result without call_id", messages: func(*testing.T) []model.Message {
			return []model.Message{model.NewToolMessage(" ", "f", "x")}
		}, want: "tool result at message 0 has an empty call_id"},
		{name: "tool result with an image", messages: func(*testing.T) []model.Message {
			result := model.NewToolMessage("c", "f", "")
			result.ContentParts = []model.ContentPart{image}
			return []model.Message{call("c", "f", "{}"), result}
		}, want: "tool messages only support text content"},
		{name: "call without name", messages: func(*testing.T) []model.Message {
			return []model.Message{call("c", " ", "{}")}
		}, want: `function call "c" has an empty name`},
		{name: "call with non-object arguments", messages: func(*testing.T) []model.Message {
			return []model.Message{call("c", "f", "[1]")}
		}, want: `function call "c" has invalid JSON arguments`},
		{name: "replay on a user message", messages: func(t *testing.T) []model.Message {
			msg := native(t, "gpt-5", nativeCall)
			msg.Role = model.RoleUser
			return []model.Message{msg}
		}, want: "replay data is only valid on assistant messages"},
		{name: "replay of another model", messages: func(t *testing.T) []model.Message {
			return []model.Message{native(t, "gpt-4.1", nativeCall)}
		}, want: "invalid replay data at message 0"},
		{name: "replay object", messages: func(t *testing.T) []model.Message {
			return []model.Message{native(t, "gpt-5", `{"type":"message"}`)}
		}, want: "replay payload must be an item array"},
		{name: "replay scalar item", messages: func(t *testing.T) []model.Message {
			return []model.Message{native(t, "gpt-5", `[1]`)}
		}, want: "decode item 0"},
		{name: "replay item without id", messages: func(t *testing.T) []model.Message {
			return []model.Message{native(t, "gpt-5", `[{"type":"reasoning","summary":[]}]`)}
		}, want: "reasoning item has an empty id"},
		{name: "replay item in progress", messages: func(t *testing.T) []model.Message {
			return []model.Message{native(t, "gpt-5", `[{"type":"reasoning","id":"rs","status":"in_progress","summary":[]}]`)}
		}, want: `has status "in_progress", want completed`},
		{name: "replay message of another role", messages: func(t *testing.T) []model.Message {
			return []model.Message{native(t, "gpt-5",
				`[{"type":"message","id":"m","status":"completed","role":"user","content":[]}]`)}
		}, want: `message item "m" has role "user", want assistant`},
		{name: "replay message with input content", messages: func(t *testing.T) []model.Message {
			return []model.Message{native(t, "gpt-5",
				`[{"type":"message","id":"m","status":"completed","role":"assistant","content":[{"type":"input_text","text":"x"}]}]`)}
		}, want: `unsupported content type "input_text"`},
		{name: "replay call without name", messages: func(t *testing.T) []model.Message {
			return []model.Message{native(t, "gpt-5", `[{"type":"function_call","call_id":"c","arguments":"{}"}]`)}
		}, want: "has an empty name"},
		{name: "replay call with non-object arguments", messages: func(t *testing.T) []model.Message {
			return []model.Message{native(t, "gpt-5", `[{"type":"function_call","call_id":"c","name":"f","arguments":"[]"}]`)}
		}, want: "has invalid JSON arguments"},
		{name: "replay tool output item", messages: func(t *testing.T) []model.Message {
			return []model.Message{native(t, "gpt-5", `[{"type":"function_call_output","call_id":"c","output":"x"}]`)}
		}, want: `unsupported native item type "function_call_output"`},
		{name: "replay call without result", messages: func(t *testing.T) []model.Message {
			return []model.Message{native(t, "gpt-5", nativeCall)}
		}, want: `function call "c" has no tool result`},
		{name: "replay call repeated", messages: func(t *testing.T) []model.Message {
			return []model.Message{
				native(t, "gpt-5", nativeCall), model.NewToolMessage("c", "f", "1"),
				native(t, "gpt-5", nativeCall), model.NewToolMessage("c", "f", "2"),
			}
		}, want: `duplicate function call call_id "c"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := m.GenerateContent(context.Background(), &model.Request{Messages: tc.messages(t)})
			require.ErrorContains(t, err, tc.want)
		})
	}
	require.Zero(t, calls.Load())
}

func requireTerminalError(t *testing.T, got []*model.Response) *model.ResponseError {
	t.Helper()
	require.NotEmpty(t, got)
	for _, resp := range got {
		for _, choice := range resp.Choices {
			require.Nil(t, choice.Message.Replay, "a failed response must not publish native output")
			require.Empty(t, choice.Message.ToolCalls)
		}
	}
	final := got[len(got)-1]
	require.True(t, final.Done)
	require.NotNil(t, final.Error)
	require.Equal(t, model.ObjectTypeError, final.Object)
	return final.Error
}

func TestNonStreamErrorsAreTerminalResponses(t *testing.T) {
	strPtrValue := func(s *string) string {
		if s == nil {
			return ""
		}
		return *s
	}
	for _, tc := range []struct {
		name      string
		status    int
		body      string
		wantMsg   string
		wantType  string
		wantCode  string
		wantParam string
	}{
		{
			name:      "http error",
			status:    http.StatusBadRequest,
			body:      `{"error":{"message":"bad input","type":"invalid_request_error","code":"bad_code","param":"input"}}`,
			wantMsg:   "bad input",
			wantType:  "invalid_request_error",
			wantCode:  "bad_code",
			wantParam: "input",
		},
		{
			name:     "response error",
			body:     `{"id":"resp_err","status":"failed","error":{"code":"server_error","message":"boom"},"output":[]}`,
			wantMsg:  "boom",
			wantType: model.ErrorTypeAPIError,
			wantCode: "server_error",
		},
		{name: "failed", body: `{"id":"r","status":"failed","output":[]}`, wantMsg: "responses request failed",
			wantType: model.ErrorTypeAPIError},
		{name: "cancelled", body: `{"id":"r","status":"cancelled","output":[]}`, wantMsg: "responses request was cancelled",
			wantType: model.ErrorTypeAPIError},
		{name: "queued", body: `{"id":"r","status":"queued","output":[]}`,
			wantMsg: "responses request did not complete: status queued", wantType: model.ErrorTypeAPIError},
		{name: "unsupported output", body: `{"id":"r","status":"completed","output":[{"type":"web_search_call","id":"ws","status":"completed"}]}`,
			wantMsg: `unsupported native item type "web_search_call"`, wantType: model.ErrorTypeAPIError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if tc.status != 0 {
					w.WriteHeader(tc.status)
				}
				_, _ = io.WriteString(w, tc.body)
			}))
			defer srv.Close()
			m := New("gpt-5", WithAPIKey("test"), WithBaseURL(srv.URL), WithOpenAIOptions(option.WithMaxRetries(0)))
			ch, err := m.GenerateContent(context.Background(), &model.Request{Messages: []model.Message{model.NewUserMessage("hi")}})
			require.NoError(t, err)
			got := collect(t, ch)
			require.Len(t, got, 1)
			respErr := requireTerminalError(t, got)
			require.Contains(t, respErr.Message, tc.wantMsg)
			require.Equal(t, tc.wantType, respErr.Type)
			require.Equal(t, tc.wantCode, strPtrValue(respErr.Code))
			require.Equal(t, tc.wantParam, strPtrValue(respErr.Param))
		})
	}
}

func TestNonStreamCallbackObservesAndCancellationEndsWithoutResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"resp_cb","status":"completed","output":[{"type":"message","id":"m","status":"completed","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}`)
	}))
	defer srv.Close()
	var seen atomic.Value
	m := New("gpt-5", WithAPIKey("test"), WithBaseURL(srv.URL), WithResponseCallback(func(
		_ context.Context,
		params *responses.ResponseNewParams,
		resp *responses.Response,
	) {
		seen.Store(string(params.Model) + "/" + resp.ID)
		panic("observer bug")
	}))
	ch, err := m.GenerateContent(context.Background(), &model.Request{Messages: []model.Message{model.NewUserMessage("hi")}})
	require.NoError(t, err)
	got := collect(t, ch)
	require.Len(t, got, 1)
	require.Nil(t, got[0].Error, "a panicking observer must not fail the response")
	require.Equal(t, "ok", got[0].Choices[0].Message.Content)
	require.Equal(t, "gpt-5/resp_cb", seen.Load())

	release := make(chan struct{})
	blocked := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer blocked.Close()
	defer close(release)
	ctx, cancel := context.WithCancel(context.Background())
	m = New("gpt-5", WithAPIKey("test"), WithBaseURL(blocked.URL), WithOpenAIOptions(option.WithMaxRetries(0)))
	ch, err = m.GenerateContent(ctx, &model.Request{Messages: []model.Message{model.NewUserMessage("hi")}})
	require.NoError(t, err)
	cancel()
	require.Empty(t, collect(t, ch), "a cancelled request ends without an error response")
}

func TestStreamProtocolViolationsAreTerminalErrors(t *testing.T) {
	message := func(text string) string {
		return `{"type":"message","id":"m","status":"completed","role":"assistant","content":[{"type":"output_text","text":"` + text + `"}]}`
	}
	done := func(index int, item string) string {
		return fmt.Sprintf(`{"type":"response.output_item.done","output_index":%d,"item":%s}`, index, item)
	}
	completed := func(items ...string) string {
		return `{"type":"response.completed","response":{"id":"r","status":"completed","output":[` + strings.Join(items, ",") + `]}}`
	}
	for _, tc := range []struct {
		name   string
		status int
		events []string
		want   string
	}{
		{name: "http error", status: http.StatusBadRequest, want: "bad input"},
		{name: "error event", events: []string{`{"type":"error","message":"rate limited"}`}, want: "rate limited"},
		{name: "error event without message", events: []string{`{"type":"error"}`},
			want: "responses stream did not complete"},
		{name: "negative output index", events: []string{done(-1, message("a"))}, want: "negative output_index"},
		{name: "duplicate output item", events: []string{done(0, message("a")), done(0, message("a"))},
			want: "duplicate output_item.done at index 0"},
		{name: "completed item differs", events: []string{done(0, message("a")), completed(message("b"))},
			want: "output_item.done at index 0 differs from response.completed"},
		{name: "completed item count", events: []string{done(0, message("a")), completed(message("a"), message("b"))},
			want: "output_item.done covered 1"},
		{name: "completed index outside", events: []string{done(1, message("a")), completed(message("a"))},
			want: "output_item.done index 1 is outside response.completed output"},
		{name: "completed with another status", events: []string{
			`{"type":"response.completed","response":{"id":"r","status":"incomplete","output":[]}}`,
		}, want: "responses request was incomplete"},
		{name: "completed with unsupported output", events: []string{
			completed(`{"type":"web_search_call","id":"ws","status":"completed"}`),
		}, want: `unsupported native item type "web_search_call"`},
		{name: "event without type", events: []string{`{"delta":"x"}`}, want: "stream event has no type"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if tc.status != 0 {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(tc.status)
					_, _ = io.WriteString(w, `{"error":{"message":"bad input","type":"invalid_request_error"}}`)
					return
				}
				writeSSE(w, tc.events...)
			}))
			defer srv.Close()
			m := New("gpt-5", WithAPIKey("test"), WithBaseURL(srv.URL), WithOpenAIOptions(option.WithMaxRetries(0)))
			ch, err := m.GenerateContent(context.Background(), &model.Request{
				Messages:         []model.Message{model.NewUserMessage("hi")},
				GenerationConfig: model.GenerationConfig{Stream: true},
			})
			require.NoError(t, err)
			respErr := requireTerminalError(t, collect(t, ch))
			require.Contains(t, respErr.Message, tc.want)
		})
	}
}

func TestStreamPartialsAndEventCallback(t *testing.T) {
	refusal := `{"type":"message","id":"msg_r","status":"completed","role":"assistant","content":[{"type":"refusal","refusal":"no"}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeSSE(w,
			`{"type":"response.created","response":{"id":"resp_s","created_at":1700000000,"status":"in_progress","output":[]}}`,
			`{"type":"response.reasoning_summary_text.delta","delta":"think"}`,
			`{"type":"response.output_text.delta","delta":""}`,
			`{"type":"response.refusal.delta","delta":"no"}`,
			`{"type":"response.output_item.done","output_index":0,"item":`+refusal+`}`,
			`{"type":"response.completed","response":{"id":"resp_s","created_at":1700000000,"status":"completed","output":[`+refusal+`]}}`,
		)
	}))
	defer srv.Close()
	var mu sync.Mutex
	var types []string
	m := New("gpt-5", WithAPIKey("test"), WithBaseURL(srv.URL), WithStreamEventCallback(func(
		_ context.Context,
		_ *responses.ResponseNewParams,
		event *responses.ResponseStreamEventUnion,
	) {
		mu.Lock()
		types = append(types, event.Type)
		mu.Unlock()
		panic("observer bug")
	}))
	ch, err := m.GenerateContent(context.Background(), &model.Request{
		Messages:         []model.Message{model.NewUserMessage("hi")},
		GenerationConfig: model.GenerationConfig{Stream: true},
	})
	require.NoError(t, err)
	got := collect(t, ch)
	require.Len(t, got, 3, "empty deltas are not published")
	for i, want := range []model.Message{
		{Role: model.RoleAssistant, ReasoningContent: "think"},
		{Role: model.RoleAssistant, Content: "no"},
	} {
		require.True(t, got[i].IsPartial)
		require.Equal(t, "resp_s", got[i].ID)
		require.EqualValues(t, 1700000000, got[i].Created)
		require.Equal(t, want, got[i].Choices[0].Delta)
	}
	final := got[2]
	require.Nil(t, final.Error)
	require.False(t, final.IsPartial)
	require.Equal(t, "no", final.Choices[0].Message.Content)
	require.NotNil(t, final.Choices[0].Message.Replay)
	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, []string{
		"response.created",
		"response.reasoning_summary_text.delta",
		"response.output_text.delta",
		"response.refusal.delta",
		"response.output_item.done",
		"response.completed",
	}, types, "the callback observes every event even when it panics")
}
