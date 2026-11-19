//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package openai

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func responsesHistory(payload string) model.Message {
	return model.Message{
		Role:             model.RoleAssistant,
		Content:          "display-only edit",
		ReasoningContent: "summary",
		ProviderData: &model.ProviderData{
			Protocol: "openai.responses",
			Model:    "first",
			Payload:  json.RawMessage(payload),
		},
	}
}

func TestChatModelProviderHistoryCapability(t *testing.T) {
	m := New("chat")
	require.True(t, m.SupportsProviderHistory("openai.responses"))
	require.False(t, m.SupportsProviderHistory("unknown"))
}

func TestPrepareChatMessagesUsesVisibleProjectionAndPreservesSession(t *testing.T) {
	raw := `[
		{"type":"reasoning","id":"rs_1","encrypted_content":"opaque","summary":[{"type":"summary_text","text":"private"}]},
		{"type":"message","id":"msg_1","status":"completed","role":"assistant","content":[{"type":"output_text","text":"native answer","annotations":[]}]},
		{"type":"function_call","call_id":"call_1","name":"lookup","arguments":"{\"key\":1}"}
	]`
	additionalText := " world"
	messages := []model.Message{
		model.NewUserMessage("question"),
		func() model.Message {
			message := responsesHistory(raw)
			message.ToolCalls = []model.ToolCall{{Type: "function", ID: "call_1", Function: model.FunctionDefinitionParam{Name: "lookup", Arguments: []byte(`{"secret":"redacted"}`)}, ExtraFields: map[string]any{"signature": "private"}}}
			return message
		}(),
		{Role: model.RoleTool, ToolID: "call_1", Content: "hello", ContentParts: []model.ContentPart{{Type: model.ContentTypeText, Text: &additionalText}}},
		model.NewUserMessage("next"),
	}
	before, err := json.Marshal(messages)
	require.NoError(t, err)
	got, _, err := New("chat").prepareProviderHistory(messages)
	require.NoError(t, err)
	require.Len(t, got, 4)
	require.Equal(t, "display-only edit", got[1].Content)
	require.Empty(t, got[1].ReasoningContent)
	require.Nil(t, got[1].ProviderData)
	require.Len(t, got[1].ToolCalls, 1)
	require.Equal(t, "call_1", got[1].ToolCalls[0].ID)
	require.JSONEq(t, `{"secret":"redacted"}`, string(got[1].ToolCalls[0].Function.Arguments))
	require.Nil(t, got[1].ToolCalls[0].ExtraFields)
	require.Equal(t, "hello world", got[2].Content)
	require.Empty(t, got[2].ContentParts)
	after, err := json.Marshal(messages)
	require.NoError(t, err)
	require.JSONEq(t, string(before), string(after))
}

func TestPrepareChatMessagesRejectsIncompleteOrLossyHistory(t *testing.T) {
	call := responsesHistory(`[{"type":"function_call","call_id":"call_1","name":"lookup","arguments":"{}"}]`)
	call.ToolCalls = []model.ToolCall{{Type: "function", ID: "call_1", Function: model.FunctionDefinitionParam{Name: "lookup", Arguments: []byte(`{}`)}}}
	imageResult := model.NewToolMessage("call_1", "lookup", "result")
	imageResult.AddImageURL("https://example.test/image.png", "")
	tests := []struct {
		name     string
		messages []model.Message
		want     string
	}{
		{"missing result", []model.Message{call, model.NewUserMessage("next")}, "missing tool results"},
		{"orphan result", []model.Message{responsesHistory(`[]`), model.NewToolMessage("orphan", "lookup", "x")}, "no pending function call"},
		{"duplicate result", []model.Message{call, model.NewToolMessage("call_1", "lookup", "x"), model.NewToolMessage("call_1", "lookup", "y")}, "no pending function call"},
		{"image result", []model.Message{call, imageResult}, `content type "image"`},
		{"unfinished native item", []model.Message{responsesHistory(`[{"type":"function_call","status":"in_progress","call_id":"call_1","name":"lookup","arguments":"{}"}]`)}, "in_progress"},
		{"invalid arguments", []model.Message{responsesHistory(`[{"type":"function_call","call_id":"call_1","name":"lookup","arguments":"[]"}]`)}, "invalid JSON"},
		{"unsupported item", []model.Message{responsesHistory(`[{"type":"web_search_call","id":"ws_1"}]`)}, "unsupported type"},
		{"annotations", []model.Message{responsesHistory(`[{"type":"message","id":"m1","role":"assistant","status":"completed","content":[{"type":"output_text","text":"hello","annotations":[{"type":"url_citation"}]}]}]`)}, "annotations"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := New("chat").prepareProviderHistory(tt.messages)
			require.ErrorContains(t, err, tt.want)
		})
	}
}

func TestPrepareChatMessagesDoesNotModifyLegacyHistory(t *testing.T) {
	imageResult := model.NewToolMessage("call_1", "lookup", "result")
	imageResult.AddImageURL("https://example.test/image.png", "")
	got, _, err := New("chat").prepareProviderHistory([]model.Message{imageResult})
	require.NoError(t, err)
	require.Equal(t, imageResult, got[0])
}

func TestPrepareChatMessagesValidatesNativeHistory(t *testing.T) {
	callPayload := `[{"type":"function_call","call_id":"call_1","name":"lookup","arguments":"{}"}]`
	nativeCall := responsesHistory(callPayload)
	nativeCall.ToolCalls = []model.ToolCall{{ID: "call_1", Type: "function", Function: model.FunctionDefinitionParam{Name: "lookup", Arguments: []byte(`{}`)}}}
	wrongProtocol := responsesHistory(`[]`)
	wrongProtocol.ProviderData.Protocol = "other"
	wrongRole := responsesHistory(`[]`)
	wrongRole.Role = model.RoleUser
	wrongCallID := nativeCall
	wrongCallID.ToolCalls = append([]model.ToolCall(nil), nativeCall.ToolCalls...)
	wrongCallID.ToolCalls[0].ID = "different"
	invalidToolResult := model.NewToolMessage("call_1", "lookup", "")
	invalidToolResult.ContentParts = []model.ContentPart{{ContentRef: &model.ContentRef{ArtifactRef: "artifact://result"}}}
	assistantImage := model.Message{Role: model.RoleAssistant, ContentParts: []model.ContentPart{{Type: model.ContentTypeImage, Image: &model.Image{URL: "https://example.test/image.png"}}}}
	unknownPart := model.Message{Role: model.RoleUser, ContentParts: []model.ContentPart{{Type: "custom"}}}
	customRole := model.Message{Role: "custom", ContentParts: []model.ContentPart{{Type: model.ContentTypeImage, Image: &model.Image{URL: "https://example.test/image.png"}}}}
	legacyCalls := model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{ID: "same"}, {ID: "same"}}}
	legacyEmptyID := model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{ID: " "}}}
	tests := []struct {
		name     string
		messages []model.Message
		want     string
	}{
		{"unsupported protocol", []model.Message{wrongProtocol}, "unsupported provider data protocol"},
		{"native record on user", []model.Message{wrongRole}, "assistant message"},
		{"object instead of native items", []model.Message{responsesHistory(`{}`)}, "item array"},
		{"reasoning without id", []model.Message{responsesHistory(`[{"type":"reasoning"}]`)}, "empty id"},
		{"call without name", []model.Message{responsesHistory(`[{"type":"function_call","call_id":"call_1","arguments":"{}"}]`)}, "empty call_id or name"},
		{"incomplete assistant message", []model.Message{responsesHistory(`[{"type":"message","id":"msg_1","role":"assistant"}]`)}, "not a completed assistant message"},
		{"assistant message after call", []model.Message{responsesHistory(`[{"type":"function_call","call_id":"call_1","name":"lookup","arguments":"{}"},{"type":"message","id":"msg_1","status":"completed","role":"assistant"}]`)}, "follows a function call"},
		{"unknown assistant content", []model.Message{responsesHistory(`[{"type":"message","id":"msg_1","status":"completed","role":"assistant","content":[{"type":"unknown"}]}]`)}, "unsupported content type"},
		{"missing call projection", []model.Message{responsesHistory(callPayload)}, "do not match the message projection"},
		{"mismatched call ID", []model.Message{wrongCallID}, "does not match the message projection"},
		{"unhydrated tool content", []model.Message{nativeCall, invalidToolResult}, "tool result content reference"},
		{"assistant image", []model.Message{nativeAnswer(), assistantImage}, `content type "image"`},
		{"unknown content part", []model.Message{nativeAnswer(), unknownPart}, `content type "custom"`},
		{"custom role content", []model.Message{nativeAnswer(), customRole}, `role "custom"`},
		{"missing tool result at end", []model.Message{nativeCall}, "missing tool results"},
		{"duplicate call IDs", []model.Message{nativeAnswer(), legacyCalls}, "duplicate function call"},
		{"empty call ID", []model.Message{nativeAnswer(), legacyEmptyID}, "empty call_id"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := New("chat").prepareProviderHistory(tt.messages)
			require.ErrorContains(t, err, tt.want)
		})
	}
}

func TestPrepareChatMessagesUserMedia(t *testing.T) {
	text := "not a file"
	tests := []struct {
		name     string
		model    *Model
		part     model.ContentPart
		wantErr  bool
		wantWire string
	}{
		{"file bytes", New("chat"), model.ContentPart{Type: model.ContentTypeFile, File: &model.File{Name: "report.txt", MimeType: "text/plain", Data: []byte("report")}}, false, "cmVwb3J0"},
		{"malformed file", New("chat"), model.ContentPart{Type: model.ContentTypeFile, Text: &text}, true, ""},
		{"image URL", New("chat"), model.ContentPart{Type: model.ContentTypeImage, Image: &model.Image{URL: "https://example.test/image.png"}}, false, "https://example.test/image.png"},
		{"audio bytes", New("chat"), model.ContentPart{Type: model.ContentTypeAudio, Audio: &model.Audio{Format: "wav", Data: []byte("audio")}}, false, "YXVkaW8="},
		{"audio URL", New("chat"), model.ContentPart{Type: model.ContentTypeAudio, Audio: &model.Audio{URL: "https://example.test/audio.wav"}}, true, ""},
		{"text-only model with image", New("chat", WithTextOnlyMessageContent(true)), model.ContentPart{Type: model.ContentTypeImage, Image: &model.Image{URL: "https://example.test/image.png"}}, true, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			user := model.Message{Role: model.RoleUser, Content: "describe", ContentParts: []model.ContentPart{tt.part}}
			messages, _, err := tt.model.prepareProviderHistory([]model.Message{nativeAnswer(), user})
			if tt.wantErr {
				require.ErrorContains(t, err, "cannot be represented")
				return
			}
			require.NoError(t, err)
			wire, err := json.Marshal(tt.model.convertMessages(messages))
			require.NoError(t, err)
			require.Contains(t, string(wire), "describe")
			require.Contains(t, string(wire), tt.wantWire)
		})
	}
}
