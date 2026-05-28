//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package agent

import (
	"context"
	"errors"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	errLazyAgentNilFactory      = "agent: lazy agent factory is nil"
	errLazyAgentFactoryNilAgent = "agent: lazy agent factory returned nil"
)

// AgentFactory creates an Agent for one run.
//
// Runner uses the same shape for request-scoped root agents. NewLazyAgent
// accepts this shape for sub-agents that should be described up front but
// constructed only when they are actually invoked.
type AgentFactory func(ctx context.Context, ro RunOptions) (Agent, error)

type lazyAgent struct {
	info    Info
	factory AgentFactory
}

// NewLazyAgent returns an Agent that exposes info immediately and builds the
// concrete Agent only when Run is called.
//
// This is useful for optional or expensive sub-agents. The parent agent can
// still advertise the lazy agent through transfer_to_agent because Info is
// available without constructing the concrete implementation.
//
// The factory should return an Agent with the same Info().Name as info.Name so
// traces, transfer targets, and session branches remain easy to follow.
func NewLazyAgent(
	info Info,
	factory func(context.Context, RunOptions) (Agent, error),
) Agent {
	return &lazyAgent{
		info:    info,
		factory: factory,
	}
}

// Run implements Agent.
func (a *lazyAgent) Run(ctx context.Context, invocation *Invocation) (<-chan *event.Event, error) {
	if a.factory == nil {
		return nil, errors.New(errLazyAgentNilFactory)
	}

	ro := RunOptions{}
	if invocation != nil {
		ro = invocation.RunOptions
	}

	created, err := a.factory(ctx, ro)
	if err != nil {
		return nil, fmt.Errorf("agent: lazy agent factory: %w", err)
	}
	if created == nil {
		return nil, errors.New(errLazyAgentFactoryNilAgent)
	}
	if invocation != nil {
		invocation.Agent = created
		invocation.AgentName = created.Info().Name
	}
	return created.Run(ctx, invocation)
}

// Tools implements Agent.
func (a *lazyAgent) Tools() []tool.Tool {
	return nil
}

// Info implements Agent.
func (a *lazyAgent) Info() Info {
	return a.info
}

// SubAgents implements Agent.
func (a *lazyAgent) SubAgents() []Agent {
	return nil
}

// FindSubAgent implements Agent.
func (a *lazyAgent) FindSubAgent(string) Agent {
	return nil
}
