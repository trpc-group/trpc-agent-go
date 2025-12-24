// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
package compiler

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	dsl "trpc.group/trpc-go/trpc-agent-go/dsl"
	dslcel "trpc.group/trpc-go/trpc-agent-go/dsl/internal/cel"
	"trpc.group/trpc-go/trpc-agent-go/dsl/internal/knowledgeconfig"
	"trpc.group/trpc-go/trpc-agent-go/dsl/internal/mcpconfig"
	"trpc.group/trpc-go/trpc-agent-go/dsl/internal/toolspec"
	"trpc.group/trpc-go/trpc-agent-go/dsl/registry"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/knowledge"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/searchfilter"
	knowledgetool "trpc.group/trpc-go/trpc-agent-go/knowledge/tool"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	ctool "trpc.group/trpc-go/trpc-agent-go/tool"
)

// Option configures a Compiler instance.
type Option func(*Compiler)

// Compiler compiles DSL graphs into executable StateGraphs.
// This is the core of the DSL system, transforming declarative JSON
// into imperative Go code that can be executed by trpc-agent-go.
type Compiler struct {
	registry        *registry.Registry
	toolProvider    dsl.ToolProvider
	toolSetProvider dsl.ToolSetProvider
	reducerRegistry *registry.ReducerRegistry
	agentRegistry   *registry.AgentRegistry
	schemaInference *dsl.SchemaInference
	allowEnvSecrets bool

	// mcpInputSource records, for each builtin.mcp node ID, the ID of its
	// primary upstream node. This is used to construct the "input" view in
	// MCP parameter expressions so that input.* refers to the immediate
	// predecessor's structured output, not a global state field.
	mcpInputSource map[string]string

	// knowledgeSearchInputSource records, for each builtin.knowledge_search node ID,
	// the ID of its primary upstream node. This is used to construct the "input" view
	// for query_template variable resolution.
	knowledgeSearchInputSource map[string]string
}

// whileBodyConfig describes the nested subgraph that forms the body of a
// builtin.while node. It mirrors a minimal Graph shape (nodes/edges +
// start/exit) but is scoped locally to the while node.
type whileBodyConfig struct {
	Nodes            []dsl.Node            `json:"nodes"`
	Edges            []dsl.Edge            `json:"edges"`
	ConditionalEdges []dsl.ConditionalEdge `json:"conditional_edges,omitempty"`
	StartNodeID      string                `json:"start_node_id"`
	ExitNodeID       string                `json:"exit_node_id"`
}

// whileConfig describes the DSL-level configuration for a builtin.while node
// in the engine DSL. It is decoded from EngineNode.Config via JSON round-
// tripping to keep the EngineNode struct simple.
type whileConfig struct {
	Body      whileBodyConfig `json:"body"`
	Condition dsl.Expression  `json:"condition"`
}

// whileExpansion contains the preprocessed information needed by the compiler
// to expand a builtin.while node into concrete nodes/edges and a conditional
// back-edge in the underlying StateGraph.
type whileExpansion struct {
	BodyEntry string
	BodyExit  string
	AfterNode string
	Cond      dsl.Expression

	BodyNodes            []dsl.Node
	BodyEdges            []dsl.Edge
	BodyConditionalEdges []dsl.ConditionalEdge
}

// New creates a new DSL compiler with sensible defaults and applies the
// provided options. Callers typically supply only the options that need to
// differ from the defaults (e.g., custom ToolProvider or ToolSetProvider).
func New(opts ...Option) *Compiler {
	c := &Compiler{
		registry:        registry.DefaultRegistry,
		toolProvider:    registry.DefaultToolRegistry,
		toolSetProvider: registry.DefaultToolSetRegistry,
		reducerRegistry: registry.NewReducerRegistry(),
		agentRegistry:   registry.NewAgentRegistry(),
	}

	for _, opt := range opts {
		if opt != nil {
			opt(c)
		}
	}

	if c.registry == nil {
		c.registry = registry.DefaultRegistry
	}

	// Schema inference depends on both the component registry and reducer
	// registry, so we construct it after options have been applied.
	c.schemaInference = dsl.NewSchemaInference(c.registry)
	c.schemaInference.SetReducerRegistry(c.reducerRegistry)

	return c
}

// WithComponentRegistry sets the component registry used by the compiler.
// This is primarily used by higher-level runners/servers that maintain their
// own *registry.Registry instance. Most callers can rely on the default.
func WithComponentRegistry(reg *registry.Registry) Option {
	return func(c *Compiler) {
		if reg != nil {
			c.registry = reg
		}
	}
}

// WithAllowEnvSecrets enables resolving "env:VAR" placeholders in model_spec
// fields such as api_key/headers/base_url.
//
// This is intended for local development and debugging only; production
// services should provide explicit secrets via model_spec to avoid ambiguity and
// accidental environment-based fallbacks.
func WithAllowEnvSecrets(enabled bool) Option {
	return func(c *Compiler) {
		c.allowEnvSecrets = enabled
	}
}

// WithToolProvider sets the tool provider for the compiler. This allows
// callers to supply a user/tenant specific provider that may combine
// framework built‑in tools with application or user defined tools.
func WithToolProvider(provider dsl.ToolProvider) Option {
	return func(c *Compiler) {
		c.toolProvider = provider
	}
}

// WithToolSetProvider sets the ToolSet provider for the compiler. This allows
// callers to supply a user/tenant specific provider that may combine
// framework built‑in ToolSets with application or user defined ToolSets.
func WithToolSetProvider(provider dsl.ToolSetProvider) Option {
	return func(c *Compiler) {
		c.toolSetProvider = provider
	}
}

// WithToolSetRegistry is a convenience helper for callers that build a
// concrete ToolSetRegistry. It simply forwards to WithToolSetProvider.
func WithToolSetRegistry(toolSetRegistry *registry.ToolSetRegistry) Option {
	return func(c *Compiler) {
		if toolSetRegistry != nil {
			c.toolSetProvider = toolSetRegistry
		}
	}
}

// WithReducerRegistry sets the reducer registry for the compiler.
// This allows the compiler to resolve reducer references in state schema inference.
func WithReducerRegistry(reducerRegistry *registry.ReducerRegistry) Option {
	return func(c *Compiler) {
		if reducerRegistry != nil {
			c.reducerRegistry = reducerRegistry
		}
	}
}

// WithAgentRegistry sets the agent registry for the compiler.
// This allows the compiler to resolve agent references in agent nodes.
func WithAgentRegistry(agentRegistry *registry.AgentRegistry) Option {
	return func(c *Compiler) {
		if agentRegistry != nil {
			c.agentRegistry = agentRegistry
		}
	}
}

// AgentRegistry returns the agent registry used by the compiler.
func (c *Compiler) AgentRegistry() *registry.AgentRegistry {
	return c.agentRegistry
}

// Compile compiles an engine-level graph definition into an executable StateGraph.
// The graph here is the engine DSL representation without any UI-specific
// concepts such as positions or visual layout.
func (c *Compiler) Compile(graphDef *dsl.Graph) (*graph.Graph, error) {
	if graphDef == nil {
		return nil, fmt.Errorf("graph is nil")
	}

	// Ensure per-compile transient maps are reset.
	c.mcpInputSource = nil
	c.knowledgeSearchInputSource = nil

	// Step 0: Expand structural builtin.while nodes. While is represented as
	// a nested subgraph in the engine DSL but compiled into a flat set of
	// nodes/edges plus a conditional back-edge in the underlying StateGraph.
	expandedGraph, whileMeta, err := c.expandWhile(graphDef)
	if err != nil {
		return nil, fmt.Errorf("while expansion failed: %w", err)
	}
	graphDef = expandedGraph

	// Detect builtin.start / builtin.end nodes (if present) so we can map
	// them to the real graph entry/finish points. There should be at most
	// one builtin.start node.
	var startNodeID string
	var endNodeIDs []string

	for _, node := range graphDef.Nodes {
		switch node.EngineNode.NodeType {
		case "builtin.start":
			startNodeID = node.ID
		case "builtin.end":
			endNodeIDs = append(endNodeIDs, node.ID)
		}
	}

	// Step 1: Infer State Schema from components
	schema, err := c.schemaInference.InferSchema(graphDef)
	if err != nil {
		return nil, fmt.Errorf("schema inference failed: %w", err)
	}

	// Step 2: Create StateGraph
	stateGraph := graph.NewStateGraph(schema)

	// Build a quick index from node ID to node for later lookups.
	nodeByID := make(map[string]dsl.Node, len(graphDef.Nodes))
	for _, n := range graphDef.Nodes {
		nodeByID[n.ID] = n
	}

	// Pre-compute the primary input source for builtin.mcp nodes so that
	// parameter expressions can treat input.* as the immediate upstream
	// structured output, mirroring the semantics used by builtin
	// conditions and while.
	mcpInputSource := make(map[string]string)
	knowledgeSearchInputSource := make(map[string]string)
	for _, edge := range graphDef.Edges {
		targetNode, ok := nodeByID[edge.Target]
		if !ok {
			continue
		}
		switch targetNode.EngineNode.NodeType {
		case "builtin.mcp":
			if existing, exists := mcpInputSource[edge.Target]; exists && existing != edge.Source {
				return nil, fmt.Errorf("builtin.mcp node %s has multiple incoming edges (%s, %s); it must have a single upstream node for input.* semantics", edge.Target, existing, edge.Source)
			}
			mcpInputSource[edge.Target] = edge.Source
		case "builtin.knowledge_search":
			if existing, exists := knowledgeSearchInputSource[edge.Target]; exists && existing != edge.Source {
				return nil, fmt.Errorf("builtin.knowledge_search node %s has multiple incoming edges (%s, %s); it must have a single upstream node for input.* semantics", edge.Target, existing, edge.Source)
			}
			knowledgeSearchInputSource[edge.Target] = edge.Source
		}
	}
	c.mcpInputSource = mcpInputSource
	c.knowledgeSearchInputSource = knowledgeSearchInputSource

	// Step 3: Add all nodes
	for _, node := range graphDef.Nodes {
		nodeFunc, err := c.createNodeFunc(node)
		if err != nil {
			return nil, fmt.Errorf("failed to create node %s: %w", node.ID, err)
		}

		stateGraph.AddNode(node.ID, nodeFunc)
	}

	// Step 4: Add edges
	for _, edge := range graphDef.Edges {
		stateGraph.AddEdge(edge.Source, edge.Target)
	}

	// Step 5: Add conditional edges
	for _, condEdge := range graphDef.ConditionalEdges {
		// Handle regular conditional edges
		condFunc, err := c.createConditionalFunc(condEdge)
		if err != nil {
			return nil, fmt.Errorf("failed to create conditional edge %s: %w", condEdge.ID, err)
		}

		// For builtin conditions, the ConditionalFunc returns the concrete
		// target node ID, so we pass nil as the path map.
		stateGraph.AddConditionalEdges(condEdge.From, condFunc, nil)
	}

	// Step 5.5: Expand builtin.while semantics into conditional edges on the
	// underlying StateGraph. For each while node, we add a conditional edge
	// from body_exit that either routes back to body_entry (continue) or to
	// the node that originally followed the while node (break).
	for whileID, exp := range whileMeta {
		exp := exp // capture loop variable
		if exp.BodyExit == "" || exp.BodyEntry == "" || exp.AfterNode == "" {
			return nil, fmt.Errorf("while node %s has incomplete expansion metadata", whileID)
		}
		if strings.TrimSpace(exp.Cond.Expression) == "" {
			return nil, fmt.Errorf("while node %s has empty condition expression", whileID)
		}

		condExpr := exp.Cond.Expression
		// Compile the CEL expression once at compile time so that runtime
		// execution of the while loop only evaluates the already compiled
		// program instead of re-parsing on each iteration.
		condProg, err := dslcel.CompileBool(condExpr)
		if err != nil {
			return nil, fmt.Errorf("while node %s has invalid condition expression: %w", whileID, err)
		}

		condFunc := func(ctx context.Context, state graph.State) (string, error) {
			// Build the input view from the body exit node so that CEL
			// expressions can use input.output_parsed.*, mirroring the
			// OpenAI workflow semantics.
			input := buildNodeInputView(state, exp.BodyExit)

			ok, err := condProg.Eval(state, input)
			if err != nil {
				return "", fmt.Errorf("while condition evaluation failed: %w", err)
			}
			if ok {
				// Continue: jump back to body_entry.
				return exp.BodyEntry, nil
			}
			// Break: jump to the node that was originally connected after the while node.
			return exp.AfterNode, nil
		}

		stateGraph.AddConditionalEdges(exp.BodyExit, condFunc, nil)
	}

	// Step 6: Set entry point
	if startNodeID != "" {
		// When a builtin.start node is present, it is now treated as a real
		// executable node and used directly as the graph entry point.
		stateGraph.SetEntryPoint(startNodeID)
	} else {
		stateGraph.SetEntryPoint(graphDef.StartNodeID)
	}

	// Step 7: Set finish points based on builtin.end nodes (if any). Each
	// builtin.end node is treated as a graph finish node, mirroring the
	// multi-ends pattern in the native graph API.
	for _, endID := range endNodeIDs {
		stateGraph.SetFinishPoint(endID)
	}

	// Step 8: Compile the graph
	compiledGraph, err := stateGraph.Compile()
	if err != nil {
		return nil, fmt.Errorf("graph compilation failed: %w", err)
	}

	return compiledGraph, nil
}

// buildWhileExpansion preprocesses a builtin.while node and its surrounding
// edges into a whileExpansion structure used by the compiler.
func (c *Compiler) buildWhileExpansion(node dsl.Node, edges []dsl.Edge, nodeIDs map[string]bool) (*whileExpansion, error) {
	engine := node.EngineNode

	rawCfg := engine.Config
	if rawCfg == nil {
		return nil, fmt.Errorf("config is required for builtin.while")
	}

	// Decode config map into whileConfig using JSON round-tripping.
	data, err := json.Marshal(rawCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal builtin.while config: %w", err)
	}
	var cfg whileConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal builtin.while config: %w", err)
	}

	// Validate loop body
	body := cfg.Body
	if len(body.Nodes) == 0 {
		return nil, fmt.Errorf("body.nodes must contain at least one node")
	}
	if strings.TrimSpace(body.StartNodeID) == "" {
		return nil, fmt.Errorf("body.start_node_id is required for builtin.while")
	}
	if strings.TrimSpace(body.ExitNodeID) == "" {
		return nil, fmt.Errorf("body.exit_node_id is required for builtin.while")
	}

	// Build a local index of body node IDs and ensure they do not conflict
	// with existing top-level nodes.
	bodyNodeIDs := make(map[string]bool, len(body.Nodes))
	for _, n := range body.Nodes {
		if strings.TrimSpace(n.ID) == "" {
			return nil, fmt.Errorf("body node has empty id")
		}
		if bodyNodeIDs[n.ID] {
			return nil, fmt.Errorf("duplicate node id %q in while body", n.ID)
		}
		if nodeIDs[n.ID] {
			return nil, fmt.Errorf("while body node id %q conflicts with existing graph node", n.ID)
		}
		bodyNodeIDs[n.ID] = true
	}

	if !bodyNodeIDs[body.StartNodeID] {
		return nil, fmt.Errorf("body.start_node_id %q does not reference a node in body.nodes", body.StartNodeID)
	}
	if !bodyNodeIDs[body.ExitNodeID] {
		return nil, fmt.Errorf("body.exit_node_id %q does not reference a node in body.nodes", body.ExitNodeID)
	}

	// Validate body edges reference only body nodes.
	for _, e := range body.Edges {
		if strings.TrimSpace(e.Source) == "" || strings.TrimSpace(e.Target) == "" {
			return nil, fmt.Errorf("while body edge has empty source or target")
		}
		if !bodyNodeIDs[e.Source] {
			return nil, fmt.Errorf("while body edge source %q is not a body node", e.Source)
		}
		if !bodyNodeIDs[e.Target] {
			return nil, fmt.Errorf("while body edge target %q is not a body node", e.Target)
		}
	}

	// (Optional) validate that conditional edges originate from body nodes.
	for _, ce := range body.ConditionalEdges {
		if strings.TrimSpace(ce.From) == "" {
			return nil, fmt.Errorf("while body conditional edge has empty from")
		}
		if !bodyNodeIDs[ce.From] {
			return nil, fmt.Errorf("while body conditional edge 'from' %q is not a body node", ce.From)
		}
	}

	// Resolve the single "after" node from outgoing edges of the while node.
	var afterNode string
	for _, e := range edges {
		if e.Source != node.ID {
			continue
		}
		if afterNode == "" {
			afterNode = e.Target
		} else if afterNode != e.Target {
			return nil, fmt.Errorf("builtin.while node %s has multiple outgoing edges (%s, %s)", node.ID, afterNode, e.Target)
		}
	}
	if strings.TrimSpace(afterNode) == "" {
		return nil, fmt.Errorf("builtin.while node %s must have exactly one outgoing edge", node.ID)
	}

	// Build a condition.CaseCondition for evaluation. We normalize variable
	// paths using the same rules as builtin conditions so that shortcuts
	// like input.* and state.* can be used. For the CEL-based while
	// condition this simply means ensuring an expression string is present.
	if strings.TrimSpace(cfg.Condition.Expression) == "" {
		return nil, fmt.Errorf("condition.expression is required for builtin.while")
	}

	return &whileExpansion{
		BodyEntry:            body.StartNodeID,
		BodyExit:             body.ExitNodeID,
		AfterNode:            afterNode,
		Cond:                 cfg.Condition,
		BodyNodes:            body.Nodes,
		BodyEdges:            body.Edges,
		BodyConditionalEdges: body.ConditionalEdges,
	}, nil
}

// createNodeFunc creates a NodeFunc for an engine-level node instance.
func (c *Compiler) createNodeFunc(node dsl.Node) (graph.NodeFunc, error) {
	engine := node.EngineNode

	// Handle LLM components specially (use AddLLMNode pattern)
	if engine.NodeType == "builtin.llm" {
		return c.createLLMNodeFunc(node)
	}

	// Handle Tools components specially (use AddToolsNode pattern)
	if engine.NodeType == "builtin.tools" {
		return c.createToolsNodeFunc(node)
	}

	// Handle standalone MCP components specially.
	if engine.NodeType == "builtin.mcp" {
		return c.createMCPNodeFunc(node)
	}

	// Handle standalone Knowledge Search components specially.
	if engine.NodeType == "builtin.knowledge_search" {
		return c.createKnowledgeSearchNodeFunc(node)
	}

	// Handle LLMAgent components specially (dynamically create LLMAgent)
	if engine.NodeType == "builtin.llmagent" {
		return c.createLLMAgentNodeFunc(node)
	}

	// Handle UserApproval components specially (graph.Interrupt-based).
	if engine.NodeType == "builtin.user_approval" {
		return c.createUserApprovalNodeFunc(node)
	}

	// Get component from registry
	component, exists := c.registry.Get(engine.NodeType)
	if !exists {
		return nil, fmt.Errorf("component %s not found in registry", engine.NodeType)
	}

	// Create a closure that captures the component and config
	config := registry.ComponentConfig(engine.Config)

	return func(ctx context.Context, state graph.State) (interface{}, error) {
		// Execute the component
		result, err := component.Execute(ctx, config, state)
		if err != nil {
			return nil, fmt.Errorf("component %s execution failed: %w", engine.NodeType, err)
		}

		// Apply output mapping if specified in DSL
		if len(engine.Outputs) > 0 {
			result, err = c.applyOutputMapping(result, engine.Outputs, component)
			if err != nil {
				return nil, fmt.Errorf("output mapping failed for node %s: %w", node.ID, err)
			}
		}

		// Return the result state
		return result, nil
	}, nil
}

// createLLMNodeFunc creates a NodeFunc for a builtin.llm node.
// The model instance is constructed from model_spec and captured via closure,
// not passed through graph state.
func (c *Compiler) createLLMNodeFunc(node dsl.Node) (graph.NodeFunc, error) {
	engine := node.EngineNode

	llmModel, _, err := resolveModelFromConfig(engine.Config, c.allowEnvSecrets)
	if err != nil {
		return nil, err
	}

	// Get instruction from config (optional)
	instruction := ""
	if inst, ok := engine.Config["instruction"].(string); ok {
		instruction = inst
	}

	// Get tools from config (optional)
	// Tools can be specified as:
	// 1. A list of tool names (strings) - resolved from ToolProvider
	// 2. "*" - use all tools from ToolProvider
	tools := make(map[string]tool.Tool)

	if c.toolProvider != nil {
		if toolsConfig, ok := engine.Config["tools"]; ok {
			switch v := toolsConfig.(type) {
			case string:
				// "*" means all tools
				if v == "*" {
					tools = c.toolProvider.GetAll()
				} else {
					// Single tool name
					if t, err := c.toolProvider.Get(v); err == nil {
						tools[v] = t
					}
				}
			case []interface{}:
				// List of tool names
				toolNames := make([]string, 0, len(v))
				for _, name := range v {
					if nameStr, ok := name.(string); ok {
						toolNames = append(toolNames, nameStr)
					}
				}
				if len(toolNames) > 0 {
					if resolvedTools, err := c.toolProvider.GetMultiple(toolNames); err == nil {
						tools = resolvedTools
					}
				}
			case []string:
				// List of tool names (already strings)
				if resolvedTools, err := c.toolProvider.GetMultiple(v); err == nil {
					tools = resolvedTools
				}
			}
		}
	}

	// Use graph.NewLLMNodeFunc to create the NodeFunc
	// This follows the same pattern as AddLLMNode
	// Pass node ID so that node_responses can be properly keyed
	llmNodeFunc := graph.NewLLMNodeFunc(llmModel, instruction, tools, graph.WithLLMNodeID(node.ID))

	// If outputs are specified, wrap the LLM node func with output mapping
	if len(engine.Outputs) > 0 {
		return func(ctx context.Context, state graph.State) (interface{}, error) {
			// Execute LLM node
			result, err := llmNodeFunc(ctx, state)
			if err != nil {
				return nil, err
			}

			// Apply output mapping
			// For LLM nodes, we need to create a pseudo-component to get metadata
			pseudoComponent := &llmPseudoComponent{}
			result, err = c.applyOutputMapping(result, engine.Outputs, pseudoComponent)
			if err != nil {
				return nil, fmt.Errorf("output mapping failed for LLM node %s: %w", node.ID, err)
			}

			return result, nil
		}, nil
	}

	return llmNodeFunc, nil
}

// createToolsNodeFunc creates a NodeFunc for a Tools component.
// This uses the AddToolsNode pattern from trpc-agent-go, where tools are
// obtained from ToolRegistry and passed via closure, not through state.
func (c *Compiler) createToolsNodeFunc(node dsl.Node) (graph.NodeFunc, error) {
	engine := node.EngineNode

	// Get tools from config (optional)
	// Tools can be specified as:
	// 1. A list of tool names (strings) - resolved from ToolRegistry
	// 2. "*" - use all tools from ToolRegistry
	tools := make(map[string]tool.Tool)

	if c.toolProvider != nil {
		if toolsConfig, ok := engine.Config["tools"]; ok {
			switch v := toolsConfig.(type) {
			case string:
				// "*" means all tools
				if v == "*" {
					tools = c.toolProvider.GetAll()
				} else {
					// Single tool name
					if t, err := c.toolProvider.Get(v); err == nil {
						tools[v] = t
					}
				}
			case []interface{}:
				// List of tool names
				toolNames := make([]string, 0, len(v))
				for _, name := range v {
					if nameStr, ok := name.(string); ok {
						toolNames = append(toolNames, nameStr)
					}
				}
				if len(toolNames) > 0 {
					if resolvedTools, err := c.toolProvider.GetMultiple(toolNames); err == nil {
						tools = resolvedTools
					}
				}
			case []string:
				// List of tool names (already strings)
				if resolvedTools, err := c.toolProvider.GetMultiple(v); err == nil {
					tools = resolvedTools
				}
			}
		} else {
			// If no tools config specified, use all tools from registry
			tools = c.toolProvider.GetAll()
		}
	}

	// Use graph.NewToolsNodeFunc to create the NodeFunc
	// This follows the same pattern as AddToolsNode
	return graph.NewToolsNodeFunc(tools), nil
}

// createMCPNodeFunc creates a NodeFunc for a standalone MCP node.
// The MCP node calls a single MCP tool on a remote MCP server and exposes
// the result under node_structured[nodeID].results for downstream nodes.
func (c *Compiler) createMCPNodeFunc(node dsl.Node) (graph.NodeFunc, error) {
	engine := node.EngineNode

	parsed, err := mcpconfig.ParseNodeConfig(engine.Config)
	if err != nil {
		return nil, err
	}

	// Build configuration map compatible with createMCPToolSet.
	mcpCfg := map[string]any{
		"transport":  parsed.Transport,
		"server_url": parsed.ServerURL,
	}
	if len(parsed.Headers) > 0 {
		mcpCfg["headers"] = parsed.Headers
	}

	mcpToolSet, err := c.createMCPToolSet(mcpCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create MCP toolset for node %s: %w", node.ID, err)
	}

	params := parsed.Params

	// Resolve the primary upstream node for this MCP node, if any, so that
	// we can build a stable input.* view from the immediate predecessor's
	// structured output (mirroring condition/while semantics).
	fromNodeID := ""
	if c.mcpInputSource != nil {
		if src, ok := c.mcpInputSource[node.ID]; ok {
			fromNodeID = src
		}
	}

	return func(ctx context.Context, state graph.State) (interface{}, error) {
		// Resolve the MCP tool from the toolset.
		var selected tool.Tool
		for _, t := range mcpToolSet.Tools(ctx) {
			if decl := t.Declaration(); decl != nil && decl.Name == parsed.ToolName {
				selected = t
				break
			}
		}
		if selected == nil {
			return nil, fmt.Errorf("MCP tool %q not found on server %q", parsed.ToolName, parsed.ServerURL)
		}

		callable, ok := selected.(tool.CallableTool)
		if !ok {
			return nil, fmt.Errorf("MCP tool %q is not callable", parsed.ToolName)
		}

		// Build the input view for MCP param expressions from the immediate
		// upstream node's structured output. This keeps input.* scoped to
		// the local edge (like builtin conditions / while) instead of a
		// global state field.
		var input any
		if fromNodeID != "" {
			input = buildNodeInputView(state, fromNodeID)
		}

		// Build arguments object by evaluating params expressions (if configured).
		args := make(map[string]any)
		for name, raw := range params {
			exprMap, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			exprStr, _ := exprMap["expression"].(string)
			if strings.TrimSpace(exprStr) == "" {
				continue
			}

			value, err := dslcel.Eval(exprStr, state, input)
			if err != nil {
				return nil, fmt.Errorf("failed to evaluate MCP param %q: %w", name, err)
			}
			args[name] = value
		}

		payload, err := json.Marshal(args)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal MCP tool arguments: %w", err)
		}

		result, err := callable.Call(ctx, payload)
		if err != nil {
			return nil, fmt.Errorf("MCP tool %q call failed: %w", parsed.ToolName, err)
		}

		if result == nil {
			return nil, nil
		}

		// Normalize MCP content into a JSON-friendly slice and aggregate text.
		normalized := make([]map[string]any, 0)
		var textBuf []string

		if b, err := json.Marshal(result); err == nil {
			var tmp []map[string]any
			if err := json.Unmarshal(b, &tmp); err == nil {
				normalized = tmp
			}
		}

		for _, item := range normalized {
			if t, ok := item["type"].(string); ok && t == "text" {
				if txt, ok := item["text"].(string); ok {
					if strings.TrimSpace(txt) != "" {
						textBuf = append(textBuf, txt)
					}
				}
			}
		}

		resultsText := strings.Join(textBuf, "\n")

		// Best-effort JSON extraction from the aggregated text. This mirrors the
		// LLMAgent structured output behavior and gives downstream Transform /
		// End nodes a structured object when the MCP tool returns JSON.
		var parsed any
		hasParsed := false
		if strings.TrimSpace(resultsText) != "" {
			if jsonText, ok := extractFirstJSONObjectFromText(resultsText); ok {
				var v any
				if err := json.Unmarshal([]byte(jsonText), &v); err == nil {
					parsed = v
					hasParsed = true
				}
			}
		}

		if len(normalized) == 0 && resultsText == "" {
			// No recognizable structure; do not emit node_structured to avoid
			// surprising callers.
			return nil, nil
		}

		// Merge with existing node_structured cache (if any) to avoid clobbering
		// structured outputs from other nodes.
		nodeStructured := map[string]any{}
		if existingRaw, ok := state["node_structured"]; ok {
			if existingMap, ok := existingRaw.(map[string]any); ok && existingMap != nil {
				for k, v := range existingMap {
					nodeStructured[k] = v
				}
			}
		}

		entry := map[string]any{}
		if len(normalized) > 0 {
			entry["results"] = normalized
		}
		if resultsText != "" {
			entry["results_text"] = resultsText
		}
		if hasParsed {
			entry["output_parsed"] = parsed
		}

		nodeStructured[node.ID] = entry

		return graph.State{
			"node_structured": nodeStructured,
		}, nil
	}, nil
}

// createConditionalFunc creates a ConditionalFunc for a conditional edge.
func (c *Compiler) createConditionalFunc(condEdge dsl.ConditionalEdge) (graph.ConditionalFunc, error) {
	return c.createBuiltinCondition(condEdge)
}

// createBuiltinCondition creates a condition function from a builtin structured condition.
func (c *Compiler) createBuiltinCondition(condEdge dsl.ConditionalEdge) (graph.ConditionalFunc, error) {
	return newBuiltinConditionFunc(condEdge.From, condEdge.Condition)
}

// applyOutputMapping applies output mapping from DSL node outputs configuration.
// It transforms the component's output according to the target specifications.
func (c *Compiler) applyOutputMapping(result interface{}, outputs []dsl.NodeIO, component registry.Component) (interface{}, error) {
	// If result is a Command slice, we can't apply output mapping
	// (Commands are for dynamic fan-out and handle their own state updates)
	if _, isCommands := result.([]*graph.Command); isCommands {
		return result, nil
	}

	// Result should be a State
	resultState, ok := result.(graph.State)
	if !ok {
		return nil, fmt.Errorf("component returned unexpected type %T, expected graph.State or []*graph.Command", result)
	}

	// Get component metadata to know the default output names
	metadata := component.Metadata()

	// Start with a copy of the original state
	// This preserves fields that are not being remapped
	mappedState := make(graph.State)
	for k, v := range resultState {
		mappedState[k] = v
	}

	// Process each output mapping
	for _, output := range outputs {
		// Find the corresponding output in component metadata
		var sourceFieldName string
		for _, metaOutput := range metadata.Outputs {
			if metaOutput.Name == output.Name {
				sourceFieldName = metaOutput.Name
				break
			}
		}

		if sourceFieldName == "" {
			return nil, fmt.Errorf("output '%s' not found in component metadata (available: %v)", output.Name, getOutputNames(metadata.Outputs))
		}

		// Get the value from result state
		value, exists := resultState[sourceFieldName]
		if !exists {
			// If not required and has default, use default
			if !output.Required && output.Default != nil {
				value = output.Default
			} else if output.Required {
				return nil, fmt.Errorf("required output '%s' not found in component result (available keys: %v)", sourceFieldName, getStateKeys(resultState))
			} else {
				// Optional output not present, skip it
				log.Debugf("optional output %q not found in result state; skipping", sourceFieldName)
				continue
			}
		}

		// Determine target field name
		targetFieldName := sourceFieldName // Default: same as source
		if output.Target != nil {
			if output.Target.Type == "state" && output.Target.Field != "" {
				targetFieldName = output.Target.Field
			}
			// If Type == "output", use the output name as-is (no remapping)
		}

		// Type conversion if needed
		// If the target type is a slice but the value is not, wrap it in a slice
		targetValue := value
		if output.Type != "" {
			targetValue = convertValueToType(value, output.Type)
		}

		// If target is different from source, remove the source field and add the target field
		if targetFieldName != sourceFieldName {
			delete(mappedState, sourceFieldName)
			mappedState[targetFieldName] = targetValue
		} else {
			// If target is the same as source, update the value
			mappedState[targetFieldName] = targetValue
		}
	}

	return mappedState, nil
}

// Helper function to get output names from metadata
func getOutputNames(outputs []registry.ParameterSchema) []string {
	names := make([]string, len(outputs))
	for i, output := range outputs {
		names[i] = output.Name
	}
	return names
}

// Helper function to get state keys
func getStateKeys(state graph.State) []string {
	keys := make([]string, 0, len(state))
	for k := range state {
		keys = append(keys, k)
	}
	return keys
}

// convertValueToType converts a value to the target type if needed.
// This is used for output mapping when the source and target types differ.
func convertValueToType(value any, targetType string) any {
	// If target type is a slice type, wrap the value in a slice
	switch targetType {
	case "[]string":
		// If value is already a []string, return as-is
		if slice, ok := value.([]string); ok {
			return slice
		}
		// If value is a string, wrap it in a slice
		if str, ok := value.(string); ok {
			return []string{str}
		}
		// Otherwise, convert to string and wrap
		return []string{fmt.Sprint(value)}

	case "[]int":
		// If value is already a []int, return as-is
		if slice, ok := value.([]int); ok {
			return slice
		}
		// If value is an int, wrap it in a slice
		if i, ok := value.(int); ok {
			return []int{i}
		}
		// Otherwise, return empty slice
		return []int{}

	case "[]map[string]any":
		// If value is already a []map[string]any, return as-is
		if slice, ok := value.([]map[string]any); ok {
			return slice
		}
		// If value is a map[string]any, wrap it in a slice
		if m, ok := value.(map[string]any); ok {
			return []map[string]any{m}
		}
		// Otherwise, return empty slice
		return []map[string]any{}

	default:
		// No conversion needed
		return value
	}
}

// llmPseudoComponent is a pseudo-component that provides metadata for LLM nodes.
// This is used for output mapping when LLM nodes have custom outputs specified.
type llmPseudoComponent struct{}

func (c *llmPseudoComponent) Metadata() registry.ComponentMetadata {
	return registry.ComponentMetadata{
		Name: "builtin.llm",
		Outputs: []registry.ParameterSchema{
			{Name: graph.StateKeyMessages, Type: "[]model.Message"},
			{Name: graph.StateKeyLastResponse, Type: "string"},
			{Name: graph.StateKeyNodeResponses, Type: "map[string]any"},
		},
	}
}

func (c *llmPseudoComponent) Execute(ctx context.Context, config registry.ComponentConfig, state graph.State) (any, error) {
	return nil, fmt.Errorf("llmPseudoComponent.Execute should never be called")
}

// createLLMAgentNodeFunc creates a NodeFunc for an LLMAgent component.
// This dynamically creates an LLMAgent based on DSL configuration and executes it.
func (c *Compiler) createLLMAgentNodeFunc(node dsl.Node) (graph.NodeFunc, error) {
	return newLLMAgentNodeFuncFromConfig(node.ID, node.EngineNode.Config, c.toolProvider, c.allowEnvSecrets)
}

// createUserApprovalNodeFunc creates a NodeFunc for a user approval step.
// It uses graph.Interrupt to pause execution and waits for a resume value.
// The resume value is normalized into "approve"/"reject" and exposed via
// approval_result, while also echoing the message as last_response.
func (c *Compiler) createUserApprovalNodeFunc(node dsl.Node) (graph.NodeFunc, error) {
	return newUserApprovalNodeFuncFromConfig(node.ID, node.EngineNode.Config)
}

// createMCPToolSet creates an MCP ToolSet from DSL configuration.
func (c *Compiler) createMCPToolSet(config map[string]interface{}) (tool.ToolSet, error) {
	return createMCPToolSet(config)
}

// createKnowledgeSearchNodeFunc creates a NodeFunc for a standalone Knowledge Search node.
// The Knowledge Search node performs vector similarity search on a knowledge base and exposes
// the results under node_structured[nodeID].documents for downstream nodes.
func (c *Compiler) createKnowledgeSearchNodeFunc(node dsl.Node) (graph.NodeFunc, error) {
	engine := node.EngineNode

	parsed, err := knowledgeconfig.ParseNodeConfig(engine.Config)
	if err != nil {
		return nil, err
	}

	// Get the query CEL expression
	queryExpr := strings.TrimSpace(parsed.Query.Expression)
	if queryExpr == "" {
		return nil, fmt.Errorf("builtin.knowledge_search node %s: query.expression is required", node.ID)
	}

	// Convert parsed config to toolspec types and create Knowledge + Tool
	vsConfig, err := mapToVectorStoreConfig(parsed.VectorStore)
	if err != nil {
		return nil, fmt.Errorf("builtin.knowledge_search node %s: %w", node.ID, err)
	}

	embConfig, err := mapToEmbedderConfig(parsed.Embedder)
	if err != nil {
		return nil, fmt.Errorf("builtin.knowledge_search node %s: %w", node.ID, err)
	}

	// Create vector store and embedder
	vs, err := createVectorStore(vsConfig)
	if err != nil {
		return nil, fmt.Errorf("builtin.knowledge_search node %s: failed to create vector store: %w", node.ID, err)
	}

	emb, err := createEmbedder(embConfig)
	if err != nil {
		return nil, fmt.Errorf("builtin.knowledge_search node %s: failed to create embedder: %w", node.ID, err)
	}

	// Create Knowledge instance
	kb := knowledge.New(
		knowledge.WithVectorStore(vs),
		knowledge.WithEmbedder(emb),
	)

	// Create knowledge search tool with options
	var toolOpts []knowledgetool.Option
	if parsed.MaxResults > 0 {
		toolOpts = append(toolOpts, knowledgetool.WithMaxResults(parsed.MaxResults))
	}
	if parsed.MinScore > 0 {
		toolOpts = append(toolOpts, knowledgetool.WithMinScore(parsed.MinScore))
	}

	// Add conditioned filter if specified
	if parsed.ConditionedFilter != nil {
		converted := convertMapFilterCondition(parsed.ConditionedFilter)
		if converted != nil {
			toolOpts = append(toolOpts, knowledgetool.WithConditionedFilter(converted))
		}
	}

	// Create the knowledge search tool
	kbTool := knowledgetool.NewKnowledgeSearchTool(kb, toolOpts...)

	// Resolve the primary upstream node for this Knowledge Search node, if any,
	// so that we can build a stable input.* view from the immediate predecessor's
	// structured output (mirroring MCP/condition/while semantics).
	fromNodeID := ""
	if c.knowledgeSearchInputSource != nil {
		if src, ok := c.knowledgeSearchInputSource[node.ID]; ok {
			fromNodeID = src
		}
	}

	return func(ctx context.Context, state graph.State) (interface{}, error) {
		// Build the input view for CEL expression evaluation from the
		// immediate upstream node's structured output.
		var input any
		if fromNodeID != "" {
			input = buildNodeInputView(state, fromNodeID)
		}

		// Evaluate the query CEL expression
		queryResult, err := dslcel.Eval(queryExpr, state, input)
		if err != nil {
			return nil, fmt.Errorf("failed to evaluate query expression: %w", err)
		}

		// Convert query result to string
		var resolvedQuery string
		switch v := queryResult.(type) {
		case string:
			resolvedQuery = v
		default:
			resolvedQuery = fmt.Sprintf("%v", v)
		}

		log.Debugf("Knowledge Search node %s executing with query length: %d", node.ID, len(resolvedQuery))

		// Call the knowledge search tool
		callableTool, ok := kbTool.(ctool.CallableTool)
		if !ok {
			return nil, fmt.Errorf("knowledge search tool does not implement CallableTool interface")
		}

		// Prepare the tool input as JSON
		toolInput := map[string]string{"query": resolvedQuery}
		toolInputJSON, err := json.Marshal(toolInput)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal tool input: %w", err)
		}

		// Execute the tool
		result, err := callableTool.Call(ctx, toolInputJSON)
		if err != nil {
			log.Warnf("Knowledge Search node %s failed: %v", node.ID, err)
			// Return empty documents on error instead of failing the workflow
			result = &knowledgetool.KnowledgeSearchResponse{
				Documents: []*knowledgetool.DocumentResult{},
				Message:   fmt.Sprintf("Search failed: %v", err),
			}
		}

		// Convert result to output format
		var entry map[string]any
		switch r := result.(type) {
		case *knowledgetool.KnowledgeSearchResponse:
			documents := make([]map[string]any, 0, len(r.Documents))
			for _, doc := range r.Documents {
				docMap := map[string]any{
					"text":  doc.Text,
					"score": doc.Score,
				}
				if doc.Metadata != nil {
					docMap["metadata"] = doc.Metadata
				}
				documents = append(documents, docMap)
			}
			entry = map[string]any{
				"documents": documents,
			}
			if r.Message != "" {
				entry["message"] = r.Message
			}
		default:
			// Fallback: try to convert result directly
			entry = map[string]any{
				"documents": []map[string]any{},
				"message":   "Unexpected result type",
			}
		}

		log.Infof("Knowledge Search node %s returned %d documents", node.ID, len(entry["documents"].([]map[string]any)))

		// Merge with existing node_structured cache (if any) to avoid clobbering
		// structured outputs from other nodes.
		nodeStructured := map[string]any{}
		if existingRaw, ok := state["node_structured"]; ok {
			if existingMap, ok := existingRaw.(map[string]any); ok && existingMap != nil {
				for k, v := range existingMap {
					nodeStructured[k] = v
				}
			}
		}

		nodeStructured[node.ID] = entry

		return graph.State{
			"node_structured": nodeStructured,
		}, nil
	}, nil
}

// mapToVectorStoreConfig converts a map[string]any to toolspec.VectorStoreConfig.
func mapToVectorStoreConfig(m map[string]any) (*toolspec.VectorStoreConfig, error) {
	if m == nil {
		return nil, fmt.Errorf("vector_store config is required")
	}

	cfg := &toolspec.VectorStoreConfig{}

	if t, ok := m["type"].(string); ok {
		cfg.Type = toolspec.VectorStoreType(t)
	} else {
		return nil, fmt.Errorf("vector_store.type is required")
	}

	// Common fields
	if v, ok := m["host"].(string); ok {
		cfg.Host = v
	}
	if v, ok := m["port"].(float64); ok {
		cfg.Port = int(v)
	} else if v, ok := m["port"].(int); ok {
		cfg.Port = v
	}
	if v, ok := m["user"].(string); ok {
		cfg.User = v
	}
	if v, ok := m["password"].(string); ok {
		cfg.Password = v
	}
	if v, ok := m["database"].(string); ok {
		cfg.Database = v
	}
	if v, ok := m["table"].(string); ok {
		cfg.Table = v
	}
	if v, ok := m["dimension"].(float64); ok {
		cfg.Dimension = int(v)
	} else if v, ok := m["dimension"].(int); ok {
		cfg.Dimension = v
	}
	if v, ok := m["ssl_mode"].(string); ok {
		cfg.SSLMode = v
	}
	if v, ok := m["address"].(string); ok {
		cfg.Address = v
	}
	if v, ok := m["collection"].(string); ok {
		cfg.Collection = v
	}
	if v, ok := m["index"].(string); ok {
		cfg.Index = v
	}
	if v, ok := m["url"].(string); ok {
		cfg.URL = v
	}

	// Handle addresses array
	if addrs, ok := m["addresses"].([]any); ok {
		for _, addr := range addrs {
			if s, ok := addr.(string); ok {
				cfg.Addresses = append(cfg.Addresses, s)
			}
		}
	} else if addrs, ok := m["addresses"].([]string); ok {
		cfg.Addresses = addrs
	}

	return cfg, nil
}

// mapToEmbedderConfig converts a map[string]any to toolspec.EmbedderConfig.
func mapToEmbedderConfig(m map[string]any) (*toolspec.EmbedderConfig, error) {
	if m == nil {
		return nil, fmt.Errorf("embedder config is required")
	}

	cfg := &toolspec.EmbedderConfig{}

	if t, ok := m["type"].(string); ok {
		cfg.Type = toolspec.EmbedderType(t)
	} else {
		return nil, fmt.Errorf("embedder.type is required")
	}

	if v, ok := m["api_key"].(string); ok {
		cfg.APIKey = v
	}
	if v, ok := m["base_url"].(string); ok {
		cfg.BaseURL = v
	}
	if v, ok := m["model"].(string); ok {
		cfg.Model = v
	}
	if v, ok := m["dimensions"].(float64); ok {
		cfg.Dimensions = int(v)
	} else if v, ok := m["dimensions"].(int); ok {
		cfg.Dimensions = v
	}

	// HuggingFace specific fields
	if v, ok := m["normalize"].(bool); ok {
		cfg.Normalize = v
	}
	if v, ok := m["prompt_name"].(string); ok {
		cfg.PromptName = v
	}
	if v, ok := m["truncate"].(bool); ok {
		cfg.Truncate = v
	}
	if v, ok := m["truncation_direction"].(string); ok {
		cfg.TruncationDirection = v
	}
	if v, ok := m["embed_route"].(string); ok {
		cfg.EmbedRoute = v
	}

	return cfg, nil
}

// convertMapFilterCondition converts a map-based filter condition to searchfilter.UniversalFilterCondition.
func convertMapFilterCondition(m map[string]any) *searchfilter.UniversalFilterCondition {
	if m == nil {
		return nil
	}

	result := &searchfilter.UniversalFilterCondition{}

	if field, ok := m["field"].(string); ok {
		result.Field = field
	}
	if op, ok := m["operator"].(string); ok {
		result.Operator = op
	}

	// Handle logical operators (and/or) - Value should be array of conditions
	if result.Operator == "and" || result.Operator == "or" {
		if conditions, ok := m["value"].([]any); ok {
			subConditions := make([]*searchfilter.UniversalFilterCondition, 0, len(conditions))
			for _, c := range conditions {
				if condMap, ok := c.(map[string]any); ok {
					subCond := convertMapFilterCondition(condMap)
					if subCond != nil {
						subConditions = append(subConditions, subCond)
					}
				}
			}
			result.Value = subConditions
		}
	} else {
		// For comparison operators, keep the value as-is
		result.Value = m["value"]
	}

	return result
}
