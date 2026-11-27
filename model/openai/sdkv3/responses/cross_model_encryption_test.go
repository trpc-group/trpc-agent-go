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
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestCrossModelEncryptedContentAcrossResponsesRequests(t *testing.T) {
	const aReason = `{"type":"reasoning","id":"rs_a","status":"completed","summary":[{"type":"summary_text","text":"A summary"}],"encrypted_content":"cipher-a","future":{"encrypted_content":"nested-cipher-a","number":9007199254740993}}`
	const aReasonWithoutCipher = `{"type":"reasoning","id":"rs_a","status":"completed","summary":[{"type":"summary_text","text":"A summary"}],"future":{"encrypted_content":"nested-cipher-a","number":9007199254740993}}`
	const aMessage = `{"type":"message","id":"msg_a","status":"completed","role":"assistant","content":[{"type":"output_text","text":"answer A"}],"future":{"encrypted_content":"message-value"}}`
	const aCall = `{"type":"function_call","id":"fc_a","status":"completed","call_id":"call_a","name":"lookup","arguments":"{\"key\":\"A\"}","future":"call-value"}`
	const bReason = `{"type":"reasoning","id":"rs_b","summary":[{"type":"summary_text","text":"B summary"}],"encrypted_content":"cipher-b","future":[1,"b"]}`
	const bReasonWithoutCipher = `{"type":"reasoning","id":"rs_b","summary":[{"type":"summary_text","text":"B summary"}],"future":[1,"b"]}`
	const bMessage = `{"type":"message","id":"msg_b","status":"completed","role":"assistant","content":[{"type":"output_text","text":"answer B"}]}`
	const bSecondReason = `{"type":"reasoning","id":"rs_b2","summary":[],"encrypted_content":"cipher-b2"}`
	const bSecondReasonWithoutCipher = `{"type":"reasoning","id":"rs_b2","summary":[]}`

	native := func(name, raw string) model.Message {
		message := model.NewAssistantMessage("")
		message.ProviderData = &model.ProviderData{
			Protocol: responseProvider,
			Model:    name,
			Payload:  json.RawMessage(raw),
		}
		return message
	}
	firstTurn := []model.Message{
		model.NewUserMessage("first"),
		native("model-a", `[`+aReason+`,`+aMessage+`,`+aCall+`]`),
		model.NewToolMessage("call_a", "lookup", "result A"),
		model.NewUserMessage("continue"),
	}
	fullHistory := append(append([]model.Message(nil), firstTurn...),
		native("model-b", `[`+bReason+`,`+bMessage+`]`),
		model.NewUserMessage("continue again"),
		native("model-b", `[`+bSecondReason+`]`),
		model.NewUserMessage("return"),
	)

	tests := []struct {
		name        string
		modelName   string
		options     []Option
		messages    []model.Message
		aCipher     bool
		bCipher     bool
		fullHistory bool
		stream      bool
	}{
		{name: "default keeps foreign ciphertext", modelName: "model-b", messages: firstTurn, aCipher: true},
		{name: "explicit replay keeps foreign ciphertext", modelName: "model-b", options: []Option{WithCrossModelReasoningPolicy(CrossModelReasoningPolicyReplay)}, messages: firstTurn, aCipher: true},
		{name: "switch A to B", modelName: "model-b", options: []Option{WithCrossModelReasoningPolicy(CrossModelReasoningPolicyOmitEncryptedContent)}, messages: firstTurn},
		{name: "stream switch A to B", modelName: "model-b", options: []Option{WithCrossModelReasoningPolicy(CrossModelReasoningPolicyOmitEncryptedContent)}, messages: firstTurn, stream: true},
		{name: "continue on B with mixed history", modelName: "model-b", options: []Option{WithCrossModelReasoningPolicy(CrossModelReasoningPolicyOmitEncryptedContent)}, messages: fullHistory, bCipher: true, fullHistory: true},
		{name: "switch back to A", modelName: "model-a", options: []Option{WithCrossModelReasoningPolicy(CrossModelReasoningPolicyOmitEncryptedContent)}, messages: fullHistory, aCipher: true, fullHistory: true},
		{name: "last option disables omission", modelName: "model-b", options: []Option{WithCrossModelReasoningPolicy(CrossModelReasoningPolicyOmitEncryptedContent), WithCrossModelReasoningPolicy(CrossModelReasoningPolicyReplay)}, messages: fullHistory, aCipher: true, bCipher: true, fullHistory: true},
		{name: "last option enables omission", modelName: "model-b", options: []Option{WithCrossModelReasoningPolicy(CrossModelReasoningPolicyReplay), WithCrossModelReasoningPolicy(CrossModelReasoningPolicyOmitEncryptedContent)}, messages: firstTurn},
		{name: "last option replaces invalid policy", modelName: "model-b", options: []Option{WithCrossModelReasoningPolicy(CrossModelReasoningPolicy(-1)), WithCrossModelReasoningPolicy(CrossModelReasoningPolicyReplay)}, messages: firstTurn, aCipher: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requests := make(chan map[string]json.RawMessage, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests <- mustReadJSONBody(t, r)
				if tt.stream {
					writeSSE(w,
						`{"type":"response.output_text.delta","delta":"ok"}`,
						`{"type":"response.output_item.done","output_index":0,"item":{"type":"reasoning","id":"rs_new","summary":[],"encrypted_content":"new-cipher"}}`,
						`{"type":"response.output_item.done","output_index":1,"item":{"type":"message","id":"msg_new","role":"assistant","status":"completed","content":[{"type":"output_text","text":"ok"}]}}`,
						`{"type":"response.completed","response":{"id":"resp_new","status":"completed","output":[{"type":"reasoning","id":"rs_new","summary":[],"encrypted_content":"new-cipher"},{"type":"message","id":"msg_new","role":"assistant","status":"completed","content":[{"type":"output_text","text":"ok"}]}]}}`,
					)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"id":"resp_new","status":"completed","output":[{"type":"reasoning","id":"rs_new","summary":[],"encrypted_content":"new-cipher"},{"type":"message","id":"msg_new","role":"assistant","status":"completed","content":[{"type":"output_text","text":"ok"}]}]}`)
			}))
			defer server.Close()

			before, err := json.Marshal(tt.messages)
			require.NoError(t, err)
			options := append([]Option{WithAPIKey("test"), WithBaseURL(server.URL)}, tt.options...)
			adapter := New(tt.modelName, options...)
			stream, err := adapter.GenerateContent(context.Background(), &model.Request{
				Messages:         tt.messages,
				GenerationConfig: model.GenerationConfig{Stream: tt.stream},
			})
			require.NoError(t, err)
			results := collect(t, stream)
			if tt.stream {
				require.Len(t, results, 2)
				require.True(t, results[0].IsPartial)
				require.Equal(t, "ok", results[0].Choices[0].Delta.Content)
			} else {
				require.Len(t, results, 1)
			}
			final := results[len(results)-1]
			require.Nil(t, final.Error)
			require.True(t, final.Done)
			require.Equal(t, "ok", final.Choices[0].Message.Content)
			require.NotNil(t, final.Choices[0].Message.ProviderData)
			require.Contains(t, string(final.Choices[0].Message.ProviderData.Payload), "new-cipher")

			body := <-requests
			require.JSONEq(t, `"`+tt.modelName+`"`, string(body["model"]))
			require.JSONEq(t, `["reasoning.encrypted_content"]`, string(body["include"]))
			require.Equal(t, "false", string(body["store"]))
			if tt.stream {
				require.Equal(t, "true", string(body["stream"]))
			}
			var items []json.RawMessage
			require.NoError(t, json.Unmarshal(body["input"], &items))
			if tt.fullHistory {
				require.Len(t, items, 11)
			} else {
				require.Len(t, items, 6)
			}
			require.JSONEq(t, `{"role":"user","content":"first"}`, string(items[0]))
			expectedA := aReasonWithoutCipher
			if tt.aCipher {
				expectedA = aReason
			}
			require.JSONEq(t, expectedA, string(items[1]))
			var reasoning struct {
				Future struct {
					Number json.RawMessage `json:"number"`
				} `json:"future"`
			}
			require.NoError(t, json.Unmarshal(items[1], &reasoning))
			require.Equal(t, "9007199254740993", string(reasoning.Future.Number), "unknown integers must retain their exact value on the wire")
			require.JSONEq(t, aMessage, string(items[2]))
			require.JSONEq(t, aCall, string(items[3]))
			require.JSONEq(t, `{"type":"function_call_output","call_id":"call_a","output":"result A"}`, string(items[4]))
			require.JSONEq(t, `{"role":"user","content":"continue"}`, string(items[5]))
			if tt.fullHistory {
				expectedB, expectedBSecond := bReasonWithoutCipher, bSecondReasonWithoutCipher
				if tt.bCipher {
					expectedB, expectedBSecond = bReason, bSecondReason
				}
				require.JSONEq(t, expectedB, string(items[6]))
				require.JSONEq(t, bMessage, string(items[7]))
				require.JSONEq(t, `{"role":"user","content":"continue again"}`, string(items[8]))
				require.JSONEq(t, expectedBSecond, string(items[9]))
				require.JSONEq(t, `{"role":"user","content":"return"}`, string(items[10]))
			}
			after, err := json.Marshal(tt.messages)
			require.NoError(t, err)
			require.Equal(t, string(before), string(after), "the session history must remain unchanged")
		})
	}
}

func TestInvalidCrossModelReasoningPolicyFailsBeforeHTTP(t *testing.T) {
	tests := []struct {
		name   string
		policy CrossModelReasoningPolicy
		stream bool
		error  string
	}{
		{name: "negative", policy: -1, error: "openai/responses: invalid cross-model reasoning policy -1"},
		{name: "unsupported", policy: 2, error: "openai/responses: invalid cross-model reasoning policy 2"},
		{name: "stream", policy: 2, stream: true, error: "openai/responses: invalid cross-model reasoning policy 2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.WriteHeader(http.StatusInternalServerError)
			}))
			defer server.Close()

			adapter := New("model-b", WithAPIKey("test"), WithBaseURL(server.URL), WithCrossModelReasoningPolicy(tt.policy))
			stream, err := adapter.GenerateContent(context.Background(), &model.Request{
				Messages:         []model.Message{model.NewUserMessage("hello")},
				GenerationConfig: model.GenerationConfig{Stream: tt.stream},
			})
			require.Nil(t, stream)
			require.EqualError(t, err, tt.error)
			require.Zero(t, calls.Load(), "an invalid policy must not reach HTTP")
		})
	}
}
