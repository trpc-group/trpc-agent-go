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
