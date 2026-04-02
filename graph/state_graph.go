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
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"reflect"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph/internal/channel"
	"trpc.group/trpc-go/trpc-agent-go/internal/jsonrepair"
	promptstate "trpc.group/trpc-go/trpc-agent-go/internal/prompt/adapter/state"
	istructure "trpc.group/trpc-go/trpc-agent-go/internal/structure"
	itelemetry "trpc.group/trpc-go/trpc-agent-go/internal/telemetry"
	itool "trpc.group/trpc-go/trpc-agent-go/internal/tool"
	"trpc.group/trpc-go/trpc-agent-go/internal/toolcall"
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
	graph       *Graph
	buildErrors *stateGraphBuildErrors
}

type stateGraphBuildErrors struct {
	errs []error
}

// NewStateGraph creates a new graph builder with the given state schema.
func NewStateGraph(schema *StateSchema) *StateGraph {
	return &StateGraph{
		graph: New(schema),
	}
}

func (sg *StateGraph) addBuildError(err error) {
	if err == nil {
		return
	}
	if sg.buildErrors == nil {
		sg.buildErrors = &stateGraphBuildErrors{}
	}
	sg.buildErrors.errs = append(sg.buildErrors.errs, err)
}

func (sg *StateGraph) buildErr() error {
	if sg.buildErrors == nil {
		return nil
	}
	return errors.Join(sg.buildErrors.errs...)
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

// WithUserInputKey sets the state key used as one-shot user input for LLM and
// Agent nodes. When empty, StateKeyUserInput is used.
func WithUserInputKey(key string) Option {
	return func(node *Node) {
		node.userInputKey = key
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

// WithInterruptBefore pauses execution before this node runs.
func WithInterruptBefore() Option {
	return func(node *Node) {
		node.interruptBefore = true
	}
}

// WithInterruptAfter pauses execution after this node runs.
func WithInterruptAfter() Option {
	return func(node *Node) {
		node.interruptAfter = true
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

// WithStreamOutput enables node-to-node streaming for this node.
//
// For LLM and Agent nodes, streaming deltas are forwarded to the named stream.
// Function nodes can write to streams directly via OpenStreamWriter.
func WithStreamOutput(streamName string) Option {
	return func(node *Node) {
		node.streamOutputName = streamName
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
			node.callbacks.AgentEvent = append(node.callbacks.AgentEvent, callbacks.AgentEvent...)
		}
	}
}

// WithToolCallbacks sets tool callbacks for this specific node.
// This allows configuring tool callbacks at the node level.
// When both node-level and state-level callbacks are present, node-level
// callbacks take precedence.
// This option is only effective for tool nodes.
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
// RawStateDelta provides the original serialized final-state snapshot map from
// the subgraph's terminal graph.execution event. Callers can decode values
// with custom types if needed. Note that FinalState is reconstructed by JSON
// decoding, which may coerce numbers to float64 and complex structures to
// map[string]any.
//
// FallbackStateDelta and FallbackState are only populated when the child ends
// with a fatal error before emitting graph.execution. They carry the best-
// effort business state accumulated from the fatal path and are intentionally
// kept separate from FinalState/RawStateDelta so callers can distinguish a
// normal terminal snapshot from fatal fallback state.
type SubgraphResult struct {
	LastResponse       string
	FinalState         State
	RawStateDelta      map[string][]byte
	FallbackState      State
	FallbackStateDelta map[string][]byte
	StructuredOutput   any // Structured output from sub-agent.
}

// EffectiveState returns the normal final state when it exists, otherwise the
// fatal fallback state.
func (r SubgraphResult) EffectiveState() State {
	if r.FinalState != nil || r.RawStateDelta != nil {
		return r.FinalState
	}
	return r.FallbackState
}

// EffectiveStateDelta returns the normal final-state delta when it exists,
// otherwise the fatal fallback delta.
func (r SubgraphResult) EffectiveStateDelta() map[string][]byte {
	if r.RawStateDelta != nil {
		return r.RawStateDelta
	}
	return r.FallbackStateDelta
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

// WithSubgraphEventScope customizes the child's event filter scope.
// Docs note: Scope may be hierarchical (can include '/').
// If empty, it defaults to the child agent name.
// The final filterKey becomes parent/scope (no UUID).
// This keeps the filterKey stable across turns.
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

// tracingDisabled reports whether tracing is disabled for the invocation.
func tracingDisabled(invocation *agent.Invocation) bool {
	return invocation != nil && invocation.RunOptions.DisableTracing
}

// tracingDisabledInContext reports whether tracing is disabled for the invocation in context.
func tracingDisabledInContext(ctx context.Context) bool {
	invocation, ok := agent.InvocationFromContext(ctx)
	return ok && tracingDisabled(invocation)
}

// startNodeSpan returns a no-op span when tracing is disabled and otherwise starts a new span.
func startNodeSpan(ctx context.Context, spanName string) (context.Context, oteltrace.Span, bool) {
	if tracingDisabledInContext(ctx) {
		return ctx, noop.Span{}, false
	}
	ctx, span := trace.Tracer.Start(ctx, spanName)
	return ctx, span, true
}

// startNodeSpanForInvocation returns a no-op span when tracing is disabled for the invocation or context.
func startNodeSpanForInvocation(ctx context.Context, invocation *agent.Invocation, spanName string) (context.Context, oteltrace.Span, bool) {
	if tracingDisabled(invocation) || tracingDisabledInContext(ctx) {
		return ctx, noop.Span{}, false
	}
	ctx, span := trace.Tracer.Start(ctx, spanName)
	return ctx, span, true
}

func workflowTypeFromNodeType(nodeType NodeType) itelemetry.WorkflowType {
	switch nodeType {
	case NodeTypeFunction:
		return itelemetry.WorkflowTypeFunction
	case NodeTypeLLM:
		return itelemetry.WorkflowTypeLLM
	case NodeTypeTool:
		return itelemetry.WorkflowTypeTool
	case NodeTypeAgent:
		return itelemetry.WorkflowTypeAgent
	case NodeTypeJoin:
		return itelemetry.WorkflowTypeJoin
	case NodeTypeRouter:
		return itelemetry.WorkflowTypeRouter
	default:
		return itelemetry.WorkflowType(nodeType)
	}
}

// AddNode adds a node with the given ID and function.
// The name and description of the node can be set with the options.
// This automatically sets up Pregel-style channel configuration.
func (sg *StateGraph) AddNode(id string, function NodeFunc, opts ...Option) *StateGraph {
	node := &Node{
		ID:   id,
		Name: id,
		Type: NodeTypeFunction, // Default to function type
	}
	for _, opt := range opts {
		opt(node)
	}

	node.Function = func(ctx context.Context, state State) (any, error) {
		if tracingDisabledInContext(ctx) {
			return function(ctx, state)
		}

		ctx, span := trace.Tracer.Start(ctx, itelemetry.NewWorkflowSpanName(fmt.Sprintf("execute_function_node %s", id)))
		workflow := &itelemetry.Workflow{
			Name:    fmt.Sprintf("execute_function_node %s", id),
			ID:      id,
			Type:    workflowTypeFromNodeType(node.Type),
			Request: state.safeClone(),
		}
		defer func() {
			itelemetry.TraceWorkflow(span, workflow)
			span.End()
		}()

		response, err := function(ctx, state)
		workflow.Response = response
		if err != nil {
			workflow.Error = err
			return response, err
		}
		return response, nil
	}

	if err := sg.graph.addNode(node); err != nil {
		sg.addBuildError(fmt.Errorf("AddNode(%q): %w", id, err))
		return sg
	}

	// Automatically set up Pregel-style configuration.
	// Create a trigger channel for this node.
	triggerChannel := fmt.Sprintf("trigger:%s", id)
	sg.graph.addChannel(triggerChannel, channel.BehaviorLastValue)
	sg.graph.addNodeTriggerChannel(id, triggerChannel)

	return sg
}

// AddLLMNode adds a node that uses the model package directly.
func (sg *StateGraph) AddLLMNode(
	id string,
	llmModel model.Model,
	instruction string,
	tools map[string]tool.Tool,
	opts ...Option,
) *StateGraph {
	node := &Node{
		ID:   id,
		Name: id,
		Type: NodeTypeLLM,
	}
	for _, opt := range opts {
		opt(node)
	}
	node.instruction = instruction
	node.llmModel = llmModel
	node.baseTools = cloneToolsMap(tools)
	runner := &llmRunner{
		llmModel:             llmModel,
		instruction:          instruction,
		tools:                tools,
		refreshToolSetsOnRun: node.refreshToolSetsOnRun,
		nodeID:               id,
		generationConfig:     model.GenerationConfig{Stream: true},
		userInputKey:         node.userInputKey,
		streamOutputName:     node.streamOutputName,
	}
	if len(node.toolSets) > 0 {
		if runner.refreshToolSetsOnRun {
			runner.toolSets = append(runner.toolSets, node.toolSets...)
		} else {
			runner.tools = mergeToolsWithToolSets(context.Background(), runner.tools, node.toolSets)
			node.baseTools = cloneToolsMap(runner.tools)
		}
	}
	if node.llmGenerationConfig != nil {
		runner.generationConfig = *node.llmGenerationConfig
	}

	workflowName := "execute_function_node " + id
	workflowSpanName := itelemetry.NewWorkflowSpanName(workflowName)
	modelName := ""
	if runner.llmModel != nil {
		modelName = runner.llmModel.Info().Name
	}
	chatSpanName := itelemetry.NewChatSpanName(modelName)

	node.Function = func(ctx context.Context, state State) (any, error) {
		ctx, wfSpan, startedWorkflowSpan := startNodeSpan(
			ctx,
			workflowSpanName,
		)
		recordWorkflow := startedWorkflowSpan && wfSpan != nil && wfSpan.IsRecording()
		var workflow *itelemetry.Workflow
		if recordWorkflow {
			workflow = &itelemetry.Workflow{
				Name:    workflowName,
				ID:      id,
				Type:    workflowTypeFromNodeType(node.Type),
				Request: state.safeClone(),
			}
		}
		defer func() {
			if recordWorkflow {
				itelemetry.TraceWorkflow(wfSpan, workflow)
			}
			if startedWorkflowSpan && wfSpan != nil {
				wfSpan.End()
			}
		}()

		ctx, span, startedSpan := startNodeSpan(ctx, chatSpanName)
		defer func() {
			if startedSpan && span != nil {
				span.End()
			}
		}()
		result, err := runner.execute(ctx, state, span)
		if recordWorkflow {
			workflow.Response = result
		}
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			wrapped := fmt.Errorf("failed to run model: %w", err)
			if recordWorkflow {
				workflow.Error = wrapped
			}
			return result, wrapped
		}
		return result, nil
	}

	if err := sg.graph.addNode(node); err != nil {
		sg.addBuildError(fmt.Errorf("AddNode(%q): %w", id, err))
		return sg
	}

	// Automatically set up Pregel-style configuration.
	// Create a trigger channel for this node.
	triggerChannel := fmt.Sprintf("trigger:%s", id)
	sg.graph.addChannel(triggerChannel, channel.BehaviorLastValue)
	sg.graph.addNodeTriggerChannel(id, triggerChannel)
	return sg
}

// AddToolsNode adds a node that uses the tools package directly.
func (sg *StateGraph) AddToolsNode(
	id string,
	tools map[string]tool.Tool,
	opts ...Option,
) *StateGraph {
	toolOpts := append([]Option{WithNodeType(NodeTypeTool)}, opts...)
	resolvedTools := cloneToolsMap(tools)
	toolsNodeFunc, baseTools := newToolsNodeRuntime(resolvedTools, toolOpts...)
	existingNode, existedBefore := sg.graph.Node(id)
	sg.AddNode(id, toolsNodeFunc, toolOpts...)
	node, ok := sg.graph.Node(id)
	if !ok || node == nil {
		return sg
	}
	if existedBefore && node == existingNode {
		return sg
	}
	node.baseTools = baseTools
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

// WithInterruptBeforeNodes enables static interrupts before the given nodes.
func (sg *StateGraph) WithInterruptBeforeNodes(
	nodeIDs ...string,
) *StateGraph {
	sg.setStaticInterruptNodes(nodeIDs, true)
	return sg
}

// WithInterruptAfterNodes enables static interrupts after the given nodes.
func (sg *StateGraph) WithInterruptAfterNodes(
	nodeIDs ...string,
) *StateGraph {
	sg.setStaticInterruptNodes(nodeIDs, false)
	return sg
}

func (sg *StateGraph) setStaticInterruptNodes(
	nodeIDs []string,
	before bool,
) {
	for _, nodeID := range nodeIDs {
		sg.graph.mu.Lock()
		node, ok := sg.graph.nodes[nodeID]
		if ok && node != nil {
			if before {
				node.interruptBefore = true
			} else {
				node.interruptAfter = true
			}
		}
		sg.graph.mu.Unlock()

		if !ok || node == nil {
			if before {
				sg.addBuildError(fmt.Errorf(
					"WithInterruptBeforeNodes(%q): node not found",
					nodeID,
				))
				continue
			}
			sg.addBuildError(fmt.Errorf(
				"WithInterruptAfterNodes(%q): node not found",
				nodeID,
			))
		}
	}
}

// channelUpdateMarker value for marking channel updates.
const channelUpdateMarker = "update"

const (
	joinChannelFromSeparator = ":from:"
	joinKeyLenBytes          = 8
)

// AddEdge adds a normal edge between two nodes.
// This automatically sets up Pregel-style channel configuration.
func (sg *StateGraph) AddEdge(from, to string) *StateGraph {
	edge := &Edge{
		From: from,
		To:   to,
	}
	if err := sg.graph.addEdge(edge); err != nil {
		sg.addBuildError(fmt.Errorf("AddEdge(%q -> %q): %w", from, to, err))
		return sg
	}
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

// AddJoinEdge adds a join edge that waits for all start nodes to complete
// before triggering the end node.
func (sg *StateGraph) AddJoinEdge(fromNodes []string, to string) *StateGraph {
	starts := normalizeJoinStarts(fromNodes)
	if to == "" || to == Start || len(starts) == 0 {
		return sg
	}

	sg.graph.mu.RLock()
	if to != End {
		if _, exists := sg.graph.nodes[to]; !exists {
			sg.graph.mu.RUnlock()
			sg.addBuildError(fmt.Errorf("AddJoinEdge(to=%q): target node %s does not exist", to, to))
			return sg
		}
	}
	for _, from := range starts {
		if _, exists := sg.graph.nodes[from]; !exists {
			sg.graph.mu.RUnlock()
			sg.addBuildError(fmt.Errorf("AddJoinEdge(from=%q -> to=%q): source node %s does not exist", from, to, from))
			return sg
		}
	}
	sg.graph.mu.RUnlock()

	for _, from := range starts {
		edge := &Edge{
			From: from,
			To:   to,
		}
		if err := sg.graph.addEdge(edge); err != nil {
			sg.addBuildError(fmt.Errorf("AddJoinEdge(%q -> %q): %w", from, to, err))
			return sg
		}
	}

	channelName := joinChannelName(to, starts)

	sg.graph.addChannel(channelName, channel.BehaviorBarrier)
	if ch, ok := sg.graph.getChannel(channelName); ok && ch != nil {
		ch.SetBarrierExpected(starts)
	}

	sg.graph.addNodeTriggerChannel(to, channelName)
	sg.graph.addNodeTrigger(channelName, to)

	for _, from := range starts {
		writer := channelWriteEntry{
			Channel: channelName,
			Value:   from,
		}
		sg.graph.addNodeWriter(from, writer)
	}
	return sg
}

func joinChannelName(to string, starts []string) string {
	joinKey := joinKeyForStarts(starts)
	return ChannelJoinPrefix + to + joinChannelFromSeparator + joinKey
}

func joinKeyForStarts(starts []string) string {
	h := sha256.New()
	for _, start := range starts {
		var lenBuf [joinKeyLenBytes]byte
		binary.BigEndian.PutUint64(lenBuf[:], uint64(len(start)))
		_, _ = h.Write(lenBuf[:])
		_, _ = h.Write([]byte(start))
	}
	return hex.EncodeToString(h.Sum(nil))
}

func normalizeJoinStarts(fromNodes []string) []string {
	if len(fromNodes) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(fromNodes))
	out := make([]string, 0, len(fromNodes))
	for _, from := range fromNodes {
		if from == "" || from == Start || from == End {
			continue
		}
		if seen[from] {
			continue
		}
		seen[from] = true
		out = append(out, from)
	}
	sort.Strings(out)
	return out
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
	if err := sg.graph.addConditionalEdge(condEdge); err != nil {
		sg.addBuildError(fmt.Errorf("AddConditionalEdges(from=%q): %w", from, err))
		return sg
	}
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
	if err := sg.graph.addConditionalEdge(condEdge); err != nil {
		sg.addBuildError(fmt.Errorf("AddMultiConditionalEdges(from=%q): %w", from, err))
		return sg
	}
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
	if err := sg.graph.addConditionalEdge(condEdge); err != nil {
		sg.addBuildError(fmt.Errorf(
			"AddToolsConditionalEdges(from=%q, tools=%q, fallback=%q): %w",
			fromLLMNode, toToolsNode, fallbackNode, err,
		))
		return sg
	}
	return sg
}

// SetEntryPoint sets the entry point of the graph.
// This is equivalent to addEdge(Start, nodeId).
func (sg *StateGraph) SetEntryPoint(nodeID string) *StateGraph {
	if err := sg.graph.setEntryPoint(nodeID); err != nil {
		sg.addBuildError(fmt.Errorf("SetEntryPoint(%q): %w", nodeID, err))
		return sg
	}
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
	if err := sg.buildErr(); err != nil {
		return nil, fmt.Errorf("graph build failed: %w", err)
	}
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

// WithLLMUserInputKey sets the one-shot input state key used by the LLM node.
// When empty, StateKeyUserInput is used.
func WithLLMUserInputKey(key string) LLMNodeFuncOption {
	return func(runner *llmRunner) {
		if key == "" {
			runner.userInputKey = StateKeyUserInput
			return
		}
		runner.userInputKey = key
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

func cloneToolsMap(tools map[string]tool.Tool) map[string]tool.Tool {
	if len(tools) == 0 {
		return nil
	}
	cloned := make(map[string]tool.Tool, len(tools))
	for name, currentTool := range tools {
		cloned[name] = currentTool
	}
	return cloned
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

// WithLLMStreamOutput sets the stream name used for node-to-node streaming.
func WithLLMStreamOutput(streamName string) LLMNodeFuncOption {
	return func(runner *llmRunner) {
		runner.streamOutputName = streamName
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
		userInputKey:     StateKeyUserInput,
	}
	for _, opt := range opts {
		opt(runner)
	}
	return func(ctx context.Context, state State) (any, error) {
		_, span, startedSpan := startNodeSpan(ctx, itelemetry.NewChatSpanName(llmModel.Info().Name))
		if startedSpan {
			defer span.End()
		}
		result, err := runner.execute(ctx, state, span)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
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
	userInputKey         string
	streamOutputName     string
}

// execute implements the three-stage rule for LLM execution.
func (r *llmRunner) execute(ctx context.Context, state State, span oteltrace.Span) (any, error) {
	if msgs, ok := GetOneShotMessagesForNode(state, r.nodeID); ok {
		return r.executeOneShotStage(
			ctx,
			state,
			msgs,
			span,
			ClearOneShotMessagesForNode(r.nodeID),
		)
	}
	if v, ok := state[StateKeyOneShotMessages].([]model.Message); ok && len(v) > 0 {
		return r.executeOneShotStage(
			ctx,
			state,
			v,
			span,
			State{
				StateKeyOneShotMessages: []model.Message(nil),
			},
		)
	}
	userInputKey := r.userInputKey
	if userInputKey == "" {
		userInputKey = StateKeyUserInput
	}
	if userInput, exists := state[userInputKey]; exists {
		if input, ok := userInput.(string); ok && input != "" {
			return r.executeUserInputStage(
				ctx,
				state,
				userInputKey,
				input,
				span,
			)
		}
	}
	return r.executeHistoryStage(ctx, state, span)
}

func (r *llmRunner) executeOneShotStage(
	ctx context.Context,
	state State,
	oneShotMsgs []model.Message,
	span oteltrace.Span,
	clearUpdate State,
) (any, error) {
	instr := r.processInstruction(state)
	used := ensureSystemHead(oneShotMsgs, instr)
	used = r.insertFewShot(state, used)
	result, err := r.executeModel(ctx, state, used, span, instr)
	if err != nil {
		return nil, err
	}
	// Preallocate the common fast-path operations slice to avoid re-slicing
	// growth on hot paths.
	ops := make([]MessageOp, 0, 2)
	if len(used) > 0 && used[len(used)-1].Role == model.RoleUser {
		ops = append(ops, ReplaceLastUser{Content: used[len(used)-1].Content})
	}
	asst := extractAssistantMessage(result)
	if asst != nil {
		ops = append(ops, AppendMessages{Items: []model.Message{*asst}})
	}
	out := State{
		StateKeyMessages:       ops,
		StateKeyLastResponse:   asst.Content,
		StateKeyLastResponseID: extractResponseID(result),
		StateKeyNodeResponses: map[string]any{
			r.nodeID: asst.Content,
		},
	}
	maps.Copy(out, clearUpdate)
	return out, nil
}

func (r *llmRunner) executeUserInputStage(
	ctx context.Context,
	state State,
	userInputKey string,
	userInput string,
	span oteltrace.Span,
) (any, error) {
	var history []model.Message
	if msgData, exists := state[StateKeyMessages]; exists {
		if msgs, ok := msgData.([]model.Message); ok {
			history = msgs
		}
	}
	instr := r.processInstruction(state)
	used := ensureSystemHead(history, instr)
	used = r.insertFewShot(state, used)
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
	if userInputKey == "" {
		userInputKey = StateKeyUserInput
	}
	return State{
		StateKeyMessages:       ops,
		userInputKey:           "", // Clear user input after execution.
		StateKeyLastResponse:   asst.Content,
		StateKeyLastResponseID: extractResponseID(result),
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
	used = r.insertFewShot(state, used)
	result, err := r.executeModel(ctx, state, used, span, instr)
	if err != nil {
		return nil, err
	}
	asst := extractAssistantMessage(result)
	if asst != nil {
		return State{
			StateKeyMessages:       AppendMessages{Items: []model.Message{*asst}},
			StateKeyLastResponse:   asst.Content,
			StateKeyLastResponseID: extractResponseID(result),
			StateKeyNodeResponses: map[string]any{
				r.nodeID: asst.Content,
			},
		}, nil
	}
	return nil, nil
}

func applyInvocationRequestOverrides(
	request *model.Request,
	invocation *agent.Invocation,
	nodeID string,
) {
	if invocation == nil {
		return
	}
	if opts := graphCallOptionsFromConfigs(
		invocation.RunOptions.CustomAgentConfigs,
	); opts != nil {
		patch := generationPatchForNode(opts, nodeID)
		request.GenerationConfig = model.ApplyGenerationConfigPatch(
			request.GenerationConfig,
			patch,
		)
	}
	if invocation.RunOptions.Stream != nil {
		request.GenerationConfig.Stream = *invocation.RunOptions.Stream
	}
}

func extractModelResponseSummary(result any) (string, string) {
	finalResponse, ok := result.(*model.Response)
	if !ok || finalResponse == nil || len(finalResponse.Choices) == 0 {
		return "", ""
	}
	return finalResponse.Choices[0].Message.Content, finalResponse.ID
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
	nodeID := r.nodeID
	if v, ok := state[StateKeyCurrentNodeID].(string); ok && v != "" {
		nodeID = v
	}
	invocation := invocationFromContextOrDefault(ctx, nil)
	effectiveModel := graphPatchedModel(invocation, nodeID, r.llmModel)
	if patch, ok := graphSurfacePatch(invocation, nodeID); ok {
		if patchedTools, ok := patch.Tools(); ok {
			tools = toolSliceToMap(patchedTools)
		}
	}
	request := &model.Request{
		Messages:         messages,
		Tools:            tools,
		GenerationConfig: r.generationConfig,
	}
	// Sanitize invalid tool calls in history to avoid poisoning future requests.
	request.Messages = toolcall.SanitizeMessagesWithTools(request.Messages, request.Tools)
	applyInvocationRequestOverrides(request, invocation, nodeID)
	invocationID, sessionID, appName, userID, eventChan := extractExecutionContext(state)
	modelCallbacks, _ := state[StateKeyModelCallbacks].(*model.Callbacks)
	emittedModelStartEvent := false

	var streamWriter *agent.StreamWriter
	if r.streamOutputName != "" {
		w, err := agent.OpenStreamWriter(ctx, r.streamOutputName)
		if err != nil {
			return nil, err
		}
		streamWriter = w
	}

	var (
		modelInput               string
		modelName                string
		modelEventBaseInvocation *agent.Invocation
		modelEventInvocation     *agent.Invocation
		modelEventInvocationID   string
		startTime                time.Time
	)
	ctx, invocation, result, err := executeModelAndProcessResponsesWithContext(ctx, modelExecutionConfig{
		Invocation:     invocation,
		ModelCallbacks: modelCallbacks,
		LLMModel:       effectiveModel,
		Request:        request,
		EventChan:      eventChan,
		InvocationID:   invocationID,
		SessionID:      sessionID,
		AppName:        appName,
		UserID:         userID,
		Span:           span,
		NodeID:         nodeID,
		DeltaStream:    streamWriter,
		BeforeGenerate: func(modelCtx context.Context) {
			modelInvocation := invocationFromContextOrDefault(modelCtx, invocation)
			if shouldDisableModelExecutionEvents(modelInvocation) {
				return
			}
			modelEventBaseInvocation = invocation
			if modelEventBaseInvocation == nil {
				modelEventBaseInvocation = modelInvocation
			}
			modelEventInvocation = modelInvocation
			modelEventInvocationID = invocationID
			if modelEventInvocation != nil && modelEventInvocation.InvocationID != "" {
				modelEventInvocationID = modelEventInvocation.InvocationID
			}
			// Build model input metadata from the original state and instruction
			// so events accurately reflect both instruction and user input.
			modelInput = extractModelInput(state, instructionUsed, r.userInputKey)
			startTime = time.Now()
			modelName = getModelName(effectiveModel)
			emitModelStartEvent(
				modelCtx,
				modelEventBaseInvocation,
				modelEventInvocation,
				eventChan,
				modelEventInvocationID,
				modelName,
				nodeID,
				modelInput,
				startTime,
			)
			emittedModelStartEvent = true
		},
	})
	endTime := time.Now()

	if streamWriter != nil {
		if err != nil {
			_ = streamWriter.CloseWithError(err)
		} else {
			_ = streamWriter.Close()
		}
	}
	if emittedModelStartEvent {
		modelOutput := ""
		responseID := ""
		if err == nil {
			modelOutput, responseID = extractModelResponseSummary(result)
		}
		emitModelCompleteEvent(
			ctx,
			modelEventBaseInvocation,
			modelEventInvocation,
			eventChan,
			modelEventInvocationID,
			modelName,
			nodeID,
			modelInput,
			modelOutput,
			responseID,
			startTime,
			endTime,
			err,
		)
	}
	return result, err
}

// processInstruction resolves placeholder variables in the instruction.
// It supports the same syntax as LLMAgent, including {invocation:*} values
// stored on the current invocation.
func (r *llmRunner) processInstruction(state State) string {
	instr := r.instruction
	if invocation := graphInvocationFromState(state); invocation != nil {
		if patch, ok := graphSurfacePatch(invocation, r.currentNodeID(state)); ok {
			if patchedInstruction, ok := patch.Instruction(); ok {
				instr = patchedInstruction
			}
		}
	}
	if instr == "" {
		return instr
	}
	invocation := graphInvocationFromState(state)

	var sess *session.Session
	if sessVal, ok := state[StateKeySession]; ok {
		if s, ok := sessVal.(*session.Session); ok {
			sess = s
		}
	}

	if injected, err := promptstate.Render(
		instr,
		invocation,
		promptstate.WithSession(sess),
	); err == nil {
		return injected
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
	if execContext := executionContextFromState(state); execContext != nil {
		eventChan = execContext.EventChan
		invocationID = execContext.InvocationID
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

func executionContextFromState(state State) *ExecutionContext {
	if state == nil {
		return nil
	}
	execCtx, exists := state[StateKeyExecContext]
	if !exists {
		return nil
	}
	execContext, ok := execCtx.(*ExecutionContext)
	if !ok {
		return nil
	}
	return execContext
}

// modelResponseConfig contains configuration for processing model responses.
type modelResponseConfig struct {
	Response         *model.Response
	Invocation       *agent.Invocation
	StableInvocation *agent.Invocation
	Tracker          *itelemetry.ChatMetricsTracker
	PartialUsage     *partialUsageState
	ModelCallbacks   *model.Callbacks
	EventChan        chan<- *event.Event
	InvocationID     string
	SessionID        string
	LLMModel         model.Model
	Request          *model.Request
	Span             oteltrace.Span
	// NodeID, when provided, is used as the event author.
	NodeID string
}

func responseModelError(rsp *model.Response) error {
	if rsp == nil || rsp.Error == nil {
		return nil
	}
	return fmt.Errorf("%s: %s", rsp.Error.Type, rsp.Error.Message)
}

func invocationFromContextOrDefault(
	ctx context.Context,
	invocation *agent.Invocation,
) *agent.Invocation {
	if updatedInvocation, ok := agent.InvocationFromContext(ctx); ok &&
		updatedInvocation != nil {
		return updatedInvocation
	}
	return invocation
}

func shouldDisableModelExecutionEvents(invocation *agent.Invocation) bool {
	return invocation != nil && invocation.RunOptions.DisableModelExecutionEvents
}

func applyBeforeModelPluginCallbacks(
	ctx context.Context,
	invocation *agent.Invocation,
	request *model.Request,
	span oteltrace.Span,
) (context.Context, bool, *model.Response, error) {
	if invocation == nil || invocation.Plugins == nil {
		return ctx, false, nil, nil
	}
	callbacks := invocation.Plugins.ModelCallbacks()
	if callbacks == nil {
		return ctx, false, nil, nil
	}
	args := &model.BeforeModelArgs{Request: request}
	result, err := callbacks.RunBeforeModel(ctx, args)
	if err != nil {
		span.SetAttributes(
			attribute.String("trpc.go.agent.error", err.Error()),
		)
		return ctx, false, nil, fmt.Errorf(
			"callback before model error: %w",
			err,
		)
	}
	if result != nil && result.Context != nil {
		ctx = result.Context
	}
	if result != nil && result.CustomResponse != nil {
		return ctx, true, result.CustomResponse, nil
	}
	return ctx, false, nil, nil
}

func applyBeforeModelCallbacks(
	ctx context.Context,
	callbacks *model.Callbacks,
	request *model.Request,
	span oteltrace.Span,
) (context.Context, *model.Response, error) {
	if callbacks == nil {
		return ctx, nil, nil
	}
	args := &model.BeforeModelArgs{Request: request}
	result, err := callbacks.RunBeforeModel(ctx, args)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return ctx, nil, fmt.Errorf("callback before model error: %w", err)
	}
	if result != nil && result.Context != nil {
		ctx = result.Context
	}
	if result != nil && result.CustomResponse != nil {
		return ctx, result.CustomResponse, nil
	}
	return ctx, nil, nil
}

func singleResponseStream(response *model.Response) modelResponseStream {
	return modelResponseStream{
		Seq: func(yield func(*model.Response) bool) {
			yield(response)
		},
	}
}

func applyAfterModelPluginCallbacks(
	ctx context.Context,
	args *model.AfterModelArgs,
	span oteltrace.Span,
) (context.Context, bool, *model.Response, error) {
	invocation, ok := agent.InvocationFromContext(ctx)
	if !ok || invocation == nil || invocation.Plugins == nil {
		return ctx, false, nil, nil
	}

	callbacks := invocation.Plugins.ModelCallbacks()
	if callbacks == nil {
		return ctx, false, nil, nil
	}

	result, err := callbacks.RunAfterModel(ctx, args)
	if err != nil {
		span.SetAttributes(
			attribute.String("trpc.go.agent.error", err.Error()),
		)
		return ctx, false, nil, fmt.Errorf(
			"callback after model error: %w",
			err,
		)
	}

	if result != nil && result.Context != nil {
		ctx = result.Context
	}
	if result != nil && result.CustomResponse != nil {
		return ctx, true, result.CustomResponse, nil
	}
	return ctx, false, nil, nil
}

func applyAfterModelCallbacks(
	ctx context.Context,
	callbacks *model.Callbacks,
	args *model.AfterModelArgs,
	span oteltrace.Span,
) (context.Context, *model.Response, error) {
	if callbacks == nil {
		return ctx, nil, nil
	}

	result, err := callbacks.RunAfterModel(ctx, args)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		span.SetAttributes(
			attribute.String("trpc.go.agent.error", err.Error()),
		)
		return ctx, nil, fmt.Errorf(
			"callback after model error: %w",
			err,
		)
	}

	if result != nil && result.Context != nil {
		ctx = result.Context
	}
	if result != nil && result.CustomResponse != nil {
		return ctx, result.CustomResponse, nil
	}
	return ctx, nil, nil
}

func modelResponseAuthor(config modelResponseConfig) string {
	if config.NodeID != "" {
		return config.NodeID
	}
	return config.LLMModel.Info().Name
}

func applyPartialEventMetadataOverrides(
	ev *event.Event,
	resp *model.Response,
	invocation *agent.Invocation,
) {
	if ev == nil || resp == nil || !resp.IsPartial || invocation == nil {
		return
	}
	if invocation.RunOptions.DisablePartialEventIDs {
		ev.ID = ""
	}
	if invocation.RunOptions.DisablePartialEventTimestamps {
		ev.Timestamp = resp.Timestamp
	}
}

func emitModelResponseEvent(
	ctx context.Context,
	config modelResponseConfig,
	eventInvocation *agent.Invocation,
	optionsInvocation *agent.Invocation,
	ev *event.Event,
) error {
	if config.EventChan == nil ||
		!shouldEmitModelResponseEvent(config.Response, optionsInvocation) {
		return nil
	}
	if eventInvocation == nil {
		eventInvocation = agent.NewInvocation(
			agent.WithInvocationID(config.InvocationID),
			agent.WithInvocationModel(config.LLMModel),
			agent.WithInvocationSession(
				&session.Session{ID: config.SessionID},
			),
		)
	}
	return agent.EmitEvent(ctx, eventInvocation, config.EventChan, ev)
}

func shouldEmitModelResponseEvent(
	rsp *model.Response,
	invocation *agent.Invocation,
) bool {
	if rsp == nil {
		return false
	}
	if invocation != nil && invocation.RunOptions.GraphEmitFinalModelResponses {
		return shouldEmitModelResponse(rsp)
	}
	return !rsp.Done
}

// processModelResponse processes a single model response.
func processModelResponse(ctx context.Context, config modelResponseConfig) (context.Context, *event.Event, error) {
	args := &model.AfterModelArgs{
		Request:  config.Request,
		Response: config.Response,
		Error:    responseModelError(config.Response),
	}

	ctx, pluginOverride, customResponse, err := applyAfterModelPluginCallbacks(
		ctx,
		args,
		config.Span,
	)
	if err != nil {
		return ctx, nil, err
	}
	if pluginOverride {
		config.Response = customResponse
		args.Response = config.Response
	}

	if !pluginOverride {
		ctx, customResponse, err = applyAfterModelCallbacks(
			ctx,
			config.ModelCallbacks,
			args,
			config.Span,
		)
		if err != nil {
			return ctx, nil, err
		}
		if customResponse != nil {
			config.Response = customResponse
		}
	}
	currentInvocation := invocationFromContextOrDefault(ctx, config.Invocation)
	timingInfo := responseUsageTimingInfo(currentInvocation)
	if config.Tracker != nil {
		config.Tracker.SetInvocationState(currentInvocation, timingInfo)
	}
	attachResponseUsageTiming(config.Response, timingInfo, config.PartialUsage)
	eventInvocation := config.StableInvocation
	if eventInvocation == nil {
		eventInvocation = config.Invocation
	}
	if eventInvocation == nil {
		eventInvocation = currentInvocation
	}
	if currentInvocation != nil &&
		jsonrepair.IsToolCallArgumentsJSONRepairEnabled(currentInvocation) {
		jsonrepair.RepairResponseToolCallArgumentsInPlace(ctx, config.Response)
	}
	llmEvent := event.NewResponseEvent(
		config.InvocationID,
		modelResponseAuthor(config),
		config.Response,
	)
	applyPartialEventMetadataOverrides(llmEvent, config.Response, currentInvocation)
	agent.InjectIntoEvent(eventInvocation, llmEvent)

	if err := emitModelResponseEvent(
		ctx,
		config,
		eventInvocation,
		currentInvocation,
		llmEvent,
	); err != nil {
		return ctx, nil, err
	}

	if config.Response.Error != nil {
		config.Span.SetAttributes(
			attribute.String(
				"trpc.go.agent.error",
				config.Response.Error.Message,
			),
		)
		return ctx, nil, fmt.Errorf(
			"model API error: %s",
			config.Response.Error.Message,
		)
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

type modelResponseStream struct {
	// Ch is the response channel returned by model.Model.GenerateContent.
	Ch <-chan *model.Response
	// Seq is the iterator returned by model.IterModel.GenerateContentIter.
	Seq model.Seq[*model.Response]
}

func generateModelStream(
	ctx context.Context,
	llmModel model.Model,
	request *model.Request,
	span oteltrace.Span,
) (modelResponseStream, error) {
	// Generate content.
	if iterModel, ok := llmModel.(model.IterModel); ok {
		seq, err := iterModel.GenerateContentIter(ctx, request)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			return modelResponseStream{}, fmt.Errorf("failed to generate content: %w", err)
		}
		if seq == nil {
			err = errors.New(errMsgNoModelResponse)
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		return modelResponseStream{Seq: seq}, nil
	}
	responseChan, err := llmModel.GenerateContent(ctx, request)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return modelResponseStream{}, fmt.Errorf("failed to generate content: %w", err)
	}
	return modelResponseStream{Ch: responseChan}, nil
}

func runModelStream(
	ctx context.Context,
	invocation *agent.Invocation,
	modelCallbacks *model.Callbacks,
	llmModel model.Model,
	request *model.Request,
	beforeGenerate func(context.Context),
) (context.Context, modelResponseStream, error) {
	ctx, span, startedSpan := startNodeSpanForInvocation(ctx, invocation, "run_model")
	if startedSpan {
		defer span.End()
	}

	// Set span attributes for model execution.
	if span != nil && span.IsRecording() {
		span.SetAttributes(
			attribute.String("trpc.go.agent.model_name", llmModel.Info().Name),
		)
	}
	pluginOverride := false
	customResponse := (*model.Response)(nil)
	var err error
	ctx, pluginOverride, customResponse, err = applyBeforeModelPluginCallbacks(
		ctx,
		invocation,
		request,
		span,
	)
	if err != nil {
		if beforeGenerate != nil {
			beforeGenerate(ctx)
		}
		return ctx, modelResponseStream{}, err
	}
	if pluginOverride {
		if beforeGenerate != nil {
			beforeGenerate(ctx)
		}
		return ctx, singleResponseStream(customResponse), nil
	}
	ctx, customResponse, err = applyBeforeModelCallbacks(
		ctx,
		modelCallbacks,
		request,
		span,
	)
	if err != nil {
		if beforeGenerate != nil {
			beforeGenerate(ctx)
		}
		return ctx, modelResponseStream{}, err
	}
	if customResponse != nil {
		if beforeGenerate != nil {
			beforeGenerate(ctx)
		}
		return ctx, singleResponseStream(customResponse), nil
	}
	if beforeGenerate != nil {
		beforeGenerate(ctx)
	}
	stream, err := generateModelStream(ctx, llmModel, request, span)
	return ctx, stream, err
}

// runModel preserves the pre-refactor test-facing helper signature by
// adapting iterator-based model streams back to the legacy channel form.
func runModel(
	ctx context.Context,
	modelCallbacks *model.Callbacks,
	llmModel model.Model,
	request *model.Request,
) (context.Context, <-chan *model.Response, error) {
	invocation, _ := agent.InvocationFromContext(ctx)
	ctx, stream, err := runModelStream(
		ctx,
		invocation,
		modelCallbacks,
		llmModel,
		request,
		nil,
	)
	if err != nil {
		return ctx, nil, err
	}
	if stream.Ch != nil {
		return ctx, stream.Ch, nil
	}
	if stream.Seq == nil {
		return ctx, nil, errors.New(errMsgNoModelResponse)
	}

	responseChan := make(chan *model.Response, 1)
	go func() {
		defer close(responseChan)
		stream.Seq(func(response *model.Response) bool {
			select {
			case responseChan <- response:
				return true
			case <-ctx.Done():
				return false
			}
		})
	}()
	return ctx, responseChan, nil
}

// NewToolsNodeFunc creates a NodeFunc that uses the tools package directly.
// This implements tools node functionality using the tools package interface.
func NewToolsNodeFunc(tools map[string]tool.Tool, opts ...Option) NodeFunc {
	nodeFunc, _ := newToolsNodeRuntime(tools, opts...)
	return nodeFunc
}

func newToolsNodeRuntime(
	tools map[string]tool.Tool,
	opts ...Option,
) (NodeFunc, map[string]tool.Tool) {
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
	// Capture tool callbacks configured on the node.
	configuredCallbacks := node.toolCallbacks

	return func(ctx context.Context, state State) (any, error) {
		ctx, span, startedSpan := startNodeSpan(ctx, itelemetry.NewWorkflowSpanName("execute_tools_node"))
		var workflow *itelemetry.Workflow
		if startedSpan {
			workflow = &itelemetry.Workflow{
				Name:    "execute_tools_node",
				ID:      "execute_tools_node",
				Type:    workflowTypeFromNodeType(node.Type),
				Request: state.safeClone(),
			}
			defer func() {
				itelemetry.TraceWorkflow(span, workflow)
				span.End()
			}()
		}

		// Extract and validate messages from state.
		toolCalls, err := extractToolCallsFromState(state, span)
		if err != nil {
			if workflow != nil {
				workflow.Error = err
			}
			return nil, err
		}

		// Extract execution context for event emission.
		invocationID, _, _, _, eventChan := extractExecutionContext(state)
		effectiveTools := resolveToolsNodeRuntimeTools(ctx, state, node, baseTools, staticTools)

		// Determine which callbacks to use: node-configured takes precedence over state.
		toolCallbacks := configuredCallbacks
		if toolCallbacks == nil {
			toolCallbacks, _ = extractToolCallbacks(state)
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
			ToolCallbacks:  toolCallbacks,
		})
		if err != nil {
			if workflow != nil {
				workflow.Error = err
			}
			return nil, err
		}
		upd := State{StateKeyMessages: newMessages}

		if len(newMessages) > 0 {
			upd[StateKeyLastToolResponse] =
				newMessages[len(newMessages)-1].Content
		}

		nodeID, _ := GetStateValue[string](state, StateKeyCurrentNodeID)
		if nodeID != "" {
			type toolNodeResponse struct {
				ToolID   string          `json:"tool_id"`
				ToolName string          `json:"tool_name"`
				Output   json.RawMessage `json:"output"`
			}

			responses := make([]toolNodeResponse, 0, len(newMessages))
			for _, msg := range newMessages {
				responses = append(responses, toolNodeResponse{
					ToolID:   msg.ToolID,
					ToolName: msg.ToolName,
					Output:   json.RawMessage(msg.Content),
				})
			}

			b, _ := json.Marshal(responses)
			upd[StateKeyNodeResponses] = map[string]any{
				nodeID: string(b),
			}
		}

		if workflow != nil {
			workflow.Response = upd
		}
		return upd, nil
	}, cloneToolsMap(staticTools)
}

func resolveToolsNodeRuntimeTools(
	ctx context.Context,
	state State,
	node *Node,
	baseTools map[string]tool.Tool,
	staticTools map[string]tool.Tool,
) map[string]tool.Tool {
	effectiveTools := staticTools
	if node.refreshToolSetsOnRun && len(node.toolSets) > 0 {
		effectiveTools = mergeToolsWithToolSets(
			ctx,
			baseTools,
			node.toolSets,
		)
	}
	invocation, ok := agent.InvocationFromContext(ctx)
	if !ok {
		return effectiveTools
	}
	localNodeID := node.ID
	if currentNodeID, ok := GetStateValue[string](state, StateKeyCurrentNodeID); ok && currentNodeID != "" {
		localNodeID = currentNodeID
	}
	if patch, ok := graphSurfacePatch(invocation, localNodeID); ok {
		if patchedTools, ok := patch.Tools(); ok {
			return toolSliceToMap(patchedTools)
		}
	}
	return effectiveTools
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

type executorProvider interface {
	Executor() *Executor
}

type subgraphInterruptInfo struct {
	parentNodeID      string
	childAgentName    string
	childCheckpointID string
	childCheckpointNS string
	childLineageID    string
	childTaskID       string
}

func subgraphInterruptInfoFromState(
	state State,
) (subgraphInterruptInfo, bool) {
	if state == nil {
		return subgraphInterruptInfo{}, false
	}
	raw, ok := state[StateKeySubgraphInterrupt]
	if !ok || raw == nil {
		return subgraphInterruptInfo{}, false
	}
	typed, ok := raw.(map[string]any)
	if !ok {
		return subgraphInterruptInfo{}, false
	}
	info := subgraphInterruptInfo{}
	if v, ok := typed[subgraphInterruptKeyParentNodeID].(string); ok {
		info.parentNodeID = v
	}
	if v, ok := typed[subgraphInterruptKeyChildAgentName].(string); ok {
		info.childAgentName = v
	}
	if v, ok := typed[subgraphInterruptKeyChildCheckpointID].(string); ok {
		info.childCheckpointID = v
	}
	if v, ok := typed[subgraphInterruptKeyChildCheckpointNS].(string); ok {
		info.childCheckpointNS = v
	}
	if v, ok := typed[subgraphInterruptKeyChildLineageID].(string); ok {
		info.childLineageID = v
	}
	if v, ok := typed[subgraphInterruptKeyChildTaskID].(string); ok {
		info.childTaskID = v
	}
	return info, true
}

func extractPregelInterrupt(e *event.Event) (*InterruptError, bool) {
	if e == nil {
		return nil, false
	}
	if e.Object != ObjectTypeGraphPregelStep {
		return nil, false
	}
	if e.StateDelta == nil {
		return nil, false
	}
	raw, ok := e.StateDelta[MetadataKeyPregel]
	if !ok || len(raw) == 0 {
		return nil, false
	}
	var meta PregelStepMetadata
	if err := json.Unmarshal(raw, &meta); err != nil {
		return nil, false
	}
	if meta.NodeID == "" || meta.InterruptValue == nil {
		return nil, false
	}
	intr := NewInterruptError(meta.InterruptValue)
	intr.NodeID = meta.NodeID
	interruptKey := meta.InterruptKey
	if interruptKey == "" {
		interruptKey = meta.NodeID
	}
	intr.Key = interruptKey
	intr.TaskID = interruptKey
	return intr, true
}

func latestInterruptedCheckpointID(
	ctx context.Context,
	targetAgent agent.Agent,
	lineageID string,
	namespace string,
) (string, error) {
	provider, ok := targetAgent.(executorProvider)
	if !ok || provider.Executor() == nil {
		return "", nil
	}
	cm := provider.Executor().CheckpointManager()
	if cm == nil {
		return "", nil
	}
	tuple, err := cm.Latest(ctx, lineageID, namespace)
	if err != nil || tuple == nil || tuple.Checkpoint == nil {
		return "", err
	}
	if !tuple.Checkpoint.IsInterrupted() {
		return "", nil
	}
	return tuple.Checkpoint.ID, nil
}

func resumeCommandForSubgraph(
	state State,
	childTaskID string,
) *Command {
	if state == nil {
		return nil
	}
	cmd := &Command{}
	hasResume := false
	if v, ok := state[ResumeChannel]; ok {
		cmd.Resume = v
		hasResume = true
	}
	if resumeMap, ok := state[StateKeyResumeMap].(map[string]any); ok {
		if childTaskID != "" {
			if v, ok := resumeMap[childTaskID]; ok {
				cmd.ResumeMap = map[string]any{childTaskID: v}
				hasResume = true
			}
		} else if len(resumeMap) > 0 {
			cloned := deepCopyAny(resumeMap).(map[string]any)
			cmd.ResumeMap = cloned
			hasResume = true
		}
	}
	if !hasResume {
		return nil
	}
	return cmd
}

const includeContentsNone = "none"

type agentNodeConfig struct {
	callbacks           *NodeCallbacks
	inputMapper         SubgraphInputMapper
	outputMapper        SubgraphOutputMapper
	isolated            bool
	scope               string
	inputFromLast       bool
	llmGenerationConfig *model.GenerationConfig
	userInputKey        string
	streamOutputName    string
}

func agentNodeConfigFromOptions(opts ...Option) agentNodeConfig {
	dummyNode := &Node{}
	for _, opt := range opts {
		opt(dummyNode)
	}
	return agentNodeConfig{
		callbacks:           dummyNode.callbacks,
		inputMapper:         dummyNode.agentInputMapper,
		outputMapper:        dummyNode.agentOutputMapper,
		isolated:            dummyNode.agentIsolatedMessages,
		scope:               dummyNode.agentEventScope,
		inputFromLast:       dummyNode.agentInputFromLastResponse,
		llmGenerationConfig: dummyNode.llmGenerationConfig,
		userInputKey:        dummyNode.userInputKey,
		streamOutputName:    dummyNode.streamOutputName,
	}
}

func targetAgentFromState(state State, agentName string) (agent.Agent, error) {
	parentAgent, parentExists := state[StateKeyParentAgent]
	if !parentExists {
		return nil, fmt.Errorf(
			"parent agent not found in state for agent node %s",
			agentName,
		)
	}
	targetAgent := findSubAgentByName(parentAgent, agentName)
	if targetAgent == nil {
		return nil, fmt.Errorf(
			"sub-agent '%s' not found in parent agent's sub-agent list",
			agentName,
		)
	}
	return targetAgent, nil
}

func initialChildStateForAgentNode(parent State, inputMapper SubgraphInputMapper) State {
	if inputMapper == nil {
		return copyRuntimeStateFiltered(parent)
	}
	if s := inputMapper(parent); s != nil {
		return s
	}
	return State{}
}

func applyDefaultCheckpointNamespace(child State, targetAgent agent.Agent) {
	if _, ok := targetAgent.(executorProvider); !ok {
		return
	}
	child[CfgKeyCheckpointNS] = targetAgent.Info().Name
	delete(child, CfgKeyCheckpointID)
}

func applyIsolatedMessages(child State, isolated bool) {
	if !isolated {
		return
	}
	child[CfgKeyIncludeContents] = includeContentsNone
}

func applyCheckpointResumeFields(child State, info subgraphInterruptInfo) {
	if info.childCheckpointID != "" {
		child[CfgKeyCheckpointID] = info.childCheckpointID
	} else {
		delete(child, CfgKeyCheckpointID)
	}
	if info.childCheckpointNS != "" {
		child[CfgKeyCheckpointNS] = info.childCheckpointNS
	}
	if info.childLineageID != "" {
		child[CfgKeyLineageID] = info.childLineageID
	}
}

func clearResumeChannelsIfNeeded(parent State, child State, cmd *Command) {
	if cmd == nil || cmd.Resume == nil {
		return
	}
	delete(parent, ResumeChannel)
	delete(child, ResumeChannel)
}

func consumeResumeMapEntryIfNeeded(parent State, childTaskID string, cmd *Command) {
	if cmd == nil || cmd.ResumeMap == nil || childTaskID == "" {
		return
	}
	resumeMap, ok := parent[StateKeyResumeMap].(map[string]any)
	if !ok {
		return
	}
	delete(resumeMap, childTaskID)
	if len(resumeMap) == 0 {
		delete(parent, StateKeyResumeMap)
	}
}

func applyResumeCommandForAgentNode(
	parent State,
	child State,
	info subgraphInterruptInfo,
) {
	cmd := resumeCommandForSubgraph(parent, info.childTaskID)
	if cmd == nil {
		return
	}
	child[StateKeyCommand] = cmd
	clearResumeChannelsIfNeeded(parent, child, cmd)
	consumeResumeMapEntryIfNeeded(parent, info.childTaskID, cmd)
	delete(child, StateKeyResumeMap)
}

func applySubgraphResumeForAgentNode(parent State, child State, nodeID string) {
	info, ok := subgraphInterruptInfoFromState(parent)
	if !ok || info.parentNodeID != nodeID {
		return
	}
	applyCheckpointResumeFields(child, info)
	applyResumeCommandForAgentNode(parent, child, info)
	delete(parent, StateKeySubgraphInterrupt)
}

func buildChildStateForAgentNode(
	parent State,
	nodeID string,
	targetAgent agent.Agent,
	cfg agentNodeConfig,
) State {
	childState := initialChildStateForAgentNode(parent, cfg.inputMapper)
	delete(childState, StateKeySubgraphInterrupt)
	if cfg.inputMapper == nil {
		applyDefaultCheckpointNamespace(childState, targetAgent)
	}
	applyIsolatedMessages(childState, cfg.isolated)
	applySubgraphResumeForAgentNode(parent, childState, nodeID)
	return childState
}

func mapParentInputFromLastResponse(
	state State,
	enabled bool,
	userInputKey string,
) State {
	if !enabled {
		return state
	}
	lastResponse, ok := GetStateValue[string](state, StateKeyLastResponse)
	if !ok || lastResponse == "" {
		return state
	}
	cloned := state.Clone()
	if userInputKey == "" {
		userInputKey = StateKeyUserInput
	}
	cloned[userInputKey] = lastResponse
	return cloned
}

func setSubgraphInterruptState(
	ctx context.Context,
	state State,
	nodeID string,
	agentName string,
	targetAgent agent.Agent,
	childState State,
	invocation *agent.Invocation,
	childTaskID string,
) {
	fallbackLineageID := ""
	if invocation != nil {
		fallbackLineageID = invocation.InvocationID
	}
	childLineageID := stateStringOr(childState, CfgKeyLineageID, fallbackLineageID)
	childNamespace := stateStringOr(childState, CfgKeyCheckpointNS, targetAgent.Info().Name)
	childCheckpointID, ckptErr := latestInterruptedCheckpointID(
		ctx, targetAgent, childLineageID, childNamespace,
	)
	if ckptErr != nil {
		log.DebugfContext(ctx, "subgraph: latest checkpoint failed: %v", ckptErr)
	}
	state[StateKeySubgraphInterrupt] = map[string]any{
		subgraphInterruptKeyParentNodeID:      nodeID,
		subgraphInterruptKeyChildAgentName:    agentName,
		subgraphInterruptKeyChildCheckpointID: childCheckpointID,
		subgraphInterruptKeyChildCheckpointNS: childNamespace,
		subgraphInterruptKeyChildLineageID:    childLineageID,
		subgraphInterruptKeyChildTaskID:       childTaskID,
	}
}

func finalizeAgentNodeOutput(
	state State,
	nodeID string,
	streamRes agentEventStreamResult,
	outputMapper SubgraphOutputMapper,
	userInputKey string,
) any {
	if userInputKey == "" {
		userInputKey = StateKeyUserInput
	}
	if execCtx := executionContextFromState(state); execCtx != nil {
		execCtx.setCompletionIdentity(
			streamRes.lastResponse,
			streamRes.lastResponseID,
		)
	}
	if outputMapper != nil {
		mapped := outputMapper(state, SubgraphResult{
			LastResponse:       streamRes.lastResponse,
			FinalState:         streamRes.finalState,
			RawStateDelta:      streamRes.rawDelta,
			FallbackState:      streamRes.fallbackState,
			FallbackStateDelta: streamRes.fallbackRawDelta,
			StructuredOutput:   streamRes.structuredOutput,
		})
		if len(mapped) == 0 {
			return State{}
		}
		if _, ok := mapped[userInputKey]; !ok {
			copied := mapped.Clone()
			copied[userInputKey] = ""
			return copied
		}
		return mapped
	}
	upd := State{}
	upd[StateKeyLastResponse] = streamRes.lastResponse
	upd[StateKeyNodeResponses] = map[string]any{
		nodeID: streamRes.lastResponse,
	}
	upd[userInputKey] = ""
	return upd
}

// NewAgentNodeFunc creates a NodeFunc that looks up and uses a sub-agent by name.
// The agent name should correspond to a sub-agent in the parent GraphAgent's sub-agent list.
func NewAgentNodeFunc(agentName string, opts ...Option) NodeFunc {
	cfg := agentNodeConfigFromOptions(opts...)
	return func(ctx context.Context, state State) (any, error) {
		// Extract execution context for event emission.
		invocationID, _, _, _, eventChan := extractExecutionContext(state)

		// Extract current node ID from state.
		nodeID, _ := GetStateValue[string](state, StateKeyCurrentNodeID)

		targetAgent, err := targetAgentFromState(state, agentName)
		if err != nil {
			return nil, err
		}

		childState := buildChildStateForAgentNode(state, nodeID, targetAgent, cfg)

		// Optionally map parent's last_response to user_input for this agent node.
		parentForInput := mapParentInputFromLastResponse(
			state,
			cfg.inputFromLast,
			cfg.userInputKey,
		)

		// Build invocation for the target agent with custom runtime state and scope.
		invocation := buildAgentInvocationWithStateScopeAndInputKey(
			ctx,
			parentForInput,
			childState,
			targetAgent,
			nodeID,
			cfg.scope,
			cfg.userInputKey,
		)

		// Emit agent execution start event.
		startTime := time.Now()
		emitAgentStartEvent(ctx, eventChan, invocationID, nodeID, startTime)

		// Execute the target agent.
		// Important: wrap the context with the sub-invocation so downstream
		// callbacks (model/tool) can access it via agent.InvocationFromContext(ctx).
		subCtx := WithGraphCompletionCapture(
			agent.NewInvocationContext(ctx, invocation),
		)
		agentEventChan, err := targetAgent.Run(subCtx, invocation)
		if err != nil {
			// Emit agent execution error event.
			endTime := time.Now()
			emitAgentErrorEvent(ctx, eventChan, invocationID, nodeID, startTime, endTime, err)
			return nil, fmt.Errorf("failed to run agent %s: %w", agentName, err)
		}

		// Process agent event stream and capture completion state.
		agentCallbacks := mergeAgentEventCallbacks(state, cfg.callbacks)
		streamRes, err := processAgentEventStream(
			ctx,
			invocation,
			agentEventChan,
			agentCallbacks,
			nodeID,
			state,
			eventChan,
			agentName,
			cfg.streamOutputName,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to process agent event stream: %w", err)
		}

		if streamRes.interrupt != nil {
			setSubgraphInterruptState(
				ctx,
				state,
				nodeID,
				agentName,
				targetAgent,
				childState,
				invocation,
				streamRes.interrupt.TaskID,
			)
			intr := NewInterruptError(streamRes.interrupt.Value)
			intr.TaskID = streamRes.interrupt.TaskID
			return nil, intr
		}

		// Emit agent execution complete event.
		endTime := time.Now()
		emitAgentCompleteEvent(
			ctx,
			eventChan,
			invocationID,
			nodeID,
			startTime,
			endTime,
		)
		return finalizeAgentNodeOutput(
			state,
			nodeID,
			streamRes,
			cfg.outputMapper,
			cfg.userInputKey,
		), nil
	}
}

func stateStringOr(state State, key string, fallback string) string {
	if state == nil {
		return fallback
	}
	if v, ok := state[key].(string); ok && v != "" {
		return v
	}
	return fallback
}

type agentEventStreamResult struct {
	lastResponse     string
	lastResponseID   string
	finalState       State
	rawDelta         map[string][]byte
	fallbackState    State
	fallbackRawDelta map[string][]byte
	structuredOutput any
	interrupt        *InterruptError
	terminalErr      error
	terminalErrMeta  agentTerminalErrorMeta
	finalError       *model.ResponseError
}

type agentTerminalErrorMeta struct {
	InvocationID string
	FilterKey    string
}

type agentDeltaStreamTap struct {
	writer       *agent.StreamWriter
	lastResponse *string

	sawDelta bool
	broken   bool
}

func newAgentDeltaStreamTap(
	ctx context.Context,
	streamName string,
	lastResponse *string,
) (*agentDeltaStreamTap, error) {
	tap := &agentDeltaStreamTap{
		lastResponse: lastResponse,
	}
	if streamName == "" {
		return tap, nil
	}
	w, err := agent.OpenStreamWriter(ctx, streamName)
	if err != nil {
		return nil, err
	}
	tap.writer = w
	return tap, nil
}

func (t *agentDeltaStreamTap) WriteDelta(ev *event.Event) {
	if t == nil || t.writer == nil || t.broken {
		return
	}
	delta := agentDeltaFromEvent(ev)
	if delta == "" {
		return
	}
	t.sawDelta = true
	if _, err := t.writer.WriteString(delta); err != nil {
		t.broken = true
	}
}

func (t *agentDeltaStreamTap) Close(errp *error) {
	if t == nil || t.writer == nil {
		return
	}
	if t.broken {
		_ = t.writer.CloseWithError(io.ErrClosedPipe)
		return
	}
	if errp != nil && *errp != nil {
		_ = t.writer.CloseWithError(*errp)
		return
	}
	if !t.sawDelta && t.lastResponse != nil && *t.lastResponse != "" {
		_, _ = t.writer.WriteString(*t.lastResponse)
	}
	_ = t.writer.Close()
}

func agentDeltaFromEvent(ev *event.Event) string {
	if ev == nil || ev.Response == nil || len(ev.Response.Choices) == 0 {
		return ""
	}
	return ev.Response.Choices[0].Delta.Content
}

func runAgentEventCallbacks(
	ctx context.Context,
	nodeCallbacks *NodeCallbacks,
	nodeID string,
	agentName string,
	state State,
	ev *event.Event,
) {
	if nodeCallbacks == nil {
		return
	}
	nodeCallbacks.RunAgentEvent(ctx, &NodeCallbackContext{
		NodeID:   nodeID,
		NodeName: agentName,
		NodeType: NodeTypeAgent,
	}, state, ev)
}

func updateAgentStreamResultFromEvent(
	ctx context.Context,
	res *agentEventStreamResult,
	ev *event.Event,
	invalidateSuccessResult bool,
	trackTerminalErrors bool,
) {
	captureAgentFallbackState(res, ev)
	updateAgentLastResponse(res, ev)
	updateAgentStructuredOutput(res, ev)
	updateAgentInterrupt(res, ev)
	updateAgentFinalState(ctx, res, ev)
	if trackTerminalErrors || (invalidateSuccessResult && shouldInvalidateAgentSuccessResult(ev)) {
		clearAgentSuccessResultOnError(res, ev)
	}
	if !trackTerminalErrors {
		return
	}
	clearAgentTerminalErrorOnContinuedOutput(res, ev)
	updateAgentTerminalError(res, ev)
}

func updateAgentLastResponse(res *agentEventStreamResult, ev *event.Event) {
	if res == nil {
		return
	}
	updateAgentLastResponseValue(&res.lastResponse, &res.lastResponseID, ev)
}

func updateAgentLastResponseValue(lastResponse *string, lastResponseID *string, ev *event.Event) {
	if lastResponse == nil || lastResponseID == nil || ev == nil || ev.Response == nil {
		return
	}
	if len(ev.Response.Choices) == 0 {
		return
	}
	msg := ev.Response.Choices[0].Message
	if msg.Role != model.RoleAssistant || msg.Content == "" {
		return
	}
	if ev.Response.ID != "" {
		*lastResponseID = ev.Response.ID
	}
	*lastResponse = msg.Content
}

func updateAgentStructuredOutput(res *agentEventStreamResult, ev *event.Event) {
	if res == nil || ev == nil {
		return
	}
	if ev.StructuredOutput == nil {
		return
	}
	res.structuredOutput = ev.StructuredOutput
}

func updateAgentInterrupt(res *agentEventStreamResult, ev *event.Event) {
	if res == nil || res.interrupt != nil {
		return
	}
	intr, ok := extractPregelInterrupt(ev)
	if !ok {
		return
	}
	res.interrupt = intr
}

func clearAgentSuccessResultOnError(res *agentEventStreamResult, ev *event.Event) {
	if res == nil || ev == nil || ev.Response == nil || ev.Response.Error == nil {
		return
	}
	res.lastResponse = ""
	res.finalState = nil
	res.rawDelta = nil
	res.structuredOutput = nil
}

func shouldInvalidateAgentSuccessResult(ev *event.Event) bool {
	if ev == nil {
		return false
	}
	if ev.Error != nil && ev.Error.Type == agent.ErrorTypeAgentCallbackError {
		return true
	}
	return ev.Response != nil &&
		ev.Response.Error != nil &&
		ev.Response.Error.Type == agent.ErrorTypeAgentCallbackError
}

func clearAgentTerminalErrorOnContinuedOutput(
	res *agentEventStreamResult,
	ev *event.Event,
) {
	if res == nil ||
		res.terminalErr == nil ||
		!matchesAgentTerminalErrorSource(res.terminalErrMeta, ev) ||
		!isAgentRecoveryEvent(ev) {
		return
	}
	res.terminalErr = nil
	res.terminalErrMeta = agentTerminalErrorMeta{}
}

func isAgentRecoveryEvent(ev *event.Event) bool {
	if isTerminalAgentSuccessEvent(ev) ||
		isGraphCompletionEvent(ev) ||
		IsVisibleGraphCompletionEvent(ev) {
		return true
	}
	if ev == nil || ev.Response == nil || ev.Response.Error != nil {
		return false
	}
	if agentDeltaFromEvent(ev) != "" {
		return true
	}
	if ev.StructuredOutput != nil {
		return true
	}
	for _, choice := range ev.Response.Choices {
		if choice.Message.Role == model.RoleAssistant &&
			choice.Message.Content != "" {
			return true
		}
	}
	return false
}

func matchesAgentTerminalErrorSource(
	meta agentTerminalErrorMeta,
	ev *event.Event,
) bool {
	if ev == nil {
		return false
	}
	if meta.InvocationID != "" && ev.InvocationID != "" {
		return ev.InvocationID == meta.InvocationID
	}
	if meta.FilterKey == "" {
		return true
	}
	if ev.FilterKey == "" {
		return true
	}
	return ev.FilterKey == meta.FilterKey
}

func internalAgentEventWithInvocationFields(
	invocation *agent.Invocation,
	ev *event.Event,
) *event.Event {
	if invocation == nil || ev == nil {
		return ev
	}
	if ev.RequestID != "" &&
		ev.InvocationID != "" &&
		ev.Branch != "" &&
		ev.FilterKey != "" &&
		(ev.ParentInvocationID != "" || invocation.GetParentInvocation() == nil) {
		return ev
	}
	internalEvent := *ev
	if internalEvent.RequestID == "" {
		internalEvent.RequestID = invocation.RunOptions.RequestID
	}
	if internalEvent.ParentInvocationID == "" && invocation.GetParentInvocation() != nil {
		internalEvent.ParentInvocationID = invocation.GetParentInvocation().InvocationID
	}
	if internalEvent.InvocationID == "" {
		internalEvent.InvocationID = invocation.InvocationID
	}
	if internalEvent.Branch == "" {
		internalEvent.Branch = invocation.Branch
	}
	if internalEvent.FilterKey == "" {
		internalEvent.FilterKey = invocation.GetEventFilterKey()
	}
	return &internalEvent
}

func updateAgentTerminalError(res *agentEventStreamResult, ev *event.Event) {
	if res == nil || res.terminalErr != nil || !isTerminalAgentErrorEvent(ev) {
		return
	}
	if ev.Error != nil && ev.Error.Type == agent.ErrorTypeStopAgentError {
		res.terminalErr = agent.NewStopError(ev.Error.Message)
		res.terminalErrMeta = agentTerminalErrorMeta{
			InvocationID: ev.InvocationID,
			FilterKey:    ev.FilterKey,
		}
		return
	}
	if ev.Error != nil {
		res.terminalErr = errors.New(ev.Error.Message)
		res.terminalErrMeta = agentTerminalErrorMeta{
			InvocationID: ev.InvocationID,
			FilterKey:    ev.FilterKey,
		}
	}
}

func isTerminalAgentErrorEvent(ev *event.Event) bool {
	if ev == nil ||
		ev.Response == nil ||
		ev.Response.Error == nil {
		return false
	}
	if ev.Object == ObjectTypeGraphPregelStep {
		var metadata PregelStepMetadata
		if err := json.Unmarshal(ev.StateDelta[MetadataKeyPregel], &metadata); err != nil {
			return true
		}
		return metadata.StepNumber < 0
	}
	if ev.Object != model.ObjectTypeError {
		return false
	}
	if len(ev.StateDelta) == 0 {
		return true
	}
	for _, key := range []string{
		MetadataKeyNode,
		MetadataKeyPregel,
		MetadataKeyTool,
		MetadataKeyModel,
		MetadataKeyChannel,
		MetadataKeyState,
		MetadataKeyCheckpoint,
		MetadataKeyCacheHit,
		MetadataKeyNodeCustom,
	} {
		if _, ok := ev.StateDelta[key]; ok {
			return false
		}
	}
	return true
}

func isTerminalAgentSuccessEvent(ev *event.Event) bool {
	if ev == nil || ev.Response == nil {
		return false
	}
	if ev.Response.Error != nil || !ev.Response.Done {
		return false
	}
	return true
}

func updateAgentFinalState(
	ctx context.Context,
	res *agentEventStreamResult,
	ev *event.Event,
) {
	if res == nil {
		return
	}
	finalState, rawDelta, ok := extractSubgraphFinalState(ctx, ev)
	if !ok {
		return
	}
	res.finalState = finalState
	res.rawDelta = rawDelta
}

func extractSubgraphFinalState(
	ctx context.Context,
	ev *event.Event,
) (State, map[string][]byte, bool) {
	if ev == nil || !ev.Done || ev.Response == nil || ev.StateDelta == nil {
		return nil, nil, false
	}
	if !isGraphCompletionEvent(ev) && !IsVisibleGraphCompletionEvent(ev) {
		return nil, nil, false
	}
	return decodeSubgraphStateDelta(ctx, ev.StateDelta)
}

func decodeSubgraphStateDelta(
	ctx context.Context,
	stateDelta map[string][]byte,
) (State, map[string][]byte, bool) {
	if len(stateDelta) == 0 {
		return nil, nil, false
	}
	tmp := make(State)
	for k, b := range stateDelta {
		var v any
		err := json.Unmarshal(b, &v)
		if err == nil {
			tmp[k] = v
			continue
		}
		// Some transports normalize JSON string scalars into plain strings,
		// which means the raw bytes are no longer valid JSON here. Preserve
		// the raw string instead of silently dropping the key.
		tmp[k] = string(b)
		log.DebugfContext(
			ctx,
			"subgraph: failed to unmarshal final state key=%s: %v",
			k,
			err,
		)
	}
	return tmp, stateDelta, true
}

// processAgentEventStream processes the event stream from the target agent.
// This function handles forwarding events and capturing completion state.
func processAgentEventStream(
	ctx context.Context,
	invocation *agent.Invocation,
	agentEventChan <-chan *event.Event,
	nodeCallbacks *NodeCallbacks,
	nodeID string,
	state State,
	eventChan chan<- *event.Event,
	agentName string,
	streamName string,
) (res agentEventStreamResult, err error) {
	parentInvocation, _ := agent.InvocationFromContext(ctx)
	suppressGraphCompletion := parentInvocation != nil &&
		agent.IsGraphCompletionEventDisabled(parentInvocation)
	invalidateSuccessResult := suppressGraphCompletion
	propagateChildAgentErrors := agent.ShouldPropagateChildAgentErrors(
		invocation,
	)
	streamLastResponse := ""
	streamLastResponseID := ""
	tap, err := newAgentDeltaStreamTap(ctx, streamName, &streamLastResponse)
	if err != nil {
		return res, err
	}
	defer tap.Close(&err)

	for agentEvent := range agentEventChan {
		internalAgentEvent := internalAgentEventWithInvocationFields(
			invocation,
			agentEvent,
		)
		runAgentEventCallbacks(
			ctx,
			nodeCallbacks,
			nodeID,
			agentName,
			state,
			agentEvent,
		)
		if suppressGraphCompletion && isGraphCompletionEvent(agentEvent) {
			tap.WriteDelta(agentEvent)
			updateAgentStreamResultFromEvent(
				ctx,
				&res,
				internalAgentEvent,
				invalidateSuccessResult,
				propagateChildAgentErrors,
			)
			updateAgentLastResponseValue(&streamLastResponse, &streamLastResponseID, agentEvent)
			continue
		}
		// Forward the event to the parent event channel.
		if err := event.EmitEvent(ctx, eventChan, agentEvent); err != nil {
			return res, err
		}
		tap.WriteDelta(agentEvent)
		updateAgentStreamResultFromEvent(
			ctx,
			&res,
			internalAgentEvent,
			invalidateSuccessResult,
			propagateChildAgentErrors,
		)
		updateAgentLastResponseValue(&streamLastResponse, &streamLastResponseID, agentEvent)
	}
	if propagateChildAgentErrors && res.terminalErr != nil {
		return res, res.terminalErr
	}

	if len(res.rawDelta) == 0 &&
		len(res.fallbackRawDelta) > 0 &&
		shouldPropagateAgentFallbackState(res.finalError) {
		finalState, rawDelta, ok := decodeSubgraphStateDelta(
			ctx,
			res.fallbackRawDelta,
		)
		if ok {
			res.fallbackState = finalState
			res.fallbackRawDelta = rawDelta
		}
	}
	if res.lastResponseID == "" {
		res.lastResponseID = stateStringOr(
			res.finalState,
			StateKeyLastResponseID,
			streamLastResponseID,
		)
	}
	if res.lastResponseID == "" {
		res.lastResponseID = stateStringOr(
			res.fallbackState,
			StateKeyLastResponseID,
			streamLastResponseID,
		)
	}

	return res, nil
}

func mergeAgentEventCallbacks(
	state State,
	perNode *NodeCallbacks,
) *NodeCallbacks {
	var global *NodeCallbacks
	if value, ok := state[StateKeyNodeCallbacks].(*NodeCallbacks); ok {
		global = value
	}
	if global == nil && perNode == nil {
		return nil
	}
	merged := NewNodeCallbacks()
	if global != nil {
		merged.AgentEvent = append(merged.AgentEvent, global.AgentEvent...)
	}
	if perNode != nil {
		merged.AgentEvent = append(merged.AgentEvent, perNode.AgentEvent...)
	}
	if len(merged.AgentEvent) == 0 {
		return nil
	}
	return merged
}

func captureAgentFallbackState(
	res *agentEventStreamResult,
	ev *event.Event,
) {
	if res == nil || ev == nil {
		return
	}
	if len(ev.StateDelta) > 0 {
		res.fallbackRawDelta = mergeAgentFallbackStateDelta(
			res.fallbackRawDelta,
			ev.StateDelta,
		)
	}
	if ev.Response == nil || ev.IsPartial {
		return
	}
	res.finalError = cloneResponseError(ev.Response.Error)
}

func mergeAgentFallbackStateDelta(
	dst map[string][]byte,
	src map[string][]byte,
) map[string][]byte {
	if len(src) == 0 {
		return dst
	}
	filtered := make(map[string][]byte, len(src))
	for key, value := range src {
		if !shouldPropagateAgentFallbackStateKey(key) {
			continue
		}
		filtered[key] = value
	}
	return mergeStateDeltaMaps(dst, filtered)
}

func shouldPropagateAgentFallbackStateKey(
	key string,
) bool {
	switch key {
	case MetadataKeyNode,
		MetadataKeyPregel,
		MetadataKeyChannel,
		MetadataKeyState,
		MetadataKeyCompletion,
		MetadataKeyTool,
		MetadataKeyModel,
		MetadataKeyCheckpoint,
		MetadataKeyCacheHit,
		MetadataKeyNodeCustom:
		return false
	default:
		return true
	}
}

func shouldPropagateAgentFallbackState(
	err *model.ResponseError,
) bool {
	if err == nil {
		return false
	}
	return err.Type != agent.ErrorTypeStopAgentError
}

// buildAgentInvocationWithStateScopeAndInputKey builds an invocation for the
// target agent
// using a custom runtime state and an optional event filter scope segment.
//
// FilterKey Strategy:
// The FilterKey is built using a stable, deterministic pattern based on the agent
// hierarchy rather than random UUIDs. This ensures that:
//  1. Multi-turn conversations work correctly with BranchFilterModePrefix
//  2. Child agent responses from previous requests are visible in subsequent requests
//  3. The prefix matching in event.Filter() works as expected across requests
//
// Format: parentKey/agentName (without UUID)
// Example: "input/knowledge" instead of "input/knowledge/random-uuid"
func buildAgentInvocationWithStateScopeAndInputKey(
	ctx context.Context,
	parentState State,
	runtime State,
	targetAgent agent.Agent,
	nodeID string,
	scope string,
	userInputKey string,
) *agent.Invocation {
	// Extract user input from parent state.
	var userInput string
	if userInputKey == "" {
		userInputKey = StateKeyUserInput
	}
	if input, exists := parentState[userInputKey]; exists {
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
		runOptions := parentInvocation.RunOptions
		// Preserve the parent's visibility preference.
		// The agent node captures completion snapshots from either raw
		// graph.execution events or visible rewritten completion snapshots.
		runOptions.RuntimeState = runtime
		runOptions.CustomAgentConfigs = withScopedGraphCallOptions(
			runOptions.CustomAgentConfigs,
			nodeID,
		)
		base := util.If(scope != "", scope, targetAgent.Info().Name)
		parentKey := parentInvocation.GetEventFilterKey()
		// Build a stable FilterKey without UUID to ensure multi-turn conversations
		var filterKey string
		if parentKey == "" {
			filterKey = base
		} else {
			filterKey = parentKey + agent.EventFilterKeyDelimiter + base
		}
		invocationOpts := []agent.InvocationOptions{
			agent.WithInvocationAgent(targetAgent),
			agent.WithInvocationMessage(
				model.NewUserMessage(userInput),
			),
			agent.WithInvocationRunOptions(runOptions),
			agent.WithInvocationEventFilterKey(filterKey),
		}
		if traceNodeID := buildAgentNodeTraceNodeID(parentInvocation, nodeID); traceNodeID != "" {
			invocationOpts = append(invocationOpts, agent.WithInvocationTraceNodeID(traceNodeID))
		}
		if surfaceRootNodeID := buildAgentNodeSurfaceRoot(parentInvocation, nodeID, targetAgent); surfaceRootNodeID != "" {
			invocationOpts = append(invocationOpts, func(inv *agent.Invocation) {
				agent.SetInvocationSurfaceRootNodeID(inv, surfaceRootNodeID)
			})
		}
		entryPredecessors := currentTraceStepPredecessors(parentState)
		if len(entryPredecessors) == 0 {
			entryPredecessors = agent.NextExecutionTracePredecessors(parentInvocation)
		}
		if len(entryPredecessors) > 0 {
			invocationOpts = append(invocationOpts, agent.WithInvocationEntryPredecessorStepIDs(entryPredecessors))
		}
		inv := parentInvocation.Clone(invocationOpts...)
		return inv
	}
	// Create standalone invocation.
	inv := agent.NewInvocation(
		agent.WithInvocationAgent(targetAgent),
		agent.WithInvocationRunOptions(agent.RunOptions{RuntimeState: runtime}),
		agent.WithInvocationMessage(model.NewUserMessage(userInput)),
		agent.WithInvocationSession(sessionData),
		// Use stable FilterKey based on agent name only (no UUID).
		agent.WithInvocationEventFilterKey(targetAgent.Info().Name),
	)
	return inv
}

func currentTraceStepPredecessors(state State) []string {
	if state == nil {
		return nil
	}
	if stepID, ok := GetStateValue[string](state, currentTraceStepIDStateKey); ok && stepID != "" {
		return []string{stepID}
	}
	return nil
}

func buildAgentInvocationWithStateAndScope(
	ctx context.Context,
	parentState State,
	runtime State,
	targetAgent agent.Agent,
	nodeID string,
	scope string,
) *agent.Invocation {
	return buildAgentInvocationWithStateScopeAndInputKey(
		ctx,
		parentState,
		runtime,
		targetAgent,
		nodeID,
		scope,
		StateKeyUserInput,
	)
}

func buildAgentNodeTraceNodeID(
	parentInvocation *agent.Invocation,
	nodeID string,
) string {
	if parentInvocation == nil || nodeID == "" {
		return ""
	}
	return istructure.JoinNodeID(
		agent.InvocationTraceNodeID(parentInvocation),
		nodeID,
	)
}

func buildAgentNodeSurfaceRoot(
	parentInvocation *agent.Invocation,
	nodeID string,
	targetAgent agent.Agent,
) string {
	if parentInvocation == nil {
		return ""
	}
	baseNodeID := agent.InvocationSurfaceRootNodeID(parentInvocation)
	if nodeID != "" {
		baseNodeID = istructure.JoinNodeID(baseNodeID, nodeID)
	}
	if targetAgent == nil || targetAgent.Info().Name == "" {
		return baseNodeID
	}
	return istructure.JoinNodeID(baseNodeID, targetAgent.Info().Name)
}

const (
	errCallbackBeforeTool = "callback before tool error: %w"
	errCallbackAfterTool  = "callback after tool error: %w"
)

func runBeforeToolPluginCallbacks(
	ctx context.Context,
	toolCall model.ToolCall,
	decl *tool.Declaration,
	state State,
) (context.Context, model.ToolCall, any, error) {
	invocation, ok := agent.InvocationFromContext(ctx)
	if !ok || invocation == nil || invocation.Plugins == nil {
		return ctx, toolCall, nil, nil
	}

	callbacks := invocation.Plugins.ToolCallbacks()
	if callbacks == nil {
		return ctx, toolCall, nil, nil
	}
	resumeValue, _ := GetStateValue[any](state, ResumeChannel)
	resumeMap, _ := GetStateValue[map[string]any](state, StateKeyResumeMap)

	args := &tool.BeforeToolArgs{
		ToolCallID:  toolCall.ID,
		ToolName:    toolCall.Function.Name,
		Declaration: decl,
		Arguments:   toolCall.Function.Arguments,
		ResumeValue: resumeValue,
		ResumeMap:   resumeMap,
	}
	result, err := callbacks.RunBeforeTool(ctx, args)
	if result != nil && result.Context != nil {
		ctx = result.Context
	}
	if result != nil && result.ModifiedArguments != nil {
		toolCall.Function.Arguments = result.ModifiedArguments
	}
	if result != nil && result.CustomResult != nil {
		if err != nil {
			return ctx, toolCall, result.CustomResult,
				fmt.Errorf(errCallbackBeforeTool, err)
		}
		return ctx, toolCall, result.CustomResult, nil
	}
	if err != nil {
		return ctx, toolCall, nil,
			fmt.Errorf(errCallbackBeforeTool, err)
	}
	return ctx, toolCall, nil, nil
}

func runBeforeToolCallbacks(
	ctx context.Context,
	toolCall model.ToolCall,
	decl *tool.Declaration,
	toolCallbacks *tool.Callbacks,
	state State,
) (context.Context, model.ToolCall, any, error) {
	if toolCallbacks == nil {
		return ctx, toolCall, nil, nil
	}
	resumeValue, _ := GetStateValue[any](state, ResumeChannel)
	resumeMap, _ := GetStateValue[map[string]any](state, StateKeyResumeMap)

	args := &tool.BeforeToolArgs{
		ToolCallID:  toolCall.ID,
		ToolName:    toolCall.Function.Name,
		Declaration: decl,
		Arguments:   toolCall.Function.Arguments,
		ResumeValue: resumeValue,
		ResumeMap:   resumeMap,
	}
	result, err := toolCallbacks.RunBeforeTool(ctx, args)
	if result != nil && result.Context != nil {
		ctx = result.Context
	}
	if result != nil && result.ModifiedArguments != nil {
		toolCall.Function.Arguments = result.ModifiedArguments
	}
	if result != nil && result.CustomResult != nil {
		if err != nil {
			return ctx, toolCall, result.CustomResult,
				fmt.Errorf(errCallbackBeforeTool, err)
		}
		return ctx, toolCall, result.CustomResult, nil
	}

	if err != nil {
		return ctx, toolCall, nil,
			fmt.Errorf(errCallbackBeforeTool, err)
	}
	return ctx, toolCall, nil, nil
}

func ensureCallableTool(
	t tool.Tool,
	toolName string,
) (tool.CallableTool, error) {
	callableTool, ok := t.(tool.CallableTool)
	if !ok {
		return nil, fmt.Errorf("tool %s is not callable", toolName)
	}
	return callableTool, nil
}

func runAfterToolPluginCallbacks(
	ctx context.Context,
	toolCall model.ToolCall,
	decl *tool.Declaration,
	result any,
	runErr error,
) (context.Context, any, error) {
	invocation, ok := agent.InvocationFromContext(ctx)
	if !ok || invocation == nil || invocation.Plugins == nil {
		return ctx, nil, nil
	}

	callbacks := invocation.Plugins.ToolCallbacks()
	if callbacks == nil {
		return ctx, nil, nil
	}

	args := &tool.AfterToolArgs{
		ToolCallID:  toolCall.ID,
		ToolName:    toolCall.Function.Name,
		Declaration: decl,
		Arguments:   toolCall.Function.Arguments,
		Result:      result,
		Error:       runErr,
		Meta:        extractMetaFromResult(result),
	}
	afterResult, err := callbacks.RunAfterTool(ctx, args)
	if afterResult != nil && afterResult.Context != nil {
		ctx = afterResult.Context
	}
	if afterResult != nil && afterResult.CustomResult != nil {
		if err != nil {
			return ctx, afterResult.CustomResult,
				fmt.Errorf(errCallbackAfterTool, err)
		}
		return ctx, afterResult.CustomResult, nil
	}
	if err != nil {
		return ctx, nil, fmt.Errorf(errCallbackAfterTool, err)
	}
	return ctx, nil, nil
}

func runAfterToolCallbacks(
	ctx context.Context,
	toolCall model.ToolCall,
	decl *tool.Declaration,
	result any,
	runErr error,
	toolCallbacks *tool.Callbacks,
) (context.Context, any, error) {
	if toolCallbacks == nil {
		return ctx, nil, nil
	}

	args := &tool.AfterToolArgs{
		ToolCallID:  toolCall.ID,
		ToolName:    toolCall.Function.Name,
		Declaration: decl,
		Arguments:   toolCall.Function.Arguments,
		Result:      result,
		Error:       runErr,
		Meta:        extractMetaFromResult(result),
	}
	afterResult, err := toolCallbacks.RunAfterTool(ctx, args)
	if afterResult != nil && afterResult.Context != nil {
		ctx = afterResult.Context
	}
	if afterResult != nil && afterResult.CustomResult != nil {
		if err != nil {
			return ctx, afterResult.CustomResult,
				fmt.Errorf(errCallbackAfterTool, err)
		}
		return ctx, afterResult.CustomResult, nil
	}
	if err != nil {
		return ctx, nil, fmt.Errorf(errCallbackAfterTool, err)
	}
	return ctx, nil, nil
}

// extractMetaFromResult extracts metadata from tool result when available.
func extractMetaFromResult(result any) map[string]any {
	if result == nil {
		return nil
	}
	type metaGetter interface {
		GetMeta() map[string]any
	}
	if mg, ok := result.(metaGetter); ok {
		return mg.GetMeta()
	}
	return nil
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
	state State,
) (context.Context, any, []byte, error) {
	_, _, finalCtx, _, result, modifiedArgs, err := runToolWithEventContexts(ctx, toolCall, toolCallbacks, t, state)
	return finalCtx, result, modifiedArgs, err
}

func runToolWithEventContexts(
	ctx context.Context,
	toolCall model.ToolCall,
	toolCallbacks *tool.Callbacks,
	t tool.Tool,
	state State,
) (context.Context, *agent.Invocation, context.Context, *agent.Invocation, any, []byte, error) {
	ctx = context.WithValue(ctx, tool.ContextKeyToolCallID{}, toolCall.ID)
	if invocation, ok := agent.InvocationFromContext(ctx); ok && jsonrepair.IsToolCallArgumentsJSONRepairEnabled(invocation) {
		jsonrepair.RepairToolCallArgumentsInPlace(ctx, &toolCall)
	}
	decl := t.Declaration()
	startInvocation := invocationFromContextOrFallback(ctx, nil)

	ctx, toolCall, customResult, err := runBeforeToolPluginCallbacks(
		ctx,
		toolCall,
		decl,
		state,
	)
	startInvocation = invocationFromContextOrFallback(ctx, startInvocation)
	if err != nil {
		return ctx, startInvocation, ctx, startInvocation, customResult, toolCall.Function.Arguments, err
	}
	if customResult != nil {
		return ctx, startInvocation, ctx, startInvocation, customResult, toolCall.Function.Arguments, nil
	}

	ctx, toolCall, customResult, err = runBeforeToolCallbacks(
		ctx,
		toolCall,
		decl,
		toolCallbacks,
		state,
	)
	startInvocation = invocationFromContextOrFallback(ctx, startInvocation)
	if err != nil {
		return ctx, startInvocation, ctx, startInvocation, customResult, toolCall.Function.Arguments, err
	}
	if customResult != nil {
		return ctx, startInvocation, ctx, startInvocation, customResult, toolCall.Function.Arguments, nil
	}
	startCtx := ctx

	callableTool, err := ensureCallableTool(t, toolCall.Function.Name)
	if err != nil {
		return startCtx, startInvocation, ctx, startInvocation, nil, toolCall.Function.Arguments, err
	}

	result, toolErr := callableTool.Call(ctx, toolCall.Function.Arguments)
	completeInvocation := startInvocation

	ctx, customResult, err = runAfterToolPluginCallbacks(
		ctx,
		toolCall,
		decl,
		result,
		toolErr,
	)
	completeInvocation = invocationFromContextOrFallback(ctx, completeInvocation)
	if err != nil {
		if customResult != nil {
			return startCtx, startInvocation, ctx, completeInvocation, customResult, toolCall.Function.Arguments, err
		}
		var interruptErr *InterruptError
		if errors.As(err, &interruptErr) {
			return startCtx, startInvocation, ctx, completeInvocation, result, toolCall.Function.Arguments, err
		}
		return startCtx, startInvocation, ctx, completeInvocation, nil, toolCall.Function.Arguments, err
	}
	if customResult != nil {
		return startCtx, startInvocation, ctx, completeInvocation, customResult, toolCall.Function.Arguments, nil
	}

	ctx, customResult, err = runAfterToolCallbacks(
		ctx,
		toolCall,
		decl,
		result,
		toolErr,
		toolCallbacks,
	)
	completeInvocation = invocationFromContextOrFallback(ctx, completeInvocation)
	if err != nil {
		if customResult != nil {
			return startCtx, startInvocation, ctx, completeInvocation, customResult, toolCall.Function.Arguments, err
		}
		var interruptErr *InterruptError
		if errors.As(err, &interruptErr) {
			return startCtx, startInvocation, ctx, completeInvocation, result, toolCall.Function.Arguments, err
		}
		return startCtx, startInvocation, ctx, completeInvocation, nil, toolCall.Function.Arguments, err
	}
	if customResult != nil {
		return startCtx, startInvocation, ctx, completeInvocation, customResult, toolCall.Function.Arguments, nil
	}

	if toolErr != nil {
		var interruptErr *InterruptError
		if errors.As(toolErr, &interruptErr) {
			return startCtx, startInvocation, ctx, completeInvocation, result, toolCall.Function.Arguments, toolErr
		}
		return startCtx, startInvocation, ctx, completeInvocation, nil, toolCall.Function.Arguments,
			fmt.Errorf("tool %s call failed: %w", toolCall.Function.Name, toolErr)
	}
	return startCtx, startInvocation, ctx, completeInvocation, result, toolCall.Function.Arguments, nil
}

// extractModelInput extracts the model input from state and instruction.
func extractModelInput(state State, instruction, userInputKey string) string {
	var input string
	// Get user input if available.
	if userInputKey == "" {
		userInputKey = StateKeyUserInput
	}
	if userInput, exists := state[userInputKey]; exists {
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
	baseInvocation *agent.Invocation,
	currentInvocation *agent.Invocation,
	eventChan chan<- *event.Event,
	invocationID, modelName, nodeID, modelInput string,
	startTime time.Time,
) {
	if eventChan == nil {
		return
	}
	invocation, _ := agent.InvocationFromContext(ctx)
	if invocation != nil && agent.IsGraphExecutorEventsDisabled(invocation) {
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
	emitInvocationScopedEvent(
		ctx,
		baseInvocation,
		currentInvocation,
		eventChan,
		invocationID,
		modelStartEvent,
	)
}

// emitModelCompleteEvent emits a model execution complete event.
func emitModelCompleteEvent(
	ctx context.Context,
	baseInvocation *agent.Invocation,
	currentInvocation *agent.Invocation,
	eventChan chan<- *event.Event,
	invocationID, modelName, nodeID, modelInput, modelOutput, responseID string,
	startTime, endTime time.Time,
	err error,
) {
	if eventChan == nil {
		return
	}
	invocation, _ := agent.InvocationFromContext(ctx)
	if invocation != nil && agent.IsGraphExecutorEventsDisabled(invocation) {
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
	emitInvocationScopedEvent(
		ctx,
		baseInvocation,
		currentInvocation,
		eventChan,
		invocationID,
		modelCompleteEvent,
	)
}

func emitInvocationScopedEvent(
	ctx context.Context,
	baseInvocation *agent.Invocation,
	currentInvocation *agent.Invocation,
	eventChan chan<- *event.Event,
	invocationID string,
	ev *event.Event,
) {
	if ev == nil || eventChan == nil {
		return
	}
	if requestID := modelExecutionEventRequestID(baseInvocation, currentInvocation); requestID != "" {
		ev.RequestID = requestID
	}
	if parentInvocationID := modelExecutionEventParentInvocationID(baseInvocation, currentInvocation); parentInvocationID != "" {
		ev.ParentInvocationID = parentInvocationID
	}
	if branch := modelExecutionEventBranch(baseInvocation, currentInvocation); branch != "" {
		ev.Branch = branch
	}
	if filterKey := modelExecutionEventFilterKey(baseInvocation, currentInvocation); filterKey != "" {
		ev.FilterKey = filterKey
	}
	if invocationID != "" {
		ev.InvocationID = invocationID
	}
	_ = event.EmitEvent(ctx, eventChan, ev)
}

func modelExecutionEventRequestID(baseInvocation, currentInvocation *agent.Invocation) string {
	if currentInvocation != nil && currentInvocation.RunOptions.RequestID != "" {
		return currentInvocation.RunOptions.RequestID
	}
	if baseInvocation != nil {
		return baseInvocation.RunOptions.RequestID
	}
	return ""
}

func modelExecutionEventParentInvocationID(baseInvocation, currentInvocation *agent.Invocation) string {
	if currentInvocation != nil {
		if parent := currentInvocation.GetParentInvocation(); parent != nil {
			return parent.InvocationID
		}
		if currentInvocation.Branch != "" ||
			currentInvocation.GetEventFilterKey() != "" {
			return ""
		}
	}
	if baseInvocation != nil {
		if parent := baseInvocation.GetParentInvocation(); parent != nil {
			return parent.InvocationID
		}
	}
	return ""
}

func modelExecutionEventBranch(baseInvocation, currentInvocation *agent.Invocation) string {
	if currentInvocation != nil && currentInvocation.Branch != "" {
		return currentInvocation.Branch
	}
	if baseInvocation != nil {
		return baseInvocation.Branch
	}
	return ""
}

func modelExecutionEventFilterKey(baseInvocation, currentInvocation *agent.Invocation) string {
	if currentInvocation != nil {
		if filterKey := currentInvocation.GetEventFilterKey(); filterKey != "" {
			return filterKey
		}
	}
	if baseInvocation != nil {
		return baseInvocation.GetEventFilterKey()
	}
	return ""
}

// modelExecutionConfig contains configuration for model execution with events.
type modelExecutionConfig struct {
	Invocation     *agent.Invocation
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
	DeltaStream    *agent.StreamWriter
	BeforeGenerate func(context.Context)
	Span           oteltrace.Span
}

const (
	errMsgNoModelResponse = "no response received from model"
	errMsgNoModelChoices  = "model returned no choices"
)

type modelDeltaStreamTap struct {
	writer *agent.StreamWriter

	sawDelta bool
	broken   bool
}

func newModelDeltaStreamTap(w *agent.StreamWriter) *modelDeltaStreamTap {
	return &modelDeltaStreamTap{
		writer: w,
	}
}

func (t *modelDeltaStreamTap) WriteDelta(resp *model.Response) {
	if t == nil || t.writer == nil || t.broken {
		return
	}
	delta := modelDeltaFromResponse(resp)
	if delta == "" {
		return
	}
	t.sawDelta = true
	if _, err := t.writer.WriteString(delta); err != nil {
		t.broken = true
	}
}

func (t *modelDeltaStreamTap) WriteFinalIfNoDelta(final *model.Response) {
	if t == nil || t.writer == nil || t.broken || t.sawDelta {
		return
	}
	msg := modelMessageFromResponse(final)
	if msg == "" {
		return
	}
	_, _ = t.writer.WriteString(msg)
}

func modelDeltaFromResponse(resp *model.Response) string {
	if resp == nil || len(resp.Choices) == 0 {
		return ""
	}
	return resp.Choices[0].Delta.Content
}

func modelMessageFromResponse(resp *model.Response) string {
	if resp == nil || len(resp.Choices) == 0 {
		return ""
	}
	return resp.Choices[0].Message.Content
}

func validateFinalModelResponse(
	span oteltrace.Span,
	resp *model.Response,
) (*model.Response, error) {
	if resp == nil {
		span.SetAttributes(attribute.String(
			"trpc.go.agent.error",
			errMsgNoModelResponse,
		))
		return nil, errors.New(errMsgNoModelResponse)
	}
	if len(resp.Choices) == 0 {
		span.SetAttributes(attribute.String(
			"trpc.go.agent.error",
			errMsgNoModelChoices,
		))
		return nil, errors.New(errMsgNoModelChoices)
	}
	return resp, nil
}

func collectToolCallsFromResponse(
	toolCalls []model.ToolCall,
	resp *model.Response,
) []model.ToolCall {
	if resp == nil || len(resp.Choices) == 0 {
		return toolCalls
	}
	calls := resp.Choices[0].Message.ToolCalls
	if len(calls) == 0 {
		return toolCalls
	}
	return append(toolCalls, calls...)
}

func mergeToolCallsIntoFinalResponse(
	resp *model.Response,
	toolCalls []model.ToolCall,
) {
	if resp == nil || len(resp.Choices) == 0 {
		return
	}
	if len(resp.Choices[0].Message.ToolCalls) >= len(toolCalls) {
		return
	}
	resp.Choices[0].Message.ToolCalls = toolCalls
}

func hasAfterModelCallbacks(
	invocation *agent.Invocation,
	modelCallbacks *model.Callbacks,
) bool {
	if hasRegisteredAfterModelCallbacks(modelCallbacks) {
		return true
	}
	if invocation == nil || invocation.Plugins == nil {
		return false
	}
	return hasRegisteredAfterModelCallbacks(
		invocation.Plugins.ModelCallbacks(),
	)
}

func hasRegisteredAfterModelCallbacks(callbacks *model.Callbacks) bool {
	return callbacks != nil && len(callbacks.AfterModel) > 0
}

func nextReusableModelEvent(
	reusableEvents []event.Event,
	reusableEventIdx *int,
) *event.Event {
	if *reusableEventIdx >= len(reusableEvents) {
		return nil
	}
	ev := &reusableEvents[*reusableEventIdx]
	*reusableEventIdx++
	return ev
}

func trackModelResponseTelemetry(
	response *model.Response,
	tracker *itelemetry.ChatMetricsTracker,
) {
	if tracker == nil || response == nil {
		return
	}
	tracker.TrackResponse(response)
}

type partialUsageState struct {
	usage      *model.Usage
	timingInfo *model.TimingInfo
}

func attachResponseUsageTiming(
	response *model.Response,
	timingInfo *model.TimingInfo,
	partialUsageState *partialUsageState,
) {
	if response == nil || timingInfo == nil {
		return
	}
	if response.Usage == nil {
		if response.IsPartial {
			if partialUsageState == nil {
				response.Usage = &model.Usage{}
			} else {
				if partialUsageState.usage == nil ||
					partialUsageState.timingInfo != timingInfo {
					partialUsageState.usage = &model.Usage{}
					partialUsageState.timingInfo = timingInfo
				}
				response.Usage = partialUsageState.usage
			}
		} else {
			response.Usage = &model.Usage{}
		}
	}
	response.Usage.TimingInfo = timingInfo
}

func responseUsageTimingInfo(invocation *agent.Invocation) *model.TimingInfo {
	if invocation == nil || invocation.RunOptions.DisableResponseUsageTracking {
		return nil
	}
	return invocation.GetOrCreateTimingInfo()
}

func emitFastModelResponseEvent(
	ctx context.Context,
	eventInvocation *agent.Invocation,
	config modelExecutionConfig,
	response *model.Response,
	author string,
	partialEventIDsDisabled bool,
	partialEventTimestampsDisabled bool,
	reusableEvent *event.Event,
) (*event.Event, error) {
	llmEvent := reusableEvent
	if llmEvent == nil {
		llmEvent = &event.Event{}
	}

	shouldEmit := !response.Done
	eventID := ""
	if !response.IsPartial || !partialEventIDsDisabled {
		eventID = uuid.NewString()
	}
	eventTimestamp := time.Now()
	if response.IsPartial && partialEventTimestampsDisabled {
		eventTimestamp = response.Timestamp
	}

	llmEvent.Response = response
	llmEvent.ID = eventID
	llmEvent.Timestamp = eventTimestamp
	llmEvent.InvocationID = config.InvocationID
	llmEvent.Author = author
	llmEvent.Version = event.CurrentVersion

	if shouldEmit {
		if err := agent.EmitEvent(ctx, eventInvocation, config.EventChan, llmEvent); err != nil {
			return nil, err
		}
	}
	if response.Error != nil {
		config.Span.SetAttributes(attribute.String("trpc.go.agent.error", response.Error.Message))
		return llmEvent, fmt.Errorf("model API error: %s", response.Error.Message)
	}
	return llmEvent, nil
}

func traceProcessedModelResponse(
	span oteltrace.Span,
	tracker *itelemetry.ChatMetricsTracker,
	invocation *agent.Invocation,
	request *model.Request,
	response *model.Response,
	lastEvent *event.Event,
) {
	if lastEvent == nil {
		return
	}
	if tracker != nil {
		tracker.SetLastEvent(lastEvent)
	}
	if invocation != nil && invocation.RunOptions.DisableTracing {
		return
	}
	var ttfb time.Duration
	if tracker != nil {
		ttfb = tracker.FirstTokenTimeDuration()
	}
	itelemetry.TraceChat(span, &itelemetry.TraceChatAttributes{
		Invocation:       invocation,
		Request:          request,
		Response:         response,
		EventID:          lastEvent.ID,
		TimeToFirstToken: ttfb,
	})
}

type modelResponseProcessor struct {
	ctx                            context.Context
	config                         modelExecutionConfig
	stableInvocation               *agent.Invocation
	observabilityInvocation        *agent.Invocation
	invocation                     *agent.Invocation
	tracker                        *itelemetry.ChatMetricsTracker
	timingInfo                     *model.TimingInfo
	partialUsageState              partialUsageState
	tap                            *modelDeltaStreamTap
	reusableEvents                 []event.Event
	reusableEventIdx               int
	author                         string
	fastResponsePath               bool
	partialEventIDsDisabled        bool
	partialEventTimestampsDisabled bool
	lastEvent                      *event.Event
	finalResponse                  *model.Response
	toolCalls                      []model.ToolCall
}

func newModelResponseProcessor(
	ctx context.Context,
	config modelExecutionConfig,
	invocation *agent.Invocation,
	runErr *error,
) *modelResponseProcessor {
	p := &modelResponseProcessor{
		ctx:              ctx,
		config:           config,
		stableInvocation: config.Invocation,
		invocation:       invocation,
		tap:              newModelDeltaStreamTap(config.DeltaStream),
	}
	if p.stableInvocation == nil {
		p.stableInvocation = invocation
	}
	p.observabilityInvocation = observabilityInvocationView(p.stableInvocation, config)
	if p.observabilityInvocation != nil {
		p.timingInfo = responseUsageTimingInfo(invocation)
		p.tracker = itelemetry.NewChatMetricsTracker(
			ctx,
			p.observabilityInvocation,
			config.Request,
			p.timingInfo,
			nil,
			runErr,
		)
	}

	p.author = config.NodeID
	if p.author == "" && config.LLMModel != nil {
		p.author = config.LLMModel.Info().Name
	}

	needAfterCallbacks := hasAfterModelCallbacks(invocation, config.ModelCallbacks)
	jsonRepairEnabled := invocation != nil &&
		jsonrepair.IsToolCallArgumentsJSONRepairEnabled(invocation)
	p.partialEventIDsDisabled = invocation != nil && invocation.RunOptions.DisablePartialEventIDs
	p.partialEventTimestampsDisabled = invocation != nil &&
		invocation.RunOptions.DisablePartialEventTimestamps
	emitFinalModelResponses := invocation != nil && invocation.RunOptions.GraphEmitFinalModelResponses
	p.fastResponsePath = config.EventChan != nil &&
		!needAfterCallbacks &&
		!jsonRepairEnabled &&
		invocation != nil &&
		!emitFinalModelResponses

	return p
}

func observabilityInvocationView(
	invocation *agent.Invocation,
	config modelExecutionConfig,
) *agent.Invocation {
	if invocation == nil &&
		config.InvocationID == "" &&
		config.SessionID == "" &&
		config.UserID == "" &&
		config.AppName == "" &&
		config.LLMModel == nil {
		return nil
	}
	var (
		invocationID string
		agentName    string
		sessionID    string
		userID       string
		appName      string
		sessionView  *session.Session
	)
	if invocation != nil {
		invocationID = invocation.InvocationID
		agentName = invocation.AgentName
		if invocation.Session != nil {
			sessionID = invocation.Session.ID
			userID = invocation.Session.UserID
			appName = invocation.Session.AppName
		}
	}
	if invocationID == "" {
		invocationID = config.InvocationID
	}
	if sessionID == "" {
		sessionID = config.SessionID
	}
	if userID == "" {
		userID = config.UserID
	}
	if appName == "" {
		appName = config.AppName
	}
	if sessionID != "" || userID != "" || appName != "" {
		sessionView = &session.Session{
			ID:      sessionID,
			UserID:  userID,
			AppName: appName,
		}
	}
	modelValue := config.LLMModel
	if modelValue == nil && invocation != nil {
		modelValue = invocation.Model
	}
	return &agent.Invocation{
		InvocationID: invocationID,
		AgentName:    agentName,
		Model:        modelValue,
		Session:      sessionView,
	}
}

func hasObservabilityModelValue(
	invocation *agent.Invocation,
	config modelExecutionConfig,
) bool {
	return config.LLMModel != nil || (invocation != nil && invocation.Model != nil)
}

func hasObservabilityInvocationIDValue(
	invocation *agent.Invocation,
	config modelExecutionConfig,
) bool {
	return config.InvocationID != "" || (invocation != nil && invocation.InvocationID != "")
}

func hasObservabilitySessionIDValue(
	invocation *agent.Invocation,
	config modelExecutionConfig,
) bool {
	return config.SessionID != "" ||
		(invocation != nil && invocation.Session != nil && invocation.Session.ID != "")
}

func hasObservabilityUserIDValue(
	invocation *agent.Invocation,
	config modelExecutionConfig,
) bool {
	return config.UserID != "" ||
		(invocation != nil && invocation.Session != nil && invocation.Session.UserID != "")
}

func hasObservabilityAppNameValue(
	invocation *agent.Invocation,
	config modelExecutionConfig,
) bool {
	return config.AppName != "" ||
		(invocation != nil && invocation.Session != nil && invocation.Session.AppName != "")
}

func shouldRefreshObservabilitySessionView(
	currentSession *session.Session,
	invocation *agent.Invocation,
	config modelExecutionConfig,
) bool {
	if currentSession == nil {
		return hasObservabilitySessionIDValue(invocation, config) ||
			hasObservabilityUserIDValue(invocation, config) ||
			hasObservabilityAppNameValue(invocation, config)
	}
	if currentSession.ID == "" && hasObservabilitySessionIDValue(invocation, config) {
		return true
	}
	if currentSession.UserID == "" && hasObservabilityUserIDValue(invocation, config) {
		return true
	}
	if currentSession.AppName == "" && hasObservabilityAppNameValue(invocation, config) {
		return true
	}
	return false
}

func shouldRefreshObservabilityInvocationView(
	currentView *agent.Invocation,
	invocation *agent.Invocation,
	config modelExecutionConfig,
) bool {
	if currentView.InvocationID == "" &&
		hasObservabilityInvocationIDValue(invocation, config) {
		return true
	}
	if currentView.AgentName == "" && invocation != nil && invocation.AgentName != "" {
		return true
	}
	if currentView.Model == nil && hasObservabilityModelValue(invocation, config) {
		return true
	}
	return shouldRefreshObservabilitySessionView(
		currentView.Session,
		invocation,
		config,
	)
}

func refreshObservabilityInvocationView(
	currentView *agent.Invocation,
	invocation *agent.Invocation,
	config modelExecutionConfig,
) *agent.Invocation {
	if currentView == nil {
		return observabilityInvocationView(invocation, config)
	}
	if shouldRefreshObservabilityInvocationView(currentView, invocation, config) {
		return observabilityInvocationView(invocation, config)
	}
	return currentView
}

func (p *modelResponseProcessor) close() {
	if p == nil || p.tracker == nil {
		return
	}
	p.tracker.RecordMetrics()()
}

func (p *modelResponseProcessor) consume(stream modelResponseStream) error {
	if stream.Ch != nil && cap(stream.Ch) > 0 {
		p.reusableEvents = make([]event.Event, cap(stream.Ch))
	}
	if stream.Ch != nil {
		for response := range stream.Ch {
			ok, err := p.handleResponse(response)
			if err != nil {
				return err
			}
			if !ok {
				return nil
			}
		}
		return nil
	}
	if stream.Seq == nil {
		return nil
	}

	var streamErr error
	stream.Seq(func(response *model.Response) bool {
		ok, err := p.handleResponse(response)
		if err != nil {
			streamErr = err
			return false
		}
		return ok
	})
	return streamErr
}

func (p *modelResponseProcessor) handleResponse(response *model.Response) (bool, error) {
	if response == nil {
		return true, nil
	}
	p.invocation = invocationFromContextOrDefault(p.ctx, p.invocation)
	p.timingInfo = responseUsageTimingInfo(p.invocation)
	if p.tracker != nil {
		p.tracker.SetInvocationState(p.invocation, p.timingInfo)
	}
	p.tap.WriteDelta(response)
	trackModelResponseTelemetry(response, p.tracker)
	p.toolCalls = collectToolCallsFromResponse(p.toolCalls, response)
	p.finalResponse = response
	reusableEvent := nextReusableModelEvent(p.reusableEvents, &p.reusableEventIdx)
	if p.fastResponsePath {
		attachResponseUsageTiming(response, p.timingInfo, &p.partialUsageState)
		lastEvent, err := emitFastModelResponseEvent(
			p.ctx,
			p.stableInvocation,
			p.config,
			response,
			p.author,
			p.partialEventIDsDisabled,
			p.partialEventTimestampsDisabled,
			reusableEvent,
		)
		p.lastEvent = lastEvent
		if err != nil {
			return false, err
		}
	} else {
		var err error
		p.ctx, p.lastEvent, err = processModelResponse(p.ctx, modelResponseConfig{
			Response:         response,
			Invocation:       p.invocation,
			StableInvocation: p.stableInvocation,
			Tracker:          p.tracker,
			PartialUsage:     &p.partialUsageState,
			ModelCallbacks:   p.config.ModelCallbacks,
			EventChan:        p.config.EventChan,
			InvocationID:     p.config.InvocationID,
			SessionID:        p.config.SessionID,
			LLMModel:         p.config.LLMModel,
			Request:          p.config.Request,
			Span:             p.config.Span,
			NodeID:           p.config.NodeID,
		})
		if err != nil {
			return false, err
		}
	}
	p.invocation = invocationFromContextOrDefault(p.ctx, p.invocation)
	p.observabilityInvocation = refreshObservabilityInvocationView(
		p.observabilityInvocation,
		p.stableInvocation,
		p.config,
	)
	traceProcessedModelResponse(
		p.config.Span,
		p.tracker,
		p.observabilityInvocation,
		p.config.Request,
		response,
		p.lastEvent,
	)
	return true, nil
}

func (p *modelResponseProcessor) finalize() (*model.Response, error) {
	finalResponse, err := validateFinalModelResponse(p.config.Span, p.finalResponse)
	if err != nil {
		return nil, err
	}
	mergeToolCallsIntoFinalResponse(finalResponse, p.toolCalls)
	p.tap.WriteFinalIfNoDelta(finalResponse)
	return finalResponse, nil
}

// executeModelAndProcessResponses runs the model call and handles response-side
// behavior such as streaming, callbacks, response events, telemetry, and final
// response assembly. DisableModelExecutionEvents only controls the model
// lifecycle events emitted around this call site; it does not bypass model
// execution or response processing.
func executeModelAndProcessResponsesWithContext(
	ctx context.Context,
	config modelExecutionConfig,
) (context.Context, *agent.Invocation, any, error) {
	invocation := invocationFromContextOrDefault(ctx, config.Invocation)
	ctx, stream, err := runModelStream(
		ctx,
		invocation,
		config.ModelCallbacks,
		config.LLMModel,
		config.Request,
		config.BeforeGenerate,
	)
	if err != nil {
		config.Span.RecordError(err)
		config.Span.SetStatus(codes.Error, err.Error())
		return ctx, invocation, nil, fmt.Errorf("failed to run model: %w", err)
	}
	invocation = invocationFromContextOrDefault(ctx, invocation)
	if invocation == nil {
		invocation = agent.NewInvocation(
			agent.WithInvocationID(config.InvocationID),
			agent.WithInvocationModel(config.LLMModel),
			agent.WithInvocationSession(&session.Session{ID: config.SessionID}),
		)
	}

	processor := newModelResponseProcessor(ctx, config, invocation, &err)
	defer processor.close()

	if err = processor.consume(stream); err != nil {
		return processor.ctx, invocationFromContextOrDefault(processor.ctx, invocation), nil, err
	}
	if err != nil {
		return processor.ctx, invocationFromContextOrDefault(processor.ctx, invocation), nil, err
	}
	finalResponse, finalizeErr := processor.finalize()
	err = finalizeErr
	if err != nil {
		return processor.ctx, invocationFromContextOrDefault(processor.ctx, invocation), nil, err
	}
	return processor.ctx, invocationFromContextOrDefault(processor.ctx, invocation), finalResponse, nil
}

func executeModelAndProcessResponses(
	ctx context.Context,
	config modelExecutionConfig,
) (any, error) {
	_, _, result, err := executeModelAndProcessResponsesWithContext(ctx, config)
	return result, err
}

// executeModelWithEvents preserves the previous helper name for existing
// tests while delegating to the refactored response-processing pipeline.
func executeModelWithEvents(ctx context.Context, config modelExecutionConfig) (any, error) {
	return executeModelAndProcessResponses(ctx, config)
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
	// ToolCallbacks specifies tool callbacks to use.
	// If nil, callbacks will be extracted from State.
	ToolCallbacks *tool.Callbacks
}

// processToolCalls executes all tool calls and returns the resulting messages.
func processToolCalls(ctx context.Context, config toolCallsConfig) ([]model.Message, error) {
	// Use callbacks from config if provided; otherwise extract from state.
	toolCallbacks := config.ToolCallbacks
	if toolCallbacks == nil {
		toolCallbacks, _ = extractToolCallbacks(config.State)
	}
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

	// Keep the original invocation as a fallback when callbacks return a bare context.
	originalInvocation, _ := agent.InvocationFromContext(ctx)
	ctx, span, startedSpan := startNodeSpan(ctx, itelemetry.NewExecuteToolSpanName(config.ToolCall.Function.Name))
	_, startEventInvocation, finalCtx, completeEventInvocation, result, modifiedArgs, err := runToolWithEventContexts(
		ctx,
		config.ToolCall,
		config.ToolCallbacks,
		t,
		config.State,
	)
	eventInvocation := invocationFromContextOrFallback(
		finalCtx,
		invocationOrFallback(
			completeEventInvocation,
			invocationOrFallback(startEventInvocation, originalInvocation),
		),
	)
	ctx = finalCtx
	eventInvocationID := invocationIDOrFallback(
		eventInvocation,
		config.InvocationID,
	)
	// Emit tool execution start event with modified arguments.
	emitToolStartEvent(
		finalCtx,
		eventInvocation,
		config.EventChan,
		eventInvocationID,
		name,
		id,
		nodeID,
		startTime, modifiedArgs, responseID,
	)

	var interruptErr *InterruptError
	eventErr := err
	if err != nil {
		if errors.As(err, &interruptErr) {
			// Do not emit error payload for interrupt so clients treat it as pause.
			eventErr = nil
			if result == nil {
				// Set result to interrupt value when no result is provided.
				result = interruptErr.Value
			}
		}
	}
	// Emit tool execution complete event.
	event := emitToolCompleteEvent(finalCtx, eventInvocation, toolCompleteEventConfig{
		EventChan:    config.EventChan,
		InvocationID: eventInvocationID,
		ToolName:     name,
		ToolID:       id,
		NodeID:       nodeID,
		StartTime:    startTime,
		Result:       result,
		Error:        eventErr,
		Arguments:    modifiedArgs,
		ResponseID:   responseID,
	})
	if startedSpan {
		itelemetry.TraceToolCall(span, sessInfo, t.Declaration(), modifiedArgs, event, err)
	}
	itelemetry.ReportExecuteToolMetrics(ctx, itelemetry.ExecuteToolAttributes{
		RequestModelName: "trpc-agent-go-graph",
		ToolName:         name,
		AgentName:        fmt.Sprintf("trpc-agent-go-graph-node-id: %s", nodeID),
		AppName:          sessInfo.AppName,
		UserID:           sessInfo.UserID,
		SessionID:        sessInfo.ID,
		Error:            err,
	}, time.Since(startTime))
	if startedSpan {
		span.End()
	}

	if err != nil {
		if interruptErr != nil {
			return model.Message{}, interruptErr
		}
		config.Span.RecordError(err)
		config.Span.SetStatus(codes.Error, err.Error())
		return model.Message{}, fmt.Errorf("tool %s call failed: %w", name, err)
	}

	// Marshal result to JSON.
	content, err := json.Marshal(result)
	if err != nil {
		config.Span.RecordError(err)
		config.Span.SetStatus(codes.Error, err.Error())
		return model.Message{}, fmt.Errorf("failed to marshal tool result: %w", err)
	}

	return model.NewToolMessage(id, name, string(content)), nil
}

func invocationFromContextOrFallback(ctx context.Context, fallback *agent.Invocation) *agent.Invocation {
	if invocation, ok := agent.InvocationFromContext(ctx); ok && invocation != nil {
		return invocation
	}
	return fallback
}

func invocationOrFallback(invocation *agent.Invocation, fallback *agent.Invocation) *agent.Invocation {
	if invocation != nil {
		return invocation
	}
	return fallback
}

func invocationIDOrFallback(invocation *agent.Invocation, fallback string) string {
	if invocation != nil && invocation.InvocationID != "" {
		return invocation.InvocationID
	}
	return fallback
}

// emitToolStartEvent emits a tool execution start event.
func emitToolStartEvent(
	ctx context.Context,
	invocation *agent.Invocation,
	eventChan chan<- *event.Event,
	invocationID, toolName, toolID, nodeID string,
	startTime time.Time,
	arguments []byte,
	responseID string,
) {
	if eventChan == nil {
		return
	}
	if invocation != nil && agent.IsGraphExecutorEventsDisabled(invocation) {
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
func emitToolCompleteEvent(
	ctx context.Context,
	invocation *agent.Invocation,
	config toolCompleteEventConfig,
) *event.Event {
	if config.EventChan == nil {
		return nil
	}
	if invocation != nil && agent.IsGraphExecutorEventsDisabled(invocation) {
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
	schema.AddField(StateKeyLastToolResponse, StateField{
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
	return buildAgentInvocationWithStateAndScope(
		ctx,
		state,
		state,
		targetAgent,
		"",
		"",
	)
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
	invocation, _ := agent.InvocationFromContext(ctx)
	if invocation != nil && agent.IsGraphExecutorEventsDisabled(invocation) {
		return
	}

	agentStartEvent := NewNodeStartEvent(
		WithNodeEventInvocationID(invocationID),
		WithNodeEventNodeID(nodeID),
		WithNodeEventNodeType(NodeTypeAgent),
		WithNodeEventStartTime(startTime),
	)
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
	invocation, _ := agent.InvocationFromContext(ctx)
	if invocation != nil && agent.IsGraphExecutorEventsDisabled(invocation) {
		return
	}

	agentCompleteEvent := NewNodeCompleteEvent(
		WithNodeEventInvocationID(invocationID),
		WithNodeEventNodeID(nodeID),
		WithNodeEventNodeType(NodeTypeAgent),
		WithNodeEventStartTime(startTime),
		WithNodeEventEndTime(endTime),
	)
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
	invocation, _ := agent.InvocationFromContext(ctx)
	if agent.IsGraphExecutorEventsDisabled(invocation) {
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
