//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package plugin

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// AfterToolMessagesArgs contains the model-facing context available after a
// tool call has been executed and converted into tool result messages.
//
// The Messages slice is a point-in-time view of the next conversation state:
// request messages, the assistant tool-call message, then current tool result
// messages. Callbacks should treat it as read-only and return
// AfterToolMessagesResult to replace the current tool result messages.
type AfterToolMessagesArgs struct {
	// Invocation is the active invocation.
	Invocation *agent.Invocation
	// Request is the model request that produced ToolCallResponse.
	Request *model.Request
	// ToolCallResponse is the assistant response containing tool calls.
	ToolCallResponse *model.Response
	// ToolResultEvent is the event carrying the current tool result messages.
	ToolResultEvent *event.Event
	// Messages is the current conversation view, including tool results.
	Messages []model.Message
	// ToolCalls contains the assistant tool calls being answered.
	ToolCalls []model.ToolCall
	// ToolResultMessages contains the current tool result messages.
	ToolResultMessages []model.Message
}

// AfterToolMessagesResult contains replacements for tool result messages.
type AfterToolMessagesResult struct {
	// ToolResultMessages replaces the current tool result messages when non-empty.
	// The framework validates that replacements preserve the same tool IDs.
	ToolResultMessages []model.Message
}

// AfterToolMessagesCallback is called after tool results are converted into
// model messages and before the tool result event is emitted.
type AfterToolMessagesCallback func(
	ctx context.Context,
	args *AfterToolMessagesArgs,
) (*AfterToolMessagesResult, error)
