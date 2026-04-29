//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package telemetry

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestParseOTelMessagesJSON_EmptyAndInvalid(t *testing.T) {
	inputMessages, err := ParseOTelInputMessagesJSON("")
	require.NoError(t, err)
	require.Nil(t, inputMessages)

	inputMessages, err = ParseOTelInputMessagesJSON("  \n\t  ")
	require.NoError(t, err)
	require.Nil(t, inputMessages)

	inputMessages, err = ParseOTelInputMessagesJSON(`{"role":"user","parts":[{"type":"text","content":"hi"}]}`)
	require.NoError(t, err)
	require.Nil(t, inputMessages)

	outputMessages, err := ParseOTelOutputMessagesJSON("")
	require.NoError(t, err)
	require.Nil(t, outputMessages)

	outputMessages, err = ParseOTelOutputMessagesJSON("  \n\t  ")
	require.NoError(t, err)
	require.Nil(t, outputMessages)

	outputMessages, err = ParseOTelOutputMessagesJSON(`{"role":"assistant","parts":[{"type":"text","content":"hi"}],"finish_reason":"stop"}`)
	require.NoError(t, err)
	require.Nil(t, outputMessages)

	_, err = ParseOTelInputMessagesJSON("{")
	require.Error(t, err)

	_, err = ParseOTelOutputMessagesJSON("{")
	require.Error(t, err)
}

func TestParseOTelMessagesJSON_RejectsLegacyOrIncompleteShapes(t *testing.T) {
	inputMessages, err := ParseOTelInputMessagesJSON(`[{"role":"user","content":"hi"}]`)
	require.NoError(t, err)
	require.Nil(t, inputMessages)

	inputMessages, err = ParseOTelInputMessagesJSON(`[{"role":"user","parts":[]}]`)
	require.NoError(t, err)
	require.Nil(t, inputMessages)

	inputMessages, err = ParseOTelInputMessagesJSON(`[{"role":"user","parts":[{"type":"tool_call"}]}]`)
	require.NoError(t, err)
	require.Nil(t, inputMessages)

	outputMessages, err := ParseOTelOutputMessagesJSON(`[{"role":"assistant","content":"hi","finish_reason":"stop"}]`)
	require.NoError(t, err)
	require.Nil(t, outputMessages)

	outputMessages, err = ParseOTelOutputMessagesJSON(`[{"role":"assistant","parts":[],"finish_reason":"stop"}]`)
	require.NoError(t, err)
	require.Nil(t, outputMessages)

	outputMessages, err = ParseOTelOutputMessagesJSON(`[{"role":"assistant","parts":[{"type":"tool_call_response"}],"finish_reason":"stop"}]`)
	require.NoError(t, err)
	require.Nil(t, outputMessages)
}

func TestParseOTelMessagesJSON_AcceptsValidOTelShapes(t *testing.T) {
	inputMessages, err := ParseOTelInputMessagesJSON(`[{"role":"user","parts":[{"type":"text","content":"hi"}]},{"role":"assistant","parts":[{"type":"tool_call","name":"lookup","arguments":{"key":"otel"}}]}]`)
	require.NoError(t, err)
	require.Len(t, inputMessages, 2)
	require.Equal(t, model.RoleUser, inputMessages[0].Role)
	require.Equal(t, otelPartTypeText, inputMessages[0].Parts[0].Type)
	require.Equal(t, otelPartTypeToolCall, inputMessages[1].Parts[0].Type)

	outputMessages, err := ParseOTelOutputMessagesJSON(`[{"role":"assistant","parts":[{"type":"reasoning","content":"thinking"},{"type":"text","content":"done"}],"finish_reason":"stop"},{"role":"tool","parts":[{"type":"tool_call_response","id":"call-1","response":{"ok":true}}]}]`)
	require.NoError(t, err)
	require.Len(t, outputMessages, 2)
	require.Equal(t, model.RoleAssistant, outputMessages[0].Role)
	require.Equal(t, otelPartTypeReasoning, outputMessages[0].Parts[0].Type)
	require.Equal(t, model.RoleTool, outputMessages[1].Role)
	require.Equal(t, otelPartTypeToolCallResponse, outputMessages[1].Parts[0].Type)
}

func TestTelemetryOutputMessageFromChoice_UsesDeltaFallback(t *testing.T) {
	msg := telemetryOutputMessageFromChoice(model.Choice{
		Delta: model.Message{
			Content: "streamed",
		},
	})

	require.Equal(t, model.RoleAssistant, msg.Role)
	require.Empty(t, msg.FinishReason)
	require.Len(t, msg.Parts, 1)
	require.Equal(t, otelPartTypeText, msg.Parts[0].Type)
	require.Equal(t, "streamed", msg.Parts[0].Content)
}

func TestTelemetryPartsFromContentParts_SkipsInvalidParts(t *testing.T) {
	parts := telemetryPartsFromContentParts([]model.ContentPart{
		{Type: model.ContentTypeText},
		{Type: model.ContentTypeImage},
		{Type: model.ContentTypeImage, Image: &model.Image{}},
		{Type: model.ContentTypeAudio},
		{Type: model.ContentTypeAudio, Audio: &model.Audio{}},
		{Type: model.ContentTypeFile},
		{Type: model.ContentTypeFile, File: &model.File{}},
		{Type: model.ContentType("unknown")},
	})

	require.Empty(t, parts)

	part, ok := telemetryPartFromImage(nil)
	require.False(t, ok)
	require.Empty(t, part)

	part, ok = telemetryPartFromFile(nil)
	require.False(t, ok)
	require.Empty(t, part)
}

func TestToolResponseRawMessage_CompositePayload(t *testing.T) {
	text := "part text"
	raw := toolResponseRawMessage(model.Message{
		Content:          `{"status":"ok"}`,
		ReasoningContent: "because",
		ContentParts: []model.ContentPart{{
			Type: model.ContentTypeText,
			Text: &text,
		}},
		ToolCalls: []model.ToolCall{{
			ID: " call-1 ",
			Function: model.FunctionDefinitionParam{
				Name:      " lookup ",
				Arguments: []byte("not json"),
			},
		}},
	})

	require.JSONEq(t, `{
		"content": {"status":"ok"},
		"parts": [{"type":"text","content":"part text"}],
		"reasoning": "because",
		"tool_calls": [{
			"type":"tool_call",
			"id":"call-1",
			"name":"lookup",
			"arguments":"not json"
		}]
	}`, string(raw))

	raw = toolResponseRawMessage(model.Message{
		Content:          "plain",
		ReasoningContent: "because",
	})
	require.JSONEq(t, `{"content":"plain","reasoning":"because"}`, string(raw))
}

func TestTelemetryPartFromToolCallResponse_RejectsEmptyPayload(t *testing.T) {
	part, ok := telemetryPartFromToolCallResponse(model.Message{
		Role:     model.RoleTool,
		ToolID:   "   ",
		ToolName: "lookup",
	})
	require.False(t, ok)
	require.Equal(t, OTelMessagePart{}, part)
	require.False(t, shouldUseToolCallResponsePart(model.Message{
		Role:     model.RoleTool,
		ToolID:   "   ",
		ToolName: "lookup",
	}))

	parts := telemetryPartsFromModelMessage(model.Message{
		Role:     model.RoleTool,
		ToolID:   "   ",
		ToolName: "lookup",
	})
	require.Empty(t, parts)
}

func TestTelemetryPartFromToolCallResponse_AcceptsIDOrResponse(t *testing.T) {
	part, ok := telemetryPartFromToolCallResponse(model.Message{
		Role:    model.RoleTool,
		ToolID:  " call-1 ",
		Content: "   ",
	})
	require.True(t, ok)
	require.Equal(t, otelPartTypeToolCallResponse, part.Type)
	require.Equal(t, "call-1", part.ID)
	require.Nil(t, part.Response)
	require.True(t, shouldUseToolCallResponsePart(model.Message{
		Role:    model.RoleTool,
		ToolID:  " call-1 ",
		Content: "   ",
	}))

	part, ok = telemetryPartFromToolCallResponse(model.Message{
		Role:    model.RoleTool,
		Content: `{"status":"ok"}`,
	})
	require.True(t, ok)
	require.Empty(t, part.ID)
	require.JSONEq(t, `{"status":"ok"}`, string(part.Response))
}

func TestRawJSONOrJSONStringAndJSONValueOrString(t *testing.T) {
	require.Nil(t, rawJSONOrJSONString([]byte("  ")))
	require.JSONEq(t, `{"ok":true}`, string(rawJSONOrJSONString([]byte(` {"ok":true} `))))

	raw := rawJSONOrJSONString([]byte("  not json\n"))
	var got string
	require.NoError(t, json.Unmarshal(raw, &got))
	require.Equal(t, "  not json\n", got)

	require.Equal(t, "", jsonValueOrString([]byte(" ")))
	require.Equal(t, float64(1), jsonValueOrString([]byte("1")))
	require.Equal(t, "  not json\n", jsonValueOrString([]byte("  not json\n")))
}

func TestTelemetryMIMEAndModalityHelpers(t *testing.T) {
	require.Equal(t, otelModalityFile, modalityFromMIMEType(""))
	require.Equal(t, otelModalityImage, modalityFromMIMEType("image/png"))
	require.Equal(t, otelModalityAudio, modalityFromMIMEType("audio/wav"))
	require.Equal(t, otelModalityVideo, modalityFromMIMEType("video/mp4"))

	require.Equal(t, "", imageMIMEType(nil))
	require.Equal(t, "image/jpeg", imageMIMEType(&model.Image{Format: ".JPG"}))
	require.Equal(t, "image/png", imageMIMEType(&model.Image{URL: "https://example.com/a/picture.png?x=1"}))
	require.Equal(t, "", mimeTypeFromURL(" "))

	require.Equal(t, "image/gif", normalizeFormatAsMIME("gif", "image"))
	require.Equal(t, "image/webp", normalizeFormatAsMIME("webp", "image"))
	require.Equal(t, "image/bmp", normalizeFormatAsMIME("bmp", "image"))
	require.Equal(t, "image/tiff", normalizeFormatAsMIME("tif", "image"))
	require.Equal(t, "image/svg+xml", normalizeFormatAsMIME("svg", "image"))
	require.Equal(t, "audio/wav", normalizeFormatAsMIME("wav", "audio"))
	require.Equal(t, "audio/mp4", normalizeFormatAsMIME("m4a", "audio"))
	require.Equal(t, "audio/ogg", normalizeFormatAsMIME("ogg", "audio"))
	require.Equal(t, "", normalizeFormatAsMIME("png", "file"))

	require.Equal(t, "application/json", mimeTypeFromName("data.json"))
	require.Equal(t, "", mimeTypeFromName("no-extension"))

	modality, mimeType := fileMetadata(nil)
	require.Equal(t, otelModalityFile, modality)
	require.Empty(t, mimeType)
}

func TestTelemetryPartFromFile_InfersMetadataFromName(t *testing.T) {
	part, ok := telemetryPartFromFile(&model.File{
		Name: "photo.jpeg",
		Data: []byte("image"),
	})

	require.True(t, ok)
	require.Equal(t, otelPartTypeBlob, part.Type)
	require.Equal(t, otelModalityImage, part.Modality)
	require.Equal(t, "image/jpeg", part.MIMEType)
	require.Equal(t, base64.StdEncoding.EncodeToString([]byte("image")), part.Content)
	require.Equal(t, "photo.jpeg", part.Filename)
}
