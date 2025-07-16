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

// Package graph provides graph-based execution functionality.
package graph

import (
	"context"
	"fmt"
	"reflect"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// Special node identifiers for graph routing.
const (
	// Start represents the virtual start node for routing.
	Start = "__start__"
	// End represents the virtual end node for routing.
	End = "__end__"
)

// Error types for graph execution.
const (
	ErrorTypeGraphExecution  = "graph_execution_error"
	ErrorTypeInvalidNode     = "invalid_node_error"
	ErrorTypeInvalidState    = "invalid_state_error"
	ErrorTypeInvalidEdge     = "invalid_edge_error"
	ErrorTypeConditionalEdge = "conditional_edge_error"
	ErrorTypeStateValidation = "state_validation_error"
	ErrorTypeNodeExecution   = "node_execution_error"
	ErrorTypeCircularRef     = "circular_reference_error"
	ErrorTypeConcurrency     = "concurrency_error"
	ErrorTypeTimeout         = "timeout_error"
	ErrorTypeModelGeneration = "model_generation_error"
	AuthorGraphExecutor      = "graph-executor"
	MessageGraphCompleted    = "Graph execution completed successfully"
)

// State represents the state that flows through the graph.
// This is the shared data structure that flows between nodes.
type State map[string]any

// Clone creates a deep copy of the state.
func (s State) Clone() State {
	clone := make(State)
	for k, v := range s {
		clone[k] = v
	}
	return clone
}

// StateReducer is a function that determines how state updates are merged.
// It takes existing and new values and returns the merged result.
type StateReducer func(existing, update any) any

// StateField defines a field in the state schema with its type and reducer.
type StateField struct {
	Type     reflect.Type
	Reducer  StateReducer
	Default  func() any
	Required bool
}

// StateSchema defines the structure and behavior of graph state.
// This defines the structure and behavior of state.
type StateSchema struct {
	Fields map[string]StateField
	mutex  sync.RWMutex
}

// NewStateSchema creates a new state schema.
func NewStateSchema() *StateSchema {
	return &StateSchema{
		Fields: make(map[string]StateField),
	}
}

// AddField adds a field to the state schema.
func (s *StateSchema) AddField(name string, field StateField) *StateSchema {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if field.Reducer == nil {
		field.Reducer = DefaultReducer
	}

	s.Fields[name] = field
	return s
}

// ApplyUpdate applies a state update using the defined reducers.
func (s *StateSchema) ApplyUpdate(currentState State, update State) State {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	result := currentState.Clone()

	for key, updateValue := range update {
		field, exists := s.Fields[key]
		if !exists {
			// If no field definition, use default behavior (override)
			result[key] = updateValue
			continue
		}

		currentValue, hasCurrentValue := result[key]
		if !hasCurrentValue && field.Default != nil {
			currentValue = field.Default()
		}

		// Apply reducer
		result[key] = field.Reducer(currentValue, updateValue)
	}

	return result
}

// Validate validates a state against the schema.
func (s *StateSchema) Validate(state State) error {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	for name, field := range s.Fields {
		value, exists := state[name]

		if field.Required && !exists {
			return fmt.Errorf("required field %s is missing", name)
		}

		if exists && value != nil {
			valueType := reflect.TypeOf(value)
			if !valueType.AssignableTo(field.Type) {
				return fmt.Errorf("field %s has wrong type: expected %v, got %v",
					name, field.Type, valueType)
			}
		}
	}
	return nil
}

// Common reducer functions.

// DefaultReducer overwrites the existing value with the update.
func DefaultReducer(existing, update any) any {
	return update
}

// AppendReducer appends update to existing slice.
func AppendReducer(existing, update any) any {
	if existing == nil {
		existing = []any{}
	}

	existingSlice, ok1 := existing.([]any)
	updateSlice, ok2 := update.([]any)

	if !ok1 || !ok2 {
		// Fallback to default behavior if not slices
		return update
	}
	return append(existingSlice, updateSlice...)
}

// StringSliceReducer appends string slices specifically.
func StringSliceReducer(existing, update any) any {
	if existing == nil {
		existing = []string{}
	}

	existingSlice, ok1 := existing.([]string)
	updateSlice, ok2 := update.([]string)

	if !ok1 || !ok2 {
		// Fallback to default behavior if not string slices
		return update
	}
	return append(existingSlice, updateSlice...)
}

// MergeReducer merges update map into existing map.
func MergeReducer(existing, update any) any {
	if existing == nil {
		existing = make(map[string]any)
	}

	existingMap, ok1 := existing.(map[string]any)
	updateMap, ok2 := update.(map[string]any)

	if !ok1 || !ok2 {
		// Fallback to default behavior if not maps
		return update
	}

	result := make(map[string]any)
	for k, v := range existingMap {
		result[k] = v
	}
	for k, v := range updateMap {
		result[k] = v
	}
	return result
}

// MessageReducer handles message arrays with ID-based updates.
func MessageReducer(existing, update any) any {
	if existing == nil {
		existing = []model.Message{}
	}

	existingMsgs, ok1 := existing.([]model.Message)
	updateMsgs, ok2 := update.([]model.Message)

	if !ok1 || !ok2 {
		return update
	}

	// For simplicity, just append for now. In a full implementation,
	// we'd handle message ID-based updates.
	return append(existingMsgs, updateMsgs...)
}

// NodeFunc is a function that can be executed by a node.
// Node function signature: (state) -> updated_state or Command.
type NodeFunc func(ctx context.Context, state State) (any, error)

// NodeResult represents the result of executing a node function.
// It can be either a State update or a Command for combined state update + routing.
type NodeResult any

// IsCommand checks if a result is a Command.
func IsCommand(result any) bool {
	_, ok := result.(*Command)
	return ok
}

// IsState checks if a result is a State update.
func IsState(result any) bool {
	_, ok := result.(State)
	return ok
}

// ConditionalFunc is a function that determines the next node(s) based on state.
// Conditional edge function signature.
type ConditionalFunc func(ctx context.Context, state State) (string, error)

// MultiConditionalFunc returns multiple next nodes for parallel execution.
type MultiConditionalFunc func(ctx context.Context, state State) ([]string, error)

// Node represents a node in the graph.
// Nodes are primarily functions with metadata.
type Node struct {
	ID          string
	Name        string
	Description string
	Function    NodeFunc
}

// Edge represents an edge in the graph.
// Simplified edge pattern.
type Edge struct {
	From string
	To   string
}

// ConditionalEdge represents a conditional edge with routing logic.
type ConditionalEdge struct {
	From      string
	Condition ConditionalFunc
	PathMap   map[string]string // Maps condition result to target node
}

// Graph represents a directed graph of nodes and edges.
// This is the compiled runtime structure created by StateGraph.Compile().
//
// Users typically don't create Graph instances directly. Instead, use:
//   - StateGraph for building graphs with compatible patterns
//   - Builder for convenience methods when building common graph types
//
// The Graph type is the immutable runtime representation that gets executed
// by the Executor.
type Graph struct {
	schema           *StateSchema
	nodes            map[string]*Node
	edges            map[string][]*Edge
	conditionalEdges map[string]*ConditionalEdge
	entryPoint       string
	mutex            sync.RWMutex
}

// New creates a new empty graph with the given state schema.
func New(schema *StateSchema) *Graph {
	if schema == nil {
		schema = NewStateSchema()
	}

	return &Graph{
		schema:           schema,
		nodes:            make(map[string]*Node),
		edges:            make(map[string][]*Edge),
		conditionalEdges: make(map[string]*ConditionalEdge),
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
	return nil
}

// AddEdge adds an edge to the graph.
func (g *Graph) AddEdge(edge *Edge) error {
	g.mutex.Lock()
	defer g.mutex.Unlock()

	if edge.From == "" || edge.To == "" {
		return fmt.Errorf("edge from and to cannot be empty")
	}

	// Allow Start and End as special nodes
	if edge.From != Start {
		if _, exists := g.nodes[edge.From]; !exists {
			return fmt.Errorf("source node %s does not exist", edge.From)
		}
	}

	if edge.To != End {
		if _, exists := g.nodes[edge.To]; !exists {
			return fmt.Errorf("target node %s does not exist", edge.To)
		}
	}

	g.edges[edge.From] = append(g.edges[edge.From], edge)
	return nil
}

// AddConditionalEdge adds a conditional edge to the graph.
func (g *Graph) AddConditionalEdge(condEdge *ConditionalEdge) error {
	g.mutex.Lock()
	defer g.mutex.Unlock()

	if condEdge.From == "" {
		return fmt.Errorf("conditional edge from cannot be empty")
	}

	if condEdge.From != Start {
		if _, exists := g.nodes[condEdge.From]; !exists {
			return fmt.Errorf("source node %s does not exist", condEdge.From)
		}
	}

	// Validate all target nodes in path map
	for _, to := range condEdge.PathMap {
		if to != End {
			if _, exists := g.nodes[to]; !exists {
				return fmt.Errorf("target node %s does not exist", to)
			}
		}
	}

	g.conditionalEdges[condEdge.From] = condEdge
	return nil
}

// SetEntryPoint sets the entry point of the graph.
func (g *Graph) SetEntryPoint(nodeID string) error {
	g.mutex.Lock()
	defer g.mutex.Unlock()

	if nodeID != "" {
		if _, exists := g.nodes[nodeID]; !exists {
			return fmt.Errorf("entry point node %s does not exist", nodeID)
		}
	}

	g.entryPoint = nodeID
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

// GetConditionalEdge returns the conditional edge from a node.
func (g *Graph) GetConditionalEdge(nodeID string) (*ConditionalEdge, bool) {
	g.mutex.RLock()
	defer g.mutex.RUnlock()
	edge, exists := g.conditionalEdges[nodeID]
	return edge, exists
}

// GetEntryPoint returns the entry point node ID.
func (g *Graph) GetEntryPoint() string {
	g.mutex.RLock()
	defer g.mutex.RUnlock()
	return g.entryPoint
}

// GetSchema returns the state schema.
func (g *Graph) GetSchema() *StateSchema {
	return g.schema
}

// Validate validates the graph structure.
func (g *Graph) Validate() error {
	g.mutex.RLock()
	defer g.mutex.RUnlock()

	if g.entryPoint == "" {
		return fmt.Errorf("graph must have an entry point")
	}

	if _, exists := g.nodes[g.entryPoint]; !exists {
		return fmt.Errorf("entry point node %s does not exist", g.entryPoint)
	}

	// Check that all nodes are reachable from entry point
	visited := make(map[string]bool)
	if err := g.dfsValidate(g.entryPoint, visited); err != nil {
		return err
	}

	// Check for unreachable nodes
	for nodeID := range g.nodes {
		if !visited[nodeID] {
			return fmt.Errorf("node %s is not reachable from entry point", nodeID)
		}
	}

	return nil
}

// dfsValidate performs depth-first search validation.
func (g *Graph) dfsValidate(nodeID string, visited map[string]bool) error {
	if visited[nodeID] {
		return nil // Already visited, cycles are allowed
	}

	visited[nodeID] = true

	// Check regular edges
	edges := g.edges[nodeID]
	for _, edge := range edges {
		if edge.To != End {
			if err := g.dfsValidate(edge.To, visited); err != nil {
				return err
			}
		}
	}

	// Check conditional edges
	if condEdge, exists := g.conditionalEdges[nodeID]; exists {
		for _, to := range condEdge.PathMap {
			if to != End {
				if err := g.dfsValidate(to, visited); err != nil {
					return err
				}
			}
		}
	}

	return nil
}

// ExecutionContext contains context for graph execution.
type ExecutionContext struct {
	Graph         *Graph
	State         State
	EventChan     chan<- *event.Event
	InvocationID  string
	ModelResolver ModelResolver
}

// ModelResolver resolves model instances for LLM nodes.
type ModelResolver interface {
	ResolveModel(name string) (model.Model, error)
}

// Command represents a command that combines state updates with routing.
// Command pattern.
type Command struct {
	Update State
	GoTo   string
	Graph  *Graph // For parent graph navigation
}

// Send represents dynamic routing to multiple nodes with different states.
// Send pattern.
type Send struct {
	NodeID string
	State  State
}
