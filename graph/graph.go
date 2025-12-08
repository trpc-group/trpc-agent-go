//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package graph provides graph-based execution functionality.
package graph

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph/internal/channel"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
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
)

// NodeFunc is a function that can be executed by a node.
// Node function signature: (state) -> updated_state or Command.
type NodeFunc func(ctx context.Context, state State) (any, error)

// NodeResult represents the result of executing a node function.
// It can be either a State update or a Command for combined state update + routing.
type NodeResult any

// ConditionalFunc is a function that determines the next node(s) based on state.
// Conditional edge function signature.
type ConditionalFunc = func(ctx context.Context, state State) (string, error)

// MultiConditionalFunc returns multiple next nodes for parallel execution.
type MultiConditionalFunc = func(ctx context.Context, state State) ([]string, error)

// UniversalCondFunc is a function that determines the next node(s) based on state.
type UniversalCondFunc = func(ctx context.Context, state State) (ConditionResult, error)

// ConditionResult represents the result of executing a conditional edge function.
type ConditionResult struct {
	NextNodes []string
}

func wrapperCondFunc(condFunc any) UniversalCondFunc {
	if condFunc == nil {
		panic("conditional function is nil")
	}

	var uniFunc UniversalCondFunc
	switch cdFunc := condFunc.(type) {
	case ConditionalFunc:
		uniFunc = func(ctx context.Context, state State) (ConditionResult, error) {
			nextNode, err := cdFunc(ctx, state)
			return ConditionResult{NextNodes: []string{nextNode}}, err
		}
	case MultiConditionalFunc:
		uniFunc = func(ctx context.Context, state State) (ConditionResult, error) {
			nextNodes, err := cdFunc(ctx, state)
			return ConditionResult{NextNodes: nextNodes}, err
		}
	case UniversalCondFunc:
		uniFunc = cdFunc
	default:
		panic(fmt.Sprintf("unsupported conditional function type: %T", condFunc))
	}

	return uniFunc
}

// channelWriteEntry represents a write operation to a channel.
type channelWriteEntry struct {
	Channel  string
	Value    any
	SkipNone bool
	Mapper   func(any) any
}

// Node represents a node in the graph.
// Nodes are primarily functions with metadata.
type Node struct {
	ID          string
	Name        string
	Description string
	Function    NodeFunc
	Type        NodeType // Type of the node (function, llm, tool, etc.)

	toolSets             []tool.ToolSet
	refreshToolSetsOnRun bool
	// Per-node callbacks for fine-grained control
	callbacks *NodeCallbacks
	// Optional per-node cache policy. If nil, graph-level policy applies.
	cachePolicy *CachePolicy
	// Optional per-node cache key selector. When set, the executor applies this
	// selector to the sanitized node input before invoking the CachePolicy.KeyFunc.
	// The selector receives a sanitized map[string]any view and should return a
	// projection to be used for key derivation (e.g., subset of fields).
	cacheKeySelector func(map[string]any) any

	// Retry policies configured for this node. When empty, executor defaults
	// (if any) will be used. Policies are evaluated in order to determine
	// whether an error is retryable and which parameters to use.
	retryPolicies []RetryPolicy

	// Pregel-style extensions
	triggers []string            // Channels that trigger this node
	channels []string            // Channels this node reads from
	writers  []channelWriteEntry // Channels this node writes to
	mapper   func(any) any       // Input transformation function

	// Declared destinations for dynamic routing visualization and static checks.
	// Keys are target node IDs; values are optional labels.
	destinations map[string]string

	// ends holds per-node named ends mapping for stronger branch semantics.
	// Keys are symbolic branch names returned by node logic (e.g. "approved",
	// "rejected"). Values are destination node IDs (or the special End).
	// This allows nodes to route via Command.GoTo using symbolic names and for
	// conditional branches to resolve results with clearer, local semantics.
	ends map[string]string

	// It's effect just for LLM node
	modelCallbacks *model.Callbacks
	// just for tool node.
	toolCallbacks *tool.Callbacks

	// enableParallelTools toggles parallel execution for Tools nodes.
	// When true, multiple tool calls in a single assistant response are executed concurrently.
	// Default is false (serial execution) for compatibility and safety.
	enableParallelTools bool

	// llmGenerationConfig stores per-node generation configuration for LLM nodes.
	// If set, AddLLMNode forwards it to the underlying LLM runner.
	llmGenerationConfig *model.GenerationConfig

	// Subgraph (agent node) options
	agentInputMapper      SubgraphInputMapper
	agentOutputMapper     SubgraphOutputMapper
	agentIsolatedMessages bool
	agentEventScope       string
	// agentInputFromLastResponse indicates whether the agent node should
	// construct the child invocation's user input from the parent's
	// StateKeyLastResponse. When true, the framework will map
	// last_response -> user_input for this agent node before invoking the
	// sub-agent. This provides a concise way to implement "pass only the
	// result" pipelines between agent nodes without extra glue nodes.
	agentInputFromLastResponse bool
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
	Condition UniversalCondFunc
	PathMap   map[string]string // Maps condition result to target node.
}

// Graph represents a directed graph of nodes and edges.
// This is the compiled runtime structure created by StateGraph.Compile().
// Users typically don't create Graph instances directly. Instead, use:
//   - StateGraph for building graphs with compatible patterns.
//
// The Graph type is the immutable runtime representation that gets executed
// by the Executor.
type Graph struct {
	mu               sync.RWMutex
	schema           *StateSchema
	nodes            map[string]*Node
	edges            map[string][]*Edge
	conditionalEdges map[string]*ConditionalEdge
	entryPoint       string
	// Pregel-style extensions
	channelManager *channel.Manager
	triggerToNodes map[string][]string // Maps channel names to nodes that are triggered

	// Caching
	cache       Cache
	cachePolicy *CachePolicy
	// Optional graph version used to scope cache namespaces, helping avoid
	// stale cache collisions across graph code changes or deployments.
	graphVersion string
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
		channelManager:   channel.NewChannelManager(),
		triggerToNodes:   make(map[string][]string),
	}
}

// Node returns a node by ID.
func (g *Graph) Node(id string) (*Node, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	node, exists := g.nodes[id]
	return node, exists
}

// Edges returns all outgoing edges from a node.
func (g *Graph) Edges(nodeID string) []*Edge {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.edges[nodeID]
}

// ConditionalEdge returns the conditional edge from a node.
func (g *Graph) ConditionalEdge(nodeID string) (*ConditionalEdge, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	edge, exists := g.conditionalEdges[nodeID]
	return edge, exists
}

// EntryPoint returns the entry point node ID.
func (g *Graph) EntryPoint() string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.entryPoint
}

// Schema returns the state schema.
func (g *Graph) Schema() *StateSchema {
	return g.schema
}

// Cache returns the graph-level cache (may be nil).
func (g *Graph) Cache() Cache {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.cache
}

// CachePolicy returns the graph-level cache policy (may be nil).
func (g *Graph) CachePolicy() *CachePolicy {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.cachePolicy
}

// setCache sets the graph-level cache.
func (g *Graph) setCache(c Cache) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.cache = c
}

// setCachePolicy sets the graph-level cache policy.
func (g *Graph) setCachePolicy(p *CachePolicy) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.cachePolicy = p
}

// setGraphVersion sets an optional version string used for cache namespacing.
func (g *Graph) setGraphVersion(v string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.graphVersion = v
}

// cacheNamespace builds a per-node namespace including optional graph version.
func (g *Graph) cacheNamespace(nodeID string) string {
	g.mu.RLock()
	v := g.graphVersion
	g.mu.RUnlock()
	if v == "" {
		return fmt.Sprintf("%s:%s", CacheNamespacePrefix, nodeID)
	}
	return fmt.Sprintf("%s:%s:%s", CacheNamespacePrefix, v, nodeID)
}

// clearCacheForNodes clears cache entries for the given node IDs.
func (g *Graph) clearCacheForNodes(nodes []string) {
	g.mu.RLock()
	c := g.cache
	g.mu.RUnlock()
	if c == nil {
		return
	}
	for _, id := range nodes {
		c.Clear(g.cacheNamespace(id))
	}
}

// validate validates the graph structure.
func (g *Graph) validate() error {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if g.entryPoint == "" {
		return fmt.Errorf("graph must have an entry point")
	}
	if _, exists := g.nodes[g.entryPoint]; !exists {
		return fmt.Errorf("entry point node %s does not exist", g.entryPoint)
	}
	// Validate declared destinations exist.
	for _, n := range g.nodes {
		if n == nil || n.destinations == nil || len(n.destinations) == 0 {
			// fallthrough to ends check
		}
		if n != nil && n.destinations != nil {
			for to := range n.destinations {
				if to == End {
					continue
				}
				if _, ok := g.nodes[to]; !ok {
					return fmt.Errorf("node %s declares destination %s which does not exist", n.ID, to)
				}
			}
		}
		// Validate per-node ends mapping targets exist.
		if n != nil && n.ends != nil {
			for _, target := range n.ends {
				if target == End {
					continue
				}
				if _, ok := g.nodes[target]; !ok {
					return fmt.Errorf("node %s declares end target %s which does not exist", n.ID, target)
				}
			}
		}
	}
	return g.schema.validateSchema()
}

// ExecutionContext contains context for graph execution.
type ExecutionContext struct {
	Graph        *Graph
	EventChan    chan<- *event.Event
	InvocationID string

	// channels holds the per-execution Pregel channels. These are constructed
	// from the Graph's static channel definitions when the execution starts
	// and are never shared across concurrent runs.
	channels *channel.Manager

	// stateMutex protects State reads/writes.
	stateMutex sync.RWMutex
	State      State

	// pendingMu protects pendingWrites operations.
	pendingMu     sync.Mutex
	pendingWrites []PendingWrite
	resumed       bool
	seq           atomic.Int64 // Atomic sequence counter for deterministic replay

	// tasksMutex protects pendingTasks queue operations.
	tasksMutex   sync.Mutex
	pendingTasks []*Task

	// versionsSeen tracks which channel versions each node has seen.
	// Map from nodeID -> channelName -> version number.
	versionsSeen   map[string]map[string]int64
	versionsSeenMu sync.RWMutex

	// lastCheckpoint holds the most recent checkpoint used for planning
	// when resuming. Keeping this per-execution avoids cross-run sharing
	// when a single Executor is reused concurrently.
	lastCheckpoint *Checkpoint
}

// Command represents a command that combines state updates with routing.
type Command struct {
	Update    State
	GoTo      string
	Resume    any
	ResumeMap map[string]any
}

// addNode adds a node to the graph.
func (g *Graph) addNode(node *Node) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if node.ID == "" {
		return fmt.Errorf("node ID cannot be empty for %+v", node)
	}
	if _, exists := g.nodes[node.ID]; exists {
		return fmt.Errorf("node with ID %s already exists for %+v", node.ID, node)
	}
	g.nodes[node.ID] = node
	return nil
}

// addEdge adds an edge to the graph.
func (g *Graph) addEdge(edge *Edge) error {
	g.mu.Lock()
	defer g.mu.Unlock()
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

// addConditionalEdge adds a conditional edge to the graph.
func (g *Graph) addConditionalEdge(condEdge *ConditionalEdge) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if condEdge.From == "" {
		return fmt.Errorf("conditional edge from cannot be empty")
	}
	// Validate condition presence and exclusivity.
	if condEdge.Condition == nil {
		return fmt.Errorf("exactly conditionFunc must be set")
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

// setEntryPoint sets the entry point of the graph.
func (g *Graph) setEntryPoint(nodeID string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if nodeID != "" {
		if _, exists := g.nodes[nodeID]; !exists {
			return fmt.Errorf("entry point node %s does not exist", nodeID)
		}
	}
	g.entryPoint = nodeID
	return nil
}

// Pregel-style methods

// addChannel adds a channel to the graph.
func (g *Graph) addChannel(name string, channelType channel.Behavior) {
	g.channelManager.AddChannel(name, channelType)
}

// getChannel retrieves a channel definition by name. This is primarily used
// during graph construction and in tests. Runtime execution should operate on
// the per-execution channels stored in ExecutionContext.
func (g *Graph) getChannel(name string) (*channel.Channel, bool) {
	return g.channelManager.GetChannel(name)
}

// getAllChannels returns all channel definitions in the graph. Callers must
// treat the returned channels as immutable templates (name + behavior).
// Per-execution channel state (values, versions, availability) is stored in
// ExecutionContext.channels.
func (g *Graph) getAllChannels() map[string]*channel.Channel {
	return g.channelManager.GetAllChannels()
}

// getTriggerToNodes returns the mapping of channels to triggered nodes.
func (g *Graph) getTriggerToNodes() map[string][]string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	result := make(map[string][]string)
	for k, v := range g.triggerToNodes {
		result[k] = append([]string{}, v...)
	}
	return result
}

// addNodeTrigger adds a trigger relationship between a channel and a node.
func (g *Graph) addNodeTrigger(channelName string, nodeID string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	// Deduplicate
	existing := g.triggerToNodes[channelName]
	for _, n := range existing {
		if n == nodeID {
			return
		}
	}
	g.triggerToNodes[channelName] = append(existing, nodeID)
}

// addNodeWriter adds a writer to a node.
func (g *Graph) addNodeWriter(nodeID string, writer channelWriteEntry) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if node, exists := g.nodes[nodeID]; exists {
		node.writers = append(node.writers, writer)
	}
}

// addNodeTrigger adds a trigger to a node.
func (g *Graph) addNodeTriggerChannel(nodeID string, channelName string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if node, exists := g.nodes[nodeID]; exists {
		node.triggers = append(node.triggers, channelName)
	}
}

// addNodeChannel adds a channel that a node reads from.
func (g *Graph) addNodeChannel(nodeID string, channelName string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if node, exists := g.nodes[nodeID]; exists {
		node.channels = append(node.channels, channelName)
	}
}
