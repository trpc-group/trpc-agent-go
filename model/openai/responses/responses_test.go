//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
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
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestGenerateContent_NonStreamTextAndUsage(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.True(t, strings.HasSuffix(r.URL.Path, "/responses"))
		gotBody = readJSONBody(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"id":"resp_text",
			"object":"response",
			"created_at":1700000000,
			"status":"completed",
			"model":"gpt-5",
			"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello world"}]}],
			"usage":{"input_tokens":11,"output_tokens":2,"total_tokens":13,"input_tokens_details":{"cached_tokens":0},"output_tokens_details":{"reasoning_tokens":0}}
		}`)
	}))
	defer srv.Close()

	m := New("gpt-5", WithAPIKey("test"), WithBaseURL(srv.URL), WithStore(false))
	ch, err := m.GenerateContent(context.Background(), &model.Request{
		Messages: []model.Message{model.NewUserMessage("hi")},
	})
	require.NoError(t, err)
	resps := collect(t, ch)
	require.Len(t, resps, 1)
	require.Nil(t, resps[0].Error)
	require.True(t, resps[0].Done)
	require.Equal(t, "hello world", resps[0].Choices[0].Message.Content)
	require.NotNil(t, resps[0].Usage)
	require.Equal(t, 11, resps[0].Usage.PromptTokens)
	require.Equal(t, 2, resps[0].Usage.CompletionTokens)
	require.Equal(t, false, gotBody["store"])
}

func TestGenerateContent_StreamTextOrderAndSingleTerminal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeSSE(w,
			`{"type":"response.created","sequence_number":0,"response":{"id":"resp_s","object":"response","status":"in_progress","output":null,"created_at":1700000001}}`,
			`{"type":"keepalive"}`,
			`{"type":"response.output_text.delta","delta":"hel","sequence_number":1}`,
			`{"type":"response.output_text.delta","delta":"lo","sequence_number":2}`,
			`{"type":"response.completed","sequence_number":3,"response":{"id":"resp_s","object":"response","status":"completed","created_at":1700000001,"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello"}]}],"usage":{"input_tokens":3,"output_tokens":2,"total_tokens":5,"input_tokens_details":{"cached_tokens":0},"output_tokens_details":{"reasoning_tokens":0}}}}`,
		)
	}))
	defer srv.Close()

	m := New("gpt-5", WithAPIKey("test"), WithBaseURL(srv.URL))
	ch, err := m.GenerateContent(context.Background(), &model.Request{
		Messages:         []model.Message{model.NewUserMessage("hi")},
		GenerationConfig: model.GenerationConfig{Stream: true},
	})
	require.NoError(t, err)
	resps := collect(t, ch)
	var deltas []string
	var terminals int
	var final string
	for _, resp := range resps {
		require.Nil(t, resp.Error)
		if resp.Done {
			terminals++
			final = resp.Choices[0].Message.Content
			continue
		}
		if len(resp.Choices) > 0 && resp.Choices[0].Delta.Content != "" {
			deltas = append(deltas, resp.Choices[0].Delta.Content)
		}
	}
	require.Equal(t, []string{"hel", "lo"}, deltas)
	require.Equal(t, 1, terminals)
	require.Equal(t, "hello", final)
	require.Equal(t, "hello", strings.Join(deltas, ""))
}

func TestGenerateContent_StreamFunctionCallArguments(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeSSE(w,
			`{"type":"response.output_item.added","item":{"type":"function_call","id":"fc_1","call_id":"call_abc","name":"calculator","arguments":""}}`,
			`{"type":"response.function_call_arguments.delta","item_id":"fc_1","delta":"{\"a\":"}`,
			`{"type":"response.function_call_arguments.delta","item_id":"fc_1","delta":"1}"}`,
			`{"type":"response.function_call_arguments.done","item_id":"fc_1","arguments":"{\"a\":1}"}`,
			`{"type":"response.completed","response":{"id":"resp_fc","status":"completed","output":[{"type":"function_call","id":"fc_1","call_id":"call_abc","name":"calculator","arguments":"{\"a\":1}"}]}}`,
		)
	}))
	defer srv.Close()

	m := New("gpt-5", WithAPIKey("test"), WithBaseURL(srv.URL))
	ch, err := m.GenerateContent(context.Background(), &model.Request{
		Messages:         []model.Message{model.NewUserMessage("calc")},
		GenerationConfig: model.GenerationConfig{Stream: true},
		Tools:            map[string]tool.Tool{"calculator": stubTool{name: "calculator"}},
	})
	require.NoError(t, err)
	resps := collect(t, ch)
	final := resps[len(resps)-1]
	require.True(t, final.Done)
	require.Len(t, final.Choices[0].Message.ToolCalls, 1)
	tc := final.Choices[0].Message.ToolCalls[0]
	require.Equal(t, "call_abc", tc.ID)
	require.Equal(t, "calculator", tc.Function.Name)
	require.JSONEq(t, `{"a":1}`, string(tc.Function.Arguments))
}

func TestGenerateContent_StructuredOutputJSONSchema(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody = readJSONBody(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"id":"resp_so",
			"object":"response",
			"status":"completed",
			"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"{\"name\":\"Ada\"}"}]}]
		}`)
	}))
	defer srv.Close()

	m := New("gpt-5", WithAPIKey("test"), WithBaseURL(srv.URL))
	ch, err := m.GenerateContent(context.Background(), &model.Request{
		Messages: []model.Message{model.NewUserMessage("extract")},
		StructuredOutput: &model.StructuredOutput{
			Type: model.StructuredOutputJSONSchema,
			JSONSchema: &model.JSONSchemaConfig{
				Name:   "person",
				Strict: false,
				Schema: map[string]any{"type": "object", "properties": map[string]any{"name": map[string]any{"type": "string"}}},
			},
		},
	})
	require.NoError(t, err)
	resps := collect(t, ch)
	require.Equal(t, `{"name":"Ada"}`, resps[0].Choices[0].Message.Content)
	format := gotBody["text"].(map[string]any)["format"].(map[string]any)
	require.Equal(t, "json_schema", format["type"])
	require.Equal(t, "person", format["name"])
}

func TestGenerateContent_OutputNullDoesNotPanic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"id":"resp_null",
			"object":"response",
			"status":"completed",
			"output":null,
			"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2,"input_tokens_details":{"cached_tokens":0},"output_tokens_details":{"reasoning_tokens":0}}
		}`)
	}))
	defer srv.Close()

	m := New("gpt-5", WithAPIKey("test"), WithBaseURL(srv.URL))
	ch, err := m.GenerateContent(context.Background(), &model.Request{
		Messages: []model.Message{model.NewUserMessage("hi")},
	})
	require.NoError(t, err)
	resps := collect(t, ch)
	require.Len(t, resps, 1)
	require.Nil(t, resps[0].Error)
	require.True(t, resps[0].Done)
}

func TestGenerateContent_FailFastUnsupportedParams(t *testing.T) {
	m := New("gpt-5", WithAPIKey("test"), WithBaseURL("http://127.0.0.1:1"))
	stop := []string{"END"}
	effort := "no_think"
	penalty := 0.2

	_, err := m.GenerateContent(context.Background(), &model.Request{
		Messages:         []model.Message{model.NewUserMessage("hi")},
		GenerationConfig: model.GenerationConfig{Stop: stop},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "stop")

	_, err = m.GenerateContent(context.Background(), &model.Request{
		Messages:         []model.Message{model.NewUserMessage("hi")},
		GenerationConfig: model.GenerationConfig{PresencePenalty: &penalty},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "presence_penalty")

	_, err = m.GenerateContent(context.Background(), &model.Request{
		Messages:         []model.Message{model.NewUserMessage("hi")},
		GenerationConfig: model.GenerationConfig{FrequencyPenalty: &penalty},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "frequency_penalty")

	_, err = m.GenerateContent(context.Background(), &model.Request{
		Messages:         []model.Message{model.NewUserMessage("hi")},
		GenerationConfig: model.GenerationConfig{ReasoningEffort: &effort},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "reasoning_effort")
}

func TestGenerateContent_IncompleteMapsToLength(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"id":"resp_inc",
			"object":"response",
			"status":"incomplete",
			"incomplete_details":{"reason":"max_output_tokens"},
			"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"partial"}]}]
		}`)
	}))
	defer srv.Close()

	m := New("gpt-5", WithAPIKey("test"), WithBaseURL(srv.URL))
	ch, err := m.GenerateContent(context.Background(), &model.Request{
		Messages: []model.Message{model.NewUserMessage("hi")},
	})
	require.NoError(t, err)
	resps := collect(t, ch)
	require.Equal(t, finishReasonLength, *resps[0].Choices[0].FinishReason)
}

func TestConvertMessages_ToolCallAndReasoningReplay(t *testing.T) {
	items := convertMessages([]model.Message{
		model.NewUserMessage("calc"),
		{
			Role:             model.RoleAssistant,
			ReasoningContent: "need a tool",
			ToolCalls: []model.ToolCall{{
				ID:   "call_1",
				Type: "function",
				Function: model.FunctionDefinitionParam{
					Name:      "calculator",
					Arguments: []byte(`{"a":1}`),
				},
			}},
		},
		model.NewToolMessage("call_1", "calculator", "2"),
	})
	raw, err := json.Marshal(items)
	require.NoError(t, err)
	var decoded []map[string]any
	require.NoError(t, json.Unmarshal(raw, &decoded))
	require.Equal(t, "user", decoded[0]["role"])
	require.Equal(t, "reasoning", decoded[1]["type"])
	require.Equal(t, "function_call", decoded[2]["type"])
	require.Equal(t, "function_call_output", decoded[3]["type"])
	require.Equal(t, "need a tool", decoded[1]["summary"].([]any)[0].(map[string]any)["text"])
	require.Equal(t, "call_1", decoded[2]["call_id"])
	require.Equal(t, "call_1", decoded[3]["call_id"])
	require.Equal(t, "2", decoded[3]["output"])
}

func TestGenerateContent_ExtraFieldsAndOfficialEffort(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody = readJSONBody(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"resp_x","object":"response","status":"completed","output":[]}`)
	}))
	defer srv.Close()

	effort := "low"
	m := New("gpt-5",
		WithAPIKey("test"),
		WithBaseURL(srv.URL),
		WithJSONSet("custom_flag", true),
	)
	ch, err := m.GenerateContent(context.Background(), &model.Request{
		Messages: []model.Message{model.NewUserMessage("hi")},
		GenerationConfig: model.GenerationConfig{
			ReasoningEffort: &effort,
		},
		ExtraFields: map[string]any{"session": "abc"},
	})
	require.NoError(t, err)
	_ = collect(t, ch)
	require.Equal(t, true, gotBody["custom_flag"])
	require.Equal(t, "abc", gotBody["session"])
	reasoning, _ := gotBody["reasoning"].(map[string]any)
	require.Equal(t, "low", reasoning["effort"])
}

func TestGenerateContent_ToolChoiceFunction(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody = readJSONBody(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"resp_x","object":"response","status":"completed","output":[]}`)
	}))
	defer srv.Close()

	m := New("gpt-5",
		WithAPIKey("test"),
		WithBaseURL(srv.URL),
		WithExtraFields(map[string]any{
			"tool_choice": map[string]any{"type": "function", "name": "calculator"},
		}),
	)
	ch, err := m.GenerateContent(context.Background(), &model.Request{
		Messages: []model.Message{model.NewUserMessage("hi")},
		Tools:    map[string]tool.Tool{"calculator": stubTool{name: "calculator"}},
	})
	require.NoError(t, err)
	_ = collect(t, ch)
	choice, _ := gotBody["tool_choice"].(map[string]any)
	require.Equal(t, "function", choice["type"])
	require.Equal(t, "calculator", choice["name"])
}

func TestInfo(t *testing.T) {
	m := New("gpt-5")
	require.Equal(t, "gpt-5", m.Info().Name)
}

type stubTool struct{ name string }

func (s stubTool) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: s.name, Description: "stub"}
}

func collect(t *testing.T, ch <-chan *model.Response) []*model.Response {
	t.Helper()
	var out []*model.Response
	for resp := range ch {
		out = append(out, resp)
	}
	return out
}

func readJSONBody(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	defer r.Body.Close()
	var body map[string]any
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
