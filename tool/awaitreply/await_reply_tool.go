//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package awaitreply provides the await_user_reply framework tool.
package awaitreply

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	// ToolName is the await_user_reply framework tool name.
	ToolName = "await_user_reply"
)

// Request is the request payload for await_user_reply.
type Request struct{}

// Response is the tool response payload for await_user_reply.
type Response struct {
	// Success indicates whether the route was recorded successfully.
	Success bool `json:"success"`
	// Message describes the result.
	Message string `json:"message"`
	// AgentName is the agent that will receive the next user turn.
	AgentName string `json:"agent_name,omitempty"`
}

// Tool marks the current agent as waiting for the next user reply.
type Tool struct{}

// New creates a new await_user_reply tool.
func New() *Tool {
	return &Tool{}
}

// Declaration implements tool.Tool.
func (t *Tool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name: ToolName,
		Description: "Mark that the next user message in this session " +
			"should resume at the current agent. Use this right " +
			"before asking the user for missing information.",
		InputSchema: &tool.Schema{
			Type:       "object",
			Properties: map[string]*tool.Schema{},
		},
	}
}

// Call implements tool.CallableTool.
func (t *Tool) Call(ctx context.Context, jsonArgs []byte) (any, error) {
	if len(bytes.TrimSpace(jsonArgs)) > 0 {
		var req Request
		if err := json.Unmarshal(jsonArgs, &req); err != nil {
			return Response{
				Success: false,
				Message: fmt.Sprintf(
					"invalid request format: %v",
					err,
				),
			}, nil
		}
	}

	invocation, ok := agent.InvocationFromContext(ctx)
	if !ok || invocation == nil {
		return Response{
			Success: false,
			Message: "await_user_reply requires an invocation context",
		}, nil
	}
	if err := agent.MarkAwaitingUserReply(invocation); err != nil {
		return Response{
			Success: false,
			Message: err.Error(),
		}, nil
	}
	return Response{
		Success:   true,
		Message:   "The next user message will resume at this agent.",
		AgentName: invocation.AgentName,
	}, nil
}
