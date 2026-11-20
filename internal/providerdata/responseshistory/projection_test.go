//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package responseshistory

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
			Protocol: Protocol,
			Model:    "first",
			Payload:  json.RawMessage(payload),
		},
	}
}

func TestProjectMessageProjectsURLCitations(t *testing.T) {
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
			projected, include, err := ProjectMessage(message)
			require.NoError(t, err)
			require.True(t, include)
			visible := projected.Content
			for _, part := range projected.ContentParts {
				require.NotNil(t, part.Text)
				visible += *part.Text
			}
			require.Equal(t, tt.want, visible)
			after, err := json.Marshal(message)
			require.NoError(t, err)
			require.JSONEq(t, string(before), string(after))
		})
	}
}

func TestProjectMessageIgnoresUnusedCitationFields(t *testing.T) {
	const raw = `[{"type":"message","id":"m1","status":"completed","role":"assistant","content":[
		{"type":"output_text","text":"hello","annotations":[
			{"type":"url_citation","url":"https://example.com/a"},
			{"type":"url_citation","url":"https://example.com/b","start_index":2,"end_index":2},
			{"type":"url_citation","url":"https://example.com/c","title":null,"start_index":-10,"end_index":9999}
		]}
	]}]`
	message := responsesHistory(raw)
	message.Content = "hello"
	got, _, err := ProjectMessage(message)
	require.NoError(t, err)
	require.Equal(t, "hello\n\nSources:\nhttps://example.com/a\nhttps://example.com/b\nhttps://example.com/c", got.Content)
}

func TestProjectMessageDeduplicatesCitationURLsAgainstBody(t *testing.T) {
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
			got, _, err := ProjectMessage(message)
			require.NoError(t, err)
			want := tt.body
			if tt.wantSource {
				want += "\n\nSources:\n" + source
			}
			require.Equal(t, want, got.Content)
		})
	}
}

func TestProjectMessageRejectsInvalidCitations(t *testing.T) {
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
			_, _, err := ProjectMessage(message)
			require.ErrorContains(t, err, "annotations")
		})
	}
}

func TestProjectMessagePreservesVisibleCallsAndNativeRecord(t *testing.T) {
	message := responsesHistory(`[{"type":"function_call","call_id":"c1","name":"lookup","arguments":"{}"}]`)
	message.Content = ""
	message.ToolCalls = []model.ToolCall{{
		ID: "c1", Type: "function", Function: model.FunctionDefinitionParam{Name: "edited"},
		ExtraFields: map[string]any{"private": "field"},
	}}
	before, err := json.Marshal(message)
	require.NoError(t, err)
	projected, include, err := ProjectMessage(message)
	require.NoError(t, err)
	require.True(t, include)
	require.Nil(t, projected.ProviderData)
	require.Empty(t, projected.ReasoningContent)
	require.Len(t, projected.ToolCalls, 1)
	require.Equal(t, "edited", projected.ToolCalls[0].Function.Name)
	require.JSONEq(t, "{}", string(projected.ToolCalls[0].Function.Arguments))
	require.Empty(t, projected.ToolCalls[0].ExtraFields)
	after, err := json.Marshal(message)
	require.NoError(t, err)
	require.JSONEq(t, string(before), string(after))

	message.ToolCalls[0].ID = "c2"
	_, _, err = ProjectMessage(message)
	require.ErrorContains(t, err, "does not match")
}

func TestProjectMessageSkipsPrivateReasoning(t *testing.T) {
	message := responsesHistory(`[{"type":"reasoning","id":"r1","encrypted_content":"opaque"}]`)
	message.Content = ""
	projected, include, err := ProjectMessage(message)
	require.NoError(t, err)
	require.False(t, include)
	require.Nil(t, projected.ProviderData)
	require.Empty(t, projected.ReasoningContent)
}

func TestProjectMessageValidatesEditedCalls(t *testing.T) {
	const raw = `[{"type":"function_call","call_id":"c1","name":"lookup","arguments":"{}"}]`
	tests := []struct {
		name, visibleName, args, wantError string
	}{
		{"edited arguments", "renamed", `{"key":9007199254740993}`, ""},
		{"missing name", "", "{}", "empty name"},
		{"arguments no longer an object", "lookup", "[]", "invalid JSON object"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			message := responsesHistory(raw)
			message.ToolCalls = []model.ToolCall{{ID: "c1", Type: "function", Function: model.FunctionDefinitionParam{
				Name: tt.visibleName, Arguments: []byte(tt.args),
			}}}
			projected, include, err := ProjectMessage(message)
			if tt.wantError != "" {
				require.ErrorContains(t, err, tt.wantError)
				return
			}
			require.NoError(t, err)
			require.True(t, include)
			require.Equal(t, tt.args, string(projected.ToolCalls[0].Function.Arguments))
		})
	}
}

func TestProjectMessageRejectsMissingOrForeignRecords(t *testing.T) {
	_, _, err := ProjectMessage(model.NewUserMessage("question"))
	require.ErrorContains(t, err, "expected Responses provider data")
	message := responsesHistory("[]")
	message.ProviderData.Protocol = "other"
	_, _, err = ProjectMessage(message)
	require.ErrorContains(t, err, "unsupported provider data protocol")
	message = responsesHistory(`[{"type":"function_call","call_id":"c1","name":"lookup","arguments":"{}"}]`)
	_, _, err = ProjectMessage(message)
	require.ErrorContains(t, err, "do not match")
}
