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

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// Executor executes a graph with the given initial state.
type Executor struct {
	graph             *Graph
	agentResolver     AgentResolver
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
func NewExecutor(graph *Graph, agentResolver AgentResolver,
	opts ...ExecutorOption) (*Executor, error) {
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
		agentResolver:     agentResolver,
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
			Graph:         e.graph,
			State:         initialState.Clone(),
			EventChan:     eventChan,
			InvocationID:  invocationID,
			AgentResolver: e.agentResolver,
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

// executeGraph executes the graph starting from the start node.
func (e *Executor) executeGraph(ctx context.Context, execCtx *ExecutionContext) error {
	currentNodeID := e.graph.GetStartNode()
	if currentNodeID == "" {
		return fmt.Errorf("no start node found")
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Check if we've reached an end node
		if e.graph.IsEndNode(currentNodeID) {
			// Send completion event
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
			return nil
		}

		// Get current node
		node, exists := e.graph.GetNode(currentNodeID)
		if !exists {
			return fmt.Errorf("node %s not found", currentNodeID)
		}

		// Execute the node
		nextNodeID, err := e.executeNode(ctx, execCtx, node)
		if err != nil {
			return fmt.Errorf("error executing node %s: %w", currentNodeID, err)
		}

		currentNodeID = nextNodeID
	}
}

// executeNode executes a single node and returns the next node ID.
func (e *Executor) executeNode(ctx context.Context, execCtx *ExecutionContext, node *Node) (string, error) {
	// Send node start event
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

	var err error
	switch node.Type {
	case NodeTypeStart:
		// Start node doesn't execute anything, just pass through
	case NodeTypeFunction:
		if node.Function != nil {
			execCtx.State, err = node.Function(ctx, execCtx.State)
			if err != nil {
				return "", fmt.Errorf("function execution failed: %w", err)
			}
		}
	case NodeTypeAgent:
		if node.AgentName != "" {
			agent, resolveErr := execCtx.AgentResolver.ResolveAgent(node.AgentName)
			if resolveErr != nil {
				return "", fmt.Errorf("failed to resolve agent %s: %w", node.AgentName, resolveErr)
			}

			newState, agentEvents, execErr := agent.Execute(ctx, execCtx.State)
			if execErr != nil {
				return "", fmt.Errorf("agent execution failed: %w", execErr)
			}

			// Forward agent events
			go func() {
				for agentEvent := range agentEvents {
					select {
					case execCtx.EventChan <- agentEvent:
					case <-ctx.Done():
						return
					}
				}
			}()

			execCtx.State = newState
		}
	case NodeTypeCondition:
		// Condition nodes are handled in edge selection
	case NodeTypeEnd:
		// End nodes don't execute anything
	default:
		return "", fmt.Errorf("unknown node type: %s", node.Type)
	}

	// Determine next node
	return e.selectNextNode(ctx, execCtx, node)
}

// selectNextNode selects the next node based on edges and conditions.
func (e *Executor) selectNextNode(ctx context.Context, execCtx *ExecutionContext, currentNode *Node) (string, error) {
	edges := e.graph.GetEdges(currentNode.ID)
	if len(edges) == 0 {
		return "", fmt.Errorf("no outgoing edges from node %s", currentNode.ID)
	}

	// For condition nodes, use the condition function
	if currentNode.Type == NodeTypeCondition && currentNode.Condition != nil {
		nextNodeID, err := currentNode.Condition(ctx, execCtx.State)
		if err != nil {
			return "", fmt.Errorf("condition evaluation failed: %w", err)
		}

		// Verify the selected node is reachable via edges
		for _, edge := range edges {
			if edge.To == nextNodeID {
				return nextNodeID, nil
			}
		}
		return "", fmt.Errorf("condition selected unreachable node %s", nextNodeID)
	}

	// For other nodes, take the first edge (or implement more sophisticated logic)
	if len(edges) == 1 {
		return edges[0].To, nil
	}

	// If multiple edges, we need some logic to choose
	// For now, take the first one without a condition, or the first one overall
	for _, edge := range edges {
		if edge.Condition == "" {
			return edge.To, nil
		}
	}

	// If all edges have conditions, take the first one
	return edges[0].To, nil
}
