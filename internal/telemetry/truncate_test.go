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
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestTelemetryMarshalHelpersPreserveEmptySlices(t *testing.T) {
	require.Nil(t, telemetryRequestForMarshal(nil))
	require.Nil(t, telemetryResponseForMarshal(nil))

	req := &model.Request{Messages: []model.Message{}}
	reqJSON, err := json.Marshal(telemetryRequestForMarshal(req))
	require.NoError(t, err)
	require.Contains(t, string(reqJSON), `"messages":[]`)
	require.NotContains(t, string(reqJSON), `"messages":null`)

	rsp := &model.Response{Choices: []model.Choice{}}
	rspJSON, err := json.Marshal(telemetryResponseForMarshal(rsp))
	require.NoError(t, err)
	require.Contains(t, string(rspJSON), `"choices":[]`)
	require.NotContains(t, string(rspJSON), `"choices":null`)
}

func TestTruncateTelemetrySliceHelpersPreserveNilAndEmpty(t *testing.T) {
	require.Nil(t, truncateTelemetryModelMessages(nil))
	require.NotNil(t, truncateTelemetryModelMessages([]model.Message{}))
	require.Empty(t, truncateTelemetryModelMessages([]model.Message{}))

	require.Nil(t, truncateTelemetryModelChoices(nil))
	require.NotNil(t, truncateTelemetryModelChoices([]model.Choice{}))
	require.Empty(t, truncateTelemetryModelChoices([]model.Choice{}))

	require.Nil(t, truncateTelemetryContentParts(nil))
	require.NotNil(t, truncateTelemetryContentParts([]model.ContentPart{}))
	require.Empty(t, truncateTelemetryContentParts([]model.ContentPart{}))

	require.Nil(t, truncateTelemetryToolCalls(nil))
	require.NotNil(t, truncateTelemetryToolCalls([]model.ToolCall{}))
	require.Empty(t, truncateTelemetryToolCalls([]model.ToolCall{}))
}

func TestTruncateTelemetryModelMessageBoundsNestedPayloads(t *testing.T) {
	largeString := strings.Repeat("x", maxTelemetryStringBytes+1)
	largeBinary := bytes.Repeat([]byte("b"), maxTelemetryBinaryBytes+1)
	largeRaw := []byte(strings.Repeat("r", maxTelemetryRawJSONBytes+1))
	text := largeString
	msg := model.Message{
		Role:             model.RoleAssistant,
		Content:          largeString,
		ReasoningContent: largeString,
		ContentParts: []model.ContentPart{
			{Type: model.ContentTypeText, Text: &text},
			{Type: model.ContentTypeImage, Image: &model.Image{
				URL:    largeString,
				Data:   append([]byte(nil), largeBinary...),
				Detail: largeString,
				Format: "png",
			}},
			{Type: model.ContentTypeAudio, Audio: &model.Audio{
				Data:   append([]byte(nil), largeBinary...),
				Format: "wav",
			}},
			{Type: model.ContentTypeFile, File: &model.File{
				Name:     largeString,
				URL:      largeString,
				Data:     append([]byte(nil), largeBinary...),
				FileID:   largeString,
				MimeType: "application/pdf",
			}},
		},
		ToolCalls: []model.ToolCall{{
			ID: "call-1",
			Function: model.FunctionDefinitionParam{
				Name:      "tool",
				Arguments: largeRaw,
			},
			ExtraFields: map[string]any{
				"thought_signature": largeString,
			},
		}},
	}

	got := truncateTelemetryModelMessage(msg)
	require.LessOrEqual(t, len(got.Content), maxTelemetryStringBytes)
	require.Contains(t, got.Content, "truncated")
	require.LessOrEqual(t, len(got.ReasoningContent), maxTelemetryStringBytes)
	require.Contains(t, got.ReasoningContent, "truncated")

	require.Len(t, got.ContentParts, 4)
	require.NotNil(t, got.ContentParts[0].Text)
	require.LessOrEqual(t, len(*got.ContentParts[0].Text), maxTelemetryStringBytes)
	require.Contains(t, *got.ContentParts[0].Text, "truncated")

	require.NotNil(t, got.ContentParts[1].Image)
	require.LessOrEqual(t, len(got.ContentParts[1].Image.URL), maxTelemetryStringBytes)
	require.Contains(t, got.ContentParts[1].Image.URL, "truncated")
	require.Len(t, got.ContentParts[1].Image.Data, maxTelemetryBinaryBytes)
	require.LessOrEqual(t, len(got.ContentParts[1].Image.Detail), maxTelemetryStringBytes)
	require.Contains(t, got.ContentParts[1].Image.Detail, "truncated")

	require.NotNil(t, got.ContentParts[2].Audio)
	require.Len(t, got.ContentParts[2].Audio.Data, maxTelemetryBinaryBytes)

	require.NotNil(t, got.ContentParts[3].File)
	require.LessOrEqual(t, len(got.ContentParts[3].File.Name), maxTelemetryStringBytes)
	require.Contains(t, got.ContentParts[3].File.Name, "truncated")
	require.LessOrEqual(t, len(got.ContentParts[3].File.URL), maxTelemetryStringBytes)
	require.Contains(t, got.ContentParts[3].File.URL, "truncated")
	require.Len(t, got.ContentParts[3].File.Data, maxTelemetryBinaryBytes)
	require.LessOrEqual(t, len(got.ContentParts[3].File.FileID), maxTelemetryStringBytes)
	require.Contains(t, got.ContentParts[3].File.FileID, "truncated")

	require.Len(t, got.ToolCalls, 1)
	require.Contains(t, string(got.ToolCalls[0].Function.Arguments), "truncated")
	require.Nil(t, got.ToolCalls[0].ExtraFields)
	require.Equal(t, largeString, msg.Content)
	require.Equal(t, largeString, msg.ReasoningContent)
	require.Equal(t, largeBinary, msg.ContentParts[1].Image.Data)
	require.Equal(t, largeRaw, msg.ToolCalls[0].Function.Arguments)
	require.Equal(t, largeString, msg.ToolCalls[0].ExtraFields["thought_signature"])
}

func TestTruncateTelemetryBoundaries(t *testing.T) {
	require.Equal(t, "short", truncateTelemetryString("short"))

	raw := []byte(`{"ok":true}`)
	require.Equal(t, raw, truncateTelemetryRawBytes(raw))
	require.Contains(t, string(truncateTelemetryRawBytes([]byte(strings.Repeat("x", maxTelemetryRawJSONBytes+1)))), "truncated")

	binary := []byte("abc")
	require.Equal(t, binary, truncateTelemetryBytes(binary, len(binary)))
	require.Equal(t, []byte("ab"), truncateTelemetryBytes(binary, 2))

	require.Equal(t, "abc", validUTF8Prefix("abc", 4))
	require.Equal(t, "", validUTF8Prefix(string([]byte{0xe4, 0xbd, 0xa0}), 1))
	prefix := validUTF8Prefix("hello"+string([]rune{0x4e16}), len("hello")+1)
	require.True(t, utf8.ValidString(prefix))
	require.Equal(t, "hello", prefix)
}
