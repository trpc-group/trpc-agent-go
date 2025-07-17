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
	"errors"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

const (
	// AuthorGraphExecutor is the author of the graph executor.
	AuthorGraphExecutor = "graph-executor"
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
	if err := graph.validate(); err != nil {
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
func (e *Executor) Execute(
	ctx context.Context,
	initialState State,
	invocationID string,
) (<-chan *event.Event, error) {
	eventChan := make(chan *event.Event, e.channelBufferSize)
	go func() {
		defer close(eventChan)
		execCtx := &ExecutionContext{
			Graph:        e.graph,
			State:        initialState.Clone(),
			EventChan:    eventChan,
			InvocationID: invocationID,
		}
		execCtx.State[StateKeyExecContext] = execCtx
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

// executeGraph executes the graph starting from the entry point.
func (e *Executor) executeGraph(ctx context.Context, execCtx *ExecutionContext) error {
	currentNodeID := e.graph.EntryPoint()
	if currentNodeID == "" {
		return errors.New("no entry point found")
	}
	// Track visited nodes to detect infinite loops
	var stepCount int
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
							Content: fmt.Sprintf("%+v", execCtx.State[StateKeyLastResponse]),
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
	// Get current node.
	node, exists := e.graph.Node(nodeID)
	if !exists {
		return "", fmt.Errorf("node %s not found", nodeID)
	}
	// Send node start event if we have an event channel.
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
	// Execute the node function.
	if node.Function != nil {
		result, err := node.Function(ctx, execCtx.State)
		if err != nil {
			return "", fmt.Errorf("node function execution failed: %w", err)
		}
		// Handle different result types.
		if command, ok := result.(*Command); ok {
			// Apply state update from command.
			if command.Update != nil {
				execCtx.State = e.graph.Schema().ApplyUpdate(execCtx.State, command.Update)
			}

			// Return the specified routing target.
			if command.GoTo != "" {
				return command.GoTo, nil
			}
		} else if newState, ok := result.(State); ok {
			// Apply state updates using schema reducers.
			execCtx.State = e.graph.Schema().ApplyUpdate(execCtx.State, newState)
		} else {
			return "", fmt.Errorf("node function returned invalid result type: %T", result)
		}
	}
	// Determine next node using edges and conditional logic.
	return e.selectNextNode(ctx, execCtx, nodeID)
}

// selectNextNode selects the next node based on edges and conditional logic.
func (e *Executor) selectNextNode(
	ctx context.Context,
	execCtx *ExecutionContext,
	currentNodeID string,
) (string, error) {
	// Check for conditional edges first.
	if condEdge, exists := e.graph.ConditionalEdge(currentNodeID); exists {
		// Execute the condition function.
		conditionResult, err := condEdge.Condition(ctx, execCtx.State)
		if err != nil {
			return "", fmt.Errorf("conditional edge evaluation failed: %w", err)
		}
		// Look up the next node in the path map.
		if nextNode, exists := condEdge.PathMap[conditionResult]; exists {
			return nextNode, nil
		}
		return "", fmt.Errorf("condition result %s not found in path map", conditionResult)
	}
	// Check for regular edges.
	edges := e.graph.Edges(currentNodeID)
	if len(edges) == 0 {
		// No outgoing edges, assume we should go to End.
		return End, nil
	}
	// For now, take the first edge (typically has single edges or conditional).
	// In a more sophisticated implementation, we could support multiple parallel paths.
	return edges[0].To, nil
}
