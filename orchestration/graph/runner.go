package graph

import (
	"context"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/core/event"
	"trpc.group/trpc-go/trpc-agent-go/core/message"
)

// StreamHandler is a function that handles streaming events.
type StreamHandler func(evt *event.Event)

// RunOptions contains options for executing a graph.
type RunOptions struct {
	// StreamHandlers are callbacks for handling streaming events.
	StreamHandlers []StreamHandler
}

// DefaultRunOptions returns the default options for executing a graph.
func DefaultRunOptions() RunOptions {
	return RunOptions{}
}

// Runner executes graphs.
type Runner struct {
	graph *Graph
}

// NewGraphRunner creates a new graph runner.
func NewGraphRunner(graph *Graph) (*Runner, error) {
	if err := graph.Validate(); err != nil {
		return nil, fmt.Errorf("invalid graph: %w", err)
	}
	return &Runner{
		graph: graph,
	}, nil
}

// Execute runs the graph with the given options.
func (r *Runner) Execute(ctx context.Context, input *message.Message, opts ...RunOptions) (*message.Message, error) {
	return r.executeSync(ctx, input)
}

// ExecuteStream runs the graph and returns the streaming channel directly.
// This is useful when the caller wants to handle streaming manually.
func (r *Runner) ExecuteStream(ctx context.Context, input *message.Message) (<-chan *event.Event, error) {
	return r.streamGraph(ctx, input)
}

// executeSync runs the graph synchronously without streaming.
func (r *Runner) executeSync(ctx context.Context, input *message.Message) (*message.Message, error) {
	// Initialize execution state
	state := &executionState{
		input:   input,
		output:  nil,
		visited: make(map[string]bool),
		queue:   []string{r.graph.startNode},
	}

	// Process nodes until we reach an end node or run out of nodes
	for len(state.queue) > 0 {
		// Pop the next node from the queue
		nodeName := state.queue[0]
		state.queue = state.queue[1:]

		// Skip if already visited
		if state.visited[nodeName] {
			continue
		}

		// Process the node
		output, err := r.processNode(ctx, nodeName, state.input)
		if err != nil {
			return nil, fmt.Errorf("node %s failed: %w", nodeName, err)
		}

		// Update state
		state.input = output
		state.output = output
		state.visited[nodeName] = true

		// Check if this is an end node
		if r.graph.endNodes[nodeName] {
			return output, nil
		}

		// Add next nodes to the queue
		for _, edge := range r.graph.edges {
			if edge.From == nodeName {
				// Check condition if present
				if edge.Condition != nil {
					if !edge.Condition(ctx, output) {
						continue
					}
				}
				// Add to queue if not already visited
				if !visited(state.visited, edge.To) {
					state.queue = append(state.queue, edge.To)
				}
			}
		}
	}

	return nil, fmt.Errorf("graph execution did not reach an end node")
}

// streamGraph executes the graph with full streaming capabilities.
// All nodes that support streaming will use their native streaming implementation.
func (r *Runner) streamGraph(ctx context.Context, input *message.Message) (<-chan *event.Event, error) {
	eventCh := make(chan *event.Event, 10)

	go func() {
		defer close(eventCh)

		// Signal stream start
		eventCh <- event.NewStreamStartEvent("")

		// Initialize execution state
		state := &executionState{
			input:   input,
			output:  nil,
			visited: make(map[string]bool),
			queue:   []string{r.graph.startNode},
		}

		// Process nodes until we reach an end node or run out of nodes
		for len(state.queue) > 0 {
			// Pop the next node from the queue
			nodeName := state.queue[0]
			state.queue = state.queue[1:]

			// Skip if already visited
			if state.visited[nodeName] {
				continue
			}

			// Get the node
			node, exists := r.graph.nodes[nodeName]
			if !exists {
				eventCh <- event.NewErrorEvent(fmt.Errorf("node %s not found", nodeName), 500)
				return
			}

			// Emit event for node start
			eventCh <- event.NewCustomEvent("node_start", map[string]interface{}{
				"node_name": nodeName,
				"node_info": node.Info(),
			})

			// Check if the node supports streaming
			var output *message.Message
			var err error

			if node.SupportsStreaming() {
				// Process the node with streaming
				nodeEventCh, err := node.ProcessStream(ctx, state.input)
				if err != nil {
					eventCh <- event.NewErrorEvent(fmt.Errorf("node %s failed: %w", nodeName, err), 500)
					return
				}

				// Forward all events from the node
				var lastMsg *message.Message
				for evt := range nodeEventCh {
					// Forward the event
					eventCh <- evt

					// If this is a message event, capture it for later use
					if evt.Type == event.TypeMessage {
						if msg, ok := evt.Data.(*message.Message); ok {
							lastMsg = msg
						}
					}
				}

				// Use the last message as the output
				if lastMsg != nil {
					output = lastMsg
				} else {
					// If no message was captured, something went wrong
					eventCh <- event.NewErrorEvent(fmt.Errorf("node %s did not produce any output", nodeName), 500)
					return
				}
			} else {
				// Process the node normally
				output, err = node.Process(ctx, state.input)
				if err != nil {
					eventCh <- event.NewErrorEvent(fmt.Errorf("node %s failed: %w", nodeName, err), 500)
					return
				}

				// Emit event for node output
				eventCh <- event.NewCustomEvent("node_output", map[string]interface{}{
					"node_name": nodeName,
					"output":    output,
				})
			}

			// Update state
			state.input = output
			state.output = output
			state.visited[nodeName] = true

			// Check if this is an end node
			if r.graph.endNodes[nodeName] {
				// Emit final message
				eventCh <- event.NewMessageEvent(output)
				eventCh <- event.NewStreamEndEvent(output.Content)
				return
			}

			// Add next nodes to the queue
			for _, edge := range r.graph.edges {
				if edge.From == nodeName {
					// Check condition if present
					if edge.Condition != nil {
						if !edge.Condition(ctx, output) {
							continue
						}
					}
					// Add to queue if not already visited
					if !visited(state.visited, edge.To) {
						state.queue = append(state.queue, edge.To)
					}
				}
			}
		}

		// If we get here, the graph execution did not reach an end node
		eventCh <- event.NewErrorEvent(fmt.Errorf("graph execution did not reach an end node"), 500)
	}()

	return eventCh, nil
}

// processNode processes a single node in the graph.
func (r *Runner) processNode(ctx context.Context, nodeName string, input *message.Message) (*message.Message, error) {
	node, exists := r.graph.nodes[nodeName]
	if !exists {
		return nil, fmt.Errorf("node %s not found", nodeName)
	}

	return node.Process(ctx, input)
}

// executionState tracks the state of a graph execution.
type executionState struct {
	input   *message.Message
	output  *message.Message
	visited map[string]bool
	queue   []string
}

// visited checks if a node has been visited.
func visited(visitedMap map[string]bool, nodeName string) bool {
	return visitedMap[nodeName]
}
