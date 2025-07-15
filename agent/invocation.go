//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//
//

package agent

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// TransferInfo contains information about a pending agent transfer.
type TransferInfo struct {
	// TargetAgentName is the name of the agent to transfer control to.
	TargetAgentName string
	// Message is the message to send to the target agent.
	Message string
	// EndInvocation indicates whether to end the current invocation after transfer.
	EndInvocation bool
}

// Invocation represents the context for a flow execution.
type Invocation struct {
	// Agent is the agent that is being invoked.
	Agent Agent
	// AgentName is the name of the agent that is being invoked.
	AgentName string
	// InvocationID is the ID of the invocation.
	InvocationID string
	// Branch is the branch identifier for hierarchical event filtering.
	Branch string
	// EndInvocation is a flag that indicates if the invocation is complete.
	EndInvocation bool
	// Session is the session that is being used for the invocation.
	Session *session.Session
	// Model is the model that is being used for the invocation.
	Model model.Model
	// Message is the message that is being sent to the agent.
	Message model.Message
	// EventCompletionCh is used to signal when events are written to session.
	EventCompletionCh <-chan string
	// RunOptions is the options for the Run method.
	RunOptions RunOptions
	// TransferInfo contains information about a pending agent transfer.
	TransferInfo *TransferInfo
	// AgentCallbacks contains callbacks for agent operations.
	AgentCallbacks *AgentCallbacks
	// ModelCallbacks contains callbacks for model operations.
	ModelCallbacks *model.ModelCallbacks
	// ToolCallbacks contains callbacks for tool operations.
	ToolCallbacks *tool.ToolCallbacks
}

type invocationKey struct{}

// NewContextWithInvocation creates a new context with the invocation.
func NewContextWithInvocation(ctx context.Context, invocation *Invocation) context.Context {
	return context.WithValue(ctx, invocationKey{}, invocation)
}

// InvocationFromContext returns the invocation from the context.
func InvocationFromContext(ctx context.Context) (*Invocation, bool) {
	invocation, ok := ctx.Value(invocationKey{}).(*Invocation)
	return invocation, ok
}

// RunOptions is the options for the Run method.
type RunOptions struct{}
