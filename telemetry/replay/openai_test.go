//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package replay

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	itelemetry "trpc.group/trpc-go/trpc-agent-go/internal/telemetry"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestExportOpenAIChatCompletions(t *testing.T) {
	audioData := base64.StdEncoding.EncodeToString([]byte("audio"))
	videoData := base64.StdEncoding.EncodeToString([]byte("video"))

	inputMessages, err := json.Marshal([]itelemetry.OTelInputMessage{
		{
			Role: model.RoleUser,
			Parts: []itelemetry.OTelMessagePart{
				{Type: "text", Content: "Describe this input"},
				{Type: "uri", Modality: "image", URI: "https://example.com/cat.png", Detail: "high"},
				{Type: "blob", Modality: "audio", MIMEType: "audio/mpeg", Content: audioData},
				{Type: "file", Modality: "file", FileID: "file-doc-1", Filename: "paper.pdf"},
			},
		},
		{
			Role: model.RoleAssistant,
			Parts: []itelemetry.OTelMessagePart{
				{Type: "text", Content: "Calling search"},
				{
					Type:      "tool_call",
					ID:        "call-1",
					Name:      "search",
					Arguments: json.RawMessage(`{"q":"otel multimodal"}`),
				},
			},
		},
		{
			Role: model.RoleTool,
			Name: "search",
			Parts: []itelemetry.OTelMessagePart{
				{
					Type:     "tool_call_response",
					ID:       "call-1",
					Response: json.RawMessage(`{"result":"ok"}`),
				},
			},
		},
		{
			Role: model.RoleUser,
			Parts: []itelemetry.OTelMessagePart{
				{Type: "blob", Modality: "video", MIMEType: "video/mp4", Content: videoData, Filename: "clip.mp4"},
			},
		},
	})
	require.NoError(t, err)

	systemInstructions, err := json.Marshal([]itelemetry.OTelMessagePart{
		{Type: "text", Content: "Be concise."},
	})
	require.NoError(t, err)

	toolDefs, err := json.Marshal([]tool.Declaration{{
		Name:        "search",
		Description: "Search docs",
		InputSchema: &tool.Schema{
			Type: "object",
			Properties: map[string]*tool.Schema{
				"q": {Type: "string"},
			},
			Required: []string{"q"},
		},
	}})
	require.NoError(t, err)

	exported, err := ExportOpenAIChatCompletions(OpenAIExportRequest{
		Model:                  "gpt-4.1",
		InputMessagesJSON:      string(inputMessages),
		ToolDefinitionsJSON:    string(toolDefs),
		SystemInstructionsJSON: string(systemInstructions),
	})
	require.NoError(t, err)

	require.Equal(t, "POST", exported.Method)
	require.Equal(t, "https://api.openai.com/v1/chat/completions", exported.URL)
	require.Equal(t, "Bearer $OPENAI_API_KEY", exported.Headers["Authorization"])
	require.Contains(t, exported.Warnings, "chat completions replay exporter does not replay video inputs")

	var body map[string]any
	require.NoError(t, json.Unmarshal(exported.Body, &body))
	require.Equal(t, "gpt-4.1", body["model"])

	messages, ok := body["messages"].([]any)
	require.True(t, ok)
	require.Len(t, messages, 4)

	systemMsg := messages[0].(map[string]any)
	require.Equal(t, "system", systemMsg["role"])
	require.Equal(t, "Be concise.", systemMsg["content"])

	userMsg := messages[1].(map[string]any)
	require.Equal(t, "user", userMsg["role"])
	userContent := userMsg["content"].([]any)
	require.Len(t, userContent, 4)
	require.Equal(t, "text", userContent[0].(map[string]any)["type"])
	require.Equal(t, "image_url", userContent[1].(map[string]any)["type"])
	require.Equal(t, "input_audio", userContent[2].(map[string]any)["type"])
	require.Equal(t, "file", userContent[3].(map[string]any)["type"])

	assistantMsg := messages[2].(map[string]any)
	require.Equal(t, "assistant", assistantMsg["role"])
	require.Equal(t, "Calling search", assistantMsg["content"])
	toolCalls := assistantMsg["tool_calls"].([]any)
	require.Len(t, toolCalls, 1)
	require.Equal(t, "call-1", toolCalls[0].(map[string]any)["id"])

	toolMsg := messages[3].(map[string]any)
	require.Equal(t, "tool", toolMsg["role"])
	require.Equal(t, "call-1", toolMsg["tool_call_id"])
	require.Equal(t, `{"result":"ok"}`, toolMsg["content"])

	tools := body["tools"].([]any)
	require.Len(t, tools, 1)
	toolDef := tools[0].(map[string]any)
	require.Equal(t, "function", toolDef["type"])
	function := toolDef["function"].(map[string]any)
	require.Equal(t, "search", function["name"])
}

func TestExportOpenAIResponses(t *testing.T) {
	imageData := base64.StdEncoding.EncodeToString([]byte("image"))
	audioData := base64.StdEncoding.EncodeToString([]byte("audio"))
	pdfData := base64.StdEncoding.EncodeToString([]byte("pdf"))

	inputMessages, err := json.Marshal([]itelemetry.OTelInputMessage{
		{
			Role: model.RoleUser,
			Parts: []itelemetry.OTelMessagePart{
				{Type: "text", Content: "Analyze the attachment"},
				{Type: "blob", Modality: "image", MIMEType: "image/png", Content: imageData},
				{Type: "file", Modality: "file", FileID: "file-doc-1", Filename: "paper.pdf"},
				{Type: "blob", Modality: "file", MIMEType: "application/pdf", Filename: "inline.pdf", Content: pdfData},
				{Type: "blob", Modality: "audio", MIMEType: "audio/mpeg", Content: audioData},
				{Type: "uri", Modality: "video", MIMEType: "video/mp4", URI: "https://example.com/clip.mp4"},
			},
		},
		{
			Role: model.RoleAssistant,
			Parts: []itelemetry.OTelMessagePart{
				{
					Type:      "tool_call",
					ID:        "call-1",
					Name:      "lookup",
					Arguments: json.RawMessage(`{"id":1}`),
				},
			},
		},
		{
			Role: model.RoleTool,
			Parts: []itelemetry.OTelMessagePart{
				{
					Type:     "tool_call_response",
					ID:       "call-1",
					Response: json.RawMessage(`{"ok":true}`),
				},
			},
		},
	})
	require.NoError(t, err)

	systemInstructions, err := json.Marshal([]itelemetry.OTelMessagePart{
		{Type: "text", Content: "Be concise."},
	})
	require.NoError(t, err)

	toolDefs, err := json.Marshal([]tool.Declaration{{
		Name:        "lookup",
		Description: "Look up a document",
		InputSchema: &tool.Schema{Type: "object"},
	}})
	require.NoError(t, err)

	exported, err := ExportOpenAIResponses(OpenAIExportRequest{
		Model:                  "gpt-5.5",
		InputMessagesJSON:      string(inputMessages),
		ToolDefinitionsJSON:    string(toolDefs),
		SystemInstructionsJSON: string(systemInstructions),
	})
	require.NoError(t, err)

	require.Equal(t, "POST", exported.Method)
	require.Equal(t, "https://api.openai.com/v1/responses", exported.URL)
	require.Contains(t, exported.Warnings, "responses replay exporter does not replay audio inputs yet")
	require.Contains(t, exported.Warnings, "responses replay exporter does not replay video inputs")

	var body map[string]any
	require.NoError(t, json.Unmarshal(exported.Body, &body))
	require.Equal(t, "gpt-5.5", body["model"])
	require.Equal(t, "Be concise.", body["instructions"])

	input := body["input"].([]any)
	require.Len(t, input, 3)

	userMsg := input[0].(map[string]any)
	require.Equal(t, "user", userMsg["role"])
	userContent := userMsg["content"].([]any)
	require.Len(t, userContent, 4)
	require.Equal(t, "input_text", userContent[0].(map[string]any)["type"])
	require.Equal(t, "input_image", userContent[1].(map[string]any)["type"])
	require.Equal(t, "input_file", userContent[2].(map[string]any)["type"])
	require.Equal(t, "input_file", userContent[3].(map[string]any)["type"])

	functionCall := input[1].(map[string]any)
	require.Equal(t, "function_call", functionCall["type"])
	require.Equal(t, "call-1", functionCall["call_id"])
	require.Equal(t, "lookup", functionCall["name"])
	require.Equal(t, `{"id":1}`, functionCall["arguments"])

	functionOutput := input[2].(map[string]any)
	require.Equal(t, "function_call_output", functionOutput["type"])
	require.Equal(t, "call-1", functionOutput["call_id"])
	require.Equal(t, `{"ok":true}`, functionOutput["output"])

	tools := body["tools"].([]any)
	require.Len(t, tools, 1)
	toolDef := tools[0].(map[string]any)
	require.Equal(t, "function", toolDef["type"])
	require.Equal(t, "lookup", toolDef["name"])
}
