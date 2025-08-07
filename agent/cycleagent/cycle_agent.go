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

const defaultChannelBufferSize = 256

// EscalationFunc is a callback function that determines if an event should
// trigger escalation (stop the cycle). Return true to stop the cycle.
type EscalationFunc func(*event.Event) bool

// CycleAgent is an agent that runs its sub-agents in a loop.
// When a sub-agent generates an event with escalation or max_iterations are
// reached, the cycle agent will stop.
type CycleAgent struct {
	name              string
	subAgents         []agent.Agent
	tools             []tool.Tool
	maxIterations     *int // Optional maximum number of iterations
	channelBufferSize int
	agentCallbacks    *agent.Callbacks
	escalationFunc    EscalationFunc // Injectable escalation logic
}

// Option configures CycleAgent settings using the functional options pattern.
// This type is exported to allow external packages to create custom options.
type Option func(*Options)

// Options contains all configuration options for CycleAgent.
// This struct is exported to allow external packages to inspect or modify options.
type Options struct {
	subAgents         []agent.Agent
	tools             []tool.Tool
	maxIterations     *int
	channelBufferSize int
	agentCallbacks    *agent.Callbacks
	escalationFunc    EscalationFunc
}

// WithSubAgents sets the sub-agents that will be executed in a loop.
// The agents will run repeatedly until an escalation condition is met
// or the maximum number of iterations is reached.
func WithSubAgents(sub []agent.Agent) Option {
	return func(o *Options) { o.subAgents = sub }
}

// WithTools configures tools available to the cycle agent.
// These tools can be used by any sub-agent during loop execution.
func WithTools(tools []tool.Tool) Option {
	return func(o *Options) { o.tools = tools }
}

// WithMaxIterations sets the maximum number of loop iterations.
// If not set, the loop will continue until an escalation condition is met.
// This prevents infinite loops in case escalation detection fails.
func WithMaxIterations(max int) Option {
	return func(o *Options) { o.maxIterations = &max }
}

// WithChannelBufferSize sets the buffer size for the event channel.
// This controls how many events can be buffered before blocking.
// Default is 256 if not specified.
func WithChannelBufferSize(size int) Option {
	return func(o *Options) { o.channelBufferSize = size }
}

// WithAgentCallbacks attaches lifecycle callbacks to the cycle agent.
// These callbacks allow custom logic to be executed before and after
// the cycle agent runs.
func WithAgentCallbacks(cb *agent.Callbacks) Option {
	return func(o *Options) { o.agentCallbacks = cb }
}

// WithEscalationFunc sets a custom function to detect escalation conditions.
// This function determines when the loop should stop based on events.
// If not set, a default escalation detection is used.
func WithEscalationFunc(f EscalationFunc) Option {
	return func(o *Options) { o.escalationFunc = f }
}

// New creates a new CycleAgent with the given name and options.
// CycleAgent executes its sub-agents in a loop until an escalation condition
// is met or the maximum number of iterations is reached.
func New(name string, opts ...Option) *CycleAgent {
	cfg := Options{channelBufferSize: defaultChannelBufferSize}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	if cfg.channelBufferSize <= 0 {
		cfg.channelBufferSize = defaultChannelBufferSize
	}
	return &CycleAgent{
		name:              name,
		subAgents:         cfg.subAgents,
		tools:             cfg.tools,
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
	subInvocation := *baseInvocation

	// Update agent-specific fields for proper agent attribution.
	subInvocation.Agent = subAgent
	subInvocation.AgentName = subAgent.Info().Name
	subInvocation.TransferInfo = nil // Clear transfer info for sub-agents.

	// Set branch info for hierarchical event filtering.
	// Do not use the sub-agent name here, it will cause the sub-agent unable to see the
	// previous agent's conversation history.
	if baseInvocation.Branch != "" {
		subInvocation.Branch = baseInvocation.Branch
	} else {
		subInvocation.Branch = a.name
	}

	return &subInvocation
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
	// Set agent name if not already set.
	if invocation.AgentName == "" {
		invocation.AgentName = a.name
	}

	// Set agent callbacks if available.
	if invocation.AgentCallbacks == nil && a.agentCallbacks != nil {
		invocation.AgentCallbacks = a.agentCallbacks
	}
}

// handleBeforeAgentCallbacks handles pre-execution callbacks.
func (a *CycleAgent) handleBeforeAgentCallbacks(
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

// runSubAgent executes a single sub-agent and forwards its events.
func (a *CycleAgent) runSubAgent(
	ctx context.Context,
	subAgent agent.Agent,
	invocation *agent.Invocation,
	eventChan chan<- *event.Event,
) bool {
	// Create a proper invocation for the sub-agent with correct attribution.
	subInvocation := a.createSubAgentInvocation(subAgent, invocation)

	// Run the sub-agent.
	subEventChan, err := subAgent.Run(ctx, subInvocation)
	if err != nil {
		// Send error event and escalate.
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
		return true // Indicates escalation
	}

	// Forward events from the sub-agent and check for escalation.
	for subEvent := range subEventChan {
		select {
		case eventChan <- subEvent:
		case <-ctx.Done():
			return true // Indicates early return
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
) bool {
	for _, subAgent := range a.subAgents {
		// Check if context was cancelled.
		select {
		case <-ctx.Done():
			return true // Indicates early return
		default:
		}

		// Run the sub-agent.
		if a.runSubAgent(ctx, subAgent, invocation, eventChan) {
			return true // Indicates escalation or early return
		}

		// Check if context was cancelled.
		select {
		case <-ctx.Done():
			return true // Indicates early return
		default:
		}
	}
	return false // No escalation
}

// handleAfterAgentCallbacks handles post-execution callbacks.
func (a *CycleAgent) handleAfterAgentCallbacks(
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

// Run implements the agent.Agent interface.
// It executes sub-agents in a loop until escalation or max iterations.
func (a *CycleAgent) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	eventChan := make(chan *event.Event, a.channelBufferSize)

	go func() {
		defer close(eventChan)

		// Setup invocation.
		a.setupInvocation(invocation)

		// Handle before agent callbacks.
		if a.handleBeforeAgentCallbacks(ctx, invocation, eventChan) {
			return
		}

		var timesLooped int

		// Main loop: continue until max iterations or escalation.
		for a.maxIterations == nil || timesLooped < *a.maxIterations {
			// Check if context was cancelled.
			select {
			case <-ctx.Done():
				return
			default:
			}

			// Run sub-agents loop.
			if a.runSubAgentsLoop(ctx, invocation, eventChan) {
				break // Escalation or early return
			}

			timesLooped++
		}

		// Handle after agent callbacks.
		a.handleAfterAgentCallbacks(ctx, invocation, eventChan)
	}()

	return eventChan, nil
}

// Tools implements the agent.Agent interface.
// It returns the tools available to this agent.
func (a *CycleAgent) Tools() []tool.Tool {
	return a.tools
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
