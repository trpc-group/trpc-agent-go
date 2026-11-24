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
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
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

func TestPrepareChatMessagesProjectsURLCitations(t *testing.T) {
	const raw = `[
		{"type":"message","id":"m1","status":"completed","role":"assistant","content":[
			{"type":"output_text","text":"See the ","annotations":[{"type":"url_citation","url":"https://example.com/a","title":"First","start_index":0,"end_index":3}]},
			{"type":"output_text","text":"sources.","annotations":[{"type":"url_citation","url":"https://example.com/a","title":"Duplicate","start_index":8,"end_index":15},{"type":"url_citation","url":"https://example.com/b?q=1","title":"Second","start_index":8,"end_index":15}]}
		]}
	]`
	secondPart := "sources."
	changedPart := "[redacted]"
	tests := []struct {
		name    string
		content string
		parts   []model.ContentPart
		want    string
	}{
		{"unchanged", "See the sources.", nil, "See the sources.\n\nSources:\nhttps://example.com/a\nhttps://example.com/b?q=1"},
		{"unchanged split", "See the ", []model.ContentPart{{Type: model.ContentTypeText, Text: &secondPart}}, "See the sources.\n\nSources:\nhttps://example.com/a\nhttps://example.com/b?q=1"},
		{"edited", "[redacted]", nil, "[redacted]"},
		{"edited part", "See the ", []model.ContentPart{{Type: model.ContentTypeText, Text: &changedPart}}, "See the [redacted]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			message := responsesHistory(raw)
			message.Content = tt.content
			message.ContentParts = tt.parts
			before, err := json.Marshal(message)
			require.NoError(t, err)
			messages, _, err := New("chat").prepareProviderHistory([]model.Message{message})
			require.NoError(t, err)
			require.Len(t, messages, 1)
			visible, ok := chatVisibleText(messages[0])
			require.True(t, ok)
			require.Equal(t, tt.want, visible)
			after, err := json.Marshal(message)
			require.NoError(t, err)
			require.JSONEq(t, string(before), string(after))
		})
	}
}

func TestPrepareChatMessagesIgnoresUnusedCitationFields(t *testing.T) {
	const raw = `[{"type":"message","id":"m1","status":"completed","role":"assistant","content":[
		{"type":"output_text","text":"hello","annotations":[
			{"type":"url_citation","url":"https://example.com/a"},
			{"type":"url_citation","url":"https://example.com/b","start_index":2,"end_index":2},
			{"type":"url_citation","url":"https://example.com/c","title":null,"start_index":-10,"end_index":9999}
		]}
	]}]`
	message := responsesHistory(raw)
	message.Content = "hello"
	got, _, err := New("chat").prepareProviderHistory([]model.Message{message})
	require.NoError(t, err)
	require.Equal(t, "hello\n\nSources:\nhttps://example.com/a\nhttps://example.com/b\nhttps://example.com/c", got[0].Content)
}

func TestPrepareChatMessagesDeduplicatesCitationURLsAgainstBody(t *testing.T) {
	const source = "https://example.com/source"
	tests := []struct {
		name       string
		body       string
		wantSource bool
	}{
		{"whole URL", "See " + source + " for details", false},
		{"angle brackets", "See <" + source + ">", false},
		{"parentheses", "See (" + source + ")", false},
		{"Markdown link", "See [source](" + source + ")", false},
		{"longer path", "See " + source + "/details", true},
		{"longer name", "See " + source + "-other", true},
		{"longer query", "See " + source + "?format=full", true},
		{"embedded in another URL", "See https://example.com/redirect/" + source, true},
		{"parentheses inside another URL", "See https://example.com/redirect/(" + source + ")", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload, err := json.Marshal([]any{map[string]any{
				"type": "message", "id": "m1", "status": "completed", "role": "assistant",
				"content": []any{map[string]any{"type": "output_text", "text": tt.body,
					"annotations": []any{map[string]any{"type": "url_citation", "url": source}}}},
			}})
			require.NoError(t, err)
			message := responsesHistory(string(payload))
			message.Content = tt.body
			got, _, err := New("chat").prepareProviderHistory([]model.Message{message})
			require.NoError(t, err)
			want := tt.body
			if tt.wantSource {
				want += "\n\nSources:\n" + source
			}
			require.Equal(t, want, got[0].Content)
		})
	}
}

func TestPrepareChatMessagesRejectsInvalidCitations(t *testing.T) {
	tests := []struct {
		name       string
		annotation string
	}{
		{"unknown", `[{"type":"file_citation","file_id":"file"}]`},
		{"not array", `{}`},
		{"null element", `[null]`},
		{"missing URL", `[{"type":"url_citation"}]`},
		{"invalid URL type", `[{"type":"url_citation","url":[]}]`},
		{"non HTTP URL", `[{"type":"url_citation","url":"javascript:alert(1)","title":"x","start_index":0,"end_index":5}]`},
		{"relative URL", `[{"type":"url_citation","url":"/source","title":"x","start_index":0,"end_index":5}]`},
		{"credentials", `[{"type":"url_citation","url":"https://user:pass@example.com","title":"x","start_index":0,"end_index":5}]`},
		{"newline", `[{"type":"url_citation","url":"https://example.com/\nInjected","title":"x","start_index":0,"end_index":5}]`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload := `[{"type":"message","id":"m1","role":"assistant","status":"completed","content":[{"type":"output_text","text":"hello","annotations":` + tt.annotation + `}]}]`
			message := responsesHistory(payload) // Text differs intentionally; malformed metadata must still fail.
			_, _, err := New("chat").prepareProviderHistory([]model.Message{message})
			require.ErrorContains(t, err, "annotations")
		})
	}
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
		{"unsupported item", []model.Message{responsesHistory(`[{"type":"web_search_call","id":"ws_1"}]`)}, `unsupported native item type "web_search_call"`},
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
		{"call without name", []model.Message{responsesHistory(`[{"type":"function_call","call_id":"call_1","arguments":"{}"}]`)}, "has an empty name"},
		{"incomplete assistant message", []model.Message{responsesHistory(`[{"type":"message","id":"msg_1","role":"assistant"}]`)}, `has status "", want completed`},
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

func TestChatRequestProjectsPairedSearchWithoutLosingOrdinaryCalls(t *testing.T) {
	const native = `[
		{"type":"message","id":"msg","status":"completed","role":"assistant","content":[{"type":"output_text","text":"Visible answer","annotations":[{"type":"url_citation","url":"https://example.com/source"}]}]},
		{"type":"tool_search_call","id":"hosted","execution":"server","call_id":null,"status":"completed","arguments":{"query":"hosted"}},
		{"type":"tool_search_output","id":"hosted_output","execution":"server","call_id":null,"status":"completed","tools":[{"type":"function","name":"old_loaded_tool"}]},
		{"type":"tool_search_call","id":"client","execution":"client","call_id":"search","status":"completed","arguments":"{\"query\":\"client\"}"},
		{"type":"function_call","call_id":"call","name":"history_lookup","arguments":"{}"}
	]`
	assistant := responsesHistory(native)
	assistant.Content = "Visible answer"
	assistant.ToolCalls = []model.ToolCall{{Type: "function", ID: "call", Function: model.FunctionDefinitionParam{Name: "history_lookup", Arguments: []byte(`{}`)}}}
	searchResult := model.Message{Role: model.RoleTool, ToolID: "search", ToolName: "tool_search", Content: "private search summary", ProviderData: &model.ProviderData{
		Protocol: "openai.responses", Model: "first", Payload: json.RawMessage(`[{"type":"tool_search_output","execution":"client","call_id":"search","status":"completed","tools":[{"type":"function","name":"another_old_tool"}]}]`),
	}}
	request := &model.Request{
		Messages: []model.Message{model.NewUserMessage("question"), assistant, searchResult, model.NewToolMessage("call", "history_lookup", "actual tool result"), model.NewUserMessage("next")},
		Tools:    map[string]tool.Tool{"current_tool": stubTool{decl: &tool.Declaration{Name: "current_tool", InputSchema: &tool.Schema{Type: "object"}}}},
	}
	before, err := json.Marshal(request)
	require.NoError(t, err)
	params, _, err := New("chat").prepareChatRequest(context.Background(), request)
	require.NoError(t, err)
	wire, err := json.Marshal(params)
	require.NoError(t, err)
	require.Contains(t, string(wire), "Visible answer")
	require.Contains(t, string(wire), "https://example.com/source")
	require.Contains(t, string(wire), "history_lookup")
	require.Contains(t, string(wire), "actual tool result")
	require.Contains(t, string(wire), "current_tool")
	for _, absent := range []string{"tool_search_call", "tool_search_output", "old_loaded_tool", "another_old_tool", "private search summary"} {
		require.NotContains(t, string(wire), absent)
	}
	after, err := json.Marshal(request)
	require.NoError(t, err)
	require.JSONEq(t, string(before), string(after))
}

func TestChatSearchProjectionRejectsInvalidOrUnpairedHistory(t *testing.T) {
	search := func(payload string) model.Message {
		message := responsesHistory(payload)
		message.Content = ""
		return message
	}
	result := func(callID string) model.Message {
		return model.Message{Role: model.RoleTool, ToolID: callID, ProviderData: &model.ProviderData{
			Protocol: "openai.responses", Model: "first", Payload: json.RawMessage(`[{"type":"tool_search_output","execution":"client","call_id":"` + callID + `","status":"completed","tools":[]}]`),
		}}
	}
	call := search(`[{"type":"tool_search_call","execution":"client","call_id":"s","status":"completed","arguments":{"query":"x"}}]`)
	ordinary := search(`[{"type":"function_call","call_id":"c","name":"lookup","arguments":"{}"},{"type":"tool_search_call","execution":"client","call_id":"s","arguments":{"query":"x"}}]`)
	ordinary.ToolCalls = []model.ToolCall{{ID: "c", Type: "function", Function: model.FunctionDefinitionParam{Name: "lookup", Arguments: []byte(`{}`)}}}
	mismatched := result("s")
	mismatched.ToolID = "other"
	otherModel := result("s")
	otherModel.ProviderData.Model = "different"
	tests := []struct {
		name     string
		messages []model.Message
		want     string
	}{
		{"client orphan", []model.Message{result("s")}, "no preceding tool_search_call"},
		{"client incomplete", []model.Message{call}, "has no tool_search_output"},
		{"client intervening user", []model.Message{call, model.NewUserMessage("next"), result("s")}, "has no tool_search_output"},
		{"client duplicate output", []model.Message{call, result("s"), result("s")}, "no preceding tool_search_call"},
		{"client duplicate call", []model.Message{search(`[{"type":"tool_search_call","execution":"client","call_id":"s","arguments":{}},{"type":"tool_search_call","execution":"client","call_id":"s","arguments":{}}]`)}, "duplicate tool_search_call"},
		{"mismatched tool id", []model.Message{call, mismatched}, "may only record"},
		{"different producer", []model.Message{call, otherModel}, "different model"},
		{"client output on assistant", []model.Message{search(`[{"type":"tool_search_output","execution":"client","call_id":"s","tools":[]}]`)}, "must be recorded on a tool message"},
		{"client search without id", []model.Message{search(`[{"type":"tool_search_call","execution":"client","arguments":{}}]`)}, "empty call_id"},
		{"client invalid arguments", []model.Message{search(`[{"type":"tool_search_call","execution":"client","call_id":"s","arguments":"[]"}]`)}, "invalid JSON arguments"},
		{"client null arguments", []model.Message{search(`[{"type":"tool_search_call","execution":"client","call_id":"s","arguments":null}]`)}, "invalid JSON arguments"},
		{"unknown execution", []model.Message{search(`[{"type":"tool_search_call","execution":"future","arguments":{}}]`)}, "unsupported execution"},
		{"unfinished search", []model.Message{search(`[{"type":"tool_search_call","execution":"server","status":"in_progress"}]`)}, "want completed"},
		{"hosted orphan", []model.Message{search(`[{"type":"tool_search_output","execution":"server","tools":[]}]`)}, "no preceding tool_search_call"},
		{"hosted incomplete", []model.Message{search(`[{"type":"tool_search_call","execution":"server","arguments":{}}]`)}, "has no tool_search_output"},
		{"malformed tools", []model.Message{call, {Role: model.RoleTool, ToolID: "s", ProviderData: &model.ProviderData{Protocol: "openai.responses", Model: "first", Payload: json.RawMessage(`[{"type":"tool_search_output","execution":"client","call_id":"s","tools":{}}]`)}}}, "decode"},
		{"unknown native item", []model.Message{search(`[{"type":"other"}]`)}, "unsupported native item type"},
		{"ordinary missing result", []model.Message{ordinary, result("s")}, "missing tool results"},
		{"ordinary orphan result", []model.Message{call, result("s"), model.NewToolMessage("other", "lookup", "x")}, "no pending function call"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := New("chat").prepareProviderHistory(tt.messages)
			require.ErrorContains(t, err, tt.want)
		})
	}
}

func TestChatSearchProjectionAcceptsParallelCallsAndHostedIDs(t *testing.T) {
	assistant := responsesHistory(`[
		{"type":"tool_search_call","execution":"server","call_id":"hosted","arguments":{}},
		{"type":"tool_search_output","execution":"server","call_id":"hosted","tools":[]},
		{"type":"tool_search_call","execution":"client","call_id":"one","arguments":{"query":"one"}},
		{"type":"tool_search_call","execution":"client","call_id":"two","arguments":"{\"query\":\"two\"}"}
	]`)
	assistant.Content = ""
	output := func(id string) model.Message {
		return model.Message{Role: model.RoleTool, ToolID: id, ProviderData: &model.ProviderData{
			Protocol: "openai.responses", Model: "first", Payload: json.RawMessage(`[{"type":"tool_search_output","execution":"client","call_id":"` + id + `","tools":[]}]`),
		}}
	}
	messages := []model.Message{model.NewUserMessage("question"), assistant, output("two"), output("one"), model.NewUserMessage("next")}
	projected, changed, err := New("chat").prepareProviderHistory(messages)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, []model.Message{model.NewUserMessage("question"), model.NewUserMessage("next")}, projected)
}

func TestChatSearchProjectionDoesNotRestoreClearedCitation(t *testing.T) {
	for _, nativeText := range []string{"sensitive original", ""} {
		t.Run(nativeText, func(t *testing.T) {
			payload, err := json.Marshal([]any{
				map[string]any{"type": "message", "id": "m", "status": "completed", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": nativeText, "annotations": []any{map[string]any{"type": "url_citation", "url": "https://example.com/private"}}}}},
				map[string]any{"type": "tool_search_call", "execution": "server", "arguments": map[string]any{}},
				map[string]any{"type": "tool_search_output", "execution": "server", "tools": []any{}},
			})
			require.NoError(t, err)
			message := responsesHistory(string(payload))
			message.Content = ""
			messages := []model.Message{model.NewUserMessage("question"), message}
			before, err := json.Marshal(messages)
			require.NoError(t, err)
			projected, changed, err := New("chat").prepareProviderHistory(messages)
			require.NoError(t, err)
			require.True(t, changed)
			require.Equal(t, []model.Message{model.NewUserMessage("question")}, projected)
			after, err := json.Marshal(messages)
			require.NoError(t, err)
			require.JSONEq(t, string(before), string(after))
		})
	}
}
