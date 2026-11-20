//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package gemini

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/genai"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func responsesHistoryMessage(payload, text string, calls ...model.ToolCall) model.Message {
	return model.Message{
		Role: model.RoleAssistant, Content: text, ToolCalls: calls,
		ReasoningContent: "private-summary", ReasoningSignature: "private-signature",
		ProviderData: &model.ProviderData{Protocol: "openai.responses", Model: "old-model", Payload: json.RawMessage(payload)},
	}
}

func historyCall(id, name, args string) model.ToolCall {
	return model.ToolCall{ID: id, Type: "function", Function: model.FunctionDefinitionParam{Name: name, Arguments: []byte(args)}}
}

func TestSupportsResponsesProviderHistory(t *testing.T) {
	m := &Model{}
	require.True(t, m.SupportsProviderHistory("openai.responses"))
	require.False(t, m.SupportsProviderHistory(""))
	require.False(t, m.SupportsProviderHistory("other"))
}

func TestResponsesHistoryWirePreservesParallelCallsAndNumbers(t *testing.T) {
	var wire []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error
		wire, err = io.ReadAll(r.Body)
		require.NoError(t, err)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"ok"}]},"finishReason":"STOP"}]}`))
	}))
	t.Cleanup(server.Close)

	args := `{"number":9007199254740992,"nested":[100000000000000000000]}`
	payload := fmt.Sprintf(`[{"type":"reasoning","id":"rs_1","encrypted_content":"private-encrypted"},{"type":"message","id":"m1","role":"assistant","status":"completed","content":[{"type":"output_text","text":"private-original"}]},{"type":"function_call","call_id":"c1","name":"old","arguments":%q},{"type":"function_call","call_id":"c2","name":"old","arguments":"{}"}]`, args)
	native := responsesHistoryMessage(payload, "visible-edit", historyCall("c1", "lookup", args), historyCall("c2", "lookup", `{}`))
	native.ToolCalls[0].ExtraFields = map[string]any{"private": "private-extra"}
	signed := historyCall("g1", "legacy", `{}`)
	signed.ExtraFields = map[string]any{geminiThoughtSignatureKey: []byte("real-gemini-signature")}
	messages := []model.Message{
		model.NewUserMessage("question"),
		{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{signed}},
		model.NewToolMessage("g1", "legacy", "previous"),
		native,
		model.NewToolMessage("c2", "lookup", `{"answer":9007199254740994}`),
		model.NewToolMessage("c1", "lookup", `{"answer":9007199254740992}`),
		model.NewUserMessage("continue"),
	}
	messages[0].ContentParts = []model.ContentPart{{
		Type: model.ContentTypeImage, Image: &model.Image{Data: []byte("image bytes"), Format: "image/png"},
	}}
	before, err := json.Marshal(messages)
	require.NoError(t, err)
	var callbackRequest []*genai.Content
	m, err := New(context.Background(), "gemini-test", WithGeminiClientConfig(&genai.ClientConfig{
		APIKey: "test-key", Backend: genai.BackendGeminiAPI,
		HTTPOptions: genai.HTTPOptions{BaseURL: server.URL, APIVersion: "v1beta"},
	}), WithChatRequestCallback(func(_ context.Context, contents []*genai.Content) {
		callbackRequest = contents
		contents[0].Parts[1].InlineData.Data[0] = 'I'
		contents[3].Parts[1].FunctionCall.Args["callback_edit"] = "retained"
	}))
	require.NoError(t, err)
	request := &model.Request{Messages: messages}
	stream, err := m.GenerateContent(context.Background(), request)
	require.NoError(t, err)
	for response := range stream {
		require.Nil(t, response.Error)
	}
	require.NotEmpty(t, wire)
	require.Contains(t, string(wire), `"number":9007199254740992`)
	require.Contains(t, string(wire), `"answer":9007199254740994`)
	require.Contains(t, string(wire), `"nested":[100000000000000000000]`)
	require.Contains(t, string(wire), `"callback_edit":"retained"`)
	require.Contains(t, string(wire), `"thoughtSignature":"skip_thought_signature_validator"`)
	for _, secret := range []string{"private-summary", "private-signature", "private-encrypted", "private-original", "private-extra"} {
		require.NotContains(t, string(wire), secret)
	}
	require.Contains(t, string(wire), "visible-edit")
	var body struct {
		Contents []struct {
			Role  string `json:"role"`
			Parts []struct {
				FunctionCall *struct {
					ID   string `json:"id"`
					Name string `json:"name"`
				} `json:"functionCall"`
				FunctionResponse *struct {
					ID       string          `json:"id"`
					Name     string          `json:"name"`
					Response json.RawMessage `json:"response"`
				} `json:"functionResponse"`
				ThoughtSignature string `json:"thoughtSignature"`
			} `json:"parts"`
		} `json:"contents"`
	}
	require.NoError(t, json.Unmarshal(wire, &body))
	require.Len(t, body.Contents, 6)
	require.Equal(t, "model", body.Contents[1].Role)
	require.Equal(t, "cmVhbC1nZW1pbmktc2lnbmF0dXJl", body.Contents[1].Parts[0].ThoughtSignature)
	require.Len(t, body.Contents[4].Parts, 2)
	require.Equal(t, "user", body.Contents[4].Role)
	require.Equal(t, "c1", body.Contents[4].Parts[0].FunctionResponse.ID)
	require.Equal(t, "c2", body.Contents[4].Parts[1].FunctionResponse.ID)
	require.Equal(t, "lookup", body.Contents[4].Parts[0].FunctionResponse.Name)
	require.Contains(t, string(body.Contents[4].Parts[0].FunctionResponse.Response), `9007199254740992`)
	require.Equal(t, json.Number("9007199254740992"), callbackRequest[3].Parts[1].FunctionCall.Args["number"])
	after, err := json.Marshal(request.Messages)
	require.NoError(t, err)
	require.JSONEq(t, string(before), string(after))
}

func TestResponsesHistoryRejectsIncompleteOrUnrepresentableInput(t *testing.T) {
	native := responsesHistoryMessage(`[{"type":"function_call","call_id":"c1","name":"lookup","arguments":"{}"}]`, "", historyCall("c1", "lookup", `{}`))
	other := responsesHistoryMessage(`[]`, "visible")
	wrongProtocol := other
	wrongProtocol.ProviderData = &model.ProviderData{Protocol: "other", Model: "old", Payload: json.RawMessage(`[]`)}
	wrongID := native
	wrongID.ToolCalls = []model.ToolCall{historyCall("wrong", "lookup", `{}`)}
	invalidArgs := native
	invalidArgs.ToolCalls = []model.ToolCall{historyCall("c1", "lookup", `[]`)}
	duplicate := model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{historyCall("c1", "lookup", `{}`), historyCall("c1", "lookup", `{}`)}}
	image := model.NewToolMessage("c1", "lookup", "result")
	image.AddImageURL("https://example.test/image.png", "")
	fileID := model.NewUserMessage("read")
	fileID.AddFileID("file-123")
	ref := model.NewToolMessage("c1", "lookup", "result")
	ref.ContentParts = []model.ContentPart{{ContentRef: &model.ContentRef{ArtifactRef: "artifact://private"}}}
	tests := []struct {
		name     string
		messages []model.Message
		want     string
	}{
		{"protocol", []model.Message{wrongProtocol}, "provider data"},
		{"mismatched call id", []model.Message{wrongID}, "does not match"},
		{"incomplete native", []model.Message{responsesHistoryMessage(`[{"type":"function_call","status":"in_progress","call_id":"c1","name":"lookup","arguments":"{}"}]`, "")}, "in_progress"},
		{"invalid call arguments", []model.Message{invalidArgs}, "arguments"},
		{"missing result", []model.Message{native, model.NewUserMessage("next")}, "missing tool results"},
		{"orphan result", []model.Message{other, model.NewToolMessage("unknown", "lookup", "x")}, "no pending"},
		{"duplicate result", []model.Message{native, model.NewToolMessage("c1", "lookup", "x"), model.NewToolMessage("c1", "lookup", "y")}, "no pending"},
		{"duplicate call", []model.Message{other, duplicate}, "duplicate function call"},
		{"media in tool result", []model.Message{native, image}, "tool result content type"},
		{"unhydrated tool content", []model.Message{native, ref}, "content reference"},
		{"unsupported file id", []model.Message{other, fileID}, "cannot be represented"},
		{"unsupported part", []model.Message{other, {Role: model.RoleUser, ContentParts: []model.ContentPart{{Type: "custom"}}}}, "cannot be represented"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prepared, _, err := (&Model{}).prepareProviderHistory(tt.messages)
			if err == nil {
				_, err = (&Model{}).convertProviderHistory(prepared)
			}
			require.ErrorContains(t, err, tt.want)
		})
	}
}

func TestResponsesHistoryReplaysLegacyNullToolArguments(t *testing.T) {
	m := &Model{}
	messages := []model.Message{
		responsesHistoryMessage(`[]`, "source answer"),
		{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{historyCall("c1", "lookup", `null`)}},
		model.NewToolMessage("c1", "lookup", "legacy result"),
		model.NewUserMessage("continue"),
	}
	prepared, projected, err := m.prepareProviderHistory(messages)
	require.NoError(t, err)
	require.True(t, projected)
	contents, err := m.convertProviderHistory(prepared)
	require.NoError(t, err)
	require.Len(t, contents, 4)
	require.Equal(t, "c1", contents[1].Parts[0].FunctionCall.ID)
	require.Equal(t, map[string]any{}, contents[1].Parts[0].FunctionCall.Args)
	require.Equal(t, "c1", contents[2].Parts[0].FunctionResponse.ID)
	require.Equal(t, "legacy result", contents[2].Parts[0].FunctionResponse.Response["output"])
	require.Equal(t, "null", string(messages[1].ToolCalls[0].Function.Arguments))
}

func TestResponsesHistoryRetainsEmptyToolOutputAndLegacyPath(t *testing.T) {
	call := responsesHistoryMessage(`[{"type":"function_call","call_id":"c1","name":"lookup","arguments":"{}"}]`, "", historyCall("c1", "lookup", `{}`))
	text := " world"
	result := model.NewToolMessage("c1", "lookup", "hello")
	result.ContentParts = []model.ContentPart{{}, {Type: model.ContentTypeText, Text: &text}}
	m := &Model{}
	prepared, projected, err := m.prepareProviderHistory([]model.Message{call, result})
	require.NoError(t, err)
	require.True(t, projected)
	contents, err := m.convertProviderHistory(prepared)
	require.NoError(t, err)
	require.Equal(t, map[string]any{"output": "hello world"}, contents[1].Parts[0].FunctionResponse.Response)

	result.Content = `{}`
	result.ContentParts = nil
	prepared, _, err = m.prepareProviderHistory([]model.Message{call, result})
	require.NoError(t, err)
	contents, err = m.convertProviderHistory(prepared)
	require.NoError(t, err)
	require.Equal(t, map[string]any{"output": "{}"}, contents[1].Parts[0].FunctionResponse.Response)

	legacy := model.NewToolMessage("x", "lookup", "result")
	legacy.AddImageURL("https://example.test/image.png", "")
	unchanged, projected, err := m.prepareProviderHistory([]model.Message{legacy})
	require.NoError(t, err)
	require.False(t, projected)
	require.Equal(t, []model.Message{legacy}, unchanged)
}

func TestResponsesHistoryRejectsLossyNumbersBeforeHTTP(t *testing.T) {
	for _, number := range []string{"9007199254740993", "0.1234567890123456789", "1e400", "1e-9999"} {
		for _, source := range []string{"arguments", "result", "callback"} {
			t.Run(number+"/"+source, func(t *testing.T) {
				call := responsesHistoryMessage(`[{"type":"function_call","call_id":"c1","name":"lookup","arguments":"{}"}]`, "", historyCall("c1", "lookup", `{}`))
				result := model.NewToolMessage("c1", "lookup", "ok")
				m := &Model{}
				switch source {
				case "arguments":
					call.ToolCalls[0].Function.Arguments = []byte(`{"value":` + number + `}`)
				case "result":
					result.Content = `{"value":` + number + `}`
				case "callback":
					m.chatRequestCallback = func(_ context.Context, contents []*genai.Content) {
						contents[0].Parts[0].FunctionCall.Args["value"] = json.Number(number)
					}
				}
				_, err := m.GenerateContent(context.Background(), &model.Request{Messages: []model.Message{call, result}})
				require.ErrorContains(t, err, "cannot be preserved by Gemini SDK")
			})
		}
	}
}

func TestResponsesHistoryRejectsUnsupportedToolContentAfterTailoring(t *testing.T) {
	call := responsesHistoryMessage(`[{"type":"function_call","call_id":"c1","name":"lookup","arguments":"{}"}]`, "", historyCall("c1", "lookup", `{}`))
	tailoredCall := model.Message{Role: model.RoleAssistant, ToolCalls: call.ToolCalls}
	tailoredResult := model.NewToolMessage("c1", "lookup", "result")
	tailoredResult.ContentParts = []model.ContentPart{{Type: model.ContentTypeImage, Image: &model.Image{URL: "https://example.test/image.png"}}}
	m := &Model{
		enableTokenTailoring: true,
		maxInputTokens:       100,
		tailoringStrategy:    overflowTailoringStrategy{tailored: []model.Message{tailoredCall, tailoredResult}},
	}
	_, err := m.GenerateContent(context.Background(), &model.Request{Messages: []model.Message{call, model.NewToolMessage("c1", "lookup", "result")}})
	require.ErrorContains(t, err, "tool result content type")
}
