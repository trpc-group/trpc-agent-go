//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package graph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph/internal/channel"
	stateinject "trpc.group/trpc-go/trpc-agent-go/internal/state"
	itelemetry "trpc.group/trpc-go/trpc-agent-go/internal/telemetry"
	itool "trpc.group/trpc-go/trpc-agent-go/internal/tool"
	"trpc.group/trpc-go/trpc-agent-go/internal/util"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/telemetry/trace"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// StateGraph provides a fluent interface for building graphs.
// This is the primary public API for creating executable graphs.
//
// StateGraph provides:
//   - Type-safe state management with schemas and reducers
//   - Conditional routing and dynamic node execution
//   - Command support for combined state updates and routing
//
// Example usage:
//
//	schema := NewStateSchema().AddField("counter", StateField{...})
//	graph, err := NewStateGraph(schema).
//	  AddNode("increment", incrementFunc).
//	  SetEntryPoint("increment").
//	  SetFinishPoint("increment").
//	  Compile()
//
// The compiled Graph can then be executed with NewExecutor(graph).
type StateGraph struct {
	graph *Graph
}

// NewStateGraph creates a new graph builder with the given state schema.
func NewStateGraph(schema *StateSchema) *StateGraph {
	return &StateGraph{
		graph: New(schema),
	}
}

// Option is a function that configures a Node.
type Option func(*Node)

// WithName sets the name of the node.
func WithName(name string) Option {
	return func(node *Node) {
		node.Name = name
	}
}

// WithDescription sets the description of the node.
func WithDescription(description string) Option {
	return func(node *Node) {
		node.Description = description
	}
}

// WithNodeType sets the type of the node.
func WithNodeType(nodeType NodeType) Option {
	return func(node *Node) {
		node.Type = nodeType
	}
}

// WithToolSets sets the ToolSets for the node. This is a declarative
// per-node configuration used by AddLLMNode to build the LLM runner.
func WithToolSets(toolSets []tool.ToolSet) Option {
	return func(node *Node) {
		if len(toolSets) == 0 {
			node.toolSets = nil
			return
		}
		copied := make([]tool.ToolSet, len(toolSets))
		copy(copied, toolSets)
		node.toolSets = copied
	}
}

// WithRefreshToolSetsOnRun controls whether tools from ToolSets are
// refreshed from the underlying ToolSet on each node run.
// When false (default), tools from ToolSets are resolved once when the
// node is created. When true, the graph will call ToolSet.Tools again
// when building the tools map for each execution.
func WithRefreshToolSetsOnRun(refresh bool) Option {
	return func(node *Node) {
		node.refreshToolSetsOnRun = refresh
	}
}

// WithEnableParallelTools enables parallel tool execution for a Tools node.
// When enabled, if the last assistant message contains multiple tool calls,
// they will be executed concurrently and their responses will be merged in
// the original order. By default, tools run serially for compatibility.
func WithEnableParallelTools(enable bool) Option {
	return func(node *Node) {
		node.enableParallelTools = enable
	}
}

// WithCacheKeyFields sets a cache key selector that derives the cache key
// input from a subset of fields in the sanitized input map. This helps avoid
// including unrelated or volatile keys in the cache key.
func WithCacheKeyFields(fields ...string) Option {
	// copy fields to avoid external mutation
	fcopy := append([]string(nil), fields...)
	return func(node *Node) {
		node.cacheKeySelector = func(m map[string]any) any {
			if m == nil {
				return nil
			}
			out := make(map[string]any, len(fcopy))
			for _, k := range fcopy {
				if v, ok := m[k]; ok {
					out[k] = v
				}
			}
			return out
		}
	}
}

// WithCacheKeySelector sets a custom selector for deriving the cache key input
// from the sanitized input map. The returned value will be passed to the
// CachePolicy.KeyFunc.
func WithCacheKeySelector(selector func(map[string]any) any) Option {
	return func(node *Node) {
		node.cacheKeySelector = selector
	}
}

// WithNodeCachePolicy sets a cache policy for this node.
// When set, the executor will attempt to cache the node's final result using this policy.
func WithNodeCachePolicy(policy *CachePolicy) Option {
	return func(node *Node) {
		node.cachePolicy = policy
	}
}

// WithRetryPolicy sets retry policies for the node. Policies are evaluated
// in order when an error occurs to determine whether to retry and what
// backoff to apply. Passing multiple policies allows matching by different
// conditions (e.g., network vs. HTTP status).
func WithRetryPolicy(policies ...RetryPolicy) Option {
	return func(node *Node) {
		if len(policies) == 0 {
			return
		}
		node.retryPolicies = append(node.retryPolicies, policies...)
	}
}

// WithGenerationConfig sets the generation config for an LLM node.
// Effective only for nodes added via AddLLMNode.
func WithGenerationConfig(cfg model.GenerationConfig) Option {
	return func(node *Node) {
		c := cfg
		node.llmGenerationConfig = &c
	}
}

// WithDestinations declares potential dynamic routing targets for a node.
// This is used for static validation (existence) and visualization only.
// It does not influence runtime execution.
func WithDestinations(dests map[string]string) Option {
	return func(node *Node) {
		if node.destinations == nil {
			node.destinations = make(map[string]string)
		}
		for k, v := range dests {
			node.destinations[k] = v
		}
	}
}

// WithEndsMap declares per-node named ends and their concrete destinations.
// The map keys are local symbolic names (e.g., "approved"), and values are
// concrete node IDs (or the special End) this node may route to.
// These ends are used at runtime to resolve Command.GoTo and conditional
// branch results, and at compile time for stronger validation.
func WithEndsMap(ends map[string]string) Option {
	return func(node *Node) {
		if node.ends == nil {
			node.ends = make(map[string]string)
		}
		for k, v := range ends {
			node.ends[k] = v
		}
	}
}

// WithEnds declares per-node named ends where the symbolic names are also the
// destination node IDs. Equivalent to WithEndsMap({name: name}).
func WithEnds(names ...string) Option {
	return func(node *Node) {
		if node.ends == nil {
			node.ends = make(map[string]string)
		}
		for _, n := range names {
			node.ends[n] = n
		}
	}
}

// WithPreNodeCallback sets a callback that will be executed before this specific node.
// This callback is specific to this node and will be executed in addition to any global callbacks.
func WithPreNodeCallback(callback BeforeNodeCallback) Option {
	return func(node *Node) {
		if node.callbacks == nil {
			node.callbacks = NewNodeCallbacks()
		}
		node.callbacks.RegisterBeforeNode(callback)
	}
}

// WithPostNodeCallback sets a callback that will be executed after this specific node.
// This callback is specific to this node and will be executed in addition to any global callbacks.
func WithPostNodeCallback(callback AfterNodeCallback) Option {
	return func(node *Node) {
		if node.callbacks == nil {
			node.callbacks = NewNodeCallbacks()
		}
		node.callbacks.RegisterAfterNode(callback)
	}
}

// WithNodeErrorCallback sets a callback that will be executed when this specific node fails.
// This callback is specific to this node and will be executed in addition to any global callbacks.
func WithNodeErrorCallback(callback OnNodeErrorCallback) Option {
	return func(node *Node) {
		if node.callbacks == nil {
			node.callbacks = NewNodeCallbacks()
		}
		node.callbacks.RegisterOnNodeError(callback)
	}
}

// WithNodeCallbacks sets multiple callbacks for this specific node.
// This allows setting multiple callbacks at once for convenience.
func WithNodeCallbacks(callbacks *NodeCallbacks) Option {
	return func(node *Node) {
		if node.callbacks == nil {
			node.callbacks = NewNodeCallbacks()
		}
		// Merge the provided callbacks with existing ones
		if callbacks != nil {
			node.callbacks.BeforeNode = append(node.callbacks.BeforeNode, callbacks.BeforeNode...)
			node.callbacks.AfterNode = append(node.callbacks.AfterNode, callbacks.AfterNode...)
			node.callbacks.OnNodeError = append(node.callbacks.OnNodeError, callbacks.OnNodeError...)
		}
	}
}

// WithToolCallbacks sets multiple callbacks for this specific node.
// This allows setting tool callbacks directly on the node.
// It's effect just for tool node.
func WithToolCallbacks(callbacks *tool.Callbacks) Option {
	return func(node *Node) {
		node.toolCallbacks = callbacks
	}
}

// WithAgentNodeEventCallback sets a callback that will be executed when an agent event is emitted.
// This callback is specific to this node and will be executed in addition to any global callbacks.
func WithAgentNodeEventCallback(callback AgentEventCallback) Option {
	return func(node *Node) {
		if node.callbacks == nil {
			node.callbacks = NewNodeCallbacks()
		}
		node.callbacks.AgentEvent = append(node.callbacks.AgentEvent, callback)
	}
}

// Subgraph I/O mapping and scope utilities

// SubgraphResult captures a subgraph's outputs exposed to the parent mapper.
// RawStateDelta provides the original serialized final-state snapshot map coming
// from the subgraph's terminal graph.execution event. Callers can decode values
// with custom types if needed. Note that FinalState is reconstructed by JSON
// decoding, which may coerce numbers to float64 and complex structures to
// map[string]any.
type SubgraphResult struct {
	LastResponse  string
	FinalState    State
	RawStateDelta map[string][]byte
}

// SubgraphInputMapper projects parent state into child runtime state.
// The returned state replaces the runtime state passed to the child.
type SubgraphInputMapper func(parent State) State

// SubgraphOutputMapper converts subgraph results into parent state updates.
// Returning nil or an empty State means "no updates" will be applied.
// Note: Prefer returning nil when there are no updates to write back;
// this reads clearer and is equivalent to applying an empty update.
type SubgraphOutputMapper func(parent State, result SubgraphResult) State

// WithSubgraphInputMapper sets a mapper used to build the child runtime state.
func WithSubgraphInputMapper(f SubgraphInputMapper) Option {
	return func(node *Node) {
		node.agentInputMapper = f
	}
}

// WithSubgraphOutputMapper sets a mapper that writes subgraph outputs back to parent state.
func WithSubgraphOutputMapper(f SubgraphOutputMapper) Option {
	return func(node *Node) {
		node.agentOutputMapper = f
	}
}

// WithSubgraphIsolatedMessages toggles seeding of session messages to the child.
// When true, the child GraphAgent runs with include_contents=none.
// Docs note: This effectively sets CfgKeyIncludeContents="none" in the child
// runtime state so the child does not inject session history and only sees the
// projected input from the parent.
func WithSubgraphIsolatedMessages(isolate bool) Option {
	return func(node *Node) {
		node.agentIsolatedMessages = isolate
	}
}

// WithSubgraphInputFromLastResponse maps the parent's last_response to the
// child sub-agent's user_input for this agent node.
//
// Use this option when you want the downstream agent to consume only the
// upstream agent's result as its current-round input, without injecting the
// session history. This keeps agent nodes as black boxes while enabling
// explicit result passing.
//
// Note: For even stricter isolation from session history, combine with
// WithSubgraphIsolatedMessages(true).
func WithSubgraphInputFromLastResponse() Option {
	return func(node *Node) {
		node.agentInputFromLastResponse = true
	}
}

// WithSubgraphEventScope customizes the child invocation's filter scope segment.
// Docs note: Scope may be hierarchical (can include '/'). If empty, it
// defaults to the child agent name. The final filterKey becomes
// parent/scope/<uuid>.
func WithSubgraphEventScope(scope string) Option {
	return func(node *Node) {
		node.agentEventScope = scope
	}
}

// WithModelCallbacks sets the model callbacks for LLM node.
func WithModelCallbacks(callbacks *model.Callbacks) Option {
	return func(node *Node) {
		node.modelCallbacks = callbacks
	}
}

// AddNode adds a node with the given ID and function.
// The name and description of the node can be set with the options.
// This automatically sets up Pregel-style channel configuration.
func (sg *StateGraph) AddNode(id string, function NodeFunc, opts ...Option) *StateGraph {
	node := &Node{
		ID:       id,
		Name:     id,
		Function: function,
		Type:     NodeTypeFunction, // Default to function type
	}
	for _, opt := range opts {
		opt(node)
	}
	sg.graph.addNode(node)

	// Automatically set up Pregel-style configuration
	// Create a trigger channel for this node
	triggerChannel := fmt.Sprintf("trigger:%s", id)
	sg.graph.addChannel(triggerChannel, channel.BehaviorLastValue)
	sg.graph.addNodeTriggerChannel(id, triggerChannel)

	return sg
}

// AddLLMNode adds a node that uses the model package directly.
func (sg *StateGraph) AddLLMNode(
	id string,
	model model.Model,
	instruction string,
	tools map[string]tool.Tool,
	opts ...Option,
) *StateGraph {
	node := &Node{}
	for _, opt := range opts {
		opt(node)
	}
	// Build LLM-specific options from node config
	llmOptsForFunc := []LLMNodeFuncOption{
		WithLLMNodeID(id),
		WithLLMRefreshToolSetsOnRun(node.refreshToolSetsOnRun),
		WithLLMToolSets(node.toolSets),
	}
	if node.llmGenerationConfig != nil {
		llmOptsForFunc = append(
			llmOptsForFunc,
			WithLLMGenerationConfig(*node.llmGenerationConfig),
		)
	}
	llmNodeFunc := NewLLMNodeFunc(model, instruction, tools, llmOptsForFunc...)
	// Add LLM node type option
	llmOpts := append([]Option{WithNodeType(NodeTypeLLM)}, opts...)
	sg.AddNode(id, llmNodeFunc, llmOpts...)
	return sg
}

// AddToolsNode adds a node that uses the tools package directly.
func (sg *StateGraph) AddToolsNode(
	id string,
	tools map[string]tool.Tool,
	opts ...Option,
) *StateGraph {
	toolsNodeFunc := NewToolsNodeFunc(tools, opts...)
	// Add tool node type option
	toolOpts := append([]Option{WithNodeType(NodeTypeTool)}, opts...)
	sg.AddNode(id, toolsNodeFunc, toolOpts...)
	return sg
}

// AddAgentNode adds a node that uses a sub-agent by name.
// The agent name should correspond to a sub-agent in the GraphAgent's sub-agent list.
func (sg *StateGraph) AddAgentNode(
	id string,
	opts ...Option,
) *StateGraph {
	agentNodeFunc := NewAgentNodeFunc(id, opts...)
	// Add agent node type option.
	agentOpts := append([]Option{WithNodeType(NodeTypeAgent)}, opts...)
	sg.AddNode(id, agentNodeFunc, agentOpts...)
	return sg
}

// AddSubgraphNode is a sugar alias of AddAgentNode to emphasize subgraph semantics.
func (sg *StateGraph) AddSubgraphNode(id string, opts ...Option) *StateGraph {
	return sg.AddAgentNode(id, opts...)
}

// channelUpdateMarker value for marking channel updates.
const channelUpdateMarker = "update"

// AddEdge adds a normal edge between two nodes.
// This automatically sets up Pregel-style channel configuration.
func (sg *StateGraph) AddEdge(from, to string) *StateGraph {
	edge := &Edge{
		From: from,
		To:   to,
	}
	sg.graph.addEdge(edge)
	// Automatically set up Pregel-style channel for the edge.
	channelName := fmt.Sprintf("branch:to:%s", to)
	sg.graph.addChannel(channelName, channel.BehaviorLastValue)
	// Set up trigger relationship (node subscribes) and trigger mapping.
	sg.graph.addNodeTriggerChannel(to, channelName)
	sg.graph.addNodeTrigger(channelName, to)
	// Add writer to source node.
	writer := channelWriteEntry{
		Channel: channelName,
		Value:   channelUpdateMarker, // Non-nil sentinel to mark update.
	}
	sg.graph.addNodeWriter(from, writer)
	return sg
}

// AddConditionalEdges adds conditional routing from a node.
func (sg *StateGraph) AddConditionalEdges(
	from string,
	condFunc any,
	pathMap map[string]string,
) *StateGraph {
	condEdge := &ConditionalEdge{
		From:      from,
		Condition: wrapperCondFunc(condFunc),
		PathMap:   pathMap,
	}
	sg.graph.addConditionalEdge(condEdge)
	return sg
}

// AddMultiConditionalEdges adds multi-conditional routing from a node.
// The condition returns multiple branch keys for parallel routing.
func (sg *StateGraph) AddMultiConditionalEdges(
	from string,
	condFunc MultiConditionalFunc,
	pathMap map[string]string,
) *StateGraph {
	condEdge := &ConditionalEdge{
		From:      from,
		Condition: wrapperCondFunc(condFunc),
		PathMap:   pathMap,
	}
	sg.graph.addConditionalEdge(condEdge)
	return sg
}

// AddToolsConditionalEdges adds conditional routing from a LLM node to a tools node.
// If the last message has tool calls, route to the tools node.
// Otherwise, route to the fallback node.
func (sg *StateGraph) AddToolsConditionalEdges(
	fromLLMNode string,
	toToolsNode string,
	fallbackNode string,
) *StateGraph {
	condition := func(ctx context.Context, state State) (ConditionResult, error) {
		if msgs, ok := state[StateKeyMessages].([]model.Message); ok {
			if len(msgs) > 0 {
				if len(msgs[len(msgs)-1].ToolCalls) > 0 {
					return ConditionResult{NextNodes: []string{toToolsNode}}, nil
				}
			}
		}
		return ConditionResult{NextNodes: []string{fallbackNode}}, nil
	}
	condEdge := &ConditionalEdge{
		From:      fromLLMNode,
		Condition: condition,
		PathMap: map[string]string{
			toToolsNode:  toToolsNode,
			fallbackNode: fallbackNode,
		},
	}
	sg.graph.addConditionalEdge(condEdge)
	return sg
}

// SetEntryPoint sets the entry point of the graph.
// This is equivalent to addEdge(Start, nodeId).
func (sg *StateGraph) SetEntryPoint(nodeID string) *StateGraph {
	sg.graph.setEntryPoint(nodeID)
	// Also add an edge from Start to make it explicit
	sg.AddEdge(Start, nodeID)
	return sg
}

// SetFinishPoint adds an edge from the node to End.
// This is equivalent to addEdge(nodeId, End).
func (sg *StateGraph) SetFinishPoint(nodeID string) *StateGraph {
	sg.AddEdge(nodeID, End)
	return sg
}

// Compile compiles the graph and returns it for execution.
func (sg *StateGraph) Compile() (*Graph, error) {
	if err := sg.graph.validate(); err != nil {
		return nil, fmt.Errorf("invalid graph: %w", err)
	}
	return sg.graph, nil
}

// WithNodeCallbacks adds node callbacks to the graph state schema.
// This allows users to register callbacks that will be executed during node execution.
func (sg *StateGraph) WithNodeCallbacks(callbacks *NodeCallbacks) *StateGraph {
	sg.graph.schema.AddField(StateKeyNodeCallbacks, StateField{
		Type:    reflect.TypeOf(&NodeCallbacks{}),
		Reducer: DefaultReducer,
		Default: func() any { return callbacks },
	})
	return sg
}

// WithCache sets the graph-level cache implementation.
func (sg *StateGraph) WithCache(cache Cache) *StateGraph {
	if cache != nil {
		sg.graph.setCache(cache)
	}
	return sg
}

// WithCachePolicy sets the default cache policy for all nodes (can be overridden per-node).
func (sg *StateGraph) WithCachePolicy(policy *CachePolicy) *StateGraph {
	sg.graph.setCachePolicy(policy)
	return sg
}

// WithGraphVersion sets an optional version string used for cache namespacing.
// This helps avoid stale cache collisions across graph code changes or deployments.
func (sg *StateGraph) WithGraphVersion(version string) *StateGraph {
	sg.graph.setGraphVersion(version)
	return sg
}

// ClearCache clears caches for the specified nodes. If nodes is empty, it clears all nodes currently in the graph.
func (sg *StateGraph) ClearCache(nodes ...string) *StateGraph {
	if len(nodes) == 0 {
		// collect all nodes
		var all []string
		sg.graph.mu.RLock()
		for id := range sg.graph.nodes {
			all = append(all, id)
		}
		sg.graph.mu.RUnlock()
		sg.graph.clearCacheForNodes(all)
		return sg
	}
	sg.graph.clearCacheForNodes(nodes)
	return sg
}

// MustCompile compiles the graph or panics if invalid.
func (sg *StateGraph) MustCompile() *Graph {
	graph, err := sg.Compile()
	if err != nil {
		panic(err)
	}
	return graph
}

// LLMNodeFuncOption is a function that configures the LLM node function.
type LLMNodeFuncOption func(*llmRunner)

// WithLLMNodeID sets the node ID for the LLM node function.
func WithLLMNodeID(nodeID string) LLMNodeFuncOption {
	return func(runner *llmRunner) {
		runner.nodeID = nodeID
	}
}

// WithLLMRefreshToolSetsOnRun controls whether tools from ToolSets are
// refreshed from the underlying ToolSet on each LLM node run.
func WithLLMRefreshToolSetsOnRun(refresh bool) LLMNodeFuncOption {
	return func(runner *llmRunner) {
		runner.refreshToolSetsOnRun = refresh
	}
}

func mergeToolsWithToolSets(
	ctx context.Context,
	base map[string]tool.Tool,
	toolSets []tool.ToolSet,
) map[string]tool.Tool {
	if len(toolSets) == 0 {
		return base
	}
	out := make(map[string]tool.Tool, len(base))
	for name, t := range base {
		out[name] = t
	}
	for _, toolSet := range toolSets {
		namedToolSet := itool.NewNamedToolSet(toolSet)
		setTools := namedToolSet.Tools(ctx)
		for _, t := range setTools {
			name := t.Declaration().Name
			if _, ok := out[name]; ok {
				log.WarnfContext(
					ctx,
					"tool %s already exists at %s toolset, will be "+
						"overridden",
					name,
					toolSet.Name(),
				)
			}
			out[name] = t
		}
	}
	return out
}

// WithLLMToolSets sets the tool sets for the LLM node function.
func WithLLMToolSets(toolSets []tool.ToolSet) LLMNodeFuncOption {
	return func(runner *llmRunner) {
		if len(toolSets) == 0 {
			return
		}
		if runner.refreshToolSetsOnRun {
			runner.toolSets = append(runner.toolSets, toolSets...)
			return
		}
		if runner.tools == nil {
			runner.tools = make(map[string]tool.Tool)
		}
		runner.tools = mergeToolsWithToolSets(
			context.Background(),
			runner.tools,
			toolSets,
		)
	}
}

// WithLLMGenerationConfig sets the generation configuration for the LLM runner.
func WithLLMGenerationConfig(cfg model.GenerationConfig) LLMNodeFuncOption {
	return func(runner *llmRunner) {
		runner.generationConfig = cfg
	}
}

// NewLLMNodeFunc creates a NodeFunc that uses the model package directly.
// This implements LLM node functionality using the model package interface.
func NewLLMNodeFunc(
	llmModel model.Model,
	instruction string,
	tools map[string]tool.Tool,
	opts ...LLMNodeFuncOption,
) NodeFunc {
	runner := &llmRunner{
		llmModel:         llmModel,
		instruction:      instruction,
		tools:            tools,
		generationConfig: model.GenerationConfig{Stream: true},
	}
	for _, opt := range opts {
		opt(runner)
	}
	return func(ctx context.Context, state State) (any, error) {

		_, span := trace.Tracer.Start(ctx, itelemetry.NewChatSpanName(llmModel.Info().Name))
		defer span.End()
		result, err := runner.execute(ctx, state, span)
		if err != nil {
			span.SetAttributes(attribute.String("trpc.go.agent.error", err.Error()))
			return nil, fmt.Errorf("failed to run model: %w", err)
		}
		return result, nil
	}
}

// llmRunner encapsulates LLM execution dependencies to avoid long parameter
// lists.
type llmRunner struct {
	llmModel             model.Model
	instruction          string
	tools                map[string]tool.Tool
	toolSets             []tool.ToolSet
	refreshToolSetsOnRun bool
	nodeID               string
	generationConfig     model.GenerationConfig
}

// execute implements the three-stage rule for LLM execution.
func (r *llmRunner) execute(ctx context.Context, state State, span oteltrace.Span) (any, error) {
	if v, ok := state[StateKeyOneShotMessages].([]model.Message); ok && len(v) > 0 {
		return r.executeOneShotStage(ctx, state, v, span)
	}
	if userInput, exists := state[StateKeyUserInput]; exists {
		if input, ok := userInput.(string); ok && input != "" {
			return r.executeUserInputStage(ctx, state, input, span)
		}
	}
	return r.executeHistoryStage(ctx, state, span)
}

func (r *llmRunner) executeOneShotStage(
	ctx context.Context,
	state State,
	oneShotMsgs []model.Message,
	span oteltrace.Span,
) (any, error) {
	instr := r.processInstruction(state)
	used := ensureSystemHead(oneShotMsgs, instr)
	result, err := r.executeModel(ctx, state, used, span, instr)
	if err != nil {
		return nil, err
	}
	var ops []MessageOp
	if len(used) > 0 && used[len(used)-1].Role == model.RoleUser {
		ops = append(ops, ReplaceLastUser{Content: used[len(used)-1].Content})
	}
	asst := extractAssistantMessage(result)
	if asst != nil {
		ops = append(ops, AppendMessages{Items: []model.Message{*asst}})
	}
	return State{
		StateKeyMessages:        ops,
		StateKeyOneShotMessages: []model.Message(nil), // Clear one-shot messages after execution.
		StateKeyLastResponse:    asst.Content,
		StateKeyLastResponseID:  extractResponseID(result),
		StateKeyNodeResponses: map[string]any{
			r.nodeID: asst.Content,
		},
	}, nil
}

func (r *llmRunner) executeUserInputStage(
	ctx context.Context, state State, userInput string, span oteltrace.Span,
) (any, error) {
	var history []model.Message
	if msgData, exists := state[StateKeyMessages]; exists {
		if msgs, ok := msgData.([]model.Message); ok {
			history = msgs
		}
	}
	instr := r.processInstruction(state)
	used := ensureSystemHead(history, instr)
	var ops []MessageOp
	if len(used) > 0 && used[len(used)-1].Role == model.RoleUser {
		if used[len(used)-1].Content != userInput {
			used[len(used)-1] = model.NewUserMessage(userInput)
			ops = append(ops, ReplaceLastUser{Content: userInput})
		}
	} else {
		used = append(used, model.NewUserMessage(userInput))
		ops = append(ops, AppendMessages{Items: []model.Message{model.NewUserMessage(userInput)}})
	}
	result, err := r.executeModel(ctx, state, used, span, instr)
	if err != nil {
		return nil, err
	}
	asst := extractAssistantMessage(result)
	if asst != nil {
		ops = append(ops, AppendMessages{Items: []model.Message{*asst}})
	}
	return State{
		StateKeyMessages:     ops,
		StateKeyUserInput:    "", // Clear user input after execution.
		StateKeyLastResponse: asst.Content,
		StateKeyLastResponseID: func() string {
			return extractResponseID(result)
		}(),
		StateKeyNodeResponses: map[string]any{
			r.nodeID: asst.Content,
		},
	}, nil
}

func (r *llmRunner) executeHistoryStage(ctx context.Context, state State, span oteltrace.Span) (any, error) {
	var history []model.Message
	if msgData, exists := state[StateKeyMessages]; exists {
		if msgs, ok := msgData.([]model.Message); ok {
			history = msgs
		}
	}
	instr := r.processInstruction(state)
	used := ensureSystemHead(history, instr)
	result, err := r.executeModel(ctx, state, used, span, instr)
	if err != nil {
		return nil, err
	}
	asst := extractAssistantMessage(result)
	if asst != nil {
		return State{
			StateKeyMessages:     AppendMessages{Items: []model.Message{*asst}},
			StateKeyLastResponse: asst.Content,
			StateKeyLastResponseID: func() string {
				return extractResponseID(result)
			}(),
			StateKeyNodeResponses: map[string]any{
				r.nodeID: asst.Content,
			},
		}, nil
	}
	return nil, nil
}

func (r *llmRunner) executeModel(
	ctx context.Context,
	state State,
	messages []model.Message,
	span oteltrace.Span,
	instructionUsed string,
) (any, error) {
	tools := r.tools
	if r.refreshToolSetsOnRun && len(r.toolSets) > 0 {
		tools = mergeToolsWithToolSets(ctx, tools, r.toolSets)
	}
	request := &model.Request{
		Messages:         messages,
		Tools:            tools,
		GenerationConfig: r.generationConfig,
	}
	invocationID, sessionID, appName, userID, eventChan := extractExecutionContext(state)
	modelCallbacks, _ := state[StateKeyModelCallbacks].(*model.Callbacks)
	var nodeID string
	if nodeIDData, exists := state[StateKeyCurrentNodeID]; exists {
		if id, ok := nodeIDData.(string); ok {
			nodeID = id
		}
	}
	// Build model input metadata from the original state and instruction
	// so events accurately reflect both instruction and user input.
	modelInput := extractModelInput(state, instructionUsed)
	startTime := time.Now()
	modelName := getModelName(r.llmModel)
	emitModelStartEvent(ctx, eventChan, invocationID, modelName, nodeID, modelInput, startTime)
	result, err := executeModelWithEvents(ctx, modelExecutionConfig{
		ModelCallbacks: modelCallbacks,
		LLMModel:       r.llmModel,
		Request:        request,
		EventChan:      eventChan,
		InvocationID:   invocationID,
		SessionID:      sessionID,
		AppName:        appName,
		UserID:         userID,
		Span:           span,
		NodeID:         nodeID,
	})
	endTime := time.Now()
	var modelOutput string
	var responseID string
	if err == nil && result != nil {
		if finalResponse, ok := result.(*model.Response); ok && len(finalResponse.Choices) > 0 {
			modelOutput = finalResponse.Choices[0].Message.Content
			responseID = finalResponse.ID
		}
	}
	emitModelCompleteEvent(
		ctx,
		eventChan,
		invocationID,
		modelName,
		nodeID,
		modelInput,
		modelOutput,
		responseID,
		startTime,
		endTime,
		err,
	)
	return result, err
}

// processInstruction resolves placeholder variables in the instruction using
// the session state present in the graph state (if any). It supports keys like
// {user:...}, {app:...}, and optional suffix {?} consistent with llmagent.
func (r *llmRunner) processInstruction(state State) string {
	instr := r.instruction
	if instr == "" {
		return instr
	}
	// Extract session from graph state.
	if sessVal, ok := state[StateKeySession]; ok {
		if sess, ok := sessVal.(*session.Session); ok && sess != nil {
			// Build a minimal invocation carrying only the session for injection.
			inv := agent.NewInvocation(agent.WithInvocationSession(sess))
			if injected, err := stateinject.InjectSessionState(instr, inv); err == nil {
				return injected
			}
		}
	}
	return instr
}

// extractAssistantMessage extracts the assistant message from model result.
func extractAssistantMessage(result any) *model.Message {
	if result == nil {
		return nil
	}
	if response, ok := result.(*model.Response); ok && len(response.Choices) > 0 {
		return &response.Choices[0].Message
	}
	return nil
}

// extractResponseID extracts response ID from model result.
func extractResponseID(result any) string {
	if response, ok := result.(*model.Response); ok {
		return response.ID
	}
	return ""
}

// ensureSystemHead ensures system prompt is at the head if provided.
func ensureSystemHead(in []model.Message, sys string) []model.Message {
	if sys == "" {
		return in
	}
	if len(in) > 0 && in[0].Role == model.RoleSystem {
		return in
	}
	out := make([]model.Message, 0, len(in)+1)
	out = append(out, model.NewSystemMessage(sys))
	out = append(out, in...)
	return out
}

// extractExecutionContext extracts execution context from state.
func extractExecutionContext(state State) (invocationID, sessionID, appName, userID string, eventChan chan<- *event.Event) {
	if execCtx, exists := state[StateKeyExecContext]; exists {
		execContext, ok := execCtx.(*ExecutionContext)
		if ok {
			eventChan = execContext.EventChan
			invocationID = execContext.InvocationID
		}
	}
	if sess, ok := state[StateKeySession]; ok {
		if s, ok := sess.(*session.Session); ok && s != nil {
			sessionID = s.ID
			appName = s.AppName
			userID = s.UserID
		}

	}
	return invocationID, sessionID, appName, userID, eventChan
}

// modelResponseConfig contains configuration for processing model responses.
type modelResponseConfig struct {
	Response       *model.Response
	ModelCallbacks *model.Callbacks
	EventChan      chan<- *event.Event
	InvocationID   string
	SessionID      string
	LLMModel       model.Model
	Request        *model.Request
	Span           oteltrace.Span
	// NodeID, when provided, is used as the event author.
	NodeID string
}

// processModelResponse processes a single model response.
func processModelResponse(ctx context.Context, config modelResponseConfig) (context.Context, *event.Event, error) {
	if config.ModelCallbacks != nil {
		// Convert response.Error to Go error for callback.
		var modelErr error
		if config.Response != nil && config.Response.Error != nil {
			modelErr = fmt.Errorf("%s: %s", config.Response.Error.Type, config.Response.Error.Message)
		}

		args := &model.AfterModelArgs{
			Request:  config.Request,
			Response: config.Response,
			Error:    modelErr,
		}
		result, err := config.ModelCallbacks.RunAfterModel(ctx, args)
		if err != nil {
			config.Span.SetAttributes(attribute.String("trpc.go.agent.error", err.Error()))
			return ctx, nil, fmt.Errorf("callback after model error: %w", err)
		}
		// Use the context from result if provided for subsequent operations.
		if result != nil && result.Context != nil {
			ctx = result.Context
		}
		if result != nil && result.CustomResponse != nil {
			config.Response = result.CustomResponse
		}
	}
	var llmEvent *event.Event
	author := config.LLMModel.Info().Name
	if config.NodeID != "" {
		author = config.NodeID
	}
	llmEvent = event.NewResponseEvent(config.InvocationID, author, config.Response)
	if config.EventChan != nil && shouldEmitModelResponse(config.Response) {
		invocation, ok := agent.InvocationFromContext(ctx)
		if !ok {
			invocation = agent.NewInvocation(
				agent.WithInvocationID(config.InvocationID),
				agent.WithInvocationModel(config.LLMModel),
				agent.WithInvocationSession(&session.Session{ID: config.SessionID}),
			)
		}

		if err := agent.EmitEvent(ctx, invocation, config.EventChan, llmEvent); err != nil {
			return ctx, nil, err
		}
	}
	if config.Response.Error != nil {
		config.Span.SetAttributes(attribute.String("trpc.go.agent.error", config.Response.Error.Message))
		return ctx, nil, fmt.Errorf("model API error: %s", config.Response.Error.Message)
	}
	return ctx, llmEvent, nil
}

func shouldEmitModelResponse(rsp *model.Response) bool {
	if rsp == nil {
		return false
	}
	if rsp.Error != nil {
		return true
	}
	if rsp.IsValidContent() {
		return true
	}
	for _, choice := range rsp.Choices {
		if choice.Message.ReasoningContent != "" {
			return true
		}
		if choice.Delta.ReasoningContent != "" {
			return true
		}
	}
	return false
}

func runModel(
	ctx context.Context,
	modelCallbacks *model.Callbacks,
	llmModel model.Model,
	request *model.Request,
) (context.Context, <-chan *model.Response, error) {
	ctx, span := trace.Tracer.Start(ctx, "run_model")
	defer span.End()

	// Set span attributes for model execution.
	span.SetAttributes(
		attribute.String("trpc.go.agent.model_name", llmModel.Info().Name),
	)

	if modelCallbacks != nil {
		args := &model.BeforeModelArgs{Request: request}
		result, err := modelCallbacks.RunBeforeModel(ctx, args)
		if err != nil {
			span.SetAttributes(attribute.String("trpc.go.agent.error", err.Error()))
			return ctx, nil, fmt.Errorf("callback before model error: %w", err)
		}
		// Use the context from result if provided.
		if result != nil && result.Context != nil {
			ctx = result.Context
		}
		if result != nil && result.CustomResponse != nil {
			responseChan := make(chan *model.Response, 1)
			responseChan <- result.CustomResponse
			close(responseChan)
			return ctx, responseChan, nil
		}
	}
	// Generate content.
	responseChan, err := llmModel.GenerateContent(ctx, request)
	if err != nil {
		span.SetAttributes(attribute.String("trpc.go.agent.error", err.Error()))
		return ctx, nil, fmt.Errorf("failed to generate content: %w", err)
	}
	return ctx, responseChan, nil
}

// NewToolsNodeFunc creates a NodeFunc that uses the tools package directly.
// This implements tools node functionality using the tools package interface.
func NewToolsNodeFunc(tools map[string]tool.Tool, opts ...Option) NodeFunc {
	node := &Node{}
	for _, opt := range opts {
		opt(node)
	}
	if tools == nil {
		tools = make(map[string]tool.Tool)
	}
	baseTools := tools
	var staticTools map[string]tool.Tool
	if node.refreshToolSetsOnRun {
		staticTools = baseTools
	} else {
		staticTools = mergeToolsWithToolSets(
			context.Background(),
			baseTools,
			node.toolSets,
		)
	}
	// Capture whether to execute tools in parallel.
	parallel := node.enableParallelTools

	return func(ctx context.Context, state State) (any, error) {
		ctx, span := trace.Tracer.Start(ctx, "execute_tools_node")
		defer span.End()

		// Extract and validate messages from state.
		toolCalls, err := extractToolCallsFromState(state, span)
		if err != nil {
			return nil, err
		}

		// Extract execution context for event emission.
		invocationID, _, _, _, eventChan := extractExecutionContext(state)

		effectiveTools := staticTools
		if node.refreshToolSetsOnRun && len(node.toolSets) > 0 {
			effectiveTools = mergeToolsWithToolSets(
				ctx,
				baseTools,
				node.toolSets,
			)
		}

		// Process all tool calls and collect results.
		newMessages, err := processToolCalls(ctx, toolCallsConfig{
			ToolCalls:      toolCalls,
			Tools:          effectiveTools,
			InvocationID:   invocationID,
			EventChan:      eventChan,
			Span:           span,
			State:          state,
			EnableParallel: parallel,
		})
		if err != nil {
			return nil, err
		}
		return State{
			StateKeyMessages: newMessages,
		}, nil
	}
}

// copyRuntimeStateFiltered creates a shallow copy of the parent state excluding
// internal/ephemeral keys that should not leak into a child sub-agent's
// Invocation.RunOptions.RuntimeState (e.g., exec context, callbacks, session).
//
// Important: This is a shallow copy (only key bindings are copied); complex
// values (map/slice) remain shared references. Avoid concurrent mutation of the
// same complex object from parent/child. If isolation is required, deep copy in
// SubgraphInputMapper.
func copyRuntimeStateFiltered(parent State) State {
	if parent == nil {
		return State{}
	}
	out := make(State, len(parent))
	for k, v := range parent {
		if isInternalStateKey(k) {
			continue
		}
		out[k] = v
	}
	return out
}

// NewAgentNodeFunc creates a NodeFunc that looks up and uses a sub-agent by name.
// The agent name should correspond to a sub-agent in the parent GraphAgent's sub-agent list.
func NewAgentNodeFunc(agentName string, opts ...Option) NodeFunc {
	dummyNode := &Node{}
	for _, opt := range opts {
		opt(dummyNode)
	}
	nodeCallbacks := dummyNode.callbacks
	inputMapper := dummyNode.agentInputMapper
	outputMapper := dummyNode.agentOutputMapper
	isolated := dummyNode.agentIsolatedMessages
	scope := dummyNode.agentEventScope
	inputFromLast := dummyNode.agentInputFromLastResponse
	return func(ctx context.Context, state State) (a any, err error) {
		ctx, span := trace.Tracer.Start(ctx, fmt.Sprintf("%s %s", itelemetry.OperationInvokeAgent, agentName))
		defer func() {
			if err != nil {
				span.SetStatus(codes.Error, err.Error())
				span.SetAttributes(attribute.String(itelemetry.KeyErrorType, itelemetry.ValueDefaultErrorType))
			}
			span.End()
		}()

		// Extract execution context for event emission.
		invocationID, _, _, _, eventChan := extractExecutionContext(state)

		// Extract current node ID from state.
		var nodeID string
		if nodeIDData, exists := state[StateKeyCurrentNodeID]; exists {
			if id, ok := nodeIDData.(string); ok {
				nodeID = id
			}
		}

		// Extract parent agent from state to find the sub-agent.
		parentAgent, parentExists := state[StateKeyParentAgent]
		if !parentExists {
			return nil, fmt.Errorf("parent agent not found in state for agent node %s", agentName)
		}

		// Look up the target agent by name from the parent's sub-agents.
		targetAgent := findSubAgentByName(parentAgent, agentName)
		if targetAgent == nil {
			return nil, fmt.Errorf("sub-agent '%s' not found in parent agent's sub-agent list", agentName)
		}

		// Build child runtime state via optional mapper; default to a filtered shallow copy
		// of the parent state to avoid leaking internal/ephemeral keys (exec context, callbacks, etc.).
		var childState State
		if inputMapper != nil {
			if s := inputMapper(state); s != nil {
				childState = s
			} else {
				childState = State{}
			}
		} else {
			childState = copyRuntimeStateFiltered(state)
		}
		if isolated {
			// Instruct child GraphAgent to not include session contents in its request.
			if childState == nil {
				childState = State{}
			}
			childState[CfgKeyIncludeContents] = "none"
		}

		// Optionally map parent's last_response to user_input for this agent node.
		parentForInput := state
		if inputFromLast {
			if lr, ok := state[StateKeyLastResponse]; ok {
				if v, ok2 := lr.(string); ok2 && v != "" {
					// Clone a shallow copy to avoid mutating the original state view.
					cloned := state.Clone()
					cloned[StateKeyUserInput] = v
					parentForInput = cloned
				}
			}
		}

		// Build invocation for the target agent with custom runtime state and scope.
		invocation := buildAgentInvocationWithStateAndScope(ctx, parentForInput, childState, targetAgent, scope)

		itelemetry.TraceBeforeInvokeAgent(span, invocation, targetAgent.Info().Description, "", nil)

		// Emit agent execution start event.
		startTime := time.Now()
		emitAgentStartEvent(ctx, eventChan, invocationID, nodeID, startTime)

		// Execute the target agent.
		// Important: wrap the context with the sub-invocation so downstream
		// callbacks (model/tool) can access it via agent.InvocationFromContext(ctx).
		subCtx := agent.NewInvocationContext(ctx, invocation)
		agentEventChan, err := targetAgent.Run(subCtx, invocation)
		if err != nil {
			// Emit agent execution error event.
			endTime := time.Now()
			emitAgentErrorEvent(ctx, eventChan, invocationID, nodeID, startTime, endTime, err)
			span.SetAttributes(attribute.String("trpc.go.agent.error", err.Error()))
			return nil, fmt.Errorf("failed to run agent %s: %w", agentName, err)
		}

		// Process agent event stream and capture completion state.
		lastResponse, finalState, rawDelta, fullRespEvent, tokenUsage, err := processAgentEventStream(
			ctx, agentEventChan, nodeCallbacks, nodeID, state, eventChan, agentName,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to process agent event stream: %w", err)
		}

		itelemetry.TraceAfterInvokeAgent(span, fullRespEvent, tokenUsage)
		// Emit agent execution complete event.
		endTime := time.Now()
		emitAgentCompleteEvent(ctx, eventChan, invocationID, nodeID, startTime, endTime)
		// Update state with either custom output mapping or default behavior.
		if outputMapper != nil {
			mapped := outputMapper(state, SubgraphResult{LastResponse: lastResponse, FinalState: finalState, RawStateDelta: rawDelta})
			if mapped != nil {
				return mapped, nil
			}
			return State{}, nil
		}
		upd := State{}
		upd[StateKeyLastResponse] = lastResponse
		upd[StateKeyNodeResponses] = map[string]any{nodeID: lastResponse}
		upd[StateKeyUserInput] = ""
		return upd, nil
	}
}

// processAgentEventStream processes the event stream from the target agent.
// This function handles forwarding events and capturing completion state.
func processAgentEventStream(
	ctx context.Context,
	agentEventChan <-chan *event.Event,
	nodeCallbacks *NodeCallbacks,
	nodeID string,
	state State,
	eventChan chan<- *event.Event,
	agentName string,
) (string, State, map[string][]byte, *event.Event, *itelemetry.TokenUsage, error) {
	var lastResponse string
	var finalState State
	var rawDelta map[string][]byte
	var fullRespEvent *event.Event
	tokenUsage := &itelemetry.TokenUsage{}

	for agentEvent := range agentEventChan {
		// Run node callbacks for this event.
		if nodeCallbacks != nil {
			for _, callback := range nodeCallbacks.AgentEvent {
				callback(ctx, &NodeCallbackContext{
					NodeID:   nodeID,
					NodeName: agentName,
				}, state, agentEvent)
			}
		}

		// Forward the event to the parent event channel.
		if err := event.EmitEvent(ctx, eventChan, agentEvent); err != nil {
			return "", nil, nil, fullRespEvent, tokenUsage, err
		}

		// Track the last response for state update.
		if agentEvent.Response != nil && len(agentEvent.Response.Choices) > 0 &&
			agentEvent.Response.Choices[0].Message.Content != "" {
			lastResponse = agentEvent.Response.Choices[0].Message.Content
		}

		if agentEvent.Response != nil {
			if !agentEvent.Response.IsPartial {
				if agentEvent.Response.Usage != nil {
					tokenUsage.PromptTokens += agentEvent.Response.Usage.PromptTokens
					tokenUsage.CompletionTokens += agentEvent.Response.Usage.CompletionTokens
					tokenUsage.TotalTokens += agentEvent.Response.Usage.TotalTokens
				}
				fullRespEvent = agentEvent
			}
		}

		// Capture subgraph completion state from its final graph.execution event.
		if agentEvent.Done && agentEvent.Response != nil &&
			agentEvent.Response.Object == ObjectTypeGraphExecution && agentEvent.StateDelta != nil {
			// Convert StateDelta (JSON bytes) back into a State map.
			tmp := make(State)
			for k, b := range agentEvent.StateDelta {
				var v any
				if err := json.Unmarshal(b, &v); err == nil {
					tmp[k] = v
				} else {
					// Debug-only: record keys that failed to unmarshal to
					// help diagnose type drift. RawStateDelta still
					// carries the original JSON.
					log.DebugfContext(
						ctx,
						"subgraph: failed to unmarshal final state key=%s: %v",
						k,
						err,
					)
				}
			}
			finalState = tmp
			rawDelta = agentEvent.StateDelta
		}
	}

	return lastResponse, finalState, rawDelta, fullRespEvent, tokenUsage, nil
}

// buildAgentInvocationWithStateAndScope builds an invocation for the target agent
// using a custom runtime state and an optional event filter scope segment.
func buildAgentInvocationWithStateAndScope(
	ctx context.Context,
	parentState State,
	runtime State,
	targetAgent agent.Agent,
	scope string,
) *agent.Invocation {
	// Extract user input from parent state.
	var userInput string
	if input, exists := parentState[StateKeyUserInput]; exists {
		if inputStr, ok := input.(string); ok {
			userInput = inputStr
		}
	}
	// Extract session from parent state.
	var sessionData *session.Session
	if sess, exists := parentState[StateKeySession]; exists {
		if sessData, ok := sess.(*session.Session); ok {
			sessionData = sessData
		}
	}

	// Clone from parent invocation if available to preserve linkage and filtering.
	if parentInvocation, ok := agent.InvocationFromContext(ctx); ok &&
		parentInvocation != nil {
		base := util.If(scope != "", scope, targetAgent.Info().Name)
		parentKey := parentInvocation.GetEventFilterKey()
		var filterKey string
		if parentKey == "" {
			filterKey = base + agent.EventFilterKeyDelimiter +
				uuid.NewString()
		} else {
			filterKey = parentKey + agent.EventFilterKeyDelimiter +
				base + agent.EventFilterKeyDelimiter +
				uuid.NewString()
		}
		inv := parentInvocation.Clone(
			agent.WithInvocationAgent(targetAgent),
			agent.WithInvocationMessage(
				model.NewUserMessage(userInput),
			),
			agent.WithInvocationRunOptions(agent.RunOptions{
				RuntimeState: runtime,
			}),
			agent.WithInvocationEventFilterKey(filterKey),
		)
		return inv
	}
	// Create standalone invocation.
	inv := agent.NewInvocation(
		agent.WithInvocationAgent(targetAgent),
		agent.WithInvocationRunOptions(agent.RunOptions{RuntimeState: runtime}),
		agent.WithInvocationMessage(model.NewUserMessage(userInput)),
		agent.WithInvocationSession(sessionData),
		// Unify format with clone branch: <agentName>/<uuid>
		agent.WithInvocationEventFilterKey(targetAgent.Info().Name+agent.EventFilterKeyDelimiter+uuid.NewString()),
	)
	return inv
}

// runTool executes a tool with before/after callbacks and returns the result.
// Parameters:
//   - ctx: context for cancellation and tracing
//   - toolCall: the tool call to execute, including function name and arguments
//   - toolCallbacks: callbacks to execute before and after tool execution
//   - t: the tool implementation to execute
//
// Returns:
//   - context.Context: the updated context from callbacks (if any)
//   - any: the result from tool execution or custom callback result
//   - []byte: the modified arguments after before-tool callbacks (for telemetry)
//   - error: any error that occurred during execution
func runTool(
	ctx context.Context,
	toolCall model.ToolCall,
	toolCallbacks *tool.Callbacks,
	t tool.Tool,
) (context.Context, any, []byte, error) {
	if toolCallbacks != nil {
		args := &tool.BeforeToolArgs{
			ToolName:    toolCall.Function.Name,
			Declaration: t.Declaration(),
			Arguments:   toolCall.Function.Arguments,
		}
		result, err := toolCallbacks.RunBeforeTool(ctx, args)
		if err != nil {
			return ctx, nil, toolCall.Function.Arguments, fmt.Errorf("callback before tool error: %w", err)
		}
		// Use the context from result if provided.
		if result != nil && result.Context != nil {
			ctx = result.Context
		}
		if result != nil && result.CustomResult != nil {
			return ctx, result.CustomResult, toolCall.Function.Arguments, nil
		}
		if result != nil && result.ModifiedArguments != nil {
			toolCall.Function.Arguments = result.ModifiedArguments
		}
	}
	if callableTool, ok := t.(tool.CallableTool); ok {
		result, err := callableTool.Call(ctx, toolCall.Function.Arguments)
		// Run after tool callbacks if they exist.
		// If the tool returns an error, the callback function will still execute to allow the user to handle the error.
		if toolCallbacks != nil {
			args := &tool.AfterToolArgs{
				ToolName:    toolCall.Function.Name,
				Declaration: t.Declaration(),
				Arguments:   toolCall.Function.Arguments,
				Result:      result,
				Error:       err,
			}
			afterResult, afterErr := toolCallbacks.RunAfterTool(ctx, args)
			if afterErr != nil {
				return ctx, nil, toolCall.Function.Arguments, fmt.Errorf("callback after tool error: %w", afterErr)
			}
			// Use the context from result if provided for subsequent operations.
			if afterResult != nil && afterResult.Context != nil {
				ctx = afterResult.Context
			}
			if afterResult != nil && afterResult.CustomResult != nil {
				return ctx, afterResult.CustomResult, toolCall.Function.Arguments, nil
			}
		}
		if err != nil {
			return ctx, nil, toolCall.Function.Arguments, fmt.Errorf("tool %s call failed: %w", toolCall.Function.Name, err)
		}
		return ctx, result, toolCall.Function.Arguments, nil
	}
	return ctx, nil, toolCall.Function.Arguments, fmt.Errorf("tool %s is not callable", toolCall.Function.Name)
}

// extractModelInput extracts the model input from state and instruction.
func extractModelInput(state State, instruction string) string {
	var input string
	// Get user input if available.
	if userInput, exists := state[StateKeyUserInput]; exists {
		if inputStr, ok := userInput.(string); ok && inputStr != "" {
			input = inputStr
		}
	}
	// Add instruction if provided.
	if instruction != "" {
		if input != "" {
			input = instruction + "\n\n" + input
		} else {
			input = instruction
		}
	}
	return input
}

// getModelName extracts the model name from the model instance.
func getModelName(llmModel model.Model) string {
	return llmModel.Info().Name
}

// emitModelStartEvent emits a model execution start event.
func emitModelStartEvent(
	ctx context.Context,
	eventChan chan<- *event.Event,
	invocationID, modelName, nodeID, modelInput string,
	startTime time.Time,
) {
	if eventChan == nil {
		return
	}

	modelStartEvent := NewModelExecutionEvent(
		WithModelEventInvocationID(invocationID),
		WithModelEventModelName(modelName),
		WithModelEventNodeID(nodeID),
		WithModelEventPhase(ModelExecutionPhaseStart),
		WithModelEventStartTime(startTime),
		WithModelEventInput(modelInput),
	)
	invocation, _ := agent.InvocationFromContext(ctx)
	agent.EmitEvent(ctx, invocation, eventChan, modelStartEvent)
}

// emitModelCompleteEvent emits a model execution complete event.
func emitModelCompleteEvent(
	ctx context.Context,
	eventChan chan<- *event.Event,
	invocationID, modelName, nodeID, modelInput, modelOutput, responseID string,
	startTime, endTime time.Time,
	err error,
) {
	if eventChan == nil {
		return
	}

	modelCompleteEvent := NewModelExecutionEvent(
		WithModelEventInvocationID(invocationID),
		WithModelEventModelName(modelName),
		WithModelEventNodeID(nodeID),
		WithModelEventPhase(ModelExecutionPhaseComplete),
		WithModelEventStartTime(startTime),
		WithModelEventEndTime(endTime),
		WithModelEventInput(modelInput),
		WithModelEventOutput(modelOutput),
		WithModelEventError(err),
		WithModelEventResponseID(responseID),
	)

	invocation, _ := agent.InvocationFromContext(ctx)
	agent.EmitEvent(ctx, invocation, eventChan, modelCompleteEvent)
}

// modelExecutionConfig contains configuration for model execution with events.
type modelExecutionConfig struct {
	ModelCallbacks *model.Callbacks
	LLMModel       model.Model
	Request        *model.Request
	EventChan      chan<- *event.Event
	InvocationID   string
	SessionID      string
	AppName        string
	UserID         string
	NodeID         string // Add NodeID for parallel execution support
	NodeResultKey  string // Add NodeResultKey for configurable result key pattern
	Span           oteltrace.Span
}

const (
	errMsgNoModelResponse = "no response received from model"
	errMsgNoModelChoices  = "model returned no choices"
)

// executeModelWithEvents executes the model with event processing.
func executeModelWithEvents(ctx context.Context, config modelExecutionConfig) (any, error) {
	ctx, responseChan, err := runModel(ctx, config.ModelCallbacks, config.LLMModel, config.Request)
	if err != nil {
		config.Span.SetAttributes(attribute.String("trpc.go.agent.error", err.Error()))
		return nil, fmt.Errorf("failed to run model: %w", err)
	}
	invocation, ok := agent.InvocationFromContext(ctx)
	if !ok {
		invocation = agent.NewInvocation(
			agent.WithInvocationID(config.InvocationID),
			agent.WithInvocationModel(config.LLMModel),
			agent.WithInvocationSession(&session.Session{ID: config.SessionID}),
		)
	}

	var lastEvent *event.Event
	// Get or create timing info from invocation (only record first LLM call)
	timingInfo := invocation.GetOrCreateTimingInfo()
	// Create telemetry tracker and defer metrics recording
	tracker := itelemetry.NewChatMetricsTracker(ctx, invocation, config.Request, timingInfo, &err)
	defer tracker.RecordMetrics()()

	// Process response.
	var finalResponse *model.Response
	var toolCalls []model.ToolCall
	for response := range responseChan {
		// Track response for telemetry
		tracker.TrackResponse(response)

		// set timing info to response
		if response.Usage == nil {
			response.Usage = &model.Usage{}
		}
		response.Usage.TimingInfo = timingInfo

		ctx, lastEvent, err = processModelResponse(ctx, modelResponseConfig{
			Response:       response,
			ModelCallbacks: config.ModelCallbacks,
			EventChan:      config.EventChan,
			InvocationID:   config.InvocationID,
			SessionID:      config.SessionID,
			LLMModel:       config.LLMModel,
			Request:        config.Request,
			Span:           config.Span,
			NodeID:         config.NodeID,
		})
		if err != nil {
			return nil, err
		}
		if lastEvent != nil {
			tracker.SetLastEvent(lastEvent)
			itelemetry.TraceChat(config.Span, invocation, config.Request, response, lastEvent.ID, tracker.FirstTokenTimeDuration())
		}

		if len(response.Choices) > 0 && len(response.Choices[0].Message.ToolCalls) > 0 {
			toolCalls = append(toolCalls, response.Choices[0].Message.ToolCalls...)
		}
		finalResponse = response
	}
	if finalResponse == nil {
		config.Span.SetAttributes(attribute.String(
			"trpc.go.agent.error",
			errMsgNoModelResponse,
		))
		return nil, errors.New(errMsgNoModelResponse)
	}
	if len(finalResponse.Choices) == 0 {
		config.Span.SetAttributes(attribute.String(
			"trpc.go.agent.error",
			errMsgNoModelChoices,
		))
		return nil, errors.New(errMsgNoModelChoices)
	}
	if len(finalResponse.Choices[0].Message.ToolCalls) < len(toolCalls) {
		finalResponse.Choices[0].Message.ToolCalls = toolCalls
	}
	return finalResponse, nil
}

// extractToolCallsFromState extracts and validates tool calls from the state.
// It scans backwards from the end to find the most recent assistant message with tool calls,
// stopping when it encounters a user message.
func extractToolCallsFromState(state State, span oteltrace.Span) ([]model.ToolCall, error) {
	var messages []model.Message
	if msgData, exists := state[StateKeyMessages]; exists {
		if msgs, ok := msgData.([]model.Message); ok {
			messages = msgs
		}
	}

	if len(messages) == 0 {
		span.SetAttributes(attribute.String("trpc.go.agent.error", "no messages in state"))
		return nil, errors.New("no messages in state")
	}

	// Scan backwards to find the most recent assistant message with tool calls.
	// Stop when encountering a user message to ensure proper tool call pairing.
	for i := len(messages) - 1; i >= 0; i-- {
		m := messages[i]
		switch m.Role {
		case model.RoleAssistant:
			if len(m.ToolCalls) > 0 {
				return m.ToolCalls, nil
			}
		case model.RoleUser:
			// Stop scanning when we encounter a user message.
			// This ensures we don't process tool calls from previous conversation turns.
			span.SetAttributes(attribute.String("trpc.go.agent.error", "no assistant message with tool calls found before user message"))
			return nil, errors.New("no assistant message with tool calls found before user message")
		default:
			// Skip system, tool, and other message types.
			continue
		}
	}

	span.SetAttributes(attribute.String("trpc.go.agent.error", "no assistant message with tool calls found"))
	return nil, errors.New("no assistant message with tool calls found")
}

// toolCallsConfig contains configuration for processing tool calls.
type toolCallsConfig struct {
	ToolCalls    []model.ToolCall
	Tools        map[string]tool.Tool
	InvocationID string
	EventChan    chan<- *event.Event
	Span         oteltrace.Span
	State        State
	// EnableParallel controls whether multiple tool calls are executed concurrently.
	// When false or when there is only one tool call, execution is serial.
	EnableParallel bool
}

// processToolCalls executes all tool calls and returns the resulting messages.
func processToolCalls(ctx context.Context, config toolCallsConfig) ([]model.Message, error) {
	toolCallbacks, _ := extractToolCallbacks(config.State)
	// Serial path or single tool call.
	if !config.EnableParallel || len(config.ToolCalls) <= 1 {
		newMessages := make([]model.Message, 0, len(config.ToolCalls))
		for _, toolCall := range config.ToolCalls {
			toolMessage, err := executeSingleToolCall(ctx, singleToolCallConfig{
				ToolCall:      toolCall,
				Tools:         config.Tools,
				InvocationID:  config.InvocationID,
				EventChan:     config.EventChan,
				Span:          config.Span,
				ToolCallbacks: toolCallbacks,
				State:         config.State,
			})
			if err != nil {
				return nil, err
			}
			newMessages = append(newMessages, toolMessage)
		}
		return newMessages, nil
	}

	// Parallel path: execute each tool call in its own goroutine while
	// preserving the original order in the resulting messages slice.
	type result struct {
		idx int
		msg model.Message
		err error
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	results := make(chan result, len(config.ToolCalls))
	var wg sync.WaitGroup
	wg.Add(len(config.ToolCalls))

	for i, tc := range config.ToolCalls {
		i, tc := i, tc
		runCtx := agent.CloneContext(ctx)
		go func(ctx context.Context) {
			defer wg.Done()
			msg, err := executeSingleToolCall(ctx, singleToolCallConfig{
				ToolCall:      tc,
				Tools:         config.Tools,
				InvocationID:  config.InvocationID,
				EventChan:     config.EventChan,
				Span:          config.Span,
				ToolCallbacks: toolCallbacks,
				State:         config.State,
			})
			// On error, cancel siblings but still report result so collector can exit cleanly.
			if err != nil {
				cancel()
				results <- result{idx: i, err: err}
				return
			}
			results <- result{idx: i, msg: msg}
		}(runCtx)
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	// Aggregate while preserving order.
	out := make([]model.Message, len(config.ToolCalls))
	var firstErr error
	received := 0
	for r := range results {
		received++
		if r.err != nil && firstErr == nil {
			firstErr = r.err
		}
		// Only set when message exists; zero value is fine otherwise.
		if r.err == nil {
			out[r.idx] = r.msg
		}
		if received == len(config.ToolCalls) {
			break
		}
	}
	if firstErr != nil {
		return nil, firstErr
	}
	return out, nil
}

// singleToolCallConfig contains configuration for executing a single tool call.
type singleToolCallConfig struct {
	ToolCall      model.ToolCall
	Tools         map[string]tool.Tool
	InvocationID  string
	EventChan     chan<- *event.Event
	Span          oteltrace.Span
	ToolCallbacks *tool.Callbacks
	State         State
}

// executeSingleToolCall executes a single tool call with event emission.
func executeSingleToolCall(ctx context.Context, config singleToolCallConfig) (model.Message, error) {
	id, name := config.ToolCall.ID, config.ToolCall.Function.Name
	t := config.Tools[name]
	if t == nil {
		config.Span.SetAttributes(attribute.String("trpc.go.agent.error", fmt.Sprintf("tool %s not found", name)))
		return model.Message{}, fmt.Errorf("tool %s not found", name)
	}

	startTime := time.Now()

	// Extract current node ID from state for event authoring.
	var nodeID string
	var responseID string
	sessInfo := &session.Session{}
	if state := config.State; state != nil {
		if nodeIDData, exists := state[StateKeyCurrentNodeID]; exists {
			if id, ok := nodeIDData.(string); ok {
				nodeID = id
			}
		}
		if rid, ok := state[StateKeyLastResponseID].(string); ok {
			responseID = rid
		}
		if sess, ok := state[StateKeySession]; ok {
			if s, ok := sess.(*session.Session); ok && s != nil {
				sessInfo.ID = s.ID
				sessInfo.AppName = s.AppName
				sessInfo.UserID = s.UserID
			}
		}
	}

	// Execute the tool with callbacks and get modified arguments.
	_, span := trace.Tracer.Start(ctx, itelemetry.NewExecuteToolSpanName(config.ToolCall.Function.Name))
	ctx, result, modifiedArgs, err := runTool(ctx, config.ToolCall, config.ToolCallbacks, t)

	// Emit tool execution start event with modified arguments.
	emitToolStartEvent(
		ctx, config.EventChan, config.InvocationID, name, id, nodeID,
		startTime, modifiedArgs, responseID,
	)

	var interruptErr *InterruptError
	eventErr := err
	if err != nil {
		if errors.As(err, &interruptErr) {
			// Do not emit error payload for interrupt so clients treat it as pause.
			eventErr = nil
		}
	}
	// Emit tool execution complete event.
	event := emitToolCompleteEvent(ctx, toolCompleteEventConfig{
		EventChan:    config.EventChan,
		InvocationID: config.InvocationID,
		ToolName:     name,
		ToolID:       id,
		NodeID:       nodeID,
		StartTime:    startTime,
		Result:       result,
		Error:        eventErr,
		Arguments:    modifiedArgs,
		ResponseID:   responseID,
	})
	itelemetry.TraceToolCall(span, sessInfo, t.Declaration(), modifiedArgs, event, err)
	itelemetry.ReportExecuteToolMetrics(ctx, itelemetry.ExecuteToolAttributes{
		RequestModelName: "trpc-agent-go-graph",
		ToolName:         name,
		AgentName:        fmt.Sprintf("trpc-agent-go-graph-node-id: %s", nodeID),
		AppName:          sessInfo.AppName,
		UserID:           sessInfo.UserID,
		SessionID:        sessInfo.ID,
		Error:            err,
	}, time.Since(startTime))
	span.End()

	if err != nil {
		if interruptErr != nil {
			return model.Message{}, interruptErr
		}
		config.Span.SetAttributes(attribute.String("trpc.go.agent.error", err.Error()))
		return model.Message{}, fmt.Errorf("tool %s call failed: %w", name, err)
	}

	// Marshal result to JSON.
	content, err := json.Marshal(result)
	if err != nil {
		config.Span.SetAttributes(attribute.String("trpc.go.agent.error", err.Error()))
		return model.Message{}, fmt.Errorf("failed to marshal tool result: %w", err)
	}

	return model.NewToolMessage(id, name, string(content)), nil
}

// emitToolStartEvent emits a tool execution start event.
func emitToolStartEvent(
	ctx context.Context,
	eventChan chan<- *event.Event,
	invocationID, toolName, toolID, nodeID string,
	startTime time.Time,
	arguments []byte,
	responseID string,
) {
	if eventChan == nil {
		return
	}

	toolStartEvent := NewToolExecutionEvent(
		WithToolEventInvocationID(invocationID),
		WithToolEventToolName(toolName),
		WithToolEventToolID(toolID),
		WithToolEventResponseID(responseID),
		WithToolEventNodeID(nodeID),
		WithToolEventPhase(ToolExecutionPhaseStart),
		WithToolEventStartTime(startTime),
		WithToolEventInput(string(arguments)),
	)

	invocation, _ := agent.InvocationFromContext(ctx)
	agent.EmitEvent(ctx, invocation, eventChan, toolStartEvent)
}

// toolCompleteEventConfig contains configuration for tool complete events.
type toolCompleteEventConfig struct {
	EventChan    chan<- *event.Event
	InvocationID string
	ToolName     string
	ToolID       string
	NodeID       string
	ResponseID   string
	StartTime    time.Time
	Result       any
	Error        error
	Arguments    []byte
}

// emitToolCompleteEvent emits a tool execution complete event.
func emitToolCompleteEvent(ctx context.Context, config toolCompleteEventConfig) *event.Event {
	if config.EventChan == nil {
		return nil
	}

	endTime := time.Now()
	var outputStr string
	if config.Error == nil && config.Result != nil {
		if outputBytes, marshalErr := json.Marshal(config.Result); marshalErr == nil {
			outputStr = string(outputBytes)
		}
	}

	toolCompleteEvent := NewToolExecutionEvent(
		WithToolEventInvocationID(config.InvocationID),
		WithToolEventToolName(config.ToolName),
		WithToolEventToolID(config.ToolID),
		WithToolEventResponseID(config.ResponseID),
		WithToolEventNodeID(config.NodeID),
		WithToolEventPhase(ToolExecutionPhaseComplete),
		WithToolEventStartTime(config.StartTime),
		WithToolEventEndTime(endTime),
		WithToolEventInput(string(config.Arguments)),
		WithToolEventOutput(outputStr),
		WithToolEventError(config.Error),
		WithToolEventIncludeResponse(true),
	)
	invocation, _ := agent.InvocationFromContext(ctx)
	agent.EmitEvent(ctx, invocation, config.EventChan, toolCompleteEvent)
	return toolCompleteEvent
}

// extractToolCallbacks extracts tool callbacks from the state.
func extractToolCallbacks(state State) (*tool.Callbacks, bool) {
	if toolCallbacks, exists := state[StateKeyToolCallbacks]; exists {
		if callbacks, ok := toolCallbacks.(*tool.Callbacks); ok {
			return callbacks, true
		}
	}
	return nil, false
}

// MessagesStateSchema creates a state schema optimized for message-based workflows.
func MessagesStateSchema() *StateSchema {
	schema := NewStateSchema()
	schema.AddField(StateKeyMessages, StateField{
		Type:    reflect.TypeOf([]model.Message{}),
		Reducer: MessageReducer,
		Default: func() any { return []model.Message{} },
	})
	schema.AddField(StateKeyUserInput, StateField{
		Type:    reflect.TypeOf(""),
		Reducer: DefaultReducer,
	})
	schema.AddField(StateKeyLastResponse, StateField{
		Type:    reflect.TypeOf(""),
		Reducer: DefaultReducer,
	})
	schema.AddField(StateKeyLastResponseID, StateField{
		Type:    reflect.TypeOf(""),
		Reducer: DefaultReducer,
	})
	schema.AddField(StateKeyNodeResponses, StateField{
		Type:    reflect.TypeOf(map[string]any{}),
		Reducer: MergeReducer,
		Default: func() any { return map[string]any{} },
	})
	schema.AddField(StateKeyMetadata, StateField{
		Type:    reflect.TypeOf(map[string]any{}),
		Reducer: MergeReducer,
		Default: func() any { return make(map[string]any) },
	})
	return schema
}

// buildAgentInvocation builds an invocation for the target agent.
func buildAgentInvocation(ctx context.Context, state State, targetAgent agent.Agent) *agent.Invocation {
	// Delegate to the unified builder with default runtime state and empty scope.
	return buildAgentInvocationWithStateAndScope(ctx, state, state, targetAgent, "")
}

// emitAgentStartEvent emits an agent execution start event.
func emitAgentStartEvent(
	ctx context.Context,
	eventChan chan<- *event.Event,
	invocationID, nodeID string,
	startTime time.Time,
) {
	if eventChan == nil {
		return
	}

	agentStartEvent := NewNodeStartEvent(
		WithNodeEventInvocationID(invocationID),
		WithNodeEventNodeID(nodeID),
		WithNodeEventNodeType(NodeTypeAgent),
		WithNodeEventStartTime(startTime),
	)
	invocation, _ := agent.InvocationFromContext(ctx)
	agent.EmitEvent(ctx, invocation, eventChan, agentStartEvent)
}

// emitAgentCompleteEvent emits an agent execution complete event.
func emitAgentCompleteEvent(
	ctx context.Context,
	eventChan chan<- *event.Event,
	invocationID, nodeID string,
	startTime, endTime time.Time,
) {
	if eventChan == nil {
		return
	}

	agentCompleteEvent := NewNodeCompleteEvent(
		WithNodeEventInvocationID(invocationID),
		WithNodeEventNodeID(nodeID),
		WithNodeEventNodeType(NodeTypeAgent),
		WithNodeEventStartTime(startTime),
		WithNodeEventEndTime(endTime),
	)

	invocation, _ := agent.InvocationFromContext(ctx)
	agent.EmitEvent(ctx, invocation, eventChan, agentCompleteEvent)
}

// emitAgentErrorEvent emits an agent execution error event.
func emitAgentErrorEvent(
	ctx context.Context,
	eventChan chan<- *event.Event,
	invocationID, nodeID string,
	startTime, endTime time.Time,
	err error,
) {
	if eventChan == nil {
		return
	}

	agentErrorEvent := NewNodeErrorEvent(
		WithNodeEventInvocationID(invocationID),
		WithNodeEventNodeID(nodeID),
		WithNodeEventNodeType(NodeTypeAgent),
		WithNodeEventStartTime(startTime),
		WithNodeEventEndTime(endTime),
		WithNodeEventError(err.Error()),
	)

	invocation, _ := agent.InvocationFromContext(ctx)
	agent.EmitEvent(ctx, invocation, eventChan, agentErrorEvent)
}

// findSubAgentByName looks up a sub-agent by name from the parent agent.
func findSubAgentByName(parentAgent any, agentName string) agent.Agent {
	// Try to cast to an interface that has SubAgents method.
	type SubAgentProvider interface {
		FindSubAgent(name string) agent.Agent
	}
	if provider, ok := parentAgent.(SubAgentProvider); ok {
		return provider.FindSubAgent(agentName)
	}
	return nil
}
