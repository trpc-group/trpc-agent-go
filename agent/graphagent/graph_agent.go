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

// Package graphagent provides a graph-based agent implementation.
package graphagent

import (
	"context"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// Option is a function that configures a GraphAgent.
type Option func(*Options)

// WithDescription sets the description of the agent.
func WithDescription(description string) Option {
	return func(opts *Options) {
		opts.Description = description
	}
}

// WithTools sets the list of tools available to the agent.
func WithTools(tools []tool.Tool) Option {
	return func(opts *Options) {
		opts.Tools = tools
	}
}

// WithSubAgents sets the list of sub-agents available to the agent.
func WithSubAgents(subAgents []agent.Agent) Option {
	return func(opts *Options) {
		opts.SubAgents = subAgents
	}
}

// WithAgentCallbacks sets the agent callbacks.
func WithAgentCallbacks(callbacks *agent.AgentCallbacks) Option {
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

// Options contains configuration options for creating a GraphAgent.
type Options struct {
	// Description is a description of the agent.
	Description string
	// Tools is the list of tools available to the agent.
	Tools []tool.Tool
	// SubAgents is the list of sub-agents available to the agent.
	SubAgents []agent.Agent
	// AgentCallbacks contains callbacks for agent operations.
	AgentCallbacks *agent.AgentCallbacks
	// InitialState is the initial state for graph execution.
	InitialState graph.State
	// ChannelBufferSize is the buffer size for event channels (default: 256).
	ChannelBufferSize int
}

// GraphAgent is an agent that executes a graph.
type GraphAgent struct {
	name           string
	description    string
	graph          *graph.Graph
	executor       *graph.Executor
	tools          []tool.Tool
	subAgents      []agent.Agent
	agentCallbacks *agent.AgentCallbacks
	initialState   graph.State
}

// New creates a new GraphAgent with the given graph and options.
func New(name string, g *graph.Graph, opts ...Option) (*GraphAgent, error) {
	var options Options

	// Apply function options.
	for _, opt := range opts {
		opt(&options)
	}

	// Create agent resolver
	resolver := &agentResolver{
		subAgents:         make(map[string]agent.Agent),
		channelBufferSize: options.ChannelBufferSize,
	}

	// Register sub-agents
	for _, subAgent := range options.SubAgents {
		resolver.subAgents[subAgent.Info().Name] = subAgent
	}

	// Set default channel buffer size if not specified.
	if options.ChannelBufferSize == 0 {
		options.ChannelBufferSize = 256
	}

	// Create executor
	executor, err := graph.NewExecutor(g, resolver,
		graph.WithChannelBufferSize(options.ChannelBufferSize))
	if err != nil {
		return nil, fmt.Errorf("failed to create graph executor: %w", err)
	}

	return &GraphAgent{
		name:           name,
		description:    options.Description,
		graph:          g,
		executor:       executor,
		tools:          options.Tools,
		subAgents:      options.SubAgents,
		agentCallbacks: options.AgentCallbacks,
		initialState:   options.InitialState,
	}, nil
}

// Run executes the graph with the provided invocation.
func (ga *GraphAgent) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	// Prepare initial state
	initialState := ga.initialState
	if initialState == nil {
		initialState = make(graph.State)
	}

	// Add invocation message to state
	if invocation.Message.Content != "" {
		initialState["input"] = invocation.Message.Content
		initialState["message"] = invocation.Message
	}

	// Add session context if available
	if invocation.Session != nil {
		initialState["session"] = invocation.Session
	}

	// Execute the graph
	return ga.executor.Execute(ctx, initialState, invocation.InvocationID)
}

// Tools returns the list of tools that this agent has access to.
func (ga *GraphAgent) Tools() []tool.Tool {
	return ga.tools
}

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

// agentResolver implements graph.AgentResolver.
type agentResolver struct {
	subAgents         map[string]agent.Agent
	channelBufferSize int
}

// ResolveAgent resolves an agent name to an agent executor.
func (ar *agentResolver) ResolveAgent(name string) (graph.AgentExecutor, error) {
	agent, exists := ar.subAgents[name]
	if !exists {
		return nil, fmt.Errorf("agent %s not found", name)
	}
	return &agentExecutor{
		agent:             agent,
		channelBufferSize: ar.channelBufferSize,
	}, nil
}

// agentExecutor wraps an agent.Agent to implement graph.AgentExecutor.
type agentExecutor struct {
	agent             agent.Agent
	channelBufferSize int
}

// Execute executes the agent with the given state.
func (ae *agentExecutor) Execute(ctx context.Context, state graph.State) (graph.State, <-chan *event.Event, error) {
	// Extract message from state
	var message model.Message
	if msg, ok := state["message"].(model.Message); ok {
		message = msg
	} else if input, ok := state["input"].(string); ok {
		message = model.Message{
			Role:    model.RoleUser,
			Content: input,
		}
	} else {
		message = model.Message{
			Role:    model.RoleUser,
			Content: "Execute agent task",
		}
	}

	// Create a minimal invocation
	invocation := &agent.Invocation{
		Agent:        ae.agent,
		AgentName:    ae.agent.Info().Name,
		InvocationID: fmt.Sprintf("graph-agent-%s", ae.agent.Info().Name),
		Message:      message,
	}

	// Run the agent
	eventChan, err := ae.agent.Run(ctx, invocation)
	if err != nil {
		return nil, nil, err
	}

	// Create output state
	outputState := state.Clone()

	// Create a new channel to forward events and collect results
	forwardChan := make(chan *event.Event, ae.channelBufferSize)

	go func() {
		defer close(forwardChan)
		var lastMessage string

		for event := range eventChan {
			// Forward the event
			forwardChan <- event

			// Extract message content from choices
			if event.Response != nil && len(event.Response.Choices) > 0 {
				lastMessage = event.Response.Choices[0].Message.Content
			}
		}

		// Update state with agent output
		if lastMessage != "" {
			outputState["output"] = lastMessage
			outputState["last_agent_output"] = lastMessage
		}
	}()

	return outputState, forwardChan, nil
}
