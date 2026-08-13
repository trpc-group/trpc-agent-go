//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package graph

import (
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// MessageOp interface defines operations that can be applied to message arrays.
// This provides atomic combination of multiple operations for state updates.
type MessageOp interface {
	Apply([]model.Message) []model.Message
}

// AppendMessages provides append capability for atomic combination.
// It can also be used for backward compatibility in unified expression.
type AppendMessages struct{ Items []model.Message }

// Apply implements the MessageOp interface.
func (op AppendMessages) Apply(dst []model.Message) []model.Message {
	return append(dst, op.Items...)
}

// ReplaceLastUser replaces the last user message in the durable history.
// If no user message is found, it falls back to appending a new user message.
type ReplaceLastUser struct{ Content string }

// Apply implements the MessageOp interface.
func (op ReplaceLastUser) Apply(dst []model.Message) []model.Message {
	for i := len(dst) - 1; i >= 0; i-- {
		if dst[i].Role == model.RoleUser {
			// Replace the content while preserving other fields.
			dst[i] = model.Message{
				Role:             model.RoleUser,
				Content:          op.Content,
				ContentParts:     dst[i].ContentParts,
				ToolID:           dst[i].ToolID,
				ToolName:         dst[i].ToolName,
				ToolCalls:        dst[i].ToolCalls,
				ReasoningContent: dst[i].ReasoningContent,
			}
			return dst
		}
	}
	// No user message at the end of history, append a new one.
	return append(dst, model.NewUserMessage(op.Content))
}

// RemoveAllMessages clears all messages for full rebuild scenarios.
// Used sparingly: for reordering/trimming when starting fresh.
type RemoveAllMessages struct{}

// Apply implements the MessageOp interface.
func (RemoveAllMessages) Apply(_ []model.Message) []model.Message { return nil }

// replaceLastUserMessage replaces the last user message with Message.
// If no user message is found, it appends Message.
// Unlike ReplaceLastUser, it persists the full rewritten message so
// multimodal ContentParts stay consistent with the model request.
type replaceLastUserMessage struct{ Message model.Message }

// Apply implements the MessageOp interface.
func (op replaceLastUserMessage) Apply(dst []model.Message) []model.Message {
	for i := len(dst) - 1; i >= 0; i-- {
		if dst[i].Role == model.RoleUser {
			dst[i] = op.Message
			return dst
		}
	}
	return append(dst, op.Message)
}

// rewriteUserMessageText rewrites msg's textual content to userInput.
// Messages without ContentParts use the legacy mismatch path:
// model.NewUserMessage(userInput) (Content only; metadata is not copied).
// Messages with ContentParts keep non-text parts and all non-textual
// metadata, collapse textual parts to exactly userInput (no stale text),
// and add a text part when none existed. userInput must be non-empty.
func rewriteUserMessageText(msg model.Message, userInput string) model.Message {
	if len(msg.ContentParts) == 0 {
		return model.NewUserMessage(userInput)
	}
	text := userInput
	textPart := model.ContentPart{
		Type: model.ContentTypeText,
		Text: &text,
	}
	parts := make([]model.ContentPart, 0, len(msg.ContentParts))
	added := false
	for _, part := range msg.ContentParts {
		if part.Type == model.ContentTypeText {
			if !added {
				parts = append(parts, textPart)
				added = true
			}
			continue
		}
		parts = append(parts, part)
	}
	if !added {
		parts = append([]model.ContentPart{textPart}, parts...)
	}
	out := msg
	out.Role = model.RoleUser
	out.Content = ""
	out.ContentParts = parts
	return out
}
