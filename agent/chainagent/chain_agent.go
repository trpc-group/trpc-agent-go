//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package chainagent provides a sequential agent implementation.
package chainagent

import (
	"context"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const defaultChannelBufferSize = 256

// ChainAgent is an agent that runs its sub-agents in sequence.
type ChainAgent struct {
	name              string
	subAgents         []agent.Agent
	channelBufferSize int
	agentCallbacks    *agent.Callbacks
}

// Option configures ChainAgent settings using the functional options pattern.
// This type is exported to allow external packages to create custom options.
type Option func(*Options)

// Options contains all configuration options for ChainAgent.
// This struct is exported to allow external packages to inspect or modify options.
type Options struct {
	subAgents         []agent.Agent
	channelBufferSize int
	agentCallbacks    *agent.Callbacks
}

// WithSubAgents sets the sub-agents that will be executed in sequence.
// The agents will run one after another, with each agent's output potentially
// influencing the next agent's execution.
func WithSubAgents(subAgents []agent.Agent) Option {
	return func(o *Options) { o.subAgents = subAgents }
}

// WithChannelBufferSize sets the buffer size for the event channel.
// This controls how many events can be buffered before blocking.
// Default is 256 if not specified.
func WithChannelBufferSize(size int) Option {
	return func(o *Options) { o.channelBufferSize = size }
}

// WithAgentCallbacks attaches lifecycle callbacks to the chain agent.
// These callbacks allow custom logic to be executed before and after
// the chain agent runs.
func WithAgentCallbacks(cb *agent.Callbacks) Option {
	return func(o *Options) { o.agentCallbacks = cb }
}

// New creates a new ChainAgent with the given name and options.
// ChainAgent executes its sub-agents sequentially, passing events through
// as they are generated. Each sub-agent can see the events from previous agents.
func New(name string, opts ...Option) *ChainAgent {
	// Apply options
	cfg := Options{
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
	subInvocation := baseInvocation.Clone(
		agent.WithInvocationAgent(subAgent),
	)

	return subInvocation
}

// Run implements the agent.Agent interface.
// It executes sub-agents in sequence, passing events through as they are generated.
func (a *ChainAgent) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	eventChan := make(chan *event.Event, a.channelBufferSize)

	go func() {
		defer close(eventChan)
		a.executeChainRun(ctx, invocation, eventChan)
	}()

	return eventChan, nil
}

// executeChainRun handles the main execution logic for chain agent.
func (a *ChainAgent) executeChainRun(
	ctx context.Context,
	invocation *agent.Invocation,
	eventChan chan<- *event.Event,
) {
	// Setup invocation.
	a.setupInvocation(invocation)

	// Handle before agent callbacks.
	if a.handleBeforeAgentCallbacks(ctx, invocation, eventChan) {
		return
	}

	// Execute sub-agents in sequence.
	a.executeSubAgents(ctx, invocation, eventChan)

	// Handle after agent callbacks.
	a.handleAfterAgentCallbacks(ctx, invocation, eventChan)
}

// setupInvocation prepares the invocation for execution.
func (a *ChainAgent) setupInvocation(invocation *agent.Invocation) {
	// Set agent and agent name.
	invocation.Agent = a
	invocation.AgentName = a.name
}

// handleBeforeAgentCallbacks handles pre-execution callbacks.
func (a *ChainAgent) handleBeforeAgentCallbacks(
	ctx context.Context,
	invocation *agent.Invocation,
	eventChan chan<- *event.Event,
) bool {
	if a.agentCallbacks == nil {
		return false
	}

	customResponse, err := a.agentCallbacks.RunBeforeAgent(ctx, invocation)
	if err != nil {
		// Send error event.
		agent.EmitEvent(ctx, invocation, eventChan, event.NewErrorEvent(
			invocation.InvocationID,
			invocation.AgentName,
			agent.ErrorTypeAgentCallbackError,
			err.Error(),
		))
		return true // Indicates early return
	}
	if customResponse != nil {
		// Create an event from the custom response and then close.
		agent.EmitEvent(ctx, invocation, eventChan, event.NewResponseEvent(
			invocation.InvocationID,
			invocation.AgentName,
			customResponse,
		))
		return true // Indicates early return
	}
	return false // Continue execution
}

// executeSubAgents runs all sub-agents in sequence.
func (a *ChainAgent) executeSubAgents(
	ctx context.Context,
	invocation *agent.Invocation,
	eventChan chan<- *event.Event,
) {
	for _, subAgent := range a.subAgents {
		// Create clean invocation for sub-agent - no shared state mutation.
		subInvocation := a.createSubAgentInvocation(subAgent, invocation)

		// Reset invocation information in context
		subAgentCtx := agent.NewInvocationContext(ctx, subInvocation)

		// Run the sub-agent.
		subEventChan, err := subAgent.Run(subAgentCtx, subInvocation)
		if err != nil {
			log.Warnf("subEventChan run failed. agent name: %s, err:%v", subInvocation.AgentName, err)
			agent.EmitEvent(ctx, invocation, eventChan, event.NewErrorEvent(
				invocation.InvocationID,
				invocation.AgentName,
				model.ErrorTypeFlowError,
				err.Error(),
			))
			return
		}

		// Forward all events from the sub-agent.
		for subEvent := range subEventChan {
			if err := event.EmitEvent(ctx, eventChan, subEvent); err != nil {
				return
			}
		}

		if err := agent.CheckContextCancelled(ctx); err != nil {
			log.Warnf("Chain agent %q cancelled execution of sub-agent %q", a.name, subAgent.Info().Name)
			return
		}
	}
}

// handleAfterAgentCallbacks handles post-execution callbacks.
func (a *ChainAgent) handleAfterAgentCallbacks(
	ctx context.Context,
	invocation *agent.Invocation,
	eventChan chan<- *event.Event,
) {
	if a.agentCallbacks == nil {
		return
	}

	customResponse, err := a.agentCallbacks.RunAfterAgent(ctx, invocation, nil)
	var evt *event.Event
	if err != nil {
		// Send error event.
		evt = event.NewErrorEvent(
			invocation.InvocationID,
			invocation.AgentName,
			agent.ErrorTypeAgentCallbackError,
			err.Error(),
		)
	} else if customResponse != nil {
		// Create an event from the custom response.
		evt = event.NewResponseEvent(
			invocation.InvocationID,
			invocation.AgentName,
			customResponse,
		)
	}
	agent.EmitEvent(ctx, invocation, eventChan, evt)
}

// Tools implements the agent.Agent interface.
// It returns the tools available to this agent.
func (a *ChainAgent) Tools() []tool.Tool {
	return []tool.Tool{}
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
