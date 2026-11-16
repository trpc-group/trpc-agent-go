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

	"github.com/openai/openai-go/v3/option"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestExtraIncludeUnionIsSentAndLeavesCallerDataUnchanged(t *testing.T) {
	const want = `["reasoning.encrypted_content","message.output_text.logprobs","file_search_call.results","future.provider.extension"]`
	modelInclude := []string{
		"message.output_text.logprobs",
		"reasoning.encrypted_content",
		"file_search_call.results",
		"message.output_text.logprobs",
	}
	modelBefore := append([]string(nil), modelInclude...)
	var decoded map[string]any
	require.NoError(t, json.Unmarshal([]byte(`{"include":["file_search_call.results","future.provider.extension","reasoning.encrypted_content"]}`), &decoded))
	requestInclude, ok := decoded["include"].([]any)
	require.True(t, ok)
	requestBefore, err := json.Marshal(requestInclude)
	require.NoError(t, err)
	modelFields := map[string]any{
		"include":          modelInclude,
		"prompt_cache_key": "cache-1",
	}
	requestFields := map[string]any{
		"include":         requestInclude,
		"metadata.source": "request",
	}

	var includes []json.RawMessage
	var sawCacheKey, sawMetadata bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode responses request: %v", err)
			http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
			return
		}
		includes = append(includes, append(json.RawMessage(nil), body["include"]...))
		if string(body["prompt_cache_key"]) == `"cache-1"` {
			sawCacheKey = true
		}
		if string(body["metadata"]) == `{"source":"request"}` {
			sawMetadata = true
		}
		if string(body["stream"]) == "true" {
			writeSSE(w, `{"type":"response.completed","response":{"id":"r","status":"completed","output":[]}}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"r","status":"completed","output":[]}`)
	}))
	t.Cleanup(srv.Close)

	m := New("gpt-5",
		WithAPIKey("test"),
		WithBaseURL(srv.URL),
		WithExtraFields(modelFields),
		WithOpenAIOptions(option.WithMaxRetries(0)),
	)
	request := &model.Request{
		Messages:    []model.Message{model.NewUserMessage("hi")},
		ExtraFields: requestFields,
	}
	for range 2 {
		ch, err := m.GenerateContent(context.Background(), request)
		require.NoError(t, err)
		for _, resp := range collect(t, ch) {
			require.Nil(t, resp.Error)
		}
	}
	streamRequest := &model.Request{
		Messages:         []model.Message{model.NewUserMessage("hi")},
		GenerationConfig: model.GenerationConfig{Stream: true},
		ExtraFields:      requestFields,
	}
	ch, err := m.GenerateContent(context.Background(), streamRequest)
	require.NoError(t, err)
	for _, resp := range collect(t, ch) {
		require.Nil(t, resp.Error)
	}

	require.Len(t, includes, 3)
	for i := range includes {
		require.JSONEq(t, want, string(includes[i]))
	}
	require.True(t, sawCacheKey, "model extra fields other than include must still reach the wire")
	require.True(t, sawMetadata, "request extra fields other than include must still reach the wire")
	require.Equal(t, modelBefore, modelInclude)
	require.Equal(t, modelInclude, modelFields["include"])
	encodedRequest, err := json.Marshal(requestInclude)
	require.NoError(t, err)
	require.JSONEq(t, string(requestBefore), string(encodedRequest))
	require.Equal(t, requestInclude, requestFields["include"])
	require.Equal(t, "cache-1", modelFields["prompt_cache_key"])
	require.Equal(t, "request", requestFields["metadata.source"])
}

func TestExtraIncludeAcceptsStringArraysWithoutChangingDefaults(t *testing.T) {
	type includeName string
	fixed := [1]string{"web_search_call.action.sources"}
	names := []includeName{"code_interpreter_call.outputs", "code_interpreter_call.outputs"}
	namesBefore := append([]includeName(nil), names...)
	anyNames := []any{includeName("message.input_image.image_url")}

	got := captureRequest(t, &model.Request{
		Messages: []model.Message{model.NewUserMessage("hi")},
		ExtraFields: map[string]any{
			"include": names,
		},
	}, WithExtraFields(map[string]any{"include": fixed}))
	requireJSONField(t, `["reasoning.encrypted_content","web_search_call.action.sources","code_interpreter_call.outputs"]`, got.body["include"])
	require.Equal(t, [1]string{"web_search_call.action.sources"}, fixed)
	require.Equal(t, namesBefore, names)

	fromAny := captureRequest(t, &model.Request{
		Messages:    []model.Message{model.NewUserMessage("hi")},
		ExtraFields: map[string]any{"include": anyNames},
	})
	requireJSONField(t, `["reasoning.encrypted_content","message.input_image.image_url"]`, fromAny.body["include"])
	require.Equal(t, []any{includeName("message.input_image.image_url")}, anyNames)

	base := captureRequest(t, &model.Request{Messages: []model.Message{model.NewUserMessage("hi")}})
	requireJSONField(t, `["reasoning.encrypted_content"]`, base.body["include"])
	for _, value := range []any{nil, []string{}, []any{}, []includeName(nil)} {
		got := captureRequest(t, &model.Request{
			Messages:    []model.Message{model.NewUserMessage("hi")},
			ExtraFields: map[string]any{"include": []string{"future.provider.extension"}},
		}, WithExtraFields(map[string]any{"include": value}))
		requireJSONField(t, `["reasoning.encrypted_content","future.provider.extension"]`, got.body["include"])
		empty := captureRequest(t, &model.Request{
			Messages:    []model.Message{model.NewUserMessage("hi")},
			ExtraFields: map[string]any{"include": value},
		})
		require.JSONEq(t, string(mustJSON(t, base.body["include"])), string(mustJSON(t, empty.body["include"])))
	}
}

func TestExtraIncludeRejectsInvalidValuesAndAliasPathsBeforeHTTP(t *testing.T) {
	srv, calls := rejectingServer(t)
	user := []model.Message{model.NewUserMessage("hi")}
	for _, tc := range []struct {
		name   string
		fields map[string]any
		want   string
	}{
		{name: "string", fields: map[string]any{"include": "message.output_text.logprobs"}, want: "must be an array of nonempty strings"},
		{name: "json text", fields: map[string]any{"include": `["future.provider.extension"]`}, want: "must be an array of nonempty strings"},
		{name: "raw json", fields: map[string]any{"include": json.RawMessage(`["future.provider.extension"]`)}, want: "must be an array of nonempty strings"},
		{name: "number", fields: map[string]any{"include": 1}, want: "must be an array of nonempty strings"},
		{name: "object", fields: map[string]any{"include": map[string]any{"name": "file_search_call.results"}}, want: "must be an array of nonempty strings"},
		{name: "bool", fields: map[string]any{"include": true}, want: "must be an array of nonempty strings"},
		{name: "empty name", fields: map[string]any{"include": []string{"future.provider.extension", ""}}, want: "include[1] must be a nonempty string"},
		{name: "mixed array", fields: map[string]any{"include": []any{"future.provider.extension", 2}}, want: "include[1] must be a nonempty string"},
		{name: "nil element", fields: map[string]any{"include": []any{nil}}, want: "include[0] must be a nonempty string"},
	} {
		for _, source := range []string{"model option", "request field"} {
			t.Run(source+"/"+tc.name, func(t *testing.T) {
				opts := []Option{WithAPIKey("test"), WithBaseURL(srv.URL), WithOpenAIOptions(option.WithMaxRetries(0))}
				req := &model.Request{Messages: user}
				if source == "model option" {
					opts = append(opts, WithExtraFields(tc.fields))
				} else {
					req.ExtraFields = tc.fields
				}
				m := New("gpt-5", opts...)
				ch, err := m.GenerateContent(context.Background(), req)
				require.Nil(t, ch)
				require.ErrorContains(t, err, tc.want)
			})
		}
	}
	for _, path := range []string{"include.0", "include[0]", "include ", ":include", `\include`, `i\include`} {
		for _, source := range []string{"model option", "request field"} {
			t.Run(source+"/"+path, func(t *testing.T) {
				opts := []Option{WithAPIKey("test"), WithBaseURL(srv.URL), WithOpenAIOptions(option.WithMaxRetries(0))}
				req := &model.Request{Messages: user}
				fields := map[string]any{path: []string{"future.provider.extension"}}
				if source == "model option" {
					opts = append(opts, WithExtraFields(fields))
				} else {
					req.ExtraFields = fields
				}
				m := New("gpt-5", opts...)
				ch, err := m.GenerateContent(context.Background(), req)
				require.Nil(t, ch)
				require.Error(t, err)
				if path == ":include" || path == `\include` || path == `i\include` {
					require.ErrorContains(t, err, "unsupported sjson path syntax")
				} else {
					require.ErrorContains(t, err, "cannot override managed request state")
				}
			})
		}
	}
	require.Zero(t, calls.Load())
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	require.NoError(t, err)
	return encoded
}
