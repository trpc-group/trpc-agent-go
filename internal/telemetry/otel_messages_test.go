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

func TestOTelPartsFromContentParts_Multimodal(t *testing.T) {
	text := "hello"
	parts := otelPartsFromContentParts([]model.ContentPart{
		{Type: model.ContentTypeText},
		{Type: model.ContentTypeText, Text: &text},
		{Type: model.ContentTypeImage},
		{Type: model.ContentTypeImage, Image: &model.Image{
			URL:    "https://example.com/picture.jpeg?size=large",
			Format: "jpg",
			Detail: " high ",
		}},
		{Type: model.ContentTypeImage, Image: &model.Image{
			Data:   []byte("image-bytes"),
			Format: ".png",
			Detail: "low",
		}},
		{Type: model.ContentTypeAudio},
		{Type: model.ContentTypeAudio, Audio: &model.Audio{
			Data:   []byte("audio-bytes"),
			Format: "m4a",
		}},
		{Type: model.ContentTypeFile},
		{Type: model.ContentTypeFile, File: &model.File{
			FileID:   " file-123 ",
			Name:     " photo.jpg ",
			MimeType: " IMAGE/PNG ",
		}},
		{Type: model.ContentTypeFile, File: &model.File{
			Name: "clip.mp4",
			Data: []byte("video-bytes"),
		}},
		{Type: model.ContentType("unknown")},
	})

	require.Len(t, parts, 6)
	require.Equal(t, OTelMessagePart{Type: otelPartTypeText, Content: text}, parts[0])
	require.Equal(t, otelPartTypeURI, parts[1].Type)
	require.Equal(t, otelModalityImage, parts[1].Modality)
	require.Equal(t, "image/jpeg", parts[1].MIMEType)
	require.Equal(t, "https://example.com/picture.jpeg?size=large", parts[1].URI)
	require.Equal(t, "high", parts[1].Detail)
	require.Equal(t, otelPartTypeBlob, parts[2].Type)
	require.Equal(t, otelModalityImage, parts[2].Modality)
	require.Equal(t, "image/png", parts[2].MIMEType)
	require.Equal(t, base64.StdEncoding.EncodeToString([]byte("image-bytes")), parts[2].Content)
	require.Equal(t, "low", parts[2].Detail)
	require.Equal(t, otelPartTypeBlob, parts[3].Type)
	require.Equal(t, otelModalityAudio, parts[3].Modality)
	require.Equal(t, "audio/mp4", parts[3].MIMEType)
	require.Equal(t, base64.StdEncoding.EncodeToString([]byte("audio-bytes")), parts[3].Content)
	require.Equal(t, otelPartTypeFile, parts[4].Type)
	require.Equal(t, otelModalityImage, parts[4].Modality)
	require.Equal(t, "image/png", parts[4].MIMEType)
	require.Equal(t, "file-123", parts[4].FileID)
	require.Equal(t, "photo.jpg", parts[4].Filename)
	require.Equal(t, otelPartTypeBlob, parts[5].Type)
	require.Equal(t, otelModalityVideo, parts[5].Modality)
	require.Equal(t, "video/mp4", parts[5].MIMEType)
	require.Equal(t, base64.StdEncoding.EncodeToString([]byte("video-bytes")), parts[5].Content)
	require.Equal(t, "clip.mp4", parts[5].Filename)
}

func TestOTelToolCallAndResponseParts(t *testing.T) {
	jsonCall := otelPartFromToolCall(model.ToolCall{
		ID: " call-1 ",
		Function: model.FunctionDefinitionParam{
			Name:      " search ",
			Arguments: []byte(`{"q":"otel"}`),
		},
	})
	require.Equal(t, otelPartTypeToolCall, jsonCall.Type)
	require.Equal(t, "call-1", jsonCall.ID)
	require.Equal(t, "search", jsonCall.Name)
	require.JSONEq(t, `{"q":"otel"}`, string(jsonCall.Arguments))

	stringCall := otelPartFromToolCall(model.ToolCall{
		Function: model.FunctionDefinitionParam{Arguments: []byte("not-json")},
	})
	require.JSONEq(t, `"not-json"`, string(stringCall.Arguments))

	jsonResponse, ok := otelPartFromToolCallResponse(model.Message{
		Role:    model.RoleTool,
		ToolID:  " call-2 ",
		Content: `{"ok":true}`,
	})
	require.True(t, ok)
	require.Equal(t, otelPartTypeToolCallResponse, jsonResponse.Type)
	require.Equal(t, "call-2", jsonResponse.ID)
	require.JSONEq(t, `{"ok":true}`, string(jsonResponse.Response))

	text := "part text"
	richResponse, ok := otelPartFromToolCallResponse(model.Message{
		Role:             model.RoleTool,
		Content:          `{"content":true}`,
		ContentParts:     []model.ContentPart{{Type: model.ContentTypeText, Text: &text}},
		ReasoningContent: "because",
		ToolCalls: []model.ToolCall{{
			ID:       "nested",
			Function: model.FunctionDefinitionParam{Name: "nested_tool"},
		}},
	})
	require.True(t, ok)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(richResponse.Response, &payload))
	require.Equal(t, "because", payload["reasoning"])
	require.Contains(t, payload, "content")
	require.Contains(t, payload, "parts")
	require.Contains(t, payload, "tool_calls")

	_, ok = otelPartFromToolCallResponse(model.Message{Role: model.RoleAssistant, Content: "not a tool"})
	require.False(t, ok)
	_, ok = otelPartFromToolCallResponse(model.Message{Role: model.RoleTool})
	require.False(t, ok)
}

func TestOTelMessageHelpers(t *testing.T) {
	require.Nil(t, rawJSONOrJSONString([]byte(" \t\n ")))
	require.JSONEq(t, `{"a":1}`, string(rawJSONOrJSONString([]byte(` {"a":1} `))))
	require.JSONEq(t, `"plain"`, string(rawJSONOrJSONString([]byte("plain"))))

	require.Equal(t, "", jsonValueOrString([]byte(" ")))
	require.Equal(t, "plain", jsonValueOrString([]byte("plain")))
	jsonValue := jsonValueOrString([]byte(`{"a":1}`))
	require.IsType(t, map[string]any{}, jsonValue)

	require.Equal(t, otelModalityImage, modalityFromMIMEType("image/gif"))
	require.Equal(t, otelModalityAudio, modalityFromMIMEType("audio/wav"))
	require.Equal(t, otelModalityVideo, modalityFromMIMEType("video/mp4"))
	require.Equal(t, otelModalityFile, modalityFromMIMEType("application/pdf"))

	require.Equal(t, "", imageMIMEType(nil))
	require.Equal(t, "image/webp", imageMIMEType(&model.Image{URL: "https://example.com/a.webp"}))
	require.Equal(t, "", fileMetadataMIME(nil))
	require.Equal(t, "application/pdf", mimeTypeFromURL("https://example.com/doc.pdf?download=1"))
	require.Equal(t, "image/svg+xml", normalizeFormatAsMIME("svg", "image"))
	require.Equal(t, "image/heic", normalizeFormatAsMIME("heic", "image"))
	require.Equal(t, "audio/mpeg", normalizeFormatAsMIME("mp3", "audio"))
	require.Equal(t, "", normalizeFormatAsMIME("txt", "file"))
	require.Equal(t, "", mimeTypeFromURL(""))
	require.Equal(t, "", mimeTypeFromName("no-extension"))
}

func fileMetadataMIME(file *model.File) string {
	_, mimeType := fileMetadata(file)
	return mimeType
}

func TestMarshalOTelTelemetryChoices_UsesDeltaWhenMessageEmpty(t *testing.T) {
	finishReason := "tool_calls"
	bts, err := marshalOTelTelemetryChoices([]model.Choice{{
		Delta: model.Message{
			Role:    model.RoleAssistant,
			Content: "delta text",
		},
		FinishReason: &finishReason,
	}})
	require.NoError(t, err)

	var messages []OTelOutputMessage
	require.NoError(t, json.Unmarshal(bts, &messages))
	require.Len(t, messages, 1)
	require.Equal(t, model.RoleAssistant, messages[0].Role)
	require.Equal(t, finishReason, messages[0].FinishReason)
	require.Len(t, messages[0].Parts, 1)
	require.Equal(t, otelPartTypeText, messages[0].Parts[0].Type)
	require.Equal(t, "delta text", messages[0].Parts[0].Content)
}
