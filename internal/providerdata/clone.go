//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package providerdata

import (
	"bytes"

	"trpc.group/trpc-go/trpc-agent-go/internal/jsonmap"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// CloneMessage isolates a native history record and its mutable projections.
// Messages without ProviderData keep their existing ownership.
func CloneMessage(message model.Message) model.Message {
	if message.ProviderData == nil {
		return message
	}
	cloned := CloneMessageProjections(message)
	record := *message.ProviderData
	record.Payload = bytes.Clone(record.Payload)
	cloned.ProviderData = &record
	return cloned
}

// CloneMessageProjections copies the mutable projections of a native message
// whose ProviderData was already detached by Response.Clone.
func CloneMessageProjections(message model.Message) model.Message {
	if message.ProviderData == nil {
		return message
	}
	cloned := message
	cloned.ToolCalls = cloneToolCalls(message.ToolCalls)
	if message.ContentParts != nil {
		cloned.ContentParts = make([]model.ContentPart, len(message.ContentParts))
		for i, part := range message.ContentParts {
			cloned.ContentParts[i] = cloneContentPart(part)
		}
	}
	return cloned
}

// ToolCallsForExecution detaches native call arguments from their authoritative
// history before callbacks or tools can edit them. Legacy calls retain their
// existing ownership and mutation behavior.
func ToolCallsForExecution(message model.Message) []model.ToolCall {
	if message.ProviderData == nil {
		return message.ToolCalls
	}
	return cloneToolCalls(message.ToolCalls)
}

func cloneToolCalls(calls []model.ToolCall) []model.ToolCall {
	if calls == nil {
		return nil
	}
	cloned := make([]model.ToolCall, len(calls))
	for i, call := range calls {
		cloned[i] = call
		cloned[i].Function.Arguments = bytes.Clone(call.Function.Arguments)
		cloned[i].ExtraFields = jsonmap.Clone(call.ExtraFields)
		if call.Index != nil {
			index := *call.Index
			cloned[i].Index = &index
		}
	}
	return cloned
}

func cloneContentPart(part model.ContentPart) model.ContentPart {
	cloned := part
	if part.Text != nil {
		text := *part.Text
		cloned.Text = &text
	}
	if part.Image != nil {
		image := *part.Image
		image.Data = bytes.Clone(image.Data)
		cloned.Image = &image
	}
	if part.Audio != nil {
		audio := *part.Audio
		audio.Data = bytes.Clone(audio.Data)
		cloned.Audio = &audio
	}
	if part.Video != nil {
		video := *part.Video
		video.Data = bytes.Clone(video.Data)
		cloned.Video = &video
	}
	if part.File != nil {
		file := *part.File
		file.Data = bytes.Clone(file.Data)
		cloned.File = &file
	}
	if part.ContentRef != nil {
		ref := *part.ContentRef
		cloned.ContentRef = &ref
	}
	return cloned
}
