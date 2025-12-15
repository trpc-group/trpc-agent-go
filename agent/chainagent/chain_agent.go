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
	itelemetry "trpc.group/trpc-go/trpc-agent-go/internal/telemetry"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/telemetry/trace"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// ChainAgent is an agent that runs its sub-agents in sequence.
type ChainAgent struct {
	name              string
	subAgents         []agent.Agent
	channelBufferSize int
	agentCallbacks    *agent.Callbacks
}

// New creates a new ChainAgent with the given name and options.
// ChainAgent executes its sub-agents sequentially, passing events through
// as they are generated. Each sub-agent can see the events from previous agents.
func New(name string, opts ...Option) *ChainAgent {
	// Apply options
	cfg := defaultOptions
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
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
func (a *ChainAgent) Run(ctx context.Context, invocation *agent.Invocation) (e <-chan *event.Event, err error) {
	eventChan := make(chan *event.Event, a.channelBufferSize)
	runCtx := agent.CloneContext(ctx)
	go func(ctx context.Context) {
		defer close(eventChan)
		a.executeChainRun(ctx, invocation, eventChan)
	}(runCtx)

	return eventChan, nil
}

// executeChainRun handles the main execution logic for chain agent.
func (a *ChainAgent) executeChainRun(
	ctx context.Context,
	invocation *agent.Invocation,
	eventChan chan<- *event.Event,
) {
	ctx, span := trace.Tracer.Start(ctx, fmt.Sprintf("%s %s", itelemetry.OperationInvokeAgent, a.name))
	itelemetry.TraceBeforeInvokeAgent(span, invocation, "chain-agent", "", nil)
	defer func() {
		span.End()
	}()
	// Setup invocation.
	a.setupInvocation(invocation)

	// Handle before agent callbacks.
	var shouldReturn bool
	ctx, shouldReturn = a.handleBeforeAgentCallbacks(ctx, invocation, eventChan)
	if shouldReturn {
		return
	}

	// Execute sub-agents in sequence.
	e, tokenUsage := a.executeSubAgents(ctx, invocation, eventChan)
	// Handle after agent callbacks.
	if a.agentCallbacks != nil {
		e = a.handleAfterAgentCallbacks(ctx, invocation, eventChan, e)
	}
	itelemetry.TraceAfterInvokeAgent(span, e, tokenUsage)
}

// setupInvocation prepares the invocation for execution.
func (a *ChainAgent) setupInvocation(invocation *agent.Invocation) {
	// Set agent and agent name.
	invocation.Agent = a
	invocation.AgentName = a.name
}

// handleBeforeAgentCallbacks handles pre-execution callbacks.
// Returns the updated context and whether execution should stop early.
func (a *ChainAgent) handleBeforeAgentCallbacks(
	ctx context.Context,
	invocation *agent.Invocation,
	eventChan chan<- *event.Event,
) (context.Context, bool) {
	if a.agentCallbacks == nil {
		return ctx, false
	}

	result, err := a.agentCallbacks.RunBeforeAgent(ctx, &agent.BeforeAgentArgs{
		Invocation: invocation,
	})
	if err != nil {
		// Send error event.
		agent.EmitEvent(ctx, invocation, eventChan, event.NewErrorEvent(
			invocation.InvocationID,
			invocation.AgentName,
			agent.ErrorTypeAgentCallbackError,
			err.Error(),
		))
		return ctx, true // Indicates early return
	}
	// Use the context from result if provided.
	if result != nil && result.Context != nil {
		ctx = result.Context
	}
	if result != nil && result.CustomResponse != nil {
		// Create an event from the custom response and then close.
		agent.EmitEvent(ctx, invocation, eventChan, event.NewResponseEvent(
			invocation.InvocationID,
			invocation.AgentName,
			result.CustomResponse,
		))
		return ctx, true // Indicates early return
	}
	return ctx, false // Continue execution
}

// executeSubAgents runs all sub-agents in sequence.
func (a *ChainAgent) executeSubAgents(
	ctx context.Context,
	invocation *agent.Invocation,
	eventChan chan<- *event.Event,
) (*event.Event, *itelemetry.TokenUsage) {
	tokenUsage := &itelemetry.TokenUsage{}
	var fullRespEvent *event.Event
	for _, subAgent := range a.subAgents {
		// Create clean invocation for sub-agent - no shared state mutation.
		subInvocation := a.createSubAgentInvocation(subAgent, invocation)

		// Reset invocation information in context
		subAgentCtx := agent.NewInvocationContext(ctx, subInvocation)

		// Run the sub-agent.
		subEventChan, err := subAgent.Run(subAgentCtx, subInvocation)
		if err != nil {
			log.Warnf("subEventChan run failed. agent name: %s, err:%v", subInvocation.AgentName, err)
			e := event.NewErrorEvent(
				invocation.InvocationID,
				invocation.AgentName,
				model.ErrorTypeFlowError,
				err.Error(),
			)
			agent.EmitEvent(ctx, invocation, eventChan, e)
			return e, tokenUsage
		}

		// Forward all events from the sub-agent.
		for subEvent := range subEventChan {
			if subEvent != nil && subEvent.Response != nil {
				if subEvent.Response.Usage != nil && !subEvent.Response.IsPartial {
					tokenUsage.PromptTokens += subEvent.Response.Usage.PromptTokens
					tokenUsage.CompletionTokens += subEvent.Response.Usage.CompletionTokens
					tokenUsage.TotalTokens += subEvent.Response.Usage.TotalTokens
				}
				if !subEvent.Response.IsPartial {
					fullRespEvent = subEvent
				}
			}
			if err := event.EmitEvent(ctx, eventChan, subEvent); err != nil {
				return nil, tokenUsage
			}
			if subEvent != nil && subEvent.Error != nil {
				return subEvent, tokenUsage
			}
		}

		if err := agent.CheckContextCancelled(ctx); err != nil {
			log.Warnf("Chain agent %q cancelled execution of sub-agent %q", a.name, subAgent.Info().Name)
			e := event.NewErrorEvent(
				invocation.InvocationID,
				invocation.AgentName,
				agent.ErrorTypeAgentContextCancelledError,
				fmt.Sprintf("chain agent %q cancelled execution of sub-agent %q: %v", a.name, subAgent.Info().Name, err),
			)
			agent.EmitEvent(ctx, invocation, eventChan, e)
			return e, tokenUsage
		}
	}
	return fullRespEvent, tokenUsage
}

// handleAfterAgentCallbacks handles post-execution callbacks.
func (a *ChainAgent) handleAfterAgentCallbacks(
	ctx context.Context,
	invocation *agent.Invocation,
	eventChan chan<- *event.Event,
	fullRespEvent *event.Event,
) *event.Event {

	result, err := a.agentCallbacks.RunAfterAgent(ctx, &agent.AfterAgentArgs{
		Invocation:        invocation,
		Error:             nil,
		FullResponseEvent: fullRespEvent,
	})
	// Use the context from result if provided.
	if result != nil && result.Context != nil {
		ctx = result.Context
	}
	var evt *event.Event
	if err != nil {
		// Send error event.
		evt = event.NewErrorEvent(
			invocation.InvocationID,
			invocation.AgentName,
			agent.ErrorTypeAgentCallbackError,
			err.Error(),
		)
	} else if result != nil && result.CustomResponse != nil {
		// Create an event from the custom response.
		evt = event.NewResponseEvent(
			invocation.InvocationID,
			invocation.AgentName,
			result.CustomResponse,
		)
	}
	agent.EmitEvent(ctx, invocation, eventChan, evt)
	return evt
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
