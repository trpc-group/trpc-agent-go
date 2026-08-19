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
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// AfterToolRoundArgs contains the model-facing result of one complete tool
// execution round.
//
// ToolResultMessages is ordered in the same way as the assistant tool calls.
// Callbacks should treat all fields as read-only. When Complete is false, the
// round was interrupted and its messages must not be used as a complete round.
type AfterToolRoundArgs struct {
	// Invocation is the active invocation.
	Invocation *agent.Invocation
	// Request is the request that produced ToolCallResponse.
	Request *model.Request
	// ToolCallResponse is the assistant response containing the tool calls.
	ToolCallResponse *model.Response
	// ToolResultMessages contains the model-visible tool results in tool-call order.
	ToolResultMessages []model.Message
	// Complete reports whether all tool calls in this round completed normally.
	Complete bool
}

// AfterToolRoundCallback observes one tool execution round.
type AfterToolRoundCallback func(
	ctx context.Context,
	args *AfterToolRoundArgs,
) error
