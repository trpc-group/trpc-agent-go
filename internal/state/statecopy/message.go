//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package statecopy provides deep-copy helpers for state snapshot boundaries.
package statecopy

import (
	"trpc.group/trpc-go/trpc-agent-go/internal/jsonmap"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// Message returns a deep copy of message.
func Message(message model.Message) model.Message {
	cloned := message
	cloned.ContentParts = cloneContentParts(message.ContentParts)
	cloned.ToolCalls = cloneToolCalls(message.ToolCalls)
	return cloned
}

// Messages returns deep copies of messages and preserves a nil input.
func Messages(messages []model.Message) []model.Message {
	if messages == nil {
		return nil
	}
	cloned := make([]model.Message, len(messages))
	for i := range messages {
		cloned[i] = Message(messages[i])
	}
	return cloned
}

func cloneContentParts(parts []model.ContentPart) []model.ContentPart {
	if parts == nil {
		return nil
	}
	cloned := make([]model.ContentPart, len(parts))
	for i := range parts {
		cloned[i] = cloneContentPart(parts[i])
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
		image.Data = append([]byte(nil), part.Image.Data...)
		cloned.Image = &image
	}
	if part.Audio != nil {
		audio := *part.Audio
		audio.Data = append([]byte(nil), part.Audio.Data...)
		cloned.Audio = &audio
	}
	if part.Video != nil {
		video := *part.Video
		video.Data = append([]byte(nil), part.Video.Data...)
		cloned.Video = &video
	}
	if part.File != nil {
		file := *part.File
		file.Data = append([]byte(nil), part.File.Data...)
		cloned.File = &file
	}
	if part.ContentRef != nil {
		contentRef := *part.ContentRef
		cloned.ContentRef = &contentRef
	}
	return cloned
}

func cloneToolCalls(toolCalls []model.ToolCall) []model.ToolCall {
	if toolCalls == nil {
		return nil
	}
	cloned := make([]model.ToolCall, len(toolCalls))
	for i := range toolCalls {
		cloned[i] = toolCalls[i]
		cloned[i].Function.Arguments = append(
			[]byte(nil),
			toolCalls[i].Function.Arguments...,
		)
		if toolCalls[i].Index != nil {
			index := *toolCalls[i].Index
			cloned[i].Index = &index
		}
		cloned[i].ExtraFields = jsonmap.Clone(toolCalls[i].ExtraFields)
	}
	return cloned
}
