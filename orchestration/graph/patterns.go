package graph

import (
	"context"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/core/message"
)

// Sequential creates a graph with nodes connected in sequence.
func Sequential(name, description string, nodes ...Node) *Graph {
	graph := NewGraph(name, description)

	// Add nodes with sequential connections
	for i, node := range nodes {
		nodeName := fmt.Sprintf("node_%d", i)
		graph.AddNode(nodeName, node)

		if i > 0 {
			prevNodeName := fmt.Sprintf("node_%d", i-1)
			graph.AddEdge(prevNodeName, nodeName)
		}
	}

	// Set start and end nodes
	if len(nodes) > 0 {
		graph.SetStartNode("node_0")
		graph.AddEndNode(fmt.Sprintf("node_%d", len(nodes)-1))
	}

	return graph
}

// Parallel creates a graph where multiple nodes execute in parallel and results are combined.
func Parallel(name, description string, combiner Node, nodes ...Node) *Graph {
	graph := NewGraph(name, description)

	// Add a start node
	graph.AddNode("start", NodeFunc(func(ctx context.Context, input *message.Message) (*message.Message, error) {
		return input, nil
	}).WithInfo("start", "Starting node that passes input to parallel branches"))

	// Add each parallel node
	for i, node := range nodes {
		nodeName := fmt.Sprintf("parallel_%d", i)
		graph.AddNode(nodeName, node)
		graph.AddEdge("start", nodeName)
	}

	// Add the combiner node
	graph.AddNode("combiner", combiner)

	// Connect all parallel nodes to the combiner
	for i := range nodes {
		nodeName := fmt.Sprintf("parallel_%d", i)
		graph.AddEdge(nodeName, "combiner")
	}

	// Set start and end nodes
	graph.SetStartNode("start")
	graph.AddEndNode("combiner")

	return graph
}

// Conditional creates a graph with conditional branching based on a condition function.
func Conditional(
	name, description string,
	condition func(ctx context.Context, msg *message.Message) bool,
	ifNode Node,
	elseNode Node,
) *Graph {
	graph := NewGraph(name, description)

	// Add condition node
	graph.AddNode("condition", NodeFunc(func(ctx context.Context, input *message.Message) (*message.Message, error) {
		return input, nil
	}).WithInfo("condition", "Node that evaluates the condition"))

	// Add if and else nodes
	graph.AddNode("if", ifNode)
	graph.AddNode("else", elseNode)

	// Add result node
	graph.AddNode("result", NodeFunc(func(ctx context.Context, input *message.Message) (*message.Message, error) {
		return input, nil
	}).WithInfo("result", "Final node that collects results"))

	// Add conditional edges
	graph.AddConditionalEdge("condition", "if", condition)
	graph.AddConditionalEdge("condition", "else", func(ctx context.Context, msg *message.Message) bool {
		return !condition(ctx, msg)
	})

	// Connect both branches to the result
	graph.AddEdge("if", "result")
	graph.AddEdge("else", "result")

	// Set start and end nodes
	graph.SetStartNode("condition")
	graph.AddEndNode("result")

	return graph
}

// Loop creates a graph that repeats execution until a condition is met.
func Loop(
	name, description string,
	bodyNode Node,
	condition func(ctx context.Context, msg *message.Message) bool,
	maxIterations int,
) *Graph {
	graph := NewGraph(name, description)

	// Add nodes
	graph.AddNode("start", NodeFunc(func(ctx context.Context, input *message.Message) (*message.Message, error) {
		// Initialize iteration counter
		input.SetMetadata("iteration", 0)
		return input, nil
	}).WithInfo("start", "Starting node that initializes loop state"))

	graph.AddNode("check", NodeFunc(func(ctx context.Context, input *message.Message) (*message.Message, error) {
		// Get current iteration
		iterVal, _ := input.GetMetadata("iteration")
		iteration, _ := iterVal.(int)

		// Check if we've reached max iterations
		if maxIterations > 0 && iteration >= maxIterations {
			input.SetMetadata("loop_exit_reason", "max_iterations_reached")
			return input, nil
		}

		return input, nil
	}).WithInfo("check", "Node that checks loop conditions"))

	graph.AddNode("body", bodyNode)

	graph.AddNode("increment", NodeFunc(func(ctx context.Context, input *message.Message) (*message.Message, error) {
		// Increment iteration counter
		iterVal, _ := input.GetMetadata("iteration")
		iteration, _ := iterVal.(int)
		input.SetMetadata("iteration", iteration+1)
		return input, nil
	}).WithInfo("increment", "Node that increments the iteration counter"))

	graph.AddNode("end", NodeFunc(func(ctx context.Context, input *message.Message) (*message.Message, error) {
		// Clean up loop metadata
		if _, exists := input.GetMetadata("loop_exit_reason"); exists {
			input.SetMetadata("loop_completed", true)
		}
		return input, nil
	}).WithInfo("end", "Final node that cleans up loop state"))

	// Connect nodes
	graph.AddEdge("start", "check")
	graph.AddConditionalEdge("check", "body", func(ctx context.Context, msg *message.Message) bool {
		// Continue loop if condition is true and we haven't reached max iterations
		if _, exists := msg.GetMetadata("loop_exit_reason"); exists {
			return false
		}
		return condition(ctx, msg)
	})
	graph.AddEdge("body", "increment")
	graph.AddEdge("increment", "check")
	graph.AddConditionalEdge("check", "end", func(ctx context.Context, msg *message.Message) bool {
		// Exit loop if condition is false or we've reached max iterations
		if _, exists := msg.GetMetadata("loop_exit_reason"); exists {
			return true
		}
		return !condition(ctx, msg)
	})

	// Set start and end nodes
	graph.SetStartNode("start")
	graph.AddEndNode("end")

	return graph
}

// Branch creates a graph that executes one of multiple branches based on a function.
func Branch(
	name, description string,
	branchSelector func(ctx context.Context, msg *message.Message) string,
	branches map[string]Node,
) *Graph {
	graph := NewGraph(name, description)

	// Add selector node
	graph.AddNode("selector", NodeFunc(func(ctx context.Context, input *message.Message) (*message.Message, error) {
		// Store the selected branch in metadata
		branch := branchSelector(ctx, input)
		input.SetMetadata("selected_branch", branch)
		return input, nil
	}).WithInfo("selector", "Node that selects which branch to execute"))

	// Add result node
	graph.AddNode("result", NodeFunc(func(ctx context.Context, input *message.Message) (*message.Message, error) {
		return input, nil
	}).WithInfo("result", "Final node that collects results"))

	// Add branch nodes and edges
	for branchName, node := range branches {
		nodeName := fmt.Sprintf("branch_%s", branchName)
		graph.AddNode(nodeName, node)

		// Add conditional edge from selector to this branch
		graph.AddConditionalEdge("selector", nodeName, func(ctx context.Context, msg *message.Message) bool {
			selectedVal, _ := msg.GetMetadata("selected_branch")
			selected, _ := selectedVal.(string)
			return selected == branchName
		})

		// Connect branch to result
		graph.AddEdge(nodeName, "result")
	}

	// Set start and end nodes
	graph.SetStartNode("selector")
	graph.AddEndNode("result")

	return graph
}

// Map creates a graph that applies a node to each item in a list and collects the results.
func Map(
	name, description string,
	itemsExtractor func(ctx context.Context, msg *message.Message) ([]interface{}, error),
	mapper Node,
	resultsCollector func(ctx context.Context, originalMsg *message.Message, results []*message.Message) (*message.Message, error),
) *Graph {
	graph := NewGraph(name, description)

	// Add extractor node
	graph.AddNode("extractor", NodeFunc(func(ctx context.Context, input *message.Message) (*message.Message, error) {
		// Extract items and store in metadata
		items, err := itemsExtractor(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("failed to extract items: %w", err)
		}

		input.SetMetadata("map_items", items)
		input.SetMetadata("map_results", make([]*message.Message, 0))
		input.SetMetadata("map_index", 0)

		return input, nil
	}).WithInfo("extractor", "Node that extracts items to map over"))

	// Add loop node
	graph.AddNode("loop", NodeFunc(func(ctx context.Context, input *message.Message) (*message.Message, error) {
		// Get current state
		itemsVal, _ := input.GetMetadata("map_items")
		items, _ := itemsVal.([]interface{})

		indexVal, _ := input.GetMetadata("map_index")
		index, _ := indexVal.(int)

		resultsVal, _ := input.GetMetadata("map_results")
		results, _ := resultsVal.([]*message.Message)

		// Process all items
		for i := index; i < len(items); i++ {
			// Create a message for this item
			itemMsg := message.NewMessage(input.Role, "")
			itemMsg.SetMetadata("item", items[i])
			itemMsg.SetMetadata("index", i)

			// Process the item
			resultMsg, err := mapper.Process(ctx, itemMsg)
			if err != nil {
				return nil, fmt.Errorf("failed to process item %d: %w", i, err)
			}

			// Store the result
			results = append(results, resultMsg)
			input.SetMetadata("map_results", results)
			input.SetMetadata("map_index", i+1)
		}

		return input, nil
	}).WithInfo("loop", "Node that processes each item"))

	// Add collector node
	graph.AddNode("collector", NodeFunc(func(ctx context.Context, input *message.Message) (*message.Message, error) {
		// Get results
		resultsVal, _ := input.GetMetadata("map_results")
		results, _ := resultsVal.([]*message.Message)

		// Collect results
		return resultsCollector(ctx, input, results)
	}).WithInfo("collector", "Node that collects mapping results"))

	// Connect nodes
	graph.AddEdge("extractor", "loop")
	graph.AddEdge("loop", "collector")

	// Set start and end nodes
	graph.SetStartNode("extractor")
	graph.AddEndNode("collector")

	return graph
}

// Router creates a graph that can be used to route messages to different subgraphs.
func Router(
	name, description string,
	routeSelector func(ctx context.Context, msg *message.Message) string,
	routes map[string]*Graph,
) *Graph {
	graph := NewGraph(name, description)

	// Add router node
	graph.AddNode("router", NodeFunc(func(ctx context.Context, input *message.Message) (*message.Message, error) {
		// Get the route
		route := routeSelector(ctx, input)
		input.SetMetadata("selected_route", route)
		return input, nil
	}).WithInfo("router", "Node that selects which route to take"))

	// Add subgraph nodes
	for routeName, subgraph := range routes {
		nodeName := fmt.Sprintf("route_%s", routeName)

		// Create a node that executes the subgraph
		graph.AddNode(nodeName, NodeFunc(func(ctx context.Context, input *message.Message) (*message.Message, error) {
			// Create a runner for the subgraph
			runner, err := NewGraphRunner(subgraph)
			if err != nil {
				return nil, fmt.Errorf("failed to create runner for subgraph: %w", err)
			}

			// Execute the subgraph
			return runner.Execute(ctx, input)
		}).WithInfo(fmt.Sprintf("subgraph_%s", routeName), fmt.Sprintf("Executes the %s subgraph", routeName)))

		// Add conditional edge from router to this subgraph
		graph.AddConditionalEdge("router", nodeName, func(ctx context.Context, msg *message.Message) bool {
			routeVal, _ := msg.GetMetadata("selected_route")
			route, _ := routeVal.(string)
			return route == routeName
		})
	}

	// Add result node
	graph.AddNode("result", NodeFunc(func(ctx context.Context, input *message.Message) (*message.Message, error) {
		return input, nil
	}).WithInfo("result", "Final node that collects results"))

	// Connect all route nodes to the result node
	for routeName := range routes {
		nodeName := fmt.Sprintf("route_%s", routeName)
		graph.AddEdge(nodeName, "result")
	}

	// Set start and end nodes
	graph.SetStartNode("router")
	graph.AddEndNode("result")

	return graph
}
