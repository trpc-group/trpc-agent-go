//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
package dsl

import (
	"fmt"
	"reflect"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/dsl/registry"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// SchemaInference automatically infers State Schema from workflow components.
// This eliminates the need for users to manually define State Schema.
//
// The inference process:
// 1. Collect all input/output parameters from all components
// 2. Merge parameters with the same name (must have compatible types)
// 3. Determine the appropriate reducer for each field (from ReducerRegistry)
// 4. Generate the final StateSchema
type SchemaInference struct {
	registry        *registry.Registry
	reducerRegistry *registry.ReducerRegistry
}

// NewSchemaInference creates a new schema inference engine.
func NewSchemaInference(reg *registry.Registry) *SchemaInference {
	return &SchemaInference{
		registry:        reg,
		reducerRegistry: registry.NewReducerRegistry(), // Default reducer registry
	}
}

// addBuiltinFields adds framework built-in fields to the schema.
// These fields are commonly used by LLM nodes, Tools nodes, and the framework itself.
func (si *SchemaInference) addBuiltinFields(schema *graph.StateSchema) {
	// Add messages field (used by LLM and Tools nodes)
	schema.AddField(graph.StateKeyMessages, graph.StateField{
		Type:    reflect.TypeOf([]model.Message{}),
		Reducer: graph.MessageReducer,
		Default: func() any { return []model.Message{} },
	})

	// Add user_input field (consumed by LLM nodes)
	schema.AddField(graph.StateKeyUserInput, graph.StateField{
		Type:    reflect.TypeOf(""),
		Reducer: graph.DefaultReducer,
	})

	// Add last_response field (set by LLM nodes)
	schema.AddField(graph.StateKeyLastResponse, graph.StateField{
		Type:    reflect.TypeOf(""),
		Reducer: graph.DefaultReducer,
	})

	// Add node_responses field (set by LLM nodes, merged across parallel branches)
	schema.AddField(graph.StateKeyNodeResponses, graph.StateField{
		Type:    reflect.TypeOf(map[string]any{}),
		Reducer: graph.MergeReducer,
		Default: func() any { return map[string]any{} },
	})
}

// InferSchema infers the State Schema from a workflow (engine DSL).
// This method only cares about executable semantics and is intentionally
// decoupled from any UI-specific concepts.
func (si *SchemaInference) InferSchema(workflow *Workflow) (*graph.StateSchema, error) {
	paramMap, err := si.buildParameterMap(workflow)
	if err != nil {
		return nil, err
	}

	schema := graph.NewStateSchema()

	// Add framework built-in fields first
	si.addBuiltinFields(schema)

	// Convert parameter map to StateSchema
	for name, param := range paramMap {
		reducer := si.getReducer(param.Reducer)

		schema.AddField(name, graph.StateField{
			Type:     param.GoType,
			Reducer:  reducer,
			Required: param.Required,
			Default:  param.DefaultFunc,
		})
	}

	return schema, nil
}

// FieldUsage describes how a particular state field is used in the workflow.
// Writers and Readers contain node IDs that write to / read from this field.
// Kind and SchemaRef/JSONSchema provide front-end-friendly type information:
//   - Kind: "string" | "number" | "boolean" | "object" | "array" | "opaque"
//   - SchemaRef / JSONSchema: optional, for complex fields where we know the shape
type FieldUsage struct {
	Name       string         `json:"name"`
	Type       string         `json:"type"`
	Kind       string         `json:"kind,omitempty"`
	SchemaRef  string         `json:"schema_ref,omitempty"`
	JSONSchema map[string]any `json:"json_schema,omitempty"`
	Writers    []string       `json:"writers,omitempty"`
	Readers    []string       `json:"readers,omitempty"`
}

// InferSchemaAndUsage returns both the inferred StateSchema and field usage
// information (which nodes read/write which fields). This is intended for
// platform / UI layers that need to present variable suggestions.
func (si *SchemaInference) InferSchemaAndUsage(workflow *Workflow) (*graph.StateSchema, map[string]FieldUsage, error) {
	paramMap, err := si.buildParameterMap(workflow)
	if err != nil {
		return nil, nil, err
	}

	// Enrich parameter info with component-level context where useful (e.g., structured_output).
	si.attachComponentContext(workflow, paramMap)

	schema := graph.NewStateSchema()
	si.addBuiltinFields(schema)

	usage := make(map[string]FieldUsage, len(paramMap))

	for name, param := range paramMap {
		reducer := si.getReducer(param.Reducer)

		schema.AddField(name, graph.StateField{
			Type:     param.GoType,
			Reducer:  reducer,
			Required: param.Required,
			Default:  param.DefaultFunc,
		})

		fieldUsage := FieldUsage{
			Name:       name,
			Type:       param.GoType.String(),
			Kind:       param.Kind,
			SchemaRef:  param.SchemaRef,
			JSONSchema: param.JSONSchema,
		}

		for _, src := range param.Sources {
			switch {
			case strings.HasPrefix(src, "output:"),
				strings.HasPrefix(src, "dsl:"),
				strings.HasPrefix(src, "code:"):
				// Writers
				parts := strings.SplitN(src, ":", 2)
				if len(parts) == 2 && parts[1] != "" {
					fieldUsage.Writers = append(fieldUsage.Writers, parts[1])
				}
			case strings.HasPrefix(src, "input:"):
				// Readers
				parts := strings.SplitN(src, ":", 2)
				if len(parts) == 2 && parts[1] != "" {
					fieldUsage.Readers = append(fieldUsage.Readers, parts[1])
				}
			default:
				// legacy tags ("input", "output") without node IDs are ignored for usage
			}
		}

		usage[name] = fieldUsage
	}

	return schema, usage, nil
}

// buildParameterMap collects all input/output parameters from components and DSL NodeIO
// into a map keyed by state field name.
func (si *SchemaInference) buildParameterMap(workflow *Workflow) (map[string]*ParameterInfo, error) {
	parameterMap := make(map[string]*ParameterInfo)

	for _, node := range workflow.Nodes {
		engine := node.EngineNode

		// Handle code components specially
		if engine.Component.Type == "code" {
			if err := si.addCodeComponentParameters(node, parameterMap); err != nil {
				return nil, fmt.Errorf("node %s: %w", node.ID, err)
			}
			continue
		}

		// Get component metadata
		component, exists := si.registry.Get(engine.Component.Ref)
		if !exists {
			// For builtin components (llm, tools), we don't have them in registry
			// but we still need to process their DSL-level outputs
			if engine.Component.Type == "component" && (engine.Component.Ref == "builtin.llm" || engine.Component.Ref == "builtin.tools") {
				// Process DSL-level outputs for builtin components
				if err := si.addDSLOutputs(node, parameterMap); err != nil {
					return nil, fmt.Errorf("node %s: %w", node.ID, err)
				}
				continue
			}
			return nil, fmt.Errorf("node %s: component %s not found", node.ID, engine.Component.Ref)
		}

		metadata := component.Metadata()

		// Add input parameters (mark as readers tied to this node)
		for _, input := range metadata.Inputs {
			if err := si.addParameter(parameterMap, input, fmt.Sprintf("input:%s", node.ID)); err != nil {
				return nil, fmt.Errorf("node %s: %w", node.ID, err)
			}
		}

		// Add output parameters from component metadata (writers tied to this node)
		for _, output := range metadata.Outputs {
			if err := si.addParameter(parameterMap, output, fmt.Sprintf("output:%s", node.ID)); err != nil {
				return nil, fmt.Errorf("node %s: %w", node.ID, err)
			}
		}

		// Add DSL-level output overrides (if specified)
		// These take precedence over component metadata outputs
		if err := si.addDSLOutputs(node, parameterMap); err != nil {
			return nil, fmt.Errorf("node %s: %w", node.ID, err)
		}
	}

	return parameterMap, nil
}

// ParameterInfo holds information about a parameter during inference.
type ParameterInfo struct {
	Name        string
	GoType      reflect.Type
	Reducer     string
	Required    bool
	DefaultFunc func() any
	Sources     []string          // Track which components use this parameter
	Kind        string            // High-level kind for front-end: string/number/boolean/object/array/opaque
	SchemaRef   string            // Optional logical schema identifier (for well-known types)
	JSONSchema  map[string]any    // Optional inline JSON Schema for complex fields (e.g., output_parsed)
}

// addParameter adds a parameter to the parameter map.
func (si *SchemaInference) addParameter(paramMap map[string]*ParameterInfo, param registry.ParameterSchema, source string) error {
	existing, exists := paramMap[param.Name]

	if !exists {
		// First time seeing this parameter
		var defaultFunc func() any
		if param.Default != nil {
			defaultValue := param.Default
			defaultFunc = func() any { return defaultValue }
		}

		kind, schemaRef := classifyGoType(param.GoType, param.Name)

		paramMap[param.Name] = &ParameterInfo{
			Name:        param.Name,
			GoType:      param.GoType,
			Reducer:     param.Reducer,
			Required:    param.Required,
			DefaultFunc: defaultFunc,
			Sources:     []string{source},
			Kind:        kind,
			SchemaRef:   schemaRef,
		}
		return nil
	}

	// Parameter already exists, check compatibility
	if existing.GoType != param.GoType {
		return fmt.Errorf("parameter %s has incompatible types: %v vs %v (sources: %v vs %s)",
			param.Name, existing.GoType, param.GoType, existing.Sources, source)
	}

	// Merge reducer (prefer more specific reducers)
	if param.Reducer != "" && param.Reducer != "default" {
		existing.Reducer = param.Reducer
	}

	// If any source requires it, mark as required
	if param.Required {
		existing.Required = true
	}

	existing.Sources = append(existing.Sources, source)
	return nil
}

// addCodeComponentParameters adds parameters from code components.
func (si *SchemaInference) addCodeComponentParameters(node Node, paramMap map[string]*ParameterInfo) error {
	engine := node.EngineNode

	if engine.Component.Code == nil {
		return nil
	}

	// For code components, we infer string type for all inputs/outputs
	// (actual type checking happens at runtime)
	stringType := reflect.TypeOf("")

	for _, inputName := range engine.Component.Code.Inputs {
		param := registry.ParameterSchema{
			Name:   inputName,
			GoType: stringType,
		}
		if err := si.addParameter(paramMap, param, fmt.Sprintf("code:%s", node.ID)); err != nil {
			return err
		}
	}

	for _, outputName := range engine.Component.Code.Outputs {
		param := registry.ParameterSchema{
			Name:   outputName,
			GoType: stringType,
		}
		if err := si.addParameter(paramMap, param, fmt.Sprintf("code:%s", node.ID)); err != nil {
			return err
		}
	}

	return nil
}

// addDSLOutputs adds output parameters from DSL node outputs configuration.
// This handles output remapping specified in the workflow DSL.
func (si *SchemaInference) addDSLOutputs(node Node, paramMap map[string]*ParameterInfo) error {
	engine := node.EngineNode

	if len(engine.Outputs) == 0 {
		return nil
	}

	for _, output := range engine.Outputs {
		// Determine the target field name
		targetFieldName := output.Name
		if output.Target != nil && output.Target.Type == "state" && output.Target.Field != "" {
			targetFieldName = output.Target.Field
		}

		// Infer Go type from the type string
		goType := inferGoType(output.Type)

		// Determine reducer
		reducer := output.Reducer
		if reducer == "" {
			// Auto-infer reducer from type
			reducer = inferReducerFromType(output.Type)
		}

		// Create parameter schema
		param := registry.ParameterSchema{
			Name:     targetFieldName,
			GoType:   goType,
			Reducer:  reducer,
			Required: output.Required,
		}

		// Add to parameter map
		if err := si.addParameter(paramMap, param, fmt.Sprintf("dsl:%s", node.ID)); err != nil {
			return err
		}
	}

	return nil
}

// inferReducerFromType infers the appropriate reducer from a type string.
func inferReducerFromType(typeStr string) string {
	switch typeStr {
	case "[]string":
		return "string_slice"
	case "[]model.Message":
		return "message"
	case "[]map[string]any":
		return "append_map_slice"
	case "map[string]any", "map[string]interface{}":
		return "merge"
	case "int":
		return "int_sum"
	default:
		return "default"
	}
}

// inferGoType infers Go type from a type string.
func inferGoType(typeStr string) reflect.Type {
	switch typeStr {
	case "string":
		return reflect.TypeOf("")
	case "int":
		return reflect.TypeOf(0)
	case "float64", "number":
		return reflect.TypeOf(0.0)
	case "bool":
		return reflect.TypeOf(false)
	case "[]string":
		return reflect.TypeOf([]string{})
	case "[]int":
		return reflect.TypeOf([]int{})
	case "[]model.Message":
		return reflect.TypeOf([]model.Message{})
	case "map[string]any", "map[string]interface{}":
		return reflect.TypeOf(map[string]any{})
	case "[]map[string]any":
		return reflect.TypeOf([]map[string]any{})
	default:
		// Default to string for unknown types
		return reflect.TypeOf("")
	}
}

// classifyGoType maps a Go reflect.Type (and optional field name) to a frontend-friendly kind/schemaRef.
func classifyGoType(t reflect.Type, fieldName string) (kind string, schemaRef string) {
	if t == nil {
		return "opaque", ""
	}

	switch t.Kind() {
	case reflect.String:
		return "string", ""
	case reflect.Bool:
		return "boolean", ""
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return "number", ""
	case reflect.Slice, reflect.Array:
		// For well-known slices, attach a logical schema ref.
		if t == reflect.TypeOf([]model.Message{}) {
			return "array", "graph.messages"
		}
		return "array", ""
	case reflect.Map, reflect.Struct:
		// Special-case structured_output field for LLMAgent: output_parsed
		if fieldName == "output_parsed" {
			return "object", "llmagent.output_parsed"
		}
		return "object", ""
	default:
		return "opaque", ""
	}
}


// getReducer returns the appropriate StateReducer for a reducer name.
// It first tries to resolve from the ReducerRegistry, then falls back to hardcoded built-in reducers.
func (si *SchemaInference) getReducer(reducerName string) graph.StateReducer {
	// If no reducer name specified, use default
	if reducerName == "" || reducerName == "default" {
		return graph.DefaultReducer
	}

	// Try to resolve from ReducerRegistry first
	if si.reducerRegistry != nil {
		if reducer, ok := si.reducerRegistry.Get(reducerName); ok {
			return reducer
		}
	}

	// Fallback to hardcoded built-in reducers for backward compatibility
	switch reducerName {
	case "append":
		return graph.AppendReducer
	case "message":
		return graph.MessageReducer
	case "merge":
		return graph.MergeReducer
	case "string_slice":
		return graph.StringSliceReducer
	default:
		// Unknown reducer, use default
		return graph.DefaultReducer
	}
}

func (si *SchemaInference) attachComponentContext(workflow *Workflow, paramMap map[string]*ParameterInfo) {
	if workflow == nil {
		return
	}

	// Build a quick index from node ID to node for later lookups.
	nodeByID := make(map[string]Node, len(workflow.Nodes))
	for _, node := range workflow.Nodes {
		nodeByID[node.ID] = node
	}

	// Track builtin.llmagent nodes that actually configure structured_output so we can:
	//   1) Attach JSONSchema for output_parsed.
	//   2) Treat only those nodes as writers of output_parsed in usage metadata.
	llmNodesWithStructuredOutput := make(map[string]map[string]any)

	for _, node := range workflow.Nodes {
		engine := node.EngineNode

		// We only care about builtin.llmagent here.
		if engine.Component.Type != "component" || engine.Component.Ref != "builtin.llmagent" {
			continue
		}

		// If the node has structured_output configured, attach its schema to output_parsed.
		rawSchema, ok := engine.Config["structured_output"]
		if !ok {
			continue
		}

		schemaMap, ok := rawSchema.(map[string]any)
		if !ok {
			continue
		}

		llmNodesWithStructuredOutput[node.ID] = schemaMap
	}

	param, exists := paramMap["output_parsed"]
	if !exists || param == nil {
		return
	}

	// Attach JSON schema once; if multiple nodes contribute, we keep the first.
	if param.JSONSchema == nil {
		for _, schemaMap := range llmNodesWithStructuredOutput {
			param.JSONSchema = schemaMap
			break
		}
	}

	// Refine writers for output_parsed:
	// - Keep non-output sources (e.g., dsl:, code:) as-is.
	// - For output:<nodeID> coming from builtin.llmagent, only keep nodes that
	//   actually configure structured_output. This prevents nodes like
	//   flight_agent / itinerary_agent (that reuse LLMAgent without structured_output)
	//   from being listed as writers of output_parsed.
	if len(param.Sources) == 0 {
		return
	}

	filtered := make([]string, 0, len(param.Sources))
	for _, src := range param.Sources {
		if strings.HasPrefix(src, "output:") {
			parts := strings.SplitN(src, ":", 2)
			if len(parts) == 2 && parts[1] != "" {
				nodeID := parts[1]
				node, ok := nodeByID[nodeID]
				if ok && node.EngineNode.Component.Type == "component" &&
					node.EngineNode.Component.Ref == "builtin.llmagent" {
					// For builtin.llmagent, only keep as writer when structured_output is configured.
					if _, hasSO := llmNodesWithStructuredOutput[nodeID]; !hasSO {
						continue
					}
				}
			}
		}
		filtered = append(filtered, src)
	}
	param.Sources = filtered
}
