// Package graph provides a graph-based orchestration system for agent workflows.
package graph

import (
	"context"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/core/event"
	"trpc.group/trpc-go/trpc-agent-go/core/message"
)

// Node represents a processing unit in a graph.
type Node interface {
	// Process handles a message and produces a result.
	Process(ctx context.Context, input *message.Message) (*message.Message, error)

	// ProcessStream handles a message and produces a stream of events.
	// Nodes can use this to provide streaming output.
	ProcessStream(ctx context.Context, input *message.Message) (<-chan *event.Event, error)

	// SupportsStreaming returns whether this node supports native streaming.
	// If false, the graph runner will use Process and convert the result to a stream.
	SupportsStreaming() bool

	// Info returns metadata about the node.
	Info() NodeInfo
}

// NodeInfo contains metadata about a node.
type NodeInfo struct {
	Name        string
	Description string
	Type        string
}

// NodeFunc is a function that implements the Node interface.
type NodeFunc func(ctx context.Context, input *message.Message) (*message.Message, error)

// Process implements the Node interface for NodeFunc.
func (f NodeFunc) Process(ctx context.Context, input *message.Message) (*message.Message, error) {
	return f(ctx, input)
}

// ProcessStream implements the Node interface for NodeFunc.
// This default implementation wraps the Process method with a stream.
func (f NodeFunc) ProcessStream(ctx context.Context, input *message.Message) (<-chan *event.Event, error) {
	eventCh := make(chan *event.Event, 10)

	go func() {
		defer close(eventCh)

		// Signal stream start
		eventCh <- event.NewStreamStartEvent("")

		// Process the input
		output, err := f(ctx, input)
		if err != nil {
			eventCh <- event.NewErrorEvent(err, 500)
			return
		}

		// Send the result as a message event
		eventCh <- event.NewMessageEvent(output)

		// Signal stream end
		eventCh <- event.NewStreamEndEvent(output.Content)
	}()

	return eventCh, nil
}

// SupportsStreaming implements the Node interface for NodeFunc.
func (f NodeFunc) SupportsStreaming() bool {
	return false // Default NodeFunc doesn't support native streaming
}

// Info returns metadata about the node function.
func (f NodeFunc) Info() NodeInfo {
	return NodeInfo{
		Name:        "anonymous-node",
		Description: "Anonymous node function",
		Type:        "function",
	}
}

// WithInfo adds metadata to a NodeFunc.
func (f NodeFunc) WithInfo(name, description string) Node {
	return &namedNodeFunc{
		fn:   f,
		info: NodeInfo{Name: name, Description: description, Type: "function"},
	}
}

// namedNodeFunc is a NodeFunc with custom metadata.
type namedNodeFunc struct {
	fn   NodeFunc
	info NodeInfo
}

// Process calls the underlying function.
func (n *namedNodeFunc) Process(ctx context.Context, input *message.Message) (*message.Message, error) {
	return n.fn(ctx, input)
}

// ProcessStream implements the Node interface.
func (n *namedNodeFunc) ProcessStream(ctx context.Context, input *message.Message) (<-chan *event.Event, error) {
	return n.fn.ProcessStream(ctx, input)
}

// SupportsStreaming implements the Node interface.
func (n *namedNodeFunc) SupportsStreaming() bool {
	return false // Default namedNodeFunc doesn't support native streaming
}

// Info returns the custom metadata.
func (n *namedNodeFunc) Info() NodeInfo {
	return n.info
}

// Edge represents a connection between nodes.
type Edge struct {
	From      string
	To        string
	Condition func(ctx context.Context, msg *message.Message) bool
}

// Graph represents a computation graph.
type Graph struct {
	name        string
	description string
	nodes       map[string]Node
	edges       []Edge
	startNode   string
	endNodes    map[string]bool
	metadata    map[string]interface{}
}

// NewGraph creates a new graph.
func NewGraph(name, description string) *Graph {
	return &Graph{
		name:        name,
		description: description,
		nodes:       make(map[string]Node),
		edges:       []Edge{},
		endNodes:    make(map[string]bool),
		metadata:    make(map[string]interface{}),
	}
}

// Name returns the name of the graph.
func (g *Graph) Name() string {
	return g.name
}

// Description returns the description of the graph.
func (g *Graph) Description() string {
	return g.description
}

// AddNode adds a node to the graph.
func (g *Graph) AddNode(name string, node Node) *Graph {
	g.nodes[name] = node
	return g
}

// AddEdge adds a directed edge between nodes.
func (g *Graph) AddEdge(from, to string) *Graph {
	g.edges = append(g.edges, Edge{From: from, To: to})
	return g
}

// AddConditionalEdge adds an edge with a condition.
func (g *Graph) AddConditionalEdge(from, to string, condition func(ctx context.Context, msg *message.Message) bool) *Graph {
	g.edges = append(g.edges, Edge{From: from, To: to, Condition: condition})
	return g
}

// SetStartNode sets the starting node.
func (g *Graph) SetStartNode(name string) *Graph {
	g.startNode = name
	return g
}

// AddEndNode adds an end node.
func (g *Graph) AddEndNode(name string) *Graph {
	g.endNodes[name] = true
	return g
}

// SetMetadata sets a metadata value.
func (g *Graph) SetMetadata(key string, value interface{}) *Graph {
	g.metadata[key] = value
	return g
}

// GetMetadata gets a metadata value.
func (g *Graph) GetMetadata(key string) (interface{}, bool) {
	val, ok := g.metadata[key]
	return val, ok
}

// Validate checks if the graph is valid.
func (g *Graph) Validate() error {
	if g.startNode == "" {
		return fmt.Errorf("no start node specified")
	}

	if _, ok := g.nodes[g.startNode]; !ok {
		return fmt.Errorf("start node '%s' not found in graph", g.startNode)
	}

	if len(g.endNodes) == 0 {
		return fmt.Errorf("no end nodes specified")
	}

	for name := range g.endNodes {
		if _, ok := g.nodes[name]; !ok {
			return fmt.Errorf("end node '%s' not found in graph", name)
		}
	}

	return nil
}

// GetNodesInfo returns info about all nodes in the graph.
func (g *Graph) GetNodesInfo() map[string]NodeInfo {
	result := make(map[string]NodeInfo)
	for name, node := range g.nodes {
		result[name] = node.Info()
	}
	return result
}

// GetEdges returns all edges in the graph.
func (g *Graph) GetEdges() []Edge {
	return g.edges
}

// GetStartNode returns the start node.
func (g *Graph) GetStartNode() string {
	return g.startNode
}

// GetEndNodes returns all end nodes.
func (g *Graph) GetEndNodes() []string {
	result := make([]string, 0, len(g.endNodes))
	for name := range g.endNodes {
		result = append(result, name)
	}
	return result
}

// GetNode returns a node by name.
func (g *Graph) GetNode(name string) (Node, bool) {
	node, ok := g.nodes[name]
	return node, ok
}
