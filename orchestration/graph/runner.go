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
	// MaxIterations sets the maximum number of iterations to prevent infinite loops.
	// Default is 10 if not specified.
	MaxIterations int
	// EnableLoopDetection enables advanced loop detection based on node visit patterns.
	EnableLoopDetection bool
}

// DefaultRunOptions returns the default options for executing a graph.
func DefaultRunOptions() RunOptions {
	return RunOptions{
		MaxIterations:       10,
		EnableLoopDetection: true,
	}
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
	options := DefaultRunOptions()
	if len(opts) > 0 {
		options = opts[0]
		// Apply defaults for unset values
		if options.MaxIterations == 0 {
			options.MaxIterations = 10
		}
	}
	return r.executeSync(ctx, input, options)
}

// ExecuteStream runs the graph and returns the streaming channel directly.
// This is useful when the caller wants to handle streaming manually.
func (r *Runner) ExecuteStream(ctx context.Context, input *message.Message, opts ...RunOptions) (<-chan *event.Event, error) {
	options := DefaultRunOptions()
	if len(opts) > 0 {
		options = opts[0]
		// Apply defaults for unset values
		if options.MaxIterations == 0 {
			options.MaxIterations = 10
		}
	}
	return r.streamGraph(ctx, input, options)
}

// executeSync runs the graph synchronously without streaming.
func (r *Runner) executeSync(ctx context.Context, input *message.Message, opts RunOptions) (*message.Message, error) {
	// Initialize execution state with iteration control
	state := &executionState{
		input:           input,
		output:          nil,
		iterationCount:  0,
		maxIterations:   opts.MaxIterations,
		nodeVisitCounts: make(map[string]int),
		queue:           []string{r.graph.startNode},
		loopDetection:   opts.EnableLoopDetection,
	}

	// Process nodes until we reach an end node or exceed iteration limit
	for len(state.queue) > 0 {
		// Check iteration limit to prevent infinite loops
		if state.iterationCount >= state.maxIterations {
			return nil, fmt.Errorf("maximum iterations (%d) exceeded, possible infinite loop", state.maxIterations)
		}

		// Pop the next node from the queue
		nodeName := state.queue[0]
		state.queue = state.queue[1:]

		// Increment iteration count for each node processing
		state.iterationCount++
		state.nodeVisitCounts[nodeName]++

		// Advanced loop detection: if a node has been visited too many times, it might be a tight loop
		if state.loopDetection && state.nodeVisitCounts[nodeName] > state.maxIterations/2 {
			return nil, fmt.Errorf("node %s visited %d times, possible tight loop detected", nodeName, state.nodeVisitCounts[nodeName])
		}

		// Process the node
		output, err := r.processNode(ctx, nodeName, state.input)
		if err != nil {
			return nil, fmt.Errorf("node %s failed (iteration %d): %w", nodeName, state.iterationCount, err)
		}

		// Update state with new output
		state.input = output
		state.output = output

		// Check if this is an end node
		if r.graph.endNodes[nodeName] {
			return output, nil
		}

		// Re-evaluate all conditional edges from this node
		// This is key difference from visited-based approach: conditions are always re-evaluated
		nextNodes := r.evaluateNextNodes(ctx, nodeName, output)

		// Add next nodes to queue (duplicates will be processed as separate iterations)
		state.queue = append(state.queue, nextNodes...)
	}

	return nil, fmt.Errorf("graph execution did not reach an end node after %d iterations", state.iterationCount)
}

// streamGraph executes the graph with full streaming capabilities and loop support.
func (r *Runner) streamGraph(ctx context.Context, input *message.Message, opts RunOptions) (<-chan *event.Event, error) {
	eventCh := make(chan *event.Event, 10)

	go func() {
		defer close(eventCh)

		// Signal stream start
		eventCh <- event.NewStreamStartEvent("")

		// Initialize execution state with iteration control
		state := &executionState{
			input:           input,
			output:          nil,
			iterationCount:  0,
			maxIterations:   opts.MaxIterations,
			nodeVisitCounts: make(map[string]int),
			queue:           []string{r.graph.startNode},
			loopDetection:   opts.EnableLoopDetection,
		}

		// Process nodes until we reach an end node or exceed iteration limit
		for len(state.queue) > 0 {
			// Check iteration limit to prevent infinite loops
			if state.iterationCount >= state.maxIterations {
				eventCh <- event.NewErrorEvent(fmt.Errorf("maximum iterations (%d) exceeded, possible infinite loop", state.maxIterations), 500)
				return
			}

			// Pop the next node from the queue
			nodeName := state.queue[0]
			state.queue = state.queue[1:]

			// Increment iteration count for each node processing
			state.iterationCount++
			state.nodeVisitCounts[nodeName]++

			// Advanced loop detection
			if state.loopDetection && state.nodeVisitCounts[nodeName] > state.maxIterations/2 {
				eventCh <- event.NewErrorEvent(fmt.Errorf("node %s visited %d times, possible tight loop detected", nodeName, state.nodeVisitCounts[nodeName]), 500)
				return
			}

			// Get the node
			node, exists := r.graph.nodes[nodeName]
			if !exists {
				eventCh <- event.NewErrorEvent(fmt.Errorf("node %s not found", nodeName), 500)
				return
			}

			// Emit event for node start with iteration info
			eventCh <- event.NewCustomEvent("node_start", map[string]interface{}{
				"node_name":    nodeName,
				"node_info":    node.Info(),
				"iteration":    state.iterationCount,
				"visit_count":  state.nodeVisitCounts[nodeName],
				"total_visits": getTotalVisits(state.nodeVisitCounts),
			})

			// Check if the node supports streaming
			var output *message.Message
			var err error

			if node.SupportsStreaming() {
				// Process the node with streaming
				nodeEventCh, err := node.ProcessStream(ctx, state.input)
				if err != nil {
					eventCh <- event.NewErrorEvent(fmt.Errorf("node %s failed (iteration %d): %w", nodeName, state.iterationCount, err), 500)
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
					eventCh <- event.NewErrorEvent(fmt.Errorf("node %s failed (iteration %d): %w", nodeName, state.iterationCount, err), 500)
					return
				}

				// Emit event for node output
				eventCh <- event.NewCustomEvent("node_output", map[string]interface{}{
					"node_name": nodeName,
					"output":    output,
					"iteration": state.iterationCount,
				})
			}

			// Update state with new output
			state.input = output
			state.output = output

			// Check if this is an end node
			if r.graph.endNodes[nodeName] {
				// Emit final message with iteration info
				eventCh <- event.NewMessageEvent(output)
				eventCh <- event.NewCustomEvent("execution_complete", map[string]interface{}{
					"total_iterations": state.iterationCount,
					"node_visits":      state.nodeVisitCounts,
				})
				eventCh <- event.NewStreamEndEvent(output.Content)
				return
			}

			// Re-evaluate all conditional edges from this node
			nextNodes := r.evaluateNextNodes(ctx, nodeName, output)

			// Add next nodes to queue
			state.queue = append(state.queue, nextNodes...)

			// Emit debug info about next nodes
			if len(nextNodes) > 0 {
				eventCh <- event.NewCustomEvent("routing_decision", map[string]interface{}{
					"from_node":  nodeName,
					"next_nodes": nextNodes,
					"iteration":  state.iterationCount,
				})
			}
		}

		// If we get here, the graph execution did not reach an end node
		eventCh <- event.NewErrorEvent(fmt.Errorf("graph execution did not reach an end node after %d iterations", state.iterationCount), 500)
	}()

	return eventCh, nil
}

// evaluateNextNodes determines which nodes should be executed next based on current output.
// This replaces the visited-based logic with condition-based routing that can be re-evaluated.
func (r *Runner) evaluateNextNodes(ctx context.Context, fromNode string, output *message.Message) []string {
	var nextNodes []string

	// Evaluate all edges from the current node
	for _, edge := range r.graph.edges {
		if edge.From == fromNode {
			// Always check conditions regardless of previous visits
			shouldFollow := true
			if edge.Condition != nil {
				shouldFollow = edge.Condition(ctx, output)
			}

			if shouldFollow {
				nextNodes = append(nextNodes, edge.To)
			}
		}
	}

	return nextNodes
}

// processNode processes a single node in the graph.
func (r *Runner) processNode(ctx context.Context, nodeName string, input *message.Message) (*message.Message, error) {
	node, exists := r.graph.nodes[nodeName]
	if !exists {
		return nil, fmt.Errorf("node %s not found", nodeName)
	}

	return node.Process(ctx, input)
}

// executionState tracks the state of a graph execution with iteration control.
type executionState struct {
	input           *message.Message
	output          *message.Message
	iterationCount  int            // Current iteration number
	maxIterations   int            // Maximum allowed iterations
	nodeVisitCounts map[string]int // Count of visits per node for loop detection
	queue           []string       // Queue of nodes to process
	loopDetection   bool           // Whether to enable advanced loop detection
}

// getTotalVisits calculates the total number of node visits across all nodes.
func getTotalVisits(visitCounts map[string]int) int {
	total := 0
	for _, count := range visitCounts {
		total += count
	}
	return total
}
