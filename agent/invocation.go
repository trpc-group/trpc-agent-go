//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package agent

import (
	"reflect"

	"trpc.group/trpc-go/trpc-agent-go/artifact"

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
	AgentCallbacks *Callbacks
	// ModelCallbacks contains callbacks for model operations.
	ModelCallbacks *model.Callbacks
	// ToolCallbacks contains callbacks for tool operations.
	ToolCallbacks *tool.Callbacks

	// StructuredOutput defines how the model should produce structured output for this invocation.
	StructuredOutput *model.StructuredOutput
	// StructuredOutputType is the Go type to unmarshal the final JSON into.
	StructuredOutputType reflect.Type

	// ArtifactService is the service for managing artifacts.
	ArtifactService artifact.Service
}

// RunOption is a function that configures a RunOptions.
type RunOption func(*RunOptions)

// WithRuntimeState sets the runtime state for the RunOptions.
func WithRuntimeState(state map[string]any) RunOption {
	return func(opts *RunOptions) {
		opts.RuntimeState = state
	}
}

// RunOptions is the options for the Run method.
type RunOptions struct {
	// RuntimeState contains key-value pairs that will be merged into the initial state
	// for this specific run. This allows callers to pass dynamic parameters
	// (e.g., room ID, user context) without modifying the agent's base initial state.
	RuntimeState map[string]any
}

// CreateBranchInvocation create a new invocation for branch agent
func (baseInvocation *Invocation) CreateBranchInvocation(branchAgent Agent) *Invocation {
	// Create a copy of the invocation - no shared state mutation.
	branchInvocation := Invocation{
		Agent:             branchAgent,
		AgentName:         branchAgent.Info().Name,
		InvocationID:      baseInvocation.InvocationID,
		Branch:            baseInvocation.Branch,
		Session:           baseInvocation.Session,
		Message:           baseInvocation.Message,
		EventCompletionCh: baseInvocation.EventCompletionCh,
		RunOptions:        baseInvocation.RunOptions,
		ArtifactService:   baseInvocation.ArtifactService,
	}

	return &branchInvocation
}
