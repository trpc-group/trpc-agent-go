//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package cycleagent provides a looping agent implementation.
package cycleagent

import (
	"context"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// CycleAgent is an agent that runs its sub-agents in a loop.
// When a sub-agent generates an event with escalation or max_iterations are
// reached, the cycle agent will stop.
type CycleAgent struct {
	name              string
	subAgents         []agent.Agent
	maxIterations     *int // Optional maximum number of iterations
	channelBufferSize int
	agentCallbacks    *agent.Callbacks
	escalationFunc    EscalationFunc // Injectable escalation logic
}

// New creates a new CycleAgent with the given name and options.
// CycleAgent executes its sub-agents in a loop until an escalation condition
// is met or the maximum number of iterations is reached.
func New(name string, opts ...Option) *CycleAgent {
	cfg := defaultOptions
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	return &CycleAgent{
		name:              name,
		subAgents:         cfg.subAgents,
		maxIterations:     cfg.maxIterations,
		channelBufferSize: cfg.channelBufferSize,
		agentCallbacks:    cfg.agentCallbacks,
		escalationFunc:    cfg.escalationFunc,
	}
}

// createSubAgentInvocation creates a proper invocation for sub-agents with correct attribution.
// This ensures events from sub-agents have the correct Author field set.
func (a *CycleAgent) createSubAgentInvocation(
	subAgent agent.Agent,
	baseInvocation *agent.Invocation,
) *agent.Invocation {
	// Create a copy of the invocation - no shared state mutation.
	subInvocation := baseInvocation.Clone(
		agent.WithInvocationAgent(subAgent),
	)

	return subInvocation
}

// shouldEscalate checks if an event indicates escalation using injectable logic.
func (a *CycleAgent) shouldEscalate(evt *event.Event) bool {
	if evt == nil {
		return false
	}

	// Only check escalation for meaningful events, not streaming chunks
	if !a.isEscalationCheckEvent(evt) {
		return false
	}

	// Use custom escalation function if provided.
	if a.escalationFunc != nil {
		return a.escalationFunc(evt)
	}

	// Default escalation logic: error events.
	if evt.Error != nil {
		return true
	}

	// Check for done events that might indicate completion or escalation.
	return evt.Done && evt.Object == model.ObjectTypeError
}

// isEscalationCheckEvent determines if an event should be checked for escalation.
// Only check meaningful completion events, not streaming chunks or preprocessing.
func (a *CycleAgent) isEscalationCheckEvent(evt *event.Event) bool {
	// Always check error events
	if evt.Error != nil {
		return true
	}

	// Check tool response events (these contain our quality assessment results)
	if evt.Object == model.ObjectTypeToolResponse {
		return true
	}

	// Check final completion events (not streaming chunks)
	if evt.Done && evt.Response != nil && evt.Object != "chat.completion.chunk" {
		return true
	}

	// Skip streaming chunks, preprocessing events, etc.
	return false
}

// setupInvocation prepares the invocation for execution.
func (a *CycleAgent) setupInvocation(invocation *agent.Invocation) {
	// Set agent and agent name
	invocation.Agent = a
	invocation.AgentName = a.name
}

// handleBeforeAgentCallbacks handles pre-execution callbacks.
// Returns the updated context and whether execution should stop early.
func (a *CycleAgent) handleBeforeAgentCallbacks(
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

// runSubAgent executes a single sub-agent and forwards its events.
func (a *CycleAgent) runSubAgent(
	ctx context.Context,
	subAgent agent.Agent,
	invocation *agent.Invocation,
	eventChan chan<- *event.Event,
	fullRespEvent **event.Event,
) bool {
	// Create a proper invocation for the sub-agent with correct attribution.
	subInvocation := a.createSubAgentInvocation(subAgent, invocation)

	// Reset invocation information in context
	subAgentCtx := agent.NewInvocationContext(ctx, subInvocation)

	// Run the sub-agent.
	subEventChan, err := subAgent.Run(subAgentCtx, subInvocation)
	if err != nil {
		// Send error event and escalate.
		agent.EmitEvent(ctx, invocation, eventChan, event.NewErrorEvent(
			invocation.InvocationID,
			invocation.AgentName,
			model.ErrorTypeFlowError,
			err.Error(),
		))
		return true // Indicates escalation
	}

	// Forward events from the sub-agent and check for escalation.
	for subEvent := range subEventChan {
		if subEvent != nil && subEvent.Response != nil && !subEvent.Response.IsPartial {
			*fullRespEvent = subEvent
		}
		if err := event.EmitEvent(ctx, eventChan, subEvent); err != nil {
			return true
		}
		if subEvent != nil && subEvent.Error != nil {
			return true
		}

		// Check if this event indicates escalation.
		if a.shouldEscalate(subEvent) {
			return true // Indicates escalation
		}
	}

	return false // No escalation
}

// runSubAgentsLoop executes all sub-agents in sequence.
func (a *CycleAgent) runSubAgentsLoop(
	ctx context.Context,
	invocation *agent.Invocation,
	eventChan chan<- *event.Event,
	fullRespEvent **event.Event,
) bool {
	for _, subAgent := range a.subAgents {
		// Check if context was cancelled.
		if err := agent.CheckContextCancelled(ctx); err != nil {
			return true
		}

		// Run the sub-agent.
		if a.runSubAgent(ctx, subAgent, invocation, eventChan, fullRespEvent) {
			return true // Indicates escalation or early return
		}

		// Check if context was cancelled.
		if err := agent.CheckContextCancelled(ctx); err != nil {
			return true
		}
	}
	return false // No escalation
}

// handleAfterAgentCallbacks handles post-execution callbacks.
func (a *CycleAgent) handleAfterAgentCallbacks(
	ctx context.Context,
	invocation *agent.Invocation,
	eventChan chan<- *event.Event,
	fullRespEvent *event.Event,
) {
	if a.agentCallbacks == nil {
		return
	}

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
}

// Run implements the agent.Agent interface.
// It executes sub-agents in a loop until escalation or max iterations.
func (a *CycleAgent) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	eventChan := make(chan *event.Event, a.channelBufferSize)

	// Setup invocation.
	a.setupInvocation(invocation)

	runCtx := agent.CloneContext(ctx)
	go func(ctx context.Context) {
		defer close(eventChan)

		// Handle before agent callbacks.
		var shouldReturn bool
		ctx, shouldReturn = a.handleBeforeAgentCallbacks(ctx, invocation, eventChan)
		if shouldReturn {
			return
		}

		var timesLooped int
		var fullRespEvent *event.Event

		// Main loop: continue until max iterations or escalation.
		for a.maxIterations == nil || timesLooped < *a.maxIterations {
			// Check if context was cancelled.
			if err := agent.CheckContextCancelled(ctx); err != nil {
				return
			}

			// Run sub-agents loop and collect full response event.
			if a.runSubAgentsLoop(ctx, invocation, eventChan, &fullRespEvent) {
				break // Escalation or early return
			}

			timesLooped++
		}

		// Handle after agent callbacks.
		a.handleAfterAgentCallbacks(ctx, invocation, eventChan, fullRespEvent)
	}(runCtx)

	return eventChan, nil
}

// Tools implements the agent.Agent interface.
// It returns the tools available to this agent.
func (a *CycleAgent) Tools() []tool.Tool {
	return []tool.Tool{}
}

// Info implements the agent.Agent interface.
// It returns the basic information about this agent.
func (a *CycleAgent) Info() agent.Info {
	maxIterStr := "unlimited"
	if a.maxIterations != nil {
		maxIterStr = fmt.Sprintf("%d", *a.maxIterations)
	}
	return agent.Info{
		Name: a.name,
		Description: fmt.Sprintf(
			"Cycle agent that runs %d sub-agents in a loop (max iterations: %s)",
			len(a.subAgents), maxIterStr,
		),
	}
}

// SubAgents implements the agent.Agent interface.
// It returns the list of sub-agents available to this agent.
func (a *CycleAgent) SubAgents() []agent.Agent {
	return a.subAgents
}

// FindSubAgent implements the agent.Agent interface.
// It finds a sub-agent by name and returns nil if not found.
func (a *CycleAgent) FindSubAgent(name string) agent.Agent {
	for _, subAgent := range a.subAgents {
		if subAgent.Info().Name == name {
			return subAgent
		}
	}
	return nil
}
