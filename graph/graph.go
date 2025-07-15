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

// Package graph provides graph-based execution functionality similar to LangGraph.
package graph

import (
	"context"
	"fmt"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/event"
)

// NodeType represents the type of a node in the graph.
type NodeType string

const (
	// NodeTypeStart represents the start node of the graph.
	NodeTypeStart NodeType = "start"
	// NodeTypeEnd represents the end node of the graph.
	NodeTypeEnd NodeType = "end"
	// NodeTypeAgent represents an agent node that executes an agent.
	NodeTypeAgent NodeType = "agent"
	// NodeTypeFunction represents a function node that executes a custom function.
	NodeTypeFunction NodeType = "function"
	// NodeTypeCondition represents a conditional node that routes based on conditions.
	NodeTypeCondition NodeType = "condition"
)

const (
	// ErrorTypeGraphExecution is used for graph execution errors.
	ErrorTypeGraphExecution = "graph_execution_error"
	// AuthorGraphExecutor is the author name for graph executor events.
	AuthorGraphExecutor = "graph-executor"
	// MessageGraphCompleted is the completion message for graph execution.
	MessageGraphCompleted = "Graph execution completed successfully"
)

// State represents the state that flows through the graph.
type State map[string]interface{}

// Clone creates a deep copy of the state.
func (s State) Clone() State {
	clone := make(State)
	for k, v := range s {
		clone[k] = v
	}
	return clone
}

// NodeFunc is a function that can be executed by a function node.
type NodeFunc func(ctx context.Context, state State) (State, error)

// ConditionFunc is a function that determines the next node based on state.
type ConditionFunc func(ctx context.Context, state State) (string, error)

// Node represents a node in the graph.
type Node struct {
	// ID is the unique identifier of the node.
	ID string
	// Type is the type of the node.
	Type NodeType
	// Name is the human-readable name of the node.
	Name string
	// Description is the description of the node.
	Description string
	// Function is the function to execute for function nodes.
	Function NodeFunc
	// Condition is the condition function for conditional nodes.
	Condition ConditionFunc
	// AgentName is the name of the agent to execute for agent nodes.
	AgentName string
}

// Edge represents an edge in the graph.
type Edge struct {
	// From is the source node ID.
	From string
	// To is the target node ID.
	To string
	// Condition is an optional condition for the edge.
	// If nil, the edge is always taken.
	Condition string
}

// Graph represents a directed graph of nodes and edges.
type Graph struct {
	// nodes maps node IDs to nodes.
	nodes map[string]*Node
	// edges maps source node IDs to their outgoing edges.
	edges map[string][]*Edge
	// startNode is the ID of the start node.
	startNode string
	// endNodes contains the IDs of end nodes.
	endNodes map[string]bool
	// mutex protects concurrent access to the graph.
	mutex sync.RWMutex
}

// New creates a new empty graph.
func New() *Graph {
	return &Graph{
		nodes:    make(map[string]*Node),
		edges:    make(map[string][]*Edge),
		endNodes: make(map[string]bool),
	}
}

// AddNode adds a node to the graph.
func (g *Graph) AddNode(node *Node) error {
	g.mutex.Lock()
	defer g.mutex.Unlock()

	if node.ID == "" {
		return fmt.Errorf("node ID cannot be empty")
	}

	if _, exists := g.nodes[node.ID]; exists {
		return fmt.Errorf("node with ID %s already exists", node.ID)
	}

	g.nodes[node.ID] = node

	// Set start node if this is a start node
	if node.Type == NodeTypeStart {
		if g.startNode != "" {
			return fmt.Errorf("start node already exists: %s", g.startNode)
		}
		g.startNode = node.ID
	}

	// Add to end nodes if this is an end node
	if node.Type == NodeTypeEnd {
		g.endNodes[node.ID] = true
	}

	return nil
}

// AddEdge adds an edge to the graph.
func (g *Graph) AddEdge(edge *Edge) error {
	g.mutex.Lock()
	defer g.mutex.Unlock()

	if edge.From == "" || edge.To == "" {
		return fmt.Errorf("edge from and to cannot be empty")
	}

	// Verify that both nodes exist
	if _, exists := g.nodes[edge.From]; !exists {
		return fmt.Errorf("source node %s does not exist", edge.From)
	}
	if _, exists := g.nodes[edge.To]; !exists {
		return fmt.Errorf("target node %s does not exist", edge.To)
	}

	g.edges[edge.From] = append(g.edges[edge.From], edge)
	return nil
}

// GetNode returns a node by ID.
func (g *Graph) GetNode(id string) (*Node, bool) {
	g.mutex.RLock()
	defer g.mutex.RUnlock()
	node, exists := g.nodes[id]
	return node, exists
}

// GetEdges returns all outgoing edges from a node.
func (g *Graph) GetEdges(nodeID string) []*Edge {
	g.mutex.RLock()
	defer g.mutex.RUnlock()
	return g.edges[nodeID]
}

// GetStartNode returns the start node ID.
func (g *Graph) GetStartNode() string {
	g.mutex.RLock()
	defer g.mutex.RUnlock()
	return g.startNode
}

// IsEndNode checks if a node is an end node.
func (g *Graph) IsEndNode(nodeID string) bool {
	g.mutex.RLock()
	defer g.mutex.RUnlock()
	return g.endNodes[nodeID]
}

// Validate validates the graph structure.
func (g *Graph) Validate() error {
	g.mutex.RLock()
	defer g.mutex.RUnlock()

	if g.startNode == "" {
		return fmt.Errorf("graph must have a start node")
	}

	if len(g.endNodes) == 0 {
		return fmt.Errorf("graph must have at least one end node")
	}

	// Check that all nodes are reachable from start
	visited := make(map[string]bool)
	if err := g.dfsValidate(g.startNode, visited); err != nil {
		return err
	}

	// Check for unreachable nodes
	for nodeID := range g.nodes {
		if !visited[nodeID] {
			return fmt.Errorf("node %s is not reachable from start node", nodeID)
		}
	}

	return nil
}

// dfsValidate performs depth-first search validation.
func (g *Graph) dfsValidate(nodeID string, visited map[string]bool) error {
	if visited[nodeID] {
		return nil // Already visited, avoid cycles
	}

	visited[nodeID] = true

	// If this is an end node, we're done with this path
	if g.endNodes[nodeID] {
		return nil
	}

	edges := g.edges[nodeID]
	if len(edges) == 0 && !g.endNodes[nodeID] {
		return fmt.Errorf("node %s has no outgoing edges and is not an end node", nodeID)
	}

	for _, edge := range edges {
		if err := g.dfsValidate(edge.To, visited); err != nil {
			return err
		}
	}

	return nil
}

// ExecutionContext contains context for graph execution.
type ExecutionContext struct {
	// Graph is the graph being executed.
	Graph *Graph
	// State is the current state.
	State State
	// EventChan is the channel for sending events.
	EventChan chan<- *event.Event
	// InvocationID is the invocation ID for events.
	InvocationID string
	// AgentResolver resolves agent names to agent instances.
	AgentResolver AgentResolver
}

// AgentResolver resolves agent names to agent instances.
type AgentResolver interface {
	ResolveAgent(name string) (AgentExecutor, error)
}

// AgentExecutor executes an agent with the given state.
type AgentExecutor interface {
	Execute(ctx context.Context, state State) (State, <-chan *event.Event, error)
}
