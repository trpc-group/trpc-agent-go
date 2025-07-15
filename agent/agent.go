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

// Package agent provides the core agent functionality.
package agent

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// Info contains basic information about an agent.
type Info struct {
	Name        string
	Description string
}

// Agent is the interface that all agents must implement.
type Agent interface {
	// Run executes the provided invocation within the given context and returns
	// a channel of events that represent the progress and results of the execution.
	Run(ctx context.Context, invocation *Invocation) (<-chan *event.Event, error)

	// Tools returns the list of tools that this agent has access to and can execute.
	// These tools represent the capabilities available to the agent during invocations.
	Tools() []tool.Tool

	// Info returns the basic information about this agent.
	Info() Info

	// SubAgents returns the list of sub-agents available to this agent.
	// Returns empty slice if no sub-agents are available.
	SubAgents() []Agent

	// FindSubAgent finds a sub-agent by name.
	// Returns nil if no sub-agent with the given name is found.
	FindSubAgent(name string) Agent
}
