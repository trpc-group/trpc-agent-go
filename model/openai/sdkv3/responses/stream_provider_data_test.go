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
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/openai/openai-go/v3/option"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestStreamProviderDataUsesDoneReasoningCiphertext(t *testing.T) {
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
	require.NotNil(t, restored.ProviderData)
	payload := restored.ProviderData.Payload
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

func TestStreamUsesCompletedOutput(t *testing.T) {
	const message = `{"type":"message","id":"final","role":"assistant","status":"completed","content":[{"type":"output_text","text":"answer"}]}`
	const preview = `{"type":"message","id":"preview","role":"assistant","status":"completed","content":[]}`
	const reason = `{"type":"reasoning","id":"rs","summary":[]}`
	const other = `{"type":"reasoning","id":"other","summary":[],"encrypted_content":"other-state"}`
	const first = `{"type":"reasoning","id":"rs","summary":[],"encrypted_content":"state-1"}`
	const second = `{"type":"reasoning","id":"rs","summary":[],"encrypted_content":"state-2"}`
	const changed = `{"type":"reasoning","id":"rs","summary":[{"type":"summary_text","text":"done summary"}],"encrypted_content":"state","extension":{"v":1}}`
	const largeDone = `{"type":"reasoning","id":"rs","summary":[],"extension":9007199254740993,"encrypted_content":"first"}`
	const largeFinal = `{"type":"reasoning","id":"rs","summary":[],"extension":9007199254740992,"encrypted_content":"final"}`
	const pending = `{"type":"reasoning","id":"rs","status":"in_progress","summary":[],"encrypted_content":"unfinished"}`
	const call = `{"type":"function_call","call_id":"call","name":"calculator","arguments":"{}"}`
	done := func(index int, item string) string {
		return fmt.Sprintf(`{"type":"response.output_item.done","output_index":%d,"item":%s}`, index, item)
	}
	completed := func(output string) string {
		return `{"type":"response.completed","response":{"id":"r","status":"completed","output":[` + output + `]}}`
	}
	for _, tc := range []struct {
		name                 string
		events               []string
		final, want, summary string
	}{
		{"duplicate index and changed reasoning ID", []string{done(0, other), done(0, preview)}, reason + "," + message, reason + "," + message, ""},
		{"missing done item and changed message ID", []string{done(0, preview)}, reason + "," + message, reason + "," + message, ""},
		{"done-only tool call", []string{done(-1, call)}, message, message, ""},
		{"unique reasoning ID across slots", []string{done(1, changed)}, reason + "," + message, changed + "," + message, "done summary"},
		{"repeated ID in distinct slots", []string{done(1, second), done(0, first)}, reason + "," + reason + "," + message, first + "," + second + "," + message, ""},
		{"same ID and slot is ambiguous", []string{done(0, first), done(0, second)}, reason + "," + message, reason + "," + message, ""},
		{"slot matches but summary differs", []string{done(0, changed), done(1, second)}, reason + "," + reason + "," + message, reason + "," + second + "," + message, ""},
		{"distinct large extension values", []string{done(0, largeDone), done(1, second)}, largeFinal + "," + reason + "," + message, largeFinal + "," + second + "," + message, ""},
		{"missing index cannot disambiguate", []string{`{"type":"response.output_item.done","item":` + first + `}`}, reason + "," + reason + "," + message, reason + "," + reason + "," + message, ""},
		{"invalid done reasoning", []string{done(0, pending)}, reason + "," + message, reason + "," + message, ""},
		{"done reasoning lacks ciphertext", []string{done(0, reason)}, first + "," + message, first + "," + message, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := collectStream(t, append(tc.events, completed(tc.final))...)
			require.Len(t, got, 1)
			require.Nil(t, got[0].Error)
			require.True(t, got[0].Done)
			msg := got[0].Choices[0].Message
			require.Equal(t, "answer", msg.Content)
			require.Equal(t, tc.summary, msg.ReasoningContent)
			require.Empty(t, msg.ToolCalls)
			require.NotNil(t, msg.ProviderData)
			require.JSONEq(t, "["+tc.want+"]", string(msg.ProviderData.Payload))
		})
	}
	t.Run("invalid terminal reasoning cannot be repaired", func(t *testing.T) {
		got := collectStream(t, done(0, first), completed(pending))
		require.Contains(t, requireTerminalError(t, got).Message, "in_progress")
	})
}
