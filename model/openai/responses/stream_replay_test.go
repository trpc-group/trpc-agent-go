//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package responses

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/openai/openai-go/v3/option"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestStreamReplayUsesDoneReasoningCiphertext(t *testing.T) {
	const doneReasoning = `{"type":"reasoning","id":"rs_done","summary":[],"encrypted_content":"opaque-item-done","future_extension":{"keep":true}}`
	const terminalReasoning = `{"type":"reasoning","id":"rs_done","summary":[],"encrypted_content":"opaque-terminal-snapshot","future_extension":{"keep":true}}`
	const message = `{"type":"message","id":"msg_done","status":"completed","role":"assistant","content":[{"type":"output_text","text":"ready"}]}`
	requests := make(chan map[string]json.RawMessage, 2)
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- mustReadJSONBody(t, r)
		calls++
		if calls == 1 {
			writeSSE(w,
				`{"type":"response.output_item.added","output_index":0,"item":{"type":"reasoning","id":"rs_done","summary":[],"encrypted_content":"incomplete-added-state"}}`,
				`{"type":"response.output_item.done","output_index":0,"item":`+doneReasoning+`}`,
				`{"type":"response.output_item.done","output_index":1,"item":`+message+`}`,
				`{"type":"response.completed","response":{"id":"r","status":"completed","output":[`+terminalReasoning+`,`+message+`]}}`,
			)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"next","status":"completed","output":[]}`)
	}))
	defer srv.Close()
	m := New("test-model", WithAPIKey("offline"), WithBaseURL(srv.URL), WithOpenAIOptions(option.WithMaxRetries(0)))
	ch, err := m.GenerateContent(context.Background(), &model.Request{Messages: []model.Message{model.NewUserMessage("start")}, GenerationConfig: model.GenerationConfig{Stream: true}})
	require.NoError(t, err)
	got := collect(t, ch)
	require.Len(t, got, 1)
	require.Nil(t, got[0].Error)
	require.True(t, got[0].Done)
	encoded, err := json.Marshal(got[0].Choices[0].Message)
	require.NoError(t, err)
	var restored model.Message
	require.NoError(t, json.Unmarshal(encoded, &restored))
	payload, err := restored.ReplayDataFor(m.Info().ReplayProtocol, m.Info().Name)
	require.NoError(t, err)
	require.JSONEq(t, `[`+doneReasoning+`,`+message+`]`, string(payload))
	ch, err = m.GenerateContent(context.Background(), &model.Request{Messages: []model.Message{model.NewUserMessage("start"), restored, model.NewUserMessage("continue")}})
	require.NoError(t, err)
	for _, response := range collect(t, ch) {
		require.Nil(t, response.Error)
	}
	<-requests
	var input []json.RawMessage
	require.NoError(t, json.Unmarshal((<-requests)["input"], &input))
	require.Len(t, input, 4)
	require.JSONEq(t, doneReasoning, string(input[1]), "the full done reasoning item is replayed without editing or re-encrypting it")
}

func TestStreamReasoningStillRejectsSemanticChanges(t *testing.T) {
	for _, tc := range []struct{ name, done, final string }{
		{"id", `{"type":"reasoning","id":"a","summary":[],"encrypted_content":"one"}`, `{"type":"reasoning","id":"b","summary":[],"encrypted_content":"two"}`},
		{"summary", `{"type":"reasoning","id":"a","summary":[],"encrypted_content":"one"}`, `{"type":"reasoning","id":"a","summary":[{"type":"summary_text","text":"changed"}],"encrypted_content":"two"}`},
		{"unknown field", `{"type":"reasoning","id":"a","summary":[],"encrypted_content":"one","future":1}`, `{"type":"reasoning","id":"a","summary":[],"encrypted_content":"two","future":2}`},
		{"missing done state", `{"type":"reasoning","id":"a","summary":[]}`, `{"type":"reasoning","id":"a","summary":[],"encrypted_content":"two"}`},
		{"tool arguments", `{"type":"function_call","id":"f","call_id":"c","name":"calculator","arguments":"{\"a\":1}"}`, `{"type":"function_call","id":"f","call_id":"c","name":"calculator","arguments":"{\"a\":2}"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				writeSSE(w, fmt.Sprintf(`{"type":"response.output_item.done","output_index":0,"item":%s}`, tc.done), fmt.Sprintf(`{"type":"response.completed","response":{"id":"r","status":"completed","output":[%s]}}`, tc.final))
			}))
			defer srv.Close()
			m := New("test-model", WithAPIKey("offline"), WithBaseURL(srv.URL), WithOpenAIOptions(option.WithMaxRetries(0)))
			ch, err := m.GenerateContent(context.Background(), &model.Request{Messages: []model.Message{model.NewUserMessage("start")}, GenerationConfig: model.GenerationConfig{Stream: true}})
			require.NoError(t, err)
			respErr := requireTerminalError(t, collect(t, ch))
			require.Contains(t, respErr.Message, "differs from response.completed")
		})
	}
}
