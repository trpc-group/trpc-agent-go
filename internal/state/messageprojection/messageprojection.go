//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package messageprojection shares invocation-scoped message projection
// results across framework layers.
package messageprojection

import (
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

const currentUserKey = "__trpc_agent_internal_projected_current_user_message__"

type currentUserProjection struct {
	merged            model.Message
	originalUserInput string
	contentPrefix     string
	contentSuffix     string
}

// ClearCurrentUser removes any current-user projection recorded for inv.
func ClearCurrentUser(inv *agent.Invocation) {
	if inv == nil {
		return
	}
	inv.DeleteState(currentUserKey)
}

// SetCurrentUser records how the current invocation user message was merged
// into the final model-facing message.
func SetCurrentUser(
	inv *agent.Invocation,
	current model.Message,
	merged model.Message,
) {
	if inv == nil || current.Content == "" ||
		merged.Role != model.RoleUser {
		return
	}
	currentIndex := strings.LastIndex(merged.Content, current.Content)
	if currentIndex < 0 {
		return
	}
	currentEnd := currentIndex + len(current.Content)
	inv.SetState(currentUserKey, currentUserProjection{
		merged:            merged,
		originalUserInput: inv.Message.Content,
		contentPrefix:     merged.Content[:currentIndex],
		contentSuffix:     merged.Content[currentEnd:],
	})
}

// ResolveCurrentUser returns the recorded model-facing message with its
// current-user portion updated to userInput. It returns false when msg no
// longer matches the recorded projection.
func ResolveCurrentUser(
	inv *agent.Invocation,
	msg model.Message,
	userInput string,
) (model.Message, bool) {
	projection, ok := agent.GetStateValue[currentUserProjection](
		inv,
		currentUserKey,
	)
	if !ok || !model.MessagesEqual(projection.merged, msg) {
		return model.Message{}, false
	}
	if userInput == projection.originalUserInput {
		return msg, true
	}
	resolved := msg
	resolved.Content = projection.contentPrefix + userInput +
		projection.contentSuffix
	return resolved, true
}
