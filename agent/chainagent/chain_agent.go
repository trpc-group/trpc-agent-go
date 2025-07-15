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

// Package chainagent provides a sequential agent implementation.
package chainagent

import (
	"context"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const defaultChannelBufferSize = 256

// ChainAgent is an agent that runs its sub-agents in sequence.
type ChainAgent struct {
	name              string
	subAgents         []agent.Agent
	tools             []tool.Tool
	channelBufferSize int
	agentCallbacks    *agent.AgentCallbacks
}

// option configures internal settings for ChainAgent.
type option func(*options)

type options struct {
	subAgents         []agent.Agent
	tools             []tool.Tool
	channelBufferSize int
	agentCallbacks    *agent.AgentCallbacks
}

// WithSubAgents sets the sub-agents executed in sequence.
func WithSubAgents(subAgents []agent.Agent) option {
	return func(o *options) { o.subAgents = subAgents }
}

// WithTools sets tools available to the chain agent.
func WithTools(tools []tool.Tool) option {
	return func(o *options) { o.tools = tools }
}

// WithChannelBufferSize overrides the default event channel buffer size.
func WithChannelBufferSize(size int) option {
	return func(o *options) { o.channelBufferSize = size }
}

// WithAgentCallbacks attaches agent lifecycle callbacks.
func WithAgentCallbacks(cb *agent.AgentCallbacks) option {
	return func(o *options) { o.agentCallbacks = cb }
}

// New instantiates a ChainAgent using functional options.
func New(name string, opts ...option) *ChainAgent {
	// Apply options
	cfg := options{
		channelBufferSize: defaultChannelBufferSize,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}

	// Ensure sane defaults.
	if cfg.channelBufferSize <= 0 {
		cfg.channelBufferSize = defaultChannelBufferSize
	}

	return &ChainAgent{
		name:              name,
		subAgents:         cfg.subAgents,
		tools:             cfg.tools,
		channelBufferSize: cfg.channelBufferSize,
		agentCallbacks:    cfg.agentCallbacks,
	}
}

// createSubAgentInvocation creates a clean invocation for a sub-agent.
// This ensures proper agent attribution for sequential execution.
func (a *ChainAgent) createSubAgentInvocation(
	subAgent agent.Agent,
	baseInvocation *agent.Invocation,
) *agent.Invocation {
	// Create a copy of the invocation - no shared state mutation.
	subInvocation := *baseInvocation

	// Update agent-specific fields.
	subInvocation.Agent = subAgent
	subInvocation.AgentName = subAgent.Info().Name
	subInvocation.TransferInfo = nil // Clear transfer info for sub-agents.

	// Set branch info to track sequence in multi-agent scenarios.
	if baseInvocation.Branch != "" {
		subInvocation.Branch = baseInvocation.Branch + "." + subAgent.Info().Name
	} else {
		subInvocation.Branch = a.name + "." + subAgent.Info().Name
	}

	return &subInvocation
}

// Run implements the agent.Agent interface.
// It executes sub-agents in sequence, passing events through as they are generated.
func (a *ChainAgent) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	eventChan := make(chan *event.Event, a.channelBufferSize)

	go func() {
		defer close(eventChan)

		// Set agent name if not already set.
		if invocation.AgentName == "" {
			invocation.AgentName = a.name
		}

		// Set agent callbacks if available.
		if invocation.AgentCallbacks == nil && a.agentCallbacks != nil {
			invocation.AgentCallbacks = a.agentCallbacks
		}

		// Run before agent callbacks if they exist.
		if invocation.AgentCallbacks != nil {
			customResponse, err := invocation.AgentCallbacks.RunBeforeAgent(ctx, invocation)
			if err != nil {
				// Send error event.
				errorEvent := event.NewErrorEvent(
					invocation.InvocationID,
					invocation.AgentName,
					agent.ErrorTypeAgentCallbackError,
					err.Error(),
				)
				select {
				case eventChan <- errorEvent:
				case <-ctx.Done():
				}
				return
			}
			if customResponse != nil {
				// Create an event from the custom response and then close.
				customEvent := event.NewResponseEvent(invocation.InvocationID, invocation.AgentName, customResponse)
				select {
				case eventChan <- customEvent:
				case <-ctx.Done():
				}
				return
			}
		}

		// Run each sub-agent in sequence.
		for _, subAgent := range a.subAgents {
			// Create clean invocation for sub-agent - no shared state mutation.
			subInvocation := a.createSubAgentInvocation(subAgent, invocation)

			// Run the sub-agent.
			subEventChan, err := subAgent.Run(ctx, subInvocation)
			if err != nil {
				// Send error event.
				errorEvent := event.NewErrorEvent(
					invocation.InvocationID,
					invocation.AgentName,
					model.ErrorTypeFlowError,
					err.Error(),
				)
				select {
				case eventChan <- errorEvent:
				case <-ctx.Done():
				}
				return
			}

			// Forward all events from the sub-agent.
			for subEvent := range subEventChan {
				select {
				case eventChan <- subEvent:
				case <-ctx.Done():
					return
				}
			}

			// Check if context was cancelled.
			select {
			case <-ctx.Done():
				return
			default:
			}
		}

		// Run after agent callbacks if they exist.
		if invocation.AgentCallbacks != nil {
			customResponse, err := invocation.AgentCallbacks.RunAfterAgent(ctx, invocation, nil)
			if err != nil {
				// Send error event.
				errorEvent := event.NewErrorEvent(
					invocation.InvocationID,
					invocation.AgentName,
					agent.ErrorTypeAgentCallbackError,
					err.Error(),
				)
				select {
				case eventChan <- errorEvent:
				case <-ctx.Done():
				}
				return
			}
			if customResponse != nil {
				// Create an event from the custom response.
				customEvent := event.NewResponseEvent(invocation.InvocationID, invocation.AgentName, customResponse)
				select {
				case eventChan <- customEvent:
				case <-ctx.Done():
				}
			}
		}
	}()

	return eventChan, nil
}

// Tools implements the agent.Agent interface.
// It returns the tools available to this agent.
func (a *ChainAgent) Tools() []tool.Tool {
	return a.tools
}

// Info implements the agent.Agent interface.
// It returns the basic information about this agent.
func (a *ChainAgent) Info() agent.Info {
	return agent.Info{
		Name:        a.name,
		Description: fmt.Sprintf("Chain agent that runs %d sub-agents in sequence", len(a.subAgents)),
	}
}

// SubAgents implements the agent.Agent interface.
// It returns the list of sub-agents available to this agent.
func (a *ChainAgent) SubAgents() []agent.Agent {
	return a.subAgents
}

// FindSubAgent implements the agent.Agent interface.
// It finds a sub-agent by name and returns nil if not found.
func (a *ChainAgent) FindSubAgent(name string) agent.Agent {
	for _, subAgent := range a.subAgents {
		if subAgent.Info().Name == name {
			return subAgent
		}
	}
	return nil
}
