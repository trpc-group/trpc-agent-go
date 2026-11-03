//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package toolfilter

import (
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

type conversationLine struct {
	role model.Role
	text string
}

// decisionConversation uses the default event prefix filter. Session events
// are not necessarily identical to the final model request's message history.
func decisionConversation(inv *agent.Invocation) (string, string) {
	if inv == nil {
		return "", ""
	}
	var lines []conversationLine
	currentIncluded := false
	if inv.Session != nil {
		for _, evt := range inv.Session.GetEvents() {
			if evt.Response == nil || evt.IsPartial || len(evt.Choices) == 0 || !evt.Filter(inv.GetEventFilterKey()) {
				continue
			}
			msg := evt.Choices[0].Message
			if (msg.Role != model.RoleUser && msg.Role != model.RoleAssistant) || msg.ToolID != "" {
				continue
			}
			if text := conversationText(msg); text != "" {
				lines = append(lines, conversationLine{role: msg.Role, text: text})
				currentIncluded = evt.InvocationID == inv.InvocationID && msg.Role == model.RoleUser && model.MessagesEqual(msg, inv.Message)
			}
		}
	}
	current := conversationText(inv.Message)
	if current != "" && !currentIncluded {
		lines = append(lines, conversationLine{role: model.RoleUser, text: current})
	}
	return fitConversation(lines, false), fitConversation(lines, true)
}

func conversationText(msg model.Message) string {
	parts := make([]string, 0, 1+len(msg.ContentParts))
	if text := strings.TrimSpace(msg.Content); text != "" {
		parts = append(parts, text)
	}
	for _, part := range msg.ContentParts {
		if part.Type == model.ContentTypeText && part.Text != nil {
			if text := strings.TrimSpace(*part.Text); text != "" {
				parts = append(parts, text)
			}
		}
	}
	return strings.Join(parts, "\n")
}

func fitConversation(lines []conversationLine, usersOnly bool) string {
	remaining := stateClipLen
	selected := make([]string, 0, len(lines))
	for i := len(lines) - 1; i >= 0 && remaining > 0; i-- {
		line := lines[i]
		if usersOnly && line.role != model.RoleUser {
			continue
		}
		prefix := string(line.role) + ": "
		if usersOnly {
			prefix = ""
		}
		if len(selected) > 0 {
			remaining--
		}
		if remaining <= len(prefix) {
			break
		}
		text := []rune(line.text)
		originalLen := len(text)
		room := remaining - len(prefix)
		if len(text) > room {
			text = text[:room]
		}
		selected = append(selected, prefix+string(text))
		remaining -= len(prefix) + len(text)
		if len(text) < originalLen {
			break
		}
	}
	for i, j := 0, len(selected)-1; i < j; i, j = i+1, j-1 {
		selected[i], selected[j] = selected[j], selected[i]
	}
	return strings.Join(selected, "\n")
}
