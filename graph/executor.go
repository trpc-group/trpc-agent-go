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

package graph

import (
	"context"
	"fmt"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// Executor executes a graph with the given initial state.
type Executor struct {
	graph             *Graph
	channelBufferSize int
}

// ExecutorOption is a function that configures an Executor.
type ExecutorOption func(*ExecutorOptions)

// ExecutorOptions contains configuration options for creating an Executor.
type ExecutorOptions struct {
	// ChannelBufferSize is the buffer size for event channels (default: 256).
	ChannelBufferSize int
}

// WithChannelBufferSize sets the buffer size for event channels.
func WithChannelBufferSize(size int) ExecutorOption {
	return func(opts *ExecutorOptions) {
		opts.ChannelBufferSize = size
	}
}

// NewExecutor creates a new graph executor.
func NewExecutor(graph *Graph, opts ...ExecutorOption) (*Executor, error) {
	if err := graph.Validate(); err != nil {
		return nil, fmt.Errorf("invalid graph: %w", err)
	}

	var options ExecutorOptions
	options.ChannelBufferSize = 256 // Default buffer size.

	// Apply function options.
	for _, opt := range opts {
		opt(&options)
	}
	return &Executor{
		graph:             graph,
		channelBufferSize: options.ChannelBufferSize,
	}, nil
}

// Execute executes the graph with the given initial state.
func (e *Executor) Execute(ctx context.Context, initialState State,
	invocationID string) (<-chan *event.Event, error) {
	eventChan := make(chan *event.Event, e.channelBufferSize)

	go func() {
		defer close(eventChan)

		execCtx := &ExecutionContext{
			Graph:        e.graph,
			State:        initialState.Clone(),
			EventChan:    eventChan,
			InvocationID: invocationID,
		}

		if err := e.executeGraph(ctx, execCtx); err != nil {
			// Send error event.
			errorEvent := event.NewErrorEvent(invocationID, AuthorGraphExecutor,
				ErrorTypeGraphExecution, err.Error())
			select {
			case eventChan <- errorEvent:
			case <-ctx.Done():
			}
		}
	}()
	return eventChan, nil
}

// Invoke executes the graph and returns the final state.
func (e *Executor) Invoke(ctx context.Context, initialState State) (State, error) {
	// Create a temporary invocation ID
	invocationID := fmt.Sprintf("invoke-%d", makeTimestamp())

	// Validate initial state against schema
	if err := e.graph.GetSchema().Validate(initialState); err != nil {
		return nil, fmt.Errorf("initial state validation failed: %w", err)
	}

	execCtx := &ExecutionContext{
		Graph:        e.graph,
		State:        initialState.Clone(),
		EventChan:    nil, // No event channel for direct invoke
		InvocationID: invocationID,
	}

	if err := e.executeGraph(ctx, execCtx); err != nil {
		return nil, err
	}
	return execCtx.State, nil
}

// executeGraph executes the graph starting from the entry point.
func (e *Executor) executeGraph(ctx context.Context, execCtx *ExecutionContext) error {
	currentNodeID := e.graph.GetEntryPoint()
	if currentNodeID == "" {
		return fmt.Errorf("no entry point found")
	}

	// Track visited nodes to detect infinite loops
	stepCount := 0
	maxSteps := 100 // Configurable recursion limit

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Check step limit to prevent infinite loops
		stepCount++
		if stepCount > maxSteps {
			return fmt.Errorf("maximum execution steps (%d) exceeded", maxSteps)
		}

		// Check if we've reached End
		if currentNodeID == End {
			// Send completion event if we have an event channel
			if execCtx.EventChan != nil {
				completionEvent := event.New(execCtx.InvocationID, AuthorGraphExecutor)
				completionEvent.Response.Done = true
				completionEvent.Response.Choices = []model.Choice{
					{
						Index: 0,
						Message: model.Message{
							Role:    model.RoleAssistant,
							Content: MessageGraphCompleted,
						},
					},
				}
				select {
				case execCtx.EventChan <- completionEvent:
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			return nil
		}

		// Execute the current node and get next node
		nextNodeID, err := e.executeNode(ctx, execCtx, currentNodeID)
		if err != nil {
			return fmt.Errorf("error executing node %s: %w", currentNodeID, err)
		}

		currentNodeID = nextNodeID
	}
}

// executeNode executes a single node and returns the next node ID.
func (e *Executor) executeNode(ctx context.Context, execCtx *ExecutionContext, nodeID string) (string, error) {
	// Get current node
	node, exists := e.graph.GetNode(nodeID)
	if !exists {
		return "", fmt.Errorf("node %s not found", nodeID)
	}

	// Send node start event if we have an event channel
	if execCtx.EventChan != nil {
		startEvent := event.New(execCtx.InvocationID, AuthorGraphExecutor)
		startEvent.Response.Choices = []model.Choice{
			{
				Index: 0,
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: fmt.Sprintf("Executing node: %s (%s)", node.Name, node.ID),
				},
			},
		}
		select {
		case execCtx.EventChan <- startEvent:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}

	// Execute the node function if it exists
	if node.Function != nil {
		result, err := node.Function(ctx, execCtx.State)
		if err != nil {
			return "", fmt.Errorf("node function execution failed: %w", err)
		}

		// Handle different result types
		if IsCommand(result) {
			command := result.(*Command)

			// Apply state update from command
			if command.Update != nil {
				execCtx.State = e.graph.GetSchema().ApplyUpdate(execCtx.State, command.Update)
			}

			// Return the specified routing target
			if command.GoTo != "" {
				return command.GoTo, nil
			}
		} else if IsState(result) {
			// Apply state updates using schema reducers
			newState := result.(State)
			execCtx.State = e.graph.GetSchema().ApplyUpdate(execCtx.State, newState)
		} else {
			return "", fmt.Errorf("node function returned invalid result type: %T", result)
		}
	}

	// Determine next node using edges and conditional logic (only if not routed by Command)
	return e.selectNextNode(ctx, execCtx, nodeID)
}

// selectNextNode selects the next node based on edges and conditional logic.
func (e *Executor) selectNextNode(ctx context.Context, execCtx *ExecutionContext, currentNodeID string) (string, error) {
	// Check for conditional edges first
	if condEdge, exists := e.graph.GetConditionalEdge(currentNodeID); exists {
		// Execute the condition function
		conditionResult, err := condEdge.Condition(ctx, execCtx.State)
		if err != nil {
			return "", fmt.Errorf("conditional edge evaluation failed: %w", err)
		}

		// Look up the next node in the path map
		if nextNode, exists := condEdge.PathMap[conditionResult]; exists {
			return nextNode, nil
		}

		return "", fmt.Errorf("condition result %s not found in path map", conditionResult)
	}

	// Check for regular edges
	edges := e.graph.GetEdges(currentNodeID)
	if len(edges) == 0 {
		// No outgoing edges, assume we should go to End
		return End, nil
	}

	// For now, take the first edge (typically has single edges or conditional)
	// In a more sophisticated implementation, we could support multiple parallel paths
	return edges[0].To, nil
}

// Stream executes the graph and streams events.
func (e *Executor) Stream(ctx context.Context, initialState State, invocationID string) (<-chan *event.Event, error) {
	return e.Execute(ctx, initialState, invocationID)
}

// Helper function to generate timestamps for invocation IDs.
func makeTimestamp() int64 {
	return time.Now().UnixNano()
}

// ExecutionMode represents different execution modes.
type ExecutionMode string

const (
	// ExecutionModeValues streams full state values after each step.
	ExecutionModeValues ExecutionMode = "values"
	// ExecutionModeUpdates streams only state updates after each step.
	ExecutionModeUpdates ExecutionMode = "updates"
)

// StreamOption is a functional option for configuring stream execution.
type StreamOption func(*streamOptions)

// streamOptions contains internal configuration for streaming execution.
type streamOptions struct {
	Mode         ExecutionMode
	InvocationID string
}

// StreamConfig contains configuration for streaming execution.
// Deprecated: Use functional options with StreamWithOptions instead.
type StreamConfig struct {
	Mode         ExecutionMode
	InvocationID string
}

// WithStreamMode sets the execution mode for streaming.
func WithStreamMode(mode ExecutionMode) StreamOption {
	return func(opts *streamOptions) {
		opts.Mode = mode
	}
}

// WithInvocationID sets a custom invocation ID for streaming.
func WithInvocationID(id string) StreamOption {
	return func(opts *streamOptions) {
		opts.InvocationID = id
	}
}

// StreamWithOptions executes the graph with functional options for streaming configuration.
// This provides more control over streaming behavior using the functional options pattern.
func (e *Executor) StreamWithOptions(ctx context.Context, initialState State, options ...StreamOption) (<-chan *event.Event, error) {
	// Apply default options
	opts := &streamOptions{
		Mode:         ExecutionModeValues,
		InvocationID: fmt.Sprintf("stream-%d", makeTimestamp()),
	}

	// Apply provided options
	for _, option := range options {
		option(opts)
	}

	// For now, use the same execution path regardless of mode
	// In a full implementation, different modes would affect event generation
	return e.Execute(ctx, initialState, opts.InvocationID)
}

// StreamWithConfig executes the graph with streaming configuration.
// Deprecated: Use StreamWithOptions instead for better flexibility.
func (e *Executor) StreamWithConfig(ctx context.Context, initialState State, config StreamConfig) (<-chan *event.Event, error) {
	return e.StreamWithOptions(ctx, initialState,
		WithStreamMode(config.Mode),
		WithInvocationID(config.InvocationID))
}

// GetState returns a copy of the current execution state.
// Note: State persistence is not implemented in this version.
// This method returns an empty state as the executor doesn't maintain persistent state.
func (e *Executor) GetState() State {
	// In a full implementation with checkpointing, this would return the current state.
	// For now, return empty state to indicate no persistent state management.
	return make(State)
}

// UpdateState updates the execution state.
// Note: State persistence is not implemented in this version.
// This method returns nil but does not actually update any persistent state.
func (e *Executor) UpdateState(update State) error {
	// In a full implementation with checkpointing, this would update persistent state.
	// For now, return nil to indicate the operation completes but has no effect.
	return nil
}
