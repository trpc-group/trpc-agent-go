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
	"fmt"
	"unicode/utf8"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

const (
	maxTelemetryStringBytes  = 64 * 1024
	maxTelemetryRawJSONBytes = 64 * 1024
	maxTelemetryBinaryBytes  = 8 * 1024
)

func truncateTelemetryModelMessages(messages []model.Message) []model.Message {
	if len(messages) == 0 {
		return nil
	}
	out := make([]model.Message, len(messages))
	for i, msg := range messages {
		out[i] = truncateTelemetryModelMessage(msg)
	}
	return out
}

func truncateTelemetryModelChoices(choices []model.Choice) []model.Choice {
	if len(choices) == 0 {
		return nil
	}
	out := make([]model.Choice, len(choices))
	for i, choice := range choices {
		out[i] = choice
		out[i].Message = truncateTelemetryModelMessage(choice.Message)
		out[i].Delta = truncateTelemetryModelMessage(choice.Delta)
	}
	return out
}

func truncateTelemetryModelMessage(msg model.Message) model.Message {
	msg.Content = truncateTelemetryString(msg.Content)
	msg.ContentParts = truncateTelemetryContentParts(msg.ContentParts)
	msg.ToolCalls = truncateTelemetryToolCalls(msg.ToolCalls)
	msg.ReasoningContent = truncateTelemetryString(msg.ReasoningContent)
	return msg
}

func telemetryRequestForMarshal(req *model.Request) *model.Request {
	if req == nil {
		return nil
	}
	clone := *req
	clone.Messages = truncateTelemetryModelMessages(req.Messages)
	return &clone
}

func telemetryResponseForMarshal(rsp *model.Response) *model.Response {
	if rsp == nil {
		return nil
	}
	clone := *rsp
	clone.Choices = truncateTelemetryModelChoices(rsp.Choices)
	return &clone
}

func truncateTelemetryString(s string) string {
	if len(s) <= maxTelemetryStringBytes {
		return s
	}
	prefix := validUTF8Prefix(s, maxTelemetryStringBytes)
	return prefix + telemetryTruncatedMarker(len(s))
}

func telemetryTruncatedMarker(size int) string {
	return fmt.Sprintf("...<truncated: %d bytes>", size)
}

func validUTF8Prefix(s string, limit int) string {
	if limit >= len(s) {
		return s
	}
	prefix := s[:limit]
	for len(prefix) > 0 && !utf8.ValidString(prefix) {
		prefix = prefix[:len(prefix)-1]
	}
	return prefix
}

func truncateTelemetryContentParts(parts []model.ContentPart) []model.ContentPart {
	if len(parts) == 0 {
		return nil
	}
	out := make([]model.ContentPart, len(parts))
	for i, part := range parts {
		out[i] = part
		if part.Text != nil {
			text := truncateTelemetryString(*part.Text)
			out[i].Text = &text
		}
		if part.Image != nil {
			image := *part.Image
			image.URL = truncateTelemetryString(image.URL)
			image.Data = truncateTelemetryBytes(image.Data, maxTelemetryBinaryBytes)
			image.Detail = truncateTelemetryString(image.Detail)
			out[i].Image = &image
		}
		if part.Audio != nil {
			audio := *part.Audio
			audio.Data = truncateTelemetryBytes(audio.Data, maxTelemetryBinaryBytes)
			out[i].Audio = &audio
		}
		if part.File != nil {
			file := *part.File
			file.Name = truncateTelemetryString(file.Name)
			file.URL = truncateTelemetryString(file.URL)
			file.Data = truncateTelemetryBytes(file.Data, maxTelemetryBinaryBytes)
			file.FileID = truncateTelemetryString(file.FileID)
			out[i].File = &file
		}
	}
	return out
}

func truncateTelemetryBytes(b []byte, limit int) []byte {
	if len(b) <= limit {
		return b
	}
	return b[:limit]
}

func truncateTelemetryToolCalls(toolCalls []model.ToolCall) []model.ToolCall {
	if len(toolCalls) == 0 {
		return nil
	}
	out := make([]model.ToolCall, len(toolCalls))
	for i, toolCall := range toolCalls {
		out[i] = toolCall
		out[i].Function.Arguments = truncateTelemetryRawBytes(toolCall.Function.Arguments)
	}
	return out
}

func truncateTelemetryRawBytes(raw []byte) []byte {
	if len(raw) <= maxTelemetryRawJSONBytes {
		return raw
	}
	return []byte(telemetryTruncatedMarker(len(raw)))
}
