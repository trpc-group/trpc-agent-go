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
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func nativeAnswer() model.Message {
	return model.Message{
		Role:             model.RoleAssistant,
		Content:          "display-only change",
		ReasoningContent: "private summary",
		ProviderData: &model.ProviderData{
			Protocol: "openai.responses",
			Model:    "old",
			Payload: json.RawMessage(`[
				{"type":"reasoning","id":"rs_1","encrypted_content":"opaque","summary":[{"type":"summary_text","text":"private summary"}]},
				{"type":"message","id":"m1","status":"completed","role":"assistant","content":[{"type":"output_text","text":"native answer","annotations":[]}]}
			]`),
		},
	}
}

func TestGenerateContentProjectsResponsesHistory(t *testing.T) {
	received := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.True(t, strings.HasSuffix(r.URL.Path, "/chat/completions"))
		var body json.RawMessage
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		received <- string(body)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"chat_1","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()
	m := New("chat", WithBaseURL(server.URL), WithAPIKey("test"))
	native := nativeAnswer()
	native.ProviderData.Payload = json.RawMessage(`[
		{"type":"reasoning","id":"rs_1","encrypted_content":"opaque","summary":[{"type":"summary_text","text":"private summary"}]},
		{"type":"message","id":"m1","status":"completed","role":"assistant","content":[{"type":"output_text","text":"native answer","annotations":[]}]},
		{"type":"function_call","call_id":"call_1","name":"lookup","arguments":"{\"key\":\"hiddenvalue\"}"}
	]`)
	native.ToolCalls = []model.ToolCall{{ID: "call_1", Type: "function", Function: model.FunctionDefinitionParam{Name: "lookup", Arguments: []byte(`{"key":"redactedvalue"}`)}, ExtraFields: map[string]any{"signature": "hidden_signature"}}}
	request := &model.Request{Messages: []model.Message{model.NewUserMessage("q"), native, model.NewToolMessage("call_1", "lookup", "result"), model.NewUserMessage("next")}}
	original, err := json.Marshal(request.Messages)
	require.NoError(t, err)
	stream, err := m.GenerateContent(context.Background(), request)
	require.NoError(t, err)
	for response := range stream {
		require.Nil(t, response.Error)
	}
	body := <-received
	require.Contains(t, body, "display-only change")
	require.NotContains(t, body, "native answer")
	require.NotContains(t, body, "private summary")
	require.NotContains(t, body, "opaque")
	require.NotContains(t, body, "hiddenvalue")
	require.NotContains(t, body, "hidden_signature")
	require.Contains(t, body, "redactedvalue")
	after, err := json.Marshal(request.Messages)
	require.NoError(t, err)
	require.JSONEq(t, string(original), string(after))
}

func TestChatMigrationRejectsUnsupportedMedia(t *testing.T) {
	m := New("chat")
	assistant := nativeAnswer()
	imageResult := model.NewToolMessage("call", "lookup", "ok")
	imageResult.AddImageURL("https://example.test/photo.png", "")
	call := model.Message{
		Role:      model.RoleAssistant,
		ToolCalls: []model.ToolCall{{Type: "function", ID: "call", Function: model.FunctionDefinitionParam{Name: "lookup", Arguments: []byte(`{}`)}}},
		ProviderData: &model.ProviderData{
			Protocol: "openai.responses", Model: "old",
			Payload: json.RawMessage(`[{"type":"function_call","call_id":"call","name":"lookup","arguments":"{}"}]`),
		},
	}
	_, _, err := m.prepareProviderHistory([]model.Message{call, imageResult})
	require.ErrorContains(t, err, `content type "image"`)

	user := model.NewUserMessage("see video")
	user.ContentParts = []model.ContentPart{{Type: model.ContentTypeVideo, Video: &model.Video{URL: "https://example.test/video.mp4"}}}
	_, _, err = m.prepareProviderHistory([]model.Message{assistant, user})
	require.ErrorContains(t, err, `content type "video"`)
}

func TestChatMigrationSupportsProviderFileReferences(t *testing.T) {
	tests := []struct {
		name       string
		model      *Model
		file       *model.File
		wantFileID bool
		wantURL    string
	}{
		{
			name:       "hunyuan file id",
			model:      New("chat", WithVariant(VariantHunyuan)),
			file:       &model.File{FileID: "file-provider-123"},
			wantFileID: true,
		},
		{
			name:    "hunyuan file url",
			model:   New("chat", WithVariant(VariantHunyuan)),
			file:    &model.File{Name: "notes.txt", URL: "https://example.test/notes.txt"},
			wantURL: "https://example.test/notes.txt",
		},
		{
			name:    "text only file url",
			model:   New("chat", WithVariant(VariantDeepSeek)),
			file:    &model.File{Name: "notes.txt", URL: "https://example.test/notes.txt"},
			wantURL: "https://example.test/notes.txt",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			user := model.NewUserMessage("read this")
			user.ContentParts = []model.ContentPart{{Type: model.ContentTypeFile, File: tt.file}}
			prepared, changed, err := tt.model.prepareProviderHistory([]model.Message{nativeAnswer(), user})
			require.NoError(t, err)
			require.True(t, changed)
			content, extras := tt.model.convertUserMessageContent(prepared[1])
			if tt.wantFileID {
				require.Equal(t, []string{"file-provider-123"}, extras["file_ids"])
			}
			if tt.wantURL != "" {
				encoded, err := json.Marshal(content)
				require.NoError(t, err)
				require.Contains(t, string(encoded), tt.wantURL)
			}
		})
	}
}

func TestChatMigrationRejectsEmptyProjectedHistory(t *testing.T) {
	m := New("chat")
	message := nativeAnswer()
	message.Content = ""
	message.ProviderData.Payload = json.RawMessage(`[{"type":"reasoning","id":"rs_1","encrypted_content":"opaque"}]`)
	_, _, err := m.prepareProviderHistory([]model.Message{message})
	require.ErrorContains(t, err, "no transferable messages")

	prepared, changed, err := m.prepareProviderHistory([]model.Message{model.NewUserMessage("q"), message})
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, []model.Message{model.NewUserMessage("q")}, prepared)
}

func TestBatchProjectsResponsesHistory(t *testing.T) {
	user := model.Message{Role: model.RoleUser, ContentParts: []model.ContentPart{
		{Type: model.ContentTypeFile, File: &model.File{FileID: "artifact://private/report.txt@2"}},
	}}
	native := nativeAnswer()
	native.ProviderData.Payload = json.RawMessage(`[{"type":"function_call","call_id":"call_1","name":"lookup","arguments":"{}"}]`)
	native.ToolCalls = []model.ToolCall{{ID: "call_1", Type: "function", Function: model.FunctionDefinitionParam{Name: "lookup"}}}
	request := &BatchRequestInput{
		CustomID: "switch",
		Body:     BatchRequest{Request: model.Request{Messages: []model.Message{user, native, model.NewToolMessage("call_1", "lookup", "found")}}},
	}
	line, err := New("chat").generateBatchJSONL([]*BatchRequestInput{request})
	require.NoError(t, err)
	require.Contains(t, string(line), "display-only change")
	require.Contains(t, string(line), "Uploaded file: report.txt (available to tools).")
	require.NotContains(t, string(line), "artifact://private/")
	require.Contains(t, string(line), `"tool_call_id":"call_1"`)
	require.Contains(t, string(line), `"arguments":"{}"`)
	require.NotContains(t, string(line), "private summary")
	require.Equal(t, "private summary", request.Body.Messages[1].ReasoningContent)
	require.NotNil(t, request.Body.Messages[1].ProviderData)
	require.Equal(t, "artifact://private/report.txt@2", request.Body.Messages[0].ContentParts[0].File.FileID)
}

func TestChatMigrationInternalFilesAndEmptyParts(t *testing.T) {
	m := New("chat")
	empty := model.ContentPart{}
	text := "part text"
	tests := []struct {
		name    string
		message model.Message
		want    []string
	}{
		{
			name: "internal file alone",
			message: model.Message{Role: model.RoleUser, ContentParts: []model.ContentPart{
				{Type: model.ContentTypeFile, File: &model.File{FileID: "artifact://private/report.txt@2"}},
			}},
			want: []string{"Uploaded file: report.txt (available to tools)."},
		},
		{
			name: "internal file with an empty text placeholder",
			message: model.Message{Role: model.RoleUser, ContentParts: []model.ContentPart{
				{Type: model.ContentTypeText},
				{Type: model.ContentTypeFile, File: &model.File{FileID: "artifact://private/report.txt@2"}},
			}},
			want: []string{"Uploaded file: report.txt (available to tools)."},
		},
		{
			name: "internal file with other content",
			message: model.Message{Role: model.RoleUser, Content: "visible", ContentParts: []model.ContentPart{
				{Type: model.ContentTypeFile, File: &model.File{FileID: "artifact://private/report.txt@2"}},
				{Type: model.ContentTypeText, Text: &text},
				empty,
			}},
			want: []string{"visible", "part text"},
		},
		{name: "system empty placeholder", message: model.Message{Role: model.RoleSystem, Content: "visible", ContentParts: []model.ContentPart{empty}}, want: []string{"visible"}},
		{name: "assistant empty placeholder", message: model.Message{Role: model.RoleAssistant, Content: "visible", ContentParts: []model.ContentPart{empty}}, want: []string{"visible"}},
		{name: "user empty placeholder", message: model.Message{Role: model.RoleUser, Content: "visible", ContentParts: []model.ContentPart{empty}}, want: []string{"visible"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			messages := []model.Message{nativeAnswer(), tt.message}
			before, err := json.Marshal(messages)
			require.NoError(t, err)
			prepared, changed, err := m.prepareProviderHistory(messages)
			require.NoError(t, err)
			require.True(t, changed)
			wire, err := json.Marshal(m.convertMessages(prepared))
			require.NoError(t, err)
			for _, value := range tt.want {
				require.Contains(t, string(wire), value)
			}
			require.NotContains(t, string(wire), "artifact://private/")
			after, err := json.Marshal(messages)
			require.NoError(t, err)
			require.JSONEq(t, string(before), string(after))
		})
	}
}

func TestChatMigrationEmptyPartDoesNotDiscardContentRef(t *testing.T) {
	messages := []model.Message{nativeAnswer(), {
		Role:         model.RoleUser,
		Content:      "visible",
		ContentParts: []model.ContentPart{{ContentRef: &model.ContentRef{ArtifactRef: "artifact://private"}}},
	}}
	_, _, err := New("chat").prepareProviderHistory(messages)
	require.ErrorContains(t, err, "cannot be represented")

	text := "visible part"
	messages[1].ContentParts[0].Type = model.ContentTypeText
	messages[1].ContentParts[0].Text = &text
	_, _, err = New("chat").prepareProviderHistory(messages)
	require.ErrorContains(t, err, "content reference cannot be represented")
}

func TestChatMigrationDropsOnlyEmptyMessages(t *testing.T) {
	user := model.Message{Role: model.RoleUser, ContentParts: []model.ContentPart{{}}}
	prepared, changed, err := New("chat").prepareProviderHistory([]model.Message{nativeAnswer(), user})
	require.NoError(t, err)
	require.True(t, changed)
	require.Len(t, prepared, 1)
	require.Equal(t, "display-only change", prepared[0].Content)
}

func TestChatMigrationProjectedToolCallsValidateSentFields(t *testing.T) {
	tests := []struct {
		name     string
		function string
		args     string
		wantErr  string
		wantArgs string
	}{
		{name: "empty function", function: "  ", args: "{}", wantErr: "name"},
		{name: "invalid JSON", function: "lookup", args: "{bad", wantErr: "arguments"},
		{name: "array JSON", function: "lookup", args: "[]", wantErr: "arguments"},
		{name: "null JSON", function: "lookup", args: "null", wantErr: "arguments"},
		{name: "empty args", function: "lookup", args: "", wantArgs: "{}"},
		{name: "whitespace args", function: "lookup", args: "  \n", wantArgs: "{}"},
		{name: "redacted object", function: "renamed", args: `{"key":"redacted"}`, wantArgs: `{"key":"redacted"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			native := responsesHistory(`[{"type":"function_call","call_id":"c1","name":"old","arguments":"{\"secret\":\"original\"}"}]`)
			native.ToolCalls = []model.ToolCall{{Type: "function", ID: "c1", Function: model.FunctionDefinitionParam{Name: tt.function, Arguments: []byte(tt.args)}}}
			messages := []model.Message{native, model.NewToolMessage("c1", "old", "failed")}
			before, err := json.Marshal(messages)
			require.NoError(t, err)
			prepared, _, err := New("chat").prepareProviderHistory(messages)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, "c1")
				require.ErrorContains(t, err, tt.wantErr)
			} else {
				require.NoError(t, err)
				wire, err := json.Marshal(New("chat").convertMessages(prepared))
				require.NoError(t, err)
				require.NotContains(t, string(wire), "original")
				require.Contains(t, string(wire), tt.function)
				require.Equal(t, tt.wantArgs, string(prepared[0].ToolCalls[0].Function.Arguments))
				require.Equal(t, "failed", prepared[1].Content)
			}
			after, err := json.Marshal(messages)
			require.NoError(t, err)
			require.JSONEq(t, string(before), string(after))
		})
	}
}

func TestChatMigrationEmptyToolPart(t *testing.T) {
	call := responsesHistory(`[{"type":"function_call","call_id":"c1","name":"lookup","arguments":"{}"}]`)
	call.ToolCalls = []model.ToolCall{{Type: "function", ID: "c1", Function: model.FunctionDefinitionParam{Name: "lookup", Arguments: []byte("{}")}}}
	result := model.NewToolMessage("c1", "lookup", "visible")
	result.ContentParts = []model.ContentPart{{}}
	prepared, _, err := New("chat").prepareProviderHistory([]model.Message{call, result})
	require.NoError(t, err)
	require.Equal(t, "visible", prepared[1].Content)
}

func TestGenerateContentRejectsInvalidProjectedToolCall(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer server.Close()
	adapter := New("chat", WithBaseURL(server.URL), WithAPIKey("test"))
	native := responsesHistory(`[{"type":"function_call","call_id":"c1","name":"lookup","arguments":"{}"}]`)
	native.ToolCalls = []model.ToolCall{{Type: "function", ID: "c1", Function: model.FunctionDefinitionParam{Name: "lookup", Arguments: []byte("{invalid")}}}
	_, err := adapter.GenerateContent(context.Background(), &model.Request{
		Messages: []model.Message{native, model.NewToolMessage("c1", "lookup", "done")},
	})
	require.ErrorContains(t, err, "c1")
	require.ErrorContains(t, err, "invalid JSON object arguments")
	require.False(t, called)
}

func TestChatMigrationOmitsUserFileWhenConfigured(t *testing.T) {
	const filename = "report.txt"
	for _, tc := range []struct {
		name     string
		text     string
		wantUser string
	}{
		{"file only", "", `{"role":"user","content":[{"type":"text","text":"Uploaded file: report.txt (available to tools)."}]}`},
		{"file with text", "read the report", `{"role":"user","content":"read the report"}`},
	} {
		for _, transport := range []string{"chat", "batch"} {
			t.Run(tc.name+"/"+transport, func(t *testing.T) {
				user := model.NewUserMessage(tc.text)
				user.ContentParts = []model.ContentPart{{Type: model.ContentTypeFile, File: &model.File{
					Name: filename, FileID: "file-provider-123",
				}}}
				request := model.Request{Messages: []model.Message{nativeAnswer(), user}}
				before, err := json.Marshal(request.Messages)
				require.NoError(t, err)
				var body json.RawMessage
				if transport == "batch" {
					m := New("chat", WithOmitFileContentParts(true))
					line, err := m.generateBatchJSONL([]*BatchRequestInput{{
						CustomID: "omitted-file", Body: BatchRequest{Request: request},
					}})
					require.NoError(t, err)
					var batch struct{ Body json.RawMessage }
					require.NoError(t, json.Unmarshal(line, &batch))
					body = batch.Body
				} else {
					received := make(chan json.RawMessage, 1)
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						var payload json.RawMessage
						if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
							http.Error(w, err.Error(), http.StatusBadRequest)
							return
						}
						received <- payload
						w.Header().Set("Content-Type", "application/json")
						fmt.Fprint(w, `{"id":"answer","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
					}))
					defer server.Close()
					m := New("chat", WithAPIKey("test"), WithBaseURL(server.URL), WithOmitFileContentParts(true))
					stream, err := m.GenerateContent(context.Background(), &request)
					require.NoError(t, err)
					for response := range stream {
						require.Nil(t, response.Error)
					}
					require.Len(t, received, 1)
					body = <-received
				}
				var payload struct{ Messages []json.RawMessage }
				require.NoError(t, json.Unmarshal(body, &payload))
				require.Len(t, payload.Messages, 2)
				require.JSONEq(t, tc.wantUser, string(payload.Messages[1]))
				require.NotContains(t, string(body), "file-provider-123")
				after, err := json.Marshal(request.Messages)
				require.NoError(t, err)
				require.JSONEq(t, string(before), string(after))
			})
		}
	}
}

func TestBatchRejectsUnrepresentableHistoryBeforeEncoding(t *testing.T) {
	request := &BatchRequestInput{
		CustomID: "unsupported-output",
		Body: BatchRequest{Request: model.Request{Messages: []model.Message{
			responsesHistory(`[{"type":"web_search_call"}]`),
		}}},
	}
	line, err := New("chat").generateBatchJSONL([]*BatchRequestInput{request})
	require.ErrorContains(t, err, request.CustomID)
	require.ErrorContains(t, err, "web_search_call")
	require.Empty(t, line)
}
