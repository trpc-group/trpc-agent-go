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
	agentCallbacks    *agent.Callbacks
}

// Option configures ChainAgent settings using the functional options pattern.
// This type is exported to allow external packages to create custom options.
type Option func(*Options)

// Options contains all configuration options for ChainAgent.
// This struct is exported to allow external packages to inspect or modify options.
type Options struct {
	subAgents         []agent.Agent
	tools             []tool.Tool
	channelBufferSize int
	agentCallbacks    *agent.Callbacks
}

// WithSubAgents sets the sub-agents that will be executed in sequence.
// The agents will run one after another, with each agent's output potentially
// influencing the next agent's execution.
func WithSubAgents(subAgents []agent.Agent) Option {
	return func(o *Options) { o.subAgents = subAgents }
}

// WithTools sets the tools available to the chain agent.
// These tools can be used by any sub-agent in the chain during execution.
func WithTools(tools []tool.Tool) Option {
	return func(o *Options) { o.tools = tools }
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
	subInvocation := baseInvocation.CreateBranchInvocation(subAgent)

	// Set branch info to track sequence in multi-agent scenarios.
	// Do not include sub-agent name in branch, so that the chain sub-agents can
	// observe each other's events.
	if subInvocation.Branch == "" {
		subInvocation.Branch = a.name
	}

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

	// Set agent callbacks.
	invocation.AgentCallbacks = a.agentCallbacks
}

// handleBeforeAgentCallbacks handles pre-execution callbacks.
func (a *ChainAgent) handleBeforeAgentCallbacks(
	ctx context.Context,
	invocation *agent.Invocation,
	eventChan chan<- *event.Event,
) bool {
	if invocation.AgentCallbacks == nil {
		return false
	}

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
		return true // Indicates early return
	}
	if customResponse != nil {
		// Create an event from the custom response and then close.
		customEvent := event.NewResponseEvent(
			invocation.InvocationID,
			invocation.AgentName,
			customResponse,
		)
		select {
		case eventChan <- customEvent:
		case <-ctx.Done():
		}
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
}

// handleAfterAgentCallbacks handles post-execution callbacks.
func (a *ChainAgent) handleAfterAgentCallbacks(
	ctx context.Context,
	invocation *agent.Invocation,
	eventChan chan<- *event.Event,
) {
	if invocation.AgentCallbacks == nil {
		return
	}

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
		customEvent := event.NewResponseEvent(
			invocation.InvocationID,
			invocation.AgentName,
			customResponse,
		)
		select {
		case eventChan <- customEvent:
		case <-ctx.Done():
		}
	}
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
