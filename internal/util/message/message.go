//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package message provides shared helpers for model messages.
package message

import (
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

// TextContent returns the textual user input of msg. Non-empty Content is
// returned unchanged. Otherwise non-empty text ContentParts are joined in
// order with newlines. Nil, empty, and non-text parts are ignored so a
// media-only payload does not fabricate text.
func TextContent(msg model.Message) string {
	if msg.Content != "" {
		return msg.Content
	}
	parts := make([]string, 0, len(msg.ContentParts))
	for _, part := range msg.ContentParts {
		if part.Type != model.ContentTypeText || part.Text == nil || *part.Text == "" {
			continue
		}
		parts = append(parts, *part.Text)
	}
	return strings.Join(parts, "\n")
}

// IsEmptyAssistantMessage reports whether an assistant message has no visible
// content and no tool calls. Reasoning content is metadata for provider replay;
// by itself it is not a valid assistant history payload for strict chat APIs.
func IsEmptyAssistantMessage(msg model.Message) bool {
	if msg.Role != model.RoleAssistant {
		return false
	}
	return msg.Content == "" &&
		len(msg.ContentParts) == 0 &&
		len(msg.ToolCalls) == 0
}
