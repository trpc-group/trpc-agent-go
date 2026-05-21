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

import "trpc.group/trpc-go/trpc-agent-go/model"

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
