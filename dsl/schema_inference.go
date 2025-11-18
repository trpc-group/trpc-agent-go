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

// SchemaInference automatically infers State Schema from graph components.
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

	// Add node_structured field (per-node structured outputs, e.g. LLMAgent
	// structured_output or End node structured results). This is a DSL-level
	// convention and is intentionally not exposed via FieldUsage; it is
	// primarily used internally by conditions and templates.
	schema.AddField("node_structured", graph.StateField{
		Type:    reflect.TypeOf(map[string]any{}),
		Reducer: graph.MergeReducer,
		Default: func() any { return map[string]any{} },
	})
}

// addDeclaredStateVariables adds graph-declared state variables to the
// schema (and optionally usage map). These declarations are the canonical
// source for graph-level variables such as those written by builtin.set_state.
func (si *SchemaInference) addDeclaredStateVariables(
	schema *graph.StateSchema,
	graphDef *Graph,
	usage map[string]FieldUsage,
) {
	if graphDef == nil || len(graphDef.StateVariables) == 0 {
		return
	}

	for _, v := range graphDef.StateVariables {
		name := strings.TrimSpace(v.Name)
		if name == "" {
			continue
		}

		goType, kind := goTypeForStateVariableKind(v.Kind)
		reducer := si.getReducer(v.Reducer)

		// Only add to schema when there is no existing field definition. This
		// lets component-derived schema stay authoritative when both exist.
		if _, exists := schema.Fields[name]; !exists {
			var defaultFunc func() any
			if v.Default != nil {
				defaultValue := v.Default
				defaultFunc = func() any { return defaultValue }
			}

			schema.AddField(name, graph.StateField{
				Type:     goType,
				Reducer:  reducer,
				Required: false,
				Default:  defaultFunc,
			})
		}

		if usage != nil {
			fieldUsage, ok := usage[name]
			if !ok {
				fieldUsage = FieldUsage{
					Name: name,
					Type: goType.String(),
					Kind: kind,
				}
			} else {
				if fieldUsage.Type == "" {
					fieldUsage.Type = goType.String()
				}
				if fieldUsage.Kind == "" {
					fieldUsage.Kind = kind
				}
			}

			// Attach JSONSchema if declared and not already present.
			if fieldUsage.JSONSchema == nil && v.JSONSchema != nil {
				fieldUsage.JSONSchema = v.JSONSchema
			}

			usage[name] = fieldUsage
		}
	}
}

// InferSchema infers the State Schema from a graph (engine DSL).
// This method only cares about executable semantics and is intentionally
// decoupled from any UI-specific concepts.
func (si *SchemaInference) InferSchema(graphDef *Graph) (*graph.StateSchema, error) {
	paramMap, err := si.buildParameterMap(graphDef)
	if err != nil {
		return nil, err
	}
	schema := si.buildSchemaFromParams(paramMap)
	// Enrich with graph-declared state variables (e.g., for builtin.set_state).
	si.addDeclaredStateVariables(schema, graphDef, nil)
	return schema, nil
}

// FieldUsage describes how a particular state field is used in the graph.
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
func (si *SchemaInference) InferSchemaAndUsage(graphDef *Graph) (*graph.StateSchema, map[string]FieldUsage, error) {
	paramMap, err := si.buildParameterMap(graphDef)
	if err != nil {
		return nil, nil, err
	}

	// Enrich parameter info with component-level context where useful (e.g., structured_output).
	si.attachComponentContext(graphDef, paramMap)

	schema := si.buildSchemaFromParams(paramMap)
	usage := si.buildUsageFromParams(paramMap)

	// Enrich schema and usage with graph-declared state variables so that
	// editor-facing variables include explicitly declared fields (e.g., via Start).
	si.addDeclaredStateVariables(schema, graphDef, usage)

	return schema, usage, nil
}

// buildSchemaFromParams constructs a StateSchema from a collected parameter map.
// It is intentionally focused on execution semantics and does not attach any
// editor-facing usage information.
func (si *SchemaInference) buildSchemaFromParams(paramMap map[string]*ParameterInfo) *graph.StateSchema {
	schema := graph.NewStateSchema()
	// Add framework built-in fields first.
	si.addBuiltinFields(schema)

	// Convert parameter map to StateSchema.
	for name, param := range paramMap {
		reducer := si.getReducer(param.Reducer)

		schema.AddField(name, graph.StateField{
			Type:     param.GoType,
			Reducer:  reducer,
			Required: param.Required,
			Default:  param.DefaultFunc,
		})
	}
	return schema
}

// buildUsageFromParams constructs FieldUsage information from a collected
// parameter map. It is used by higher layers (e.g., editors) to drive
// variable pickers and type hints.
func (si *SchemaInference) buildUsageFromParams(paramMap map[string]*ParameterInfo) map[string]FieldUsage {
	usage := make(map[string]FieldUsage, len(paramMap))

	for name, param := range paramMap {
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
				// Legacy tags ("input", "output") without node IDs are ignored for usage.
			}
		}

		usage[name] = fieldUsage
	}

	return usage
}

// buildParameterMap collects all input/output parameters from components and DSL NodeIO
// into a map keyed by state field name.
func (si *SchemaInference) buildParameterMap(graphDef *Graph) (map[string]*ParameterInfo, error) {
	parameterMap := make(map[string]*ParameterInfo)

	for _, node := range graphDef.Nodes {
		engine := node.EngineNode

		// Get component metadata
		component, exists := si.registry.Get(engine.NodeType)
		if !exists {
			// For builtin components not present in the registry, fall back to
			// processing DSL-level outputs only.
			if engine.NodeType == "builtin.llm" || engine.NodeType == "builtin.tools" {
				if err := si.addDSLOutputs(node, parameterMap); err != nil {
					return nil, fmt.Errorf("node %s: %w", node.ID, err)
				}
				continue
			}
			return nil, fmt.Errorf("node %s: component %s not found", node.ID, engine.NodeType)
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

// addDSLOutputs adds output parameters from DSL node outputs configuration.
// This handles output remapping specified in the graph DSL.
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

// goTypeForStateVariableKind maps a StateVariable.Kind string to a Go
// reflect.Type suitable for StateSchema. It is intentionally coarse-grained
// and aligned with the "kind" vocabulary used by NodeIO.
func goTypeForStateVariableKind(kind string) (reflect.Type, string) {
	switch kind {
	case "string":
		return reflect.TypeOf(""), "string"
	case "number":
		// Use float64 as the generic number type for schema purposes.
		return reflect.TypeOf(float64(0)), "number"
	case "boolean":
		return reflect.TypeOf(false), "boolean"
	case "object":
		return reflect.TypeOf(map[string]any{}), "object"
	case "array":
		return reflect.TypeOf([]any{}), "array"
	default:
		// Fallback to opaque string representation.
		return reflect.TypeOf(""), "opaque"
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

func (si *SchemaInference) attachComponentContext(graphDef *Graph, paramMap map[string]*ParameterInfo) {
	if graphDef == nil {
		return
	}

	// Build a quick index from node ID to node for later lookups.
	nodeByID := make(map[string]Node, len(graphDef.Nodes))
	for _, node := range graphDef.Nodes {
		nodeByID[node.ID] = node
	}

	// LLMAgent structured outputs are now stored exclusively in the per-node
	// node_structured cache and surfaced to editors via higher-level variables
	// (e.g., input.output_parsed, nodes.<id>.output_parsed). We intentionally
	// avoid enriching a global output_parsed field here to keep the engine
	// schema focused on true state fields.
	si.attachLLMAgentStructuredOutput(graphDef, paramMap, nodeByID)
	si.attachEndStructuredOutput(graphDef, paramMap, nodeByID)
}

// attachLLMAgentStructuredOutput used to enrich a global output_parsed field
// with JSONSchema and precise writers. LLMAgent no longer exposes a global
// output_parsed in StateSchema, so this hook is intentionally a no-op kept for
// potential future extensions.
func (si *SchemaInference) attachLLMAgentStructuredOutput(graphDef *Graph, paramMap map[string]*ParameterInfo, nodeByID map[string]Node) {
	_ = graphDef
	_ = paramMap
	_ = nodeByID
}

// attachEndStructuredOutput enriches end_structured_output usage when builtin.end declares an output_schema.
func (si *SchemaInference) attachEndStructuredOutput(graphDef *Graph, paramMap map[string]*ParameterInfo, nodeByID map[string]Node) {
	endNodesWithSchema := make(map[string]map[string]any)

	for _, node := range graphDef.Nodes {
		engine := node.EngineNode
		if engine.NodeType != "builtin.end" {
			continue
		}

		rawSchema, ok := engine.Config["output_schema"]
		if !ok {
			continue
		}
		schemaMap, ok := rawSchema.(map[string]any)
		if !ok {
			continue
		}
		endNodesWithSchema[node.ID] = schemaMap
	}

	param, exists := paramMap["end_structured_output"]
	if !exists || param == nil {
		return
	}

	// Attach JSON schema for end_structured_output once (first End node wins).
	if param.JSONSchema == nil {
		for _, schemaMap := range endNodesWithSchema {
			param.JSONSchema = schemaMap
			break
		}
	}

	// Refine writers: for builtin.end, if an End node does not declare output_schema,
	// we still allow it to be a writer (schema-less), so we don't filter here.
	// However, we keep this hook for future refinement if needed.
	_ = nodeByID
}
