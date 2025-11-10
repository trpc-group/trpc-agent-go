//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package graphagent provides a graph-based agent implementation.
package graphagent

import (
	"context"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/internal/flow/processor"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

var defaultChannelBufferSize = 256

const (
	// BranchFilterModePrefix Prefix matching pattern
	BranchFilterModePrefix = processor.BranchFilterModePrefix
	// BranchFilterModeAll include all
	BranchFilterModeAll = processor.BranchFilterModeAll
	// BranchFilterModeExact exact match
	BranchFilterModeExact = processor.BranchFilterModeExact

	// TimelineFilterAll includes all historical message records
	// Suitable for scenarios requiring full conversation context
	TimelineFilterAll = processor.TimelineFilterAll
	// TimelineFilterCurrentRequest only includes messages within the current request cycle
	// Filters out previous historical records, keeping only messages related to this request
	TimelineFilterCurrentRequest = processor.TimelineFilterCurrentRequest
	// TimelineFilterCurrentInvocation only includes messages within the current invocation session
	// Suitable for scenarios requiring isolation between different invocation cycles in long-running sessions
	TimelineFilterCurrentInvocation = processor.TimelineFilterCurrentInvocation
)

// Option is a function that configures a GraphAgent.
type Option func(*Options)

// WithDescription sets the description of the agent.
func WithDescription(description string) Option {
	return func(opts *Options) {
		opts.Description = description
	}
}

// WithAgentCallbacks sets the agent callbacks.
func WithAgentCallbacks(callbacks *agent.Callbacks) Option {
	return func(opts *Options) {
		opts.AgentCallbacks = callbacks
	}
}

// WithInitialState sets the initial state for graph execution.
func WithInitialState(state graph.State) Option {
	return func(opts *Options) {
		opts.InitialState = state
	}
}

// WithChannelBufferSize sets the buffer size for event channels.
func WithChannelBufferSize(size int) Option {
	return func(opts *Options) {
		opts.ChannelBufferSize = size
	}
}

// WithSubAgents sets the list of sub-agents available to this agent.
func WithSubAgents(subAgents []agent.Agent) Option {
	return func(opts *Options) {
		opts.SubAgents = subAgents
	}
}

// WithCheckpointSaver sets the checkpoint saver for the executor.
func WithCheckpointSaver(saver graph.CheckpointSaver) Option {
	return func(opts *Options) {
		opts.CheckpointSaver = saver
	}
}

// WithMessageTimelineFilterMode sets the message timeline filter mode.
func WithMessageTimelineFilterMode(mode string) Option {
	return func(opts *Options) {
		opts.messageTimelineFilterMode = mode
	}
}

// WithMessageBranchFilterMode sets the message branch filter mode.
func WithMessageBranchFilterMode(mode string) Option {
	return func(opts *Options) {
		opts.messageBranchFilterMode = mode
	}
}

// Options contains configuration options for creating a GraphAgent.
type Options struct {
	// Description is a description of the agent.
	Description string
	// SubAgents is the list of sub-agents available to this agent.
	SubAgents []agent.Agent
	// AgentCallbacks contains callbacks for agent operations.
	AgentCallbacks *agent.Callbacks
	// InitialState is the initial state for graph execution.
	InitialState graph.State
	// ChannelBufferSize is the buffer size for event channels (default: 256).
	ChannelBufferSize int
	// CheckpointSaver is the checkpoint saver for the executor.
	CheckpointSaver graph.CheckpointSaver

	// MessageTimelineFilterMode is the message timeline filter mode.
	messageTimelineFilterMode string
	// MessageBranchFilterMode is the message branch filter mode.
	messageBranchFilterMode string
}

// GraphAgent is an agent that executes a graph.
type GraphAgent struct {
	name              string
	description       string
	graph             *graph.Graph
	executor          *graph.Executor
	subAgents         []agent.Agent
	agentCallbacks    *agent.Callbacks
	initialState      graph.State
	channelBufferSize int
	options           Options
}

// New creates a new GraphAgent with the given graph and options.
func New(name string, g *graph.Graph, opts ...Option) (*GraphAgent, error) {
	// set default channel buffer size.
	var options Options = Options{ChannelBufferSize: defaultChannelBufferSize}

	// Apply function options.
	for _, opt := range opts {
		opt(&options)
	}

	// Build executor options.
	var executorOpts []graph.ExecutorOption
	executorOpts = append(executorOpts,
		graph.WithChannelBufferSize(options.ChannelBufferSize))
	if options.CheckpointSaver != nil {
		executorOpts = append(executorOpts,
			graph.WithCheckpointSaver(options.CheckpointSaver))
	}

	executor, err := graph.NewExecutor(g, executorOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create graph executor: %w", err)
	}

	return &GraphAgent{
		name:              name,
		description:       options.Description,
		graph:             g,
		executor:          executor,
		subAgents:         options.SubAgents,
		agentCallbacks:    options.AgentCallbacks,
		initialState:      options.InitialState,
		channelBufferSize: options.ChannelBufferSize,
		options:           options,
	}, nil
}

// Run executes the graph with the provided invocation.
func (ga *GraphAgent) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	// Setup invocation.
	ga.setupInvocation(invocation)

	// Prepare initial state.
	initialState := ga.createInitialState(ctx, invocation)

	// Execute the graph.
	if ga.agentCallbacks != nil {
		customResponse, err := ga.agentCallbacks.RunBeforeAgent(ctx, invocation)
		if err != nil {
			return nil, fmt.Errorf("before agent callback failed: %w", err)
		}
		if customResponse != nil {
			// Create a channel that returns the custom response and then closes.
			eventChan := make(chan *event.Event, 1)
			// Create an event from the custom response.
			customevent := event.NewResponseEvent(invocation.InvocationID, invocation.AgentName, customResponse)
			agent.EmitEvent(ctx, invocation, eventChan, customevent)
			close(eventChan)
			return eventChan, nil
		}
	}
	eventChan, err := ga.executor.Execute(ctx, initialState, invocation)
	if err != nil {
		return nil, err
	}
	if ga.agentCallbacks != nil {
		return ga.wrapEventChannel(ctx, invocation, eventChan), nil
	}
	return eventChan, nil
}

func (ga *GraphAgent) createInitialState(ctx context.Context, invocation *agent.Invocation) graph.State {
	var initialState graph.State

	if ga.initialState != nil {
		// Clone the base initial state to avoid modifying the original.
		initialState = ga.initialState.Clone()
	} else {
		initialState = make(graph.State)
	}

	// Merge runtime state from RunOptions if provided.
	if invocation.RunOptions.RuntimeState != nil {
		for key, value := range invocation.RunOptions.RuntimeState {
			initialState[key] = value
		}
	}

	// Seed messages from session events so multi‑turn runs share history.
	// This mirrors ContentRequestProcessor behavior used by non-graph flows.
	if invocation.Session != nil {
		// Build a temporary request to reuse the processor logic.
		req := &model.Request{}

		// Default processor: include (possibly overridden) + preserve same branch.
		p := processor.NewContentRequestProcessor(
			processor.WithBranchFilterMode(ga.options.messageBranchFilterMode),
			processor.WithPreserveSameBranch(true),
			processor.WithTimelineFilterMode(ga.options.messageTimelineFilterMode),
		)
		// We only need messages side effect; no output channel needed.
		p.ProcessRequest(ctx, invocation, req, nil)
		if len(req.Messages) > 0 {
			initialState[graph.StateKeyMessages] = req.Messages
		}
	}

	// Add invocation message to state.
	// When resuming from checkpoint, only add user input if it's meaningful content
	// (not just a resume signal), following LangGraph's pattern.
	isResuming := invocation.RunOptions.RuntimeState != nil &&
		invocation.RunOptions.RuntimeState[graph.CfgKeyCheckpointID] != nil

	if invocation.Message.Content != "" {
		// If resuming and the message is just "resume", don't add it as input
		// This allows pure checkpoint resumption without input interference
		if isResuming && invocation.Message.Content == "resume" {
			// Skip adding user_input to preserve checkpoint state
		} else {
			// Add user input for normal execution or resume with meaningful input
			initialState[graph.StateKeyUserInput] = invocation.Message.Content
		}
	}
	// Add session context if available.
	if invocation.Session != nil {
		initialState[graph.StateKeySession] = invocation.Session
	}
	// Add parent agent to state so agent nodes can access sub-agents.
	initialState[graph.StateKeyParentAgent] = ga

	return initialState
}

func (ga *GraphAgent) setupInvocation(invocation *agent.Invocation) {
	// Set agent and agent name.
	invocation.Agent = ga
	invocation.AgentName = ga.name
}

// Tools returns the list of tools available to this agent.
func (ga *GraphAgent) Tools() []tool.Tool { return nil }

// Info returns the basic information about this agent.
func (ga *GraphAgent) Info() agent.Info {
	return agent.Info{
		Name:        ga.name,
		Description: ga.description,
	}
}

// SubAgents returns the list of sub-agents available to this agent.
func (ga *GraphAgent) SubAgents() []agent.Agent {
	return ga.subAgents
}

// FindSubAgent finds a sub-agent by name.
func (ga *GraphAgent) FindSubAgent(name string) agent.Agent {
	for _, subAgent := range ga.subAgents {
		if subAgent.Info().Name == name {
			return subAgent
		}
	}
	return nil
}

// wrapEventChannel wraps the event channel to apply after agent callbacks.
func (ga *GraphAgent) wrapEventChannel(
	ctx context.Context,
	invocation *agent.Invocation,
	originalChan <-chan *event.Event,
) <-chan *event.Event {
	wrappedChan := make(chan *event.Event, ga.channelBufferSize)
	go func() {
		defer close(wrappedChan)
		// Forward all events from the original channel
		for evt := range originalChan {
			if err := event.EmitEvent(ctx, wrappedChan, evt); err != nil {
				return
			}
		}
		// After all events are processed, run after agent callbacks
		customResponse, err := ga.agentCallbacks.RunAfterAgent(ctx, invocation, nil)
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

		agent.EmitEvent(ctx, invocation, wrappedChan, evt)
	}()
	return wrappedChan
}

// Executor returns the graph executor for direct access to checkpoint management.
func (ga *GraphAgent) Executor() *graph.Executor {
	return ga.executor
}
