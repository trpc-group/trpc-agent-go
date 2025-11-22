//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
package dsl

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/dsl/condition"
	"trpc.group/trpc-go/trpc-agent-go/dsl/registry"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/mcp"
)

// Compiler compiles DSL workflows into executable StateGraphs.
// This is the core of the DSL system, transforming declarative JSON
// into imperative Go code that can be executed by trpc-agent-go.
type Compiler struct {
	registry        *registry.Registry
	modelRegistry   *registry.ModelRegistry
	toolRegistry    *registry.ToolRegistry
	toolSetRegistry *registry.ToolSetRegistry
	reducerRegistry *registry.ReducerRegistry
	agentRegistry   *registry.AgentRegistry
	schemaInference *SchemaInference
}

// NewCompiler creates a new DSL compiler.
func NewCompiler(reg *registry.Registry) *Compiler {
	c := &Compiler{
		registry:        reg,
		modelRegistry:   registry.NewModelRegistry(),
		reducerRegistry: registry.NewReducerRegistry(),
		agentRegistry:   registry.NewAgentRegistry(),
		schemaInference: NewSchemaInference(reg),
	}
	// Pass reducer registry to schema inference
	c.schemaInference.reducerRegistry = c.reducerRegistry
	return c
}

// WithModelRegistry sets the model registry for the compiler.
// This allows the compiler to resolve model references in LLM nodes.
func (c *Compiler) WithModelRegistry(modelRegistry *registry.ModelRegistry) *Compiler {
	c.modelRegistry = modelRegistry
	return c
}

// WithToolRegistry sets the tool registry for the compiler.
// This allows the compiler to resolve tool references in LLM nodes.
func (c *Compiler) WithToolRegistry(toolRegistry *registry.ToolRegistry) *Compiler {
	c.toolRegistry = toolRegistry
	return c
}

// WithToolSetRegistry sets the toolset registry for the compiler.
// This allows the compiler to resolve toolset references in LLM nodes.
func (c *Compiler) WithToolSetRegistry(toolSetRegistry *registry.ToolSetRegistry) *Compiler {
	c.toolSetRegistry = toolSetRegistry
	return c
}

// WithReducerRegistry sets the reducer registry for the compiler.
// This allows the compiler to resolve reducer references in state schema inference.
func (c *Compiler) WithReducerRegistry(reducerRegistry *registry.ReducerRegistry) *Compiler {
	c.reducerRegistry = reducerRegistry
	// Update schema inference to use the new reducer registry
	c.schemaInference.reducerRegistry = reducerRegistry
	return c
}

// ModelRegistry returns the model registry used by the compiler.
func (c *Compiler) ModelRegistry() *registry.ModelRegistry {
	return c.modelRegistry
}

// ToolRegistry returns the tool registry used by the compiler.
func (c *Compiler) ToolRegistry() *registry.ToolRegistry {
	return c.toolRegistry
}

// WithAgentRegistry sets the agent registry for the compiler.
// This allows the compiler to resolve agent references in agent nodes.
func (c *Compiler) WithAgentRegistry(agentRegistry *registry.AgentRegistry) *Compiler {
	c.agentRegistry = agentRegistry
	return c
}

// AgentRegistry returns the agent registry used by the compiler.
func (c *Compiler) AgentRegistry() *registry.AgentRegistry {
	return c.agentRegistry
}

// Compile compiles an engine-level workflow into an executable StateGraph.
// The workflow here is the engine DSL representation without any UI-specific
// concepts such as positions or visual layout.
func (c *Compiler) Compile(workflow *Workflow) (*graph.Graph, error) {
	if workflow == nil {
		return nil, fmt.Errorf("workflow is nil")
	}

	// Step 1: Infer State Schema from components
	schema, err := c.schemaInference.InferSchema(workflow)
	if err != nil {
		return nil, fmt.Errorf("schema inference failed: %w", err)
	}

	// Step 2: Create StateGraph
	stateGraph := graph.NewStateGraph(schema)

	// Step 3: Add all nodes
	for _, node := range workflow.Nodes {
		nodeFunc, err := c.createNodeFunc(node)
		if err != nil {
			return nil, fmt.Errorf("failed to create node %s: %w", node.ID, err)
		}

		stateGraph.AddNode(node.ID, nodeFunc)
	}

	// Step 4: Add edges
	for _, edge := range workflow.Edges {
		stateGraph.AddEdge(edge.Source, edge.Target)
	}

	// Step 5: Add conditional edges
	for _, condEdge := range workflow.ConditionalEdges {
		// Handle tool_routing specially
		if condEdge.Condition.Type == "tool_routing" {
			if err := c.addToolRoutingEdge(stateGraph, condEdge); err != nil {
				return nil, fmt.Errorf("failed to add tool routing edge %s: %w", condEdge.ID, err)
			}
			continue
		}

		// Handle regular conditional edges
		condFunc, err := c.createConditionalFunc(condEdge)
		if err != nil {
			return nil, fmt.Errorf("failed to create conditional edge %s: %w", condEdge.ID, err)
		}

		stateGraph.AddConditionalEdges(condEdge.From, condFunc, condEdge.Condition.Routes)
	}

	// Step 6: Set entry point
	stateGraph.SetEntryPoint(workflow.EntryPoint)

	// Step 7: Set finish point (if specified)
	if workflow.FinishPoint != "" {
		stateGraph.SetFinishPoint(workflow.FinishPoint)
	}

	// Step 8: Compile the graph
	compiledGraph, err := stateGraph.Compile()
	if err != nil {
		return nil, fmt.Errorf("graph compilation failed: %w", err)
	}

	return compiledGraph, nil
}

// createNodeFunc creates a NodeFunc for an engine-level node instance.
func (c *Compiler) createNodeFunc(node Node) (graph.NodeFunc, error) {
	engine := node.EngineNode

	// Handle code components
	if engine.Component.Type == "code" {
		return c.createCodeNodeFunc(node)
	}

	// Handle LLM components specially (use AddLLMNode pattern)
	if engine.Component.Ref == "builtin.llm" {
		return c.createLLMNodeFunc(node)
	}

	// Handle Tools components specially (use AddToolsNode pattern)
	if engine.Component.Ref == "builtin.tools" {
		return c.createToolsNodeFunc(node)
	}

	// Handle LLMAgent components specially (dynamically create LLMAgent)
	if engine.Component.Ref == "builtin.llmagent" {
		return c.createLLMAgentNodeFunc(node)
	}

	// Get component from registry
	component, exists := c.registry.Get(engine.Component.Ref)
	if !exists {
		return nil, fmt.Errorf("component %s not found in registry", engine.Component.Ref)
	}

	// Create a closure that captures the component and config
	config := registry.ComponentConfig(engine.Config)

	return func(ctx context.Context, state graph.State) (interface{}, error) {
		// Execute the component
		result, err := component.Execute(ctx, config, state)
		if err != nil {
			return nil, fmt.Errorf("component %s execution failed: %w", engine.Component.Ref, err)
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

// createLLMNodeFunc creates a NodeFunc for an LLM component.
// This uses the AddLLMNode pattern from trpc-agent-go, where the model instance
// is obtained from ModelRegistry and passed via closure, not through state.
func (c *Compiler) createLLMNodeFunc(node Node) (graph.NodeFunc, error) {
	engine := node.EngineNode

	// Get model_name from config
	modelName, ok := engine.Config["model_name"].(string)
	if !ok || modelName == "" {
		return nil, fmt.Errorf("model_name is required in LLM node config")
	}

	// Get model from registry
	if c.modelRegistry == nil {
		return nil, fmt.Errorf("model registry is not set, use WithModelRegistry() to set it")
	}

	llmModel, err := c.modelRegistry.Get(modelName)
	if err != nil {
		return nil, fmt.Errorf("failed to get model %q from registry: %w", modelName, err)
	}

	// Get instruction from config (optional)
	instruction := ""
	if inst, ok := engine.Config["instruction"].(string); ok {
		instruction = inst
	}

	// Get tools from config (optional)
	// Tools can be specified as:
	// 1. A list of tool names (strings) - resolved from ToolRegistry
	// 2. "*" - use all tools from ToolRegistry
	tools := make(map[string]tool.Tool)

	if c.toolRegistry != nil {
		if toolsConfig, ok := engine.Config["tools"]; ok {
			switch v := toolsConfig.(type) {
			case string:
				// "*" means all tools
				if v == "*" {
					tools = c.toolRegistry.GetAll()
				} else {
					// Single tool name
					if t, err := c.toolRegistry.Get(v); err == nil {
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
					if resolvedTools, err := c.toolRegistry.GetMultiple(toolNames); err == nil {
						tools = resolvedTools
					}
				}
			case []string:
				// List of tool names (already strings)
				if resolvedTools, err := c.toolRegistry.GetMultiple(v); err == nil {
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
func (c *Compiler) createToolsNodeFunc(node Node) (graph.NodeFunc, error) {
	engine := node.EngineNode

	// Get tools from config (optional)
	// Tools can be specified as:
	// 1. A list of tool names (strings) - resolved from ToolRegistry
	// 2. "*" - use all tools from ToolRegistry
	tools := make(map[string]tool.Tool)

	if c.toolRegistry != nil {
		if toolsConfig, ok := engine.Config["tools"]; ok {
			switch v := toolsConfig.(type) {
			case string:
				// "*" means all tools
				if v == "*" {
					tools = c.toolRegistry.GetAll()
				} else {
					// Single tool name
					if t, err := c.toolRegistry.Get(v); err == nil {
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
					if resolvedTools, err := c.toolRegistry.GetMultiple(toolNames); err == nil {
						tools = resolvedTools
					}
				}
			case []string:
				// List of tool names (already strings)
				if resolvedTools, err := c.toolRegistry.GetMultiple(v); err == nil {
					tools = resolvedTools
				}
			}
		} else {
			// If no tools config specified, use all tools from registry
			tools = c.toolRegistry.GetAll()
		}
	}

	// Use graph.NewToolsNodeFunc to create the NodeFunc
	// This follows the same pattern as AddToolsNode
	return graph.NewToolsNodeFunc(tools), nil
}

// createCodeNodeFunc creates a NodeFunc for a code component.
func (c *Compiler) createCodeNodeFunc(node Node) (graph.NodeFunc, error) {
	engine := node.EngineNode

	if engine.Component.Code == nil {
		return nil, fmt.Errorf("code config is nil")
	}

	// TODO: Implement code executor integration
	// For now, return a placeholder that returns an error
	codeConfig := engine.Component.Code

	return func(ctx context.Context, state graph.State) (interface{}, error) {
		return nil, fmt.Errorf("code executor not yet implemented (language: %s)", codeConfig.Language)
	}, nil
}

// createConditionalFunc creates a ConditionalFunc for a conditional edge.
func (c *Compiler) createConditionalFunc(condEdge ConditionalEdge) (graph.ConditionalFunc, error) {
	condition := condEdge.Condition

	switch condition.Type {
	case "builtin":
		return c.createBuiltinCondition(condition)
	case "expression":
		return c.createExpressionCondition(condition)
	case "function":
		return c.createFunctionCondition(condition)
	default:
		return nil, fmt.Errorf("unsupported condition type: %s", condition.Type)
	}
}

// createBuiltinCondition creates a condition function from a builtin structured condition.
func (c *Compiler) createBuiltinCondition(cond Condition) (graph.ConditionalFunc, error) {
	if cond.Builtin == nil {
		return nil, fmt.Errorf("builtin condition configuration is nil")
	}

	// Convert DSL BuiltinCondition to condition package type
	builtinCond := &condition.BuiltinCondition{
		Conditions:      make([]condition.ConditionRule, len(cond.Builtin.Conditions)),
		LogicalOperator: cond.Builtin.LogicalOperator,
	}

	for i, rule := range cond.Builtin.Conditions {
		builtinCond.Conditions[i] = condition.ConditionRule{
			Variable: rule.Variable,
			Operator: rule.Operator,
			Value:    rule.Value,
		}
	}

	return func(ctx context.Context, state graph.State) (string, error) {
		// Evaluate the builtin condition
		result, err := condition.Evaluate(ctx, state, builtinCond)
		if err != nil {
			return "", fmt.Errorf("failed to evaluate builtin condition: %w", err)
		}

		// Map boolean result to route
		routeKey := "false"
		if result {
			routeKey = "true"
		}

		// Look up the target node in routes
		target, ok := cond.Routes[routeKey]
		if !ok {
			// If no route found, try default
			if cond.Default != "" {
				return cond.Default, nil
			}
			return "", fmt.Errorf("no route found for result '%s' and no default specified", routeKey)
		}

		return target, nil
	}, nil
}

// createExpressionCondition creates a condition function from an expression.
func (c *Compiler) createExpressionCondition(condition Condition) (graph.ConditionalFunc, error) {
	// TODO: Implement expression evaluation
	// For now, return a simple condition that always returns the default route

	return func(ctx context.Context, state graph.State) (string, error) {
		// Placeholder: always return default route
		if condition.Default != "" {
			return condition.Default, nil
		}

		// If no default, return the first route
		for _, target := range condition.Routes {
			return target, nil
		}

		return "", fmt.Errorf("no valid route found")
	}, nil
}

// createFunctionCondition creates a condition function from a function reference.
func (c *Compiler) createFunctionCondition(condition Condition) (graph.ConditionalFunc, error) {
	// Get the function component from registry
	functionRef := condition.Function
	if functionRef == "" {
		return nil, fmt.Errorf("function reference is empty")
	}

	component, exists := c.registry.Get(functionRef)
	if !exists {
		return nil, fmt.Errorf("function '%s' not found in registry", functionRef)
	}

	return func(ctx context.Context, state graph.State) (string, error) {
		// Execute the function component
		result, err := component.Execute(ctx, registry.ComponentConfig{}, state)
		if err != nil {
			return "", fmt.Errorf("error executing function '%s': %w", functionRef, err)
		}

		// result should be graph.State (map[string]any), extract route from it
		resultState, ok := result.(graph.State)
		if !ok {
			return "", fmt.Errorf("function '%s' did not return graph.State", functionRef)
		}

		if route, ok := resultState["route"].(string); ok {
			return route, nil
		}

		return "", fmt.Errorf("function '%s' did not return a route in state", functionRef)
	}, nil
}

// addToolRoutingEdge adds a tool routing edge (AddToolsConditionalEdges pattern).
func (c *Compiler) addToolRoutingEdge(stateGraph *graph.StateGraph, condEdge ConditionalEdge) error {
	toolsNode := condEdge.Condition.ToolsNode
	next := condEdge.Condition.Next

	if toolsNode == "" {
		return fmt.Errorf("tools_node is required for tool_routing")
	}
	if next == "" {
		return fmt.Errorf("next is required for tool_routing")
	}

	// Use AddToolsConditionalEdges from graph package
	stateGraph.AddToolsConditionalEdges(condEdge.From, toolsNode, next)

	return nil
}

// applyOutputMapping applies output mapping from DSL node outputs configuration.
// It transforms the component's output according to the target specifications.
func (c *Compiler) applyOutputMapping(result interface{}, outputs []NodeIO, component registry.Component) (interface{}, error) {
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
				fmt.Printf("⚠️  [DEBUG] Optional output '%s' not found in result state, skipping\n", sourceFieldName)
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
func (c *Compiler) createLLMAgentNodeFunc(node Node) (graph.NodeFunc, error) {
	engine := node.EngineNode
	// Import required packages
	// Note: These are already imported at the top of the file

	// Extract and validate model_name
	modelName, ok := engine.Config["model_name"].(string)
	if !ok || modelName == "" {
		return nil, fmt.Errorf("model_name is required for builtin.llmagent")
	}

	// Get model from registry
	if c.modelRegistry == nil {
		return nil, fmt.Errorf("model registry is not set, use WithModelRegistry() to set it")
	}

	llmModel, err := c.modelRegistry.Get(modelName)
	if err != nil {
		return nil, fmt.Errorf("failed to get model %q from registry: %w", modelName, err)
	}

	// Get instruction from config (optional)
	instruction := ""
	if inst, ok := engine.Config["instruction"].(string); ok {
		instruction = inst
	}

	// Get description from config (optional)
	description := ""
	if desc, ok := engine.Config["description"].(string); ok {
		description = desc
	}

	// Get tools from config (optional)
	var tools []tool.Tool
	if c.toolRegistry != nil {
		if toolsConfig, ok := engine.Config["tools"]; ok {
			switch v := toolsConfig.(type) {
			case []interface{}:
				// List of tool names
				for _, toolNameInterface := range v {
					if toolName, ok := toolNameInterface.(string); ok {
						if t, err := c.toolRegistry.Get(toolName); err == nil {
							tools = append(tools, t)
						}
					}
				}
			case []string:
				// List of tool names (already strings)
				for _, toolName := range v {
					if t, err := c.toolRegistry.Get(toolName); err == nil {
						tools = append(tools, t)
					}
				}
			}
		}
	}

	// Get tool_sets from config (optional)
	var toolSets []tool.ToolSet
	if c.toolSetRegistry != nil {
		if toolSetsConfig, ok := engine.Config["tool_sets"]; ok {
			switch v := toolSetsConfig.(type) {
			case []interface{}:
				// List of toolset names
				for _, toolSetNameInterface := range v {
					if toolSetName, ok := toolSetNameInterface.(string); ok {
						if ts, err := c.toolSetRegistry.Get(toolSetName); err == nil {
							toolSets = append(toolSets, ts)
						}
					}
				}
			case []string:
				// List of toolset names (already strings)
				for _, toolSetName := range v {
					if ts, err := c.toolSetRegistry.Get(toolSetName); err == nil {
						toolSets = append(toolSets, ts)
					}
				}
			}
		}
	}

	// Get MCP tools from config (optional)
	var mcpToolSets []tool.ToolSet
	if mcpToolsConfig, ok := engine.Config["mcp_tools"]; ok {
		if mcpToolsList, ok := mcpToolsConfig.([]interface{}); ok {
			for _, mcpToolInterface := range mcpToolsList {
				if mcpToolConfig, ok := mcpToolInterface.(map[string]interface{}); ok {
					// Create MCP ToolSet from config
					if toolSet, err := c.createMCPToolSet(mcpToolConfig); err == nil {
						mcpToolSets = append(mcpToolSets, toolSet)
					} else {
						log.Warnf("Failed to create MCP toolset: %v", err)
					}
				}
			}
		}
	}

	// Get structured_output from config (optional)
	var structuredOutput map[string]any
	if so, ok := engine.Config["structured_output"].(map[string]any); ok {
		structuredOutput = so
	}

	// Build generation config
	var genConfig model.GenerationConfig
	hasGenConfig := false

	if temperature, ok := engine.Config["temperature"].(float64); ok {
		genConfig.Temperature = &temperature
		hasGenConfig = true
	}

	if maxTokens, ok := engine.Config["max_tokens"].(float64); ok {
		// JSON numbers are float64, convert to int
		tokens := int(maxTokens)
		genConfig.MaxTokens = &tokens
		hasGenConfig = true
	}

	if topP, ok := engine.Config["top_p"].(float64); ok {
		genConfig.TopP = &topP
		hasGenConfig = true
	}

	// Optional streaming flag (enable token streaming).
	if stream, ok := engine.Config["stream"].(bool); ok {
		genConfig.Stream = stream
		hasGenConfig = true
	}

	// Create the NodeFunc that will be executed
	return func(ctx context.Context, state graph.State) (interface{}, error) {

		// Import llmagent package
		// Note: Already imported at the top

		// Build LLMAgent options
		var opts []llmagent.Option

		// Set model
		opts = append(opts, llmagent.WithModel(llmModel))

		// Set instruction if provided
		if instruction != "" {
			opts = append(opts, llmagent.WithInstruction(instruction))
		}

		// Set description if provided
		if description != "" {
			opts = append(opts, llmagent.WithDescription(description))
		}

		// Set tools if provided
		if len(tools) > 0 {
			opts = append(opts, llmagent.WithTools(tools))
		}

		// Set tool sets if provided (from tool_sets config)
		if len(toolSets) > 0 {
			opts = append(opts, llmagent.WithToolSets(toolSets))
		}

		// Set MCP tool sets if provided (from mcp_tools config)
		if len(mcpToolSets) > 0 {
			opts = append(opts, llmagent.WithToolSets(mcpToolSets))
		}

		// Set structured_output if provided
		if len(structuredOutput) > 0 {
			opts = append(opts, llmagent.WithOutputSchema(structuredOutput))
			// Automatically set output_key to "output_parsed" when structured_output is configured
			// This allows conditions to access structured fields like: output_parsed.classification
			opts = append(opts, llmagent.WithOutputKey("output_parsed"))
		}

		// Set generation config if provided
		if hasGenConfig {
			opts = append(opts, llmagent.WithGenerationConfig(genConfig))
		}

		// Create LLMAgent
		agentName := fmt.Sprintf("llmagent_%s_%s", node.ID, modelName)
		llmAgent := llmagent.New(agentName, opts...)

		// Get parent invocation from context
		parentInvocation, ok := agent.InvocationFromContext(ctx)
		if !ok || parentInvocation == nil {
			return nil, fmt.Errorf("invocation not found in context")
		}

		// Extract execution context for event forwarding
		var parentEventChan chan<- *event.Event
		if execCtx, exists := state[graph.StateKeyExecContext]; exists {
			if execContext, ok := execCtx.(*graph.ExecutionContext); ok {
				parentEventChan = execContext.EventChan
			}
		}

		// Build invocation for the LLMAgent
		// Extract user input from state
		var userInput string
		if input, exists := state[graph.StateKeyUserInput]; exists {
			if inputStr, ok := input.(string); ok {
				userInput = inputStr
			}
		}

		// Extract session from state
		var sessionData *session.Session
		if sess, exists := state[graph.StateKeySession]; exists {
			if sessData, ok := sess.(*session.Session); ok {
				sessionData = sessData
			}
		}

		// Create invocation for the LLMAgent
		// Clone from parent invocation if available to preserve linkage
		var invocation *agent.Invocation
		if parentInvocation != nil {
			// Clone from parent with LLMAgent-specific settings
			invocation = parentInvocation.Clone(
				agent.WithInvocationAgent(llmAgent),
				agent.WithInvocationMessage(model.NewUserMessage(userInput)),
				agent.WithInvocationRunOptions(agent.RunOptions{RuntimeState: state}),
			)
		} else {
			// Create standalone invocation
			invocation = agent.NewInvocation(
				agent.WithInvocationAgent(llmAgent),
				agent.WithInvocationMessage(model.NewUserMessage(userInput)),
				agent.WithInvocationSession(sessionData),
				agent.WithInvocationRunOptions(agent.RunOptions{RuntimeState: state}),
			)
		}

		// Create new context with the invocation
		subCtx := agent.NewInvocationContext(ctx, invocation)

		// Run the agent with the new context
		agentEventChan, err := llmAgent.Run(subCtx, invocation)
		if err != nil {
			return nil, fmt.Errorf("failed to run LLM agent: %w", err)
		}

		// Process events: forward them to parent and extract final response.
		// This follows the same pattern as graph.processAgentEventStream.
		var lastResponse string
		var messages []model.Message
		var outputParsed any
		hasOutputParsed := false

		// Add timeout to detect hanging. We reset the timer whenever we
		// successfully receive an event, so the timeout only fires when
		// there has been *no* activity for the full duration.
		const llmAgentInactivityTimeout = 30 * time.Second
		timeout := time.NewTimer(llmAgentInactivityTimeout)
		defer timeout.Stop()

		resetTimer := func() {
			if !timeout.Stop() {
				select {
				case <-timeout.C:
				default:
				}
			}
			timeout.Reset(llmAgentInactivityTimeout)
		}

		for {
			select {
			case ev, ok := <-agentEventChan:
				if !ok {
					// Channel closed
					goto done
				}

				// We received an event, so reset inactivity timer.
				resetTimer()

				// Handle errors
				if ev.Error != nil {
					return nil, fmt.Errorf("LLM agent error: %s", ev.Error.Message)
				}

				// Notify completion if required
				// This is critical for LLMAgent's flow to continue
				if ev.RequiresCompletion {
					completionID := agent.GetAppendEventNoticeKey(ev.ID)
					if err := invocation.NotifyCompletion(subCtx, completionID); err != nil {
						log.Warnf("Failed to notify completion for %s: %v", completionID, err)
					}
				}

				// Forward the event to the parent event channel
				if parentEventChan != nil {
					if err := event.EmitEvent(ctx, parentEventChan, ev); err != nil {
						return nil, fmt.Errorf("failed to forward event: %w", err)
					}
				}

				// Extract last response from any event with content
				if ev.Response != nil && len(ev.Response.Choices) > 0 {
					msg := ev.Response.Choices[0].Message
					if msg.Content != "" {
						lastResponse = msg.Content
						// Collect message for state
						if msg.Role != "" {
							messages = append(messages, msg)
						}
					}
				}

			case <-timeout.C:
				return nil, fmt.Errorf("timeout waiting for LLM agent to complete")
			}
		}

	done:

		// If structured_output is configured, extract the structured JSON
		// content from the final lastResponse text and expose it as
		// output_parsed in graph.State. This keeps all structured_output
		// handling at the DSL layer without depending on internal flow
		// processor details.
		if len(structuredOutput) > 0 && !hasOutputParsed && lastResponse != "" {
			if jsonText, ok := extractFirstJSONObjectFromText(lastResponse); ok {
				var parsed any
				if err := json.Unmarshal([]byte(jsonText), &parsed); err != nil {
					log.Warnf("Failed to parse structured_output JSON from lastResponse: %v", err)
				} else {
					outputParsed = parsed
					hasOutputParsed = true
				}
			}
		}

		// Build the state delta returned to the graph executor.
		// We explicitly expose:
		//   - last_response / messages (for downstream LLM nodes)
		//   - output_parsed (parsed JSON structured output), when present.
		result := graph.State{}
		if lastResponse != "" {
			result[graph.StateKeyLastResponse] = lastResponse
		}
		if len(messages) > 0 {
			result[graph.StateKeyMessages] = messages
		}
		if hasOutputParsed {
			result["output_parsed"] = outputParsed
		}
		if len(result) == 0 {
			return nil, nil
		}
		return result, nil
	}, nil
}

// createMCPToolSet creates an MCP ToolSet from DSL configuration.
func (c *Compiler) createMCPToolSet(config map[string]interface{}) (tool.ToolSet, error) {
	// Extract transport type
	transport, ok := config["transport"].(string)
	if !ok || transport == "" {
		return nil, fmt.Errorf("transport is required in MCP tool config")
	}

	// Build connection config
	connConfig := mcp.ConnectionConfig{
		Transport: transport,
	}

	// Extract timeout (default to 10 seconds)
	timeout := 10 * time.Second
	if timeoutVal, ok := config["timeout"]; ok {
		switch v := timeoutVal.(type) {
		case float64:
			timeout = time.Duration(v) * time.Second
		case int:
			timeout = time.Duration(v) * time.Second
		}
	}
	connConfig.Timeout = timeout

	// Configure based on transport type
	switch transport {
	case "stdio":
		// Extract command and args
		command, ok := config["command"].(string)
		if !ok || command == "" {
			return nil, fmt.Errorf("command is required for stdio transport")
		}
		connConfig.Command = command

		// Extract args (optional)
		if argsVal, ok := config["args"]; ok {
			if argsList, ok := argsVal.([]interface{}); ok {
				args := make([]string, 0, len(argsList))
				for _, arg := range argsList {
					if argStr, ok := arg.(string); ok {
						args = append(args, argStr)
					}
				}
				connConfig.Args = args
			}
		}

	case "streamable_http", "sse":
		// Extract server URL
		serverURL, ok := config["server_url"].(string)
		if !ok || serverURL == "" {
			return nil, fmt.Errorf("server_url is required for %s transport", transport)
		}
		connConfig.ServerURL = serverURL

		// Extract headers (optional)
		if headersVal, ok := config["headers"]; ok {
			if headersMap, ok := headersVal.(map[string]interface{}); ok {
				headers := make(map[string]string)
				for k, v := range headersMap {
					if vStr, ok := v.(string); ok {
						headers[k] = vStr
					}
				}
				connConfig.Headers = headers
			}
		}

	default:
		return nil, fmt.Errorf("unsupported transport type: %s", transport)
	}

	// Build MCP options
	var mcpOpts []mcp.ToolSetOption

	// Extract tool filter (optional)
	if toolFilterVal, ok := config["tool_filter"]; ok {
		if toolFilterList, ok := toolFilterVal.([]interface{}); ok {
			toolNames := make([]string, 0, len(toolFilterList))
			for _, name := range toolFilterList {
				if nameStr, ok := name.(string); ok {
					toolNames = append(toolNames, nameStr)
				}
			}
			if len(toolNames) > 0 {
				mcpOpts = append(mcpOpts, mcp.WithToolFilter(mcp.NewIncludeFilter(toolNames...)))
			}
		}
	}

	// Create and return the MCP ToolSet
	toolSet := mcp.NewMCPToolSet(connConfig, mcpOpts...)
	return toolSet, nil
}

// extractFirstJSONObjectFromText tries to extract the first balanced top-level
// JSON object or array from the given text. This mirrors the behavior of the
// internal flow processor's extraction logic but keeps the dependency entirely
// within the DSL layer.
func extractFirstJSONObjectFromText(s string) (string, bool) {
	start := findJSONStartInText(s)
	if start == -1 {
		return "", false
	}
	return scanBalancedJSONInText(s, start)
}

// findJSONStartInText finds the index of the first opening brace/bracket.
func findJSONStartInText(s string) int {
	for i := 0; i < len(s); i++ {
		if s[i] == '{' || s[i] == '[' {
			return i
		}
	}
	return -1
}

// scanBalancedJSONInText scans for a balanced JSON object/array starting at start.
func scanBalancedJSONInText(s string, start int) (string, bool) {
	stack := make([]byte, 0, 8)
	inString := false
	escaped := false

	for i := start; i < len(s); i++ {
		c := s[i]

		if escaped {
			escaped = false
			continue
		}

		if inString {
			switch c {
			case '\\':
				escaped = true
			case '"':
				inString = false
			}
			continue
		}

		switch c {
		case '"':
			inString = true
		case '{', '[':
			stack = append(stack, c)
		case '}', ']':
			if len(stack) == 0 {
				return "", false
			}
			top := stack[len(stack)-1]
			if (top == '{' && c == '}') || (top == '[' && c == ']') {
				stack = stack[:len(stack)-1]
				if len(stack) == 0 {
					return s[start : i+1], true
				}
			} else {
				return "", false
			}
		}
	}
	return "", false
}
