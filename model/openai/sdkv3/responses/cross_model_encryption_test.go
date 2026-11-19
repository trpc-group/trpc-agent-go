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

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestCrossModelEncryptedContentAcrossResponsesRequests(t *testing.T) {
	const aReason = `{"type":"reasoning","id":"rs_a","status":"completed","summary":[{"type":"summary_text","text":"A summary"}],"encrypted_content":"cipher-a","future":{"encrypted_content":"nested-cipher-a","number":7}}`
	const aReasonWithoutCipher = `{"type":"reasoning","id":"rs_a","status":"completed","summary":[{"type":"summary_text","text":"A summary"}],"future":{"encrypted_content":"nested-cipher-a","number":7}}`
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
	}{
		{name: "default keeps foreign ciphertext", modelName: "model-b", messages: firstTurn, aCipher: true},
		{name: "explicit false keeps foreign ciphertext", modelName: "model-b", options: []Option{WithOmitCrossModelEncryptedContent(false)}, messages: firstTurn, aCipher: true},
		{name: "switch A to B", modelName: "model-b", options: []Option{WithOmitCrossModelEncryptedContent(true)}, messages: firstTurn},
		{name: "continue on B with mixed history", modelName: "model-b", options: []Option{WithOmitCrossModelEncryptedContent(true)}, messages: fullHistory, bCipher: true, fullHistory: true},
		{name: "switch back to A", modelName: "model-a", options: []Option{WithOmitCrossModelEncryptedContent(true)}, messages: fullHistory, aCipher: true, fullHistory: true},
		{name: "last option disables omission", modelName: "model-b", options: []Option{WithOmitCrossModelEncryptedContent(true), WithOmitCrossModelEncryptedContent(false)}, messages: fullHistory, aCipher: true, bCipher: true, fullHistory: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requests := make(chan map[string]json.RawMessage, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests <- mustReadJSONBody(t, r)
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"id":"resp_new","status":"completed","output":[{"type":"reasoning","id":"rs_new","summary":[],"encrypted_content":"new-cipher"},{"type":"message","id":"msg_new","role":"assistant","status":"completed","content":[{"type":"output_text","text":"ok"}]}]}`)
			}))
			defer server.Close()

			before, err := json.Marshal(tt.messages)
			require.NoError(t, err)
			options := append([]Option{WithAPIKey("test"), WithBaseURL(server.URL)}, tt.options...)
			adapter := New(tt.modelName, options...)
			stream, err := adapter.GenerateContent(context.Background(), &model.Request{Messages: tt.messages})
			require.NoError(t, err)
			results := collect(t, stream)
			require.Len(t, results, 1)
			require.Nil(t, results[0].Error)
			require.NotNil(t, results[0].Choices[0].Message.ProviderData)
			require.Contains(t, string(results[0].Choices[0].Message.ProviderData.Payload), "new-cipher")

			body := <-requests
			require.JSONEq(t, `"`+tt.modelName+`"`, string(body["model"]))
			require.Contains(t, string(body["include"]), "reasoning.encrypted_content")
			var items []json.RawMessage
			require.NoError(t, json.Unmarshal(body["input"], &items))
			expectedA := aReasonWithoutCipher
			if tt.aCipher {
				expectedA = aReason
			}
			require.JSONEq(t, expectedA, string(items[1]))
			require.JSONEq(t, aMessage, string(items[2]))
			require.JSONEq(t, aCall, string(items[3]))
			require.JSONEq(t, `{"type":"function_call_output","call_id":"call_a","output":"result A"}`, string(items[4]))
			if tt.fullHistory {
				require.Len(t, items, 11)
				expectedB, expectedBSecond := bReasonWithoutCipher, bSecondReasonWithoutCipher
				if tt.bCipher {
					expectedB, expectedBSecond = bReason, bSecondReason
				}
				require.JSONEq(t, expectedB, string(items[6]))
				require.JSONEq(t, bMessage, string(items[7]))
				require.JSONEq(t, expectedBSecond, string(items[9]))
			} else {
				require.Len(t, items, 6)
			}
			after, err := json.Marshal(tt.messages)
			require.NoError(t, err)
			require.Equal(t, string(before), string(after), "the session history must remain unchanged")
		})
	}
}
