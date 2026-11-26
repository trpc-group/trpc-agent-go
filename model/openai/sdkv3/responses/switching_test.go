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

func TestGenerateContentSwitchFromChatKeepsOriginalHistory(t *testing.T) {
	var body map[string]json.RawMessage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body = mustReadJSONBody(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"resp_new","status":"completed","output":[]}`)
	}))
	defer srv.Close()

	const native = `[{"type":"reasoning","id":"rs_old","summary":[],"encrypted_content":"old_ciphertext"},{"type":"message","id":"msg_old","status":"completed","role":"assistant","content":[{"type":"output_text","text":"old answer"}]}]`
	old := model.NewAssistantMessage("old answer")
	old.ProviderData = &model.ProviderData{Protocol: responseProvider, Model: "responses-a", Payload: json.RawMessage(native)}
	chat := model.Message{
		Role:               model.RoleAssistant,
		ReasoningContent:   "chat reasoning",
		ReasoningSignature: "chat signature",
		ToolCalls: []model.ToolCall{{ID: "call_chat", Type: "function", Function: model.FunctionDefinitionParam{
			Name: "lookup", Arguments: []byte(`{"key":"value"}`),
		}}},
	}
	messages := []model.Message{model.NewUserMessage("first"), old, chat,
		model.NewToolMessage("call_chat", "lookup", "result"), model.NewUserMessage("continue")}
	adapter := New("responses-b", WithAPIKey("test"), WithBaseURL(srv.URL))
	ch, err := adapter.GenerateContent(context.Background(), &model.Request{Messages: messages})
	require.NoError(t, err)
	result := collect(t, ch)
	require.Len(t, result, 1)
	require.Nil(t, result[0].Error)
	require.Equal(t, "chat reasoning", messages[2].ReasoningContent)
	require.Equal(t, "chat signature", messages[2].ReasoningSignature)
	require.JSONEq(t, native, string(messages[1].ProviderData.Payload))

	var input []json.RawMessage
	require.NoError(t, json.Unmarshal(body["input"], &input))
	require.Len(t, input, 6)
	require.JSONEq(t, `{"type":"reasoning","id":"rs_old","summary":[],"encrypted_content":"old_ciphertext"}`, string(input[1]))
	require.JSONEq(t, `{"type":"message","id":"msg_old","status":"completed","role":"assistant","content":[{"type":"output_text","text":"old answer"}]}`, string(input[2]))
	require.JSONEq(t, `{"type":"function_call","name":"lookup","call_id":"call_chat","arguments":"{\"key\":\"value\"}"}`, string(input[3]))
	require.JSONEq(t, `{"type":"function_call_output","call_id":"call_chat","output":"result"}`, string(input[4]))
}

func TestGenerateContentSwitchFromChatWithoutNativeHistory(t *testing.T) {
	var body map[string]json.RawMessage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body = mustReadJSONBody(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"resp_new","status":"completed","output":[]}`)
	}))
	defer srv.Close()

	chat := model.NewAssistantMessage("visible answer")
	chat.ReasoningContent = "chat reasoning"
	chat.ReasoningSignature = "chat signature"
	messages := []model.Message{model.NewUserMessage("first"), chat, model.NewUserMessage("continue")}
	adapter := New("responses-b", WithAPIKey("test"), WithBaseURL(srv.URL))
	ch, err := adapter.GenerateContent(context.Background(), &model.Request{Messages: messages})
	require.NoError(t, err)
	result := collect(t, ch)
	require.Len(t, result, 1)
	require.Nil(t, result[0].Error)
	require.Equal(t, "chat reasoning", messages[1].ReasoningContent)
	require.Equal(t, "chat signature", messages[1].ReasoningSignature)
	require.NotContains(t, string(body["input"]), "chat reasoning")
	require.NotContains(t, string(body["input"]), "chat signature")
	require.Contains(t, string(body["input"]), "visible answer")
}

func TestSwitchModelReturnsGatewayEncryptedContentError(t *testing.T) {
	var calls atomic.Int32
	var body map[string]json.RawMessage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		body = mustReadJSONBody(t, r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"type":"invalid_request_error","code":"invalid_encrypted_content","message":"unable to decrypt"}}`)
	}))
	defer srv.Close()

	const native = `[{"type":"reasoning","id":"rs_old","summary":[],"encrypted_content":"old_ciphertext"}]`
	old := model.NewAssistantMessage("")
	old.ProviderData = &model.ProviderData{Protocol: responseProvider, Model: "responses-a", Payload: json.RawMessage(native)}
	adapter := New("responses-b", WithAPIKey("test"), WithBaseURL(srv.URL))
	ch, err := adapter.GenerateContent(context.Background(), &model.Request{Messages: []model.Message{old}})
	require.NoError(t, err)
	result := collect(t, ch)
	require.Len(t, result, 1)
	require.NotNil(t, result[0].Error)
	require.Equal(t, "unable to decrypt", result[0].Error.Message)
	require.NotNil(t, result[0].Error.Code)
	require.Equal(t, "invalid_encrypted_content", *result[0].Error.Code)
	require.EqualValues(t, 1, calls.Load())
	require.JSONEq(t, native, string(body["input"]))
}
