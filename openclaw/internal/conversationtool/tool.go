//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package conversationtool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/conversation"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	toolConversationHistory = "conversation_history"

	defaultTurnLimit = 12
	maxTurnLimit     = 50
)

var (
	errToolNotInInvocation = errors.New(
		"conversation_history: current session is unavailable",
	)
)

// Tool inspects the current conversation session.
type Tool struct{}

// NewTool creates a conversation history inspection tool.
func NewTool() *Tool {
	return &Tool{}
}

func (t *Tool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name: toolConversationHistory,
		Description: "Inspect the current conversation session with " +
			"speaker attribution. Use this for questions about " +
			"what was discussed, who said something, recent " +
			"tasks, or current-chat history. Prefer this over " +
			"long-term memory tools when the user asks about " +
			"the active chat session.",
		InputSchema: &tool.Schema{
			Type: "object",
			Properties: map[string]*tool.Schema{
				"limit": {
					Type: "number",
					Description: "Optional number of recent turns " +
						"to return. Defaults to 12 and is capped " +
						"at 50.",
				},
				"include_system": {
					Type: "boolean",
					Description: "When true, include persisted " +
						"system turns such as previous summaries.",
				},
			},
		},
	}
}

type toolInput struct {
	Limit         *int `json:"limit,omitempty"`
	IncludeSystem bool `json:"include_system,omitempty"`
}

func (t *Tool) Call(ctx context.Context, args []byte) (any, error) {
	var in toolInput
	if len(args) > 0 {
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, fmt.Errorf("invalid args: %w", err)
		}
	}

	inv, ok := agent.InvocationFromContext(ctx)
	if !ok || inv == nil || inv.Session == nil {
		return nil, errToolNotInInvocation
	}

	limit := normalizeLimit(in.Limit)
	labelOverrides := conversationLabelOverrides(inv)
	turns := conversation.BuildTurns(
		inv.Session,
		conversation.TurnOptions{
			Limit:          limit,
			IncludeSystem:  in.IncludeSystem,
			LabelOverrides: labelOverrides,
		},
	)
	transcript := conversation.FormatTurns(turns)

	return map[string]any{
		"session_id":   inv.Session.ID,
		"session_user": inv.Session.UserID,
		"turn_count":   len(turns),
		"transcript":   transcript,
		"turns":        turns,
	}, nil
}

func conversationLabelOverrides(
	inv *agent.Invocation,
) map[string]string {
	if inv == nil {
		return nil
	}
	annotation, ok := conversation.AnnotationFromRuntimeState(
		inv.RunOptions.RuntimeState,
	)
	if !ok {
		return nil
	}
	return annotation.ActorLabels
}

func normalizeLimit(raw *int) int {
	if raw == nil || *raw <= 0 {
		return defaultTurnLimit
	}
	if *raw > maxTurnLimit {
		return maxTurnLimit
	}
	return *raw
}
