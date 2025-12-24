package dsl

import (
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/dsl/internal/knowledgeconfig"
	"trpc.group/trpc-go/trpc-agent-go/dsl/internal/outputformat"
)

// Diagnostic describes a generic diagnostic message used by multiple APIs.
// It matches the Diagnostic schema in dsl/schema/engine_api.openapi.json.
type Diagnostic struct {
	Code     string `json:"code,omitempty"`
	Message  string `json:"message"`
	Path     string `json:"path,omitempty"`
	Severity string `json:"severity,omitempty"`
}

// EdgeInspectionResult describes the result of inspecting a connection
// between two nodes. It matches the EdgeInspectionResult schema in
// dsl/schema/engine_api.openapi.json.
type EdgeInspectionResult struct {
	Valid              bool           `json:"valid"`
	Errors             []Diagnostic   `json:"errors,omitempty"`
	SourceOutputSchema map[string]any `json:"source_output_schema,omitempty"`
	TargetInputSchema  map[string]any `json:"target_input_schema,omitempty"`
}

// InspectEdge performs a lightweight type-compatibility check between two
// nodes in the given graph. It is intentionally conservative: when either
// side has no schema information, the edge is treated as valid and no
// diagnostics are returned.
//
// Today this focuses on the most common authoring scenario:
//   - Source: builtin.llmagent / builtin.transform / builtin.mcp
//   - Target: builtin.mcp
//
// For other node types, SourceOutputSchema/TargetInputSchema may be nil and
// the edge is treated as valid.
func InspectEdge(graphDef *Graph, sourceNodeID, targetNodeID string) (*EdgeInspectionResult, error) {
	if graphDef == nil {
		return nil, fmt.Errorf("graph is nil")
	}
	if sourceNodeID == "" || targetNodeID == "" {
		return nil, fmt.Errorf("source_node_id and target_node_id are required")
	}

	var (
		sourceNode *Node
		targetNode *Node
	)

	for i := range graphDef.Nodes {
		n := &graphDef.Nodes[i]
		switch n.ID {
		case sourceNodeID:
			sourceNode = n
		case targetNodeID:
			targetNode = n
		}
	}

	if sourceNode == nil {
		return nil, fmt.Errorf("source node %q not found in graph", sourceNodeID)
	}
	if targetNode == nil {
		return nil, fmt.Errorf("target node %q not found in graph", targetNodeID)
	}

	// Validate: end nodes cannot have outgoing edges.
	if sourceNode.EngineNode.NodeType == "builtin.end" {
		return &EdgeInspectionResult{
			Valid: false,
			Errors: []Diagnostic{
				{
					Code:     "invalid_source",
					Message:  fmt.Sprintf("end node %q cannot have outgoing edges", sourceNodeID),
					Path:     sourceNodeID,
					Severity: "error",
				},
			},
		}, nil
	}

	res := &EdgeInspectionResult{}

	res.SourceOutputSchema = inferNodeOutputSchema(sourceNode)
	res.TargetInputSchema = inferNodeInputSchema(targetNode)

	// If either side has no schema information, consider the edge valid.
	if res.SourceOutputSchema == nil || res.TargetInputSchema == nil {
		res.Valid = true
		return res, nil
	}

	valid, diags := compareObjectSchemas(res.SourceOutputSchema, res.TargetInputSchema)
	res.Valid = valid
	if len(diags) > 0 {
		res.Errors = diags
	}

	return res, nil
}

// inferNodeOutputSchema derives a JSON Schema describing the logical output
// object of a node when used as the source of an edge.
func inferNodeOutputSchema(node *Node) map[string]any {
	if node == nil {
		return nil
	}

	engine := node.EngineNode
	cfg := engine.Config

	switch engine.NodeType {
	case "builtin.llmagent":
		// LLM agent exposes two views for downstream nodes:
		//   - output_text: string
		//   - output_parsed: object (shape from output_format.schema when type=json)
		props := map[string]any{
			"output_text": map[string]any{
				"type": "string",
			},
		}

		op := map[string]any{
			"type":       "object",
			"properties": props,
		}

		if schema := outputformat.StructuredSchema(cfg["output_format"]); schema != nil {
			props["output_parsed"] = map[string]any{
				"type":       "object",
				"properties": schema["properties"],
			}
		}

		return op

	case "builtin.transform", "builtin.end":
		// Transform and End nodes expose a structured object defined by
		// output_schema. We treat that schema as-is.
		if schema, ok := cfg["output_schema"].(map[string]any); ok {
			return schema
		}

	case "builtin.mcp":
		// MCP node exposes a normalized structured result via output_parsed,
		// whose shape is declared in MCPConfig.output_schema.
		if schema, ok := cfg["output_schema"].(map[string]any); ok {
			return schema
		}

	case "builtin.knowledge_search":
		// Knowledge Search node exposes a fixed output structure.
		// If output_schema is explicitly provided, use it; otherwise use the default.
		if schema, ok := cfg["output_schema"].(map[string]any); ok {
			return schema
		}
		// Return the default output schema for knowledge search
		return knowledgeconfig.DefaultOutputSchema()
	}

	return nil
}

// inferNodeInputSchema derives a JSON Schema describing the logical input
// object expected by a node when used as the target of an edge.
func inferNodeInputSchema(node *Node) map[string]any {
	if node == nil {
		return nil
	}

	engine := node.EngineNode
	cfg := engine.Config

	switch engine.NodeType {
	case "builtin.mcp":
		// MCP node input comes from MCPConfig.input_schema when available.
		if schema, ok := cfg["input_schema"].(map[string]any); ok {
			return schema
		}

	case "builtin.knowledge_search":
		// Knowledge Search node input depends on variables used in the query CEL expression.
		// If input_schema is explicitly provided (describing dependency schema / used input paths),
		// use it for edge validation.
		if schema, ok := cfg["input_schema"].(map[string]any); ok {
			return schema
		}
		// Otherwise, inferring input schema from CEL expression requires parsing the expression
		// to extract variable references. For now, return nil to indicate no explicit input
		// schema (edge is treated as valid).
	}

	return nil
}

// compareObjectSchemas performs a minimal compatibility check between two
// JSON Schemas describing objects. It focuses on:
//   - required fields present in both source and target
//   - basic type equality for intersecting fields
func compareObjectSchemas(source, target map[string]any) (bool, []Diagnostic) {
	srcProps := getSchemaProperties(source)
	dstProps := getSchemaProperties(target)
	if dstProps == nil {
		// Target has no structural information; treat as compatible.
		return true, nil
	}
	if srcProps == nil {
		// Source has no structural information; we cannot confidently report
		// missing fields, so treat as compatible.
		return true, nil
	}

	required := getSchemaRequired(target)
	var diags []Diagnostic

	for _, name := range required {
		if _, ok := srcProps[name]; !ok {
			diags = append(diags, Diagnostic{
				Code:     "missing_field",
				Message:  fmt.Sprintf("target requires field %q but source does not define it", name),
				Path:     name,
				Severity: "error",
			})
		}
	}

	for name, dstProp := range dstProps {
		srcProp, ok := srcProps[name]
		if !ok {
			continue
		}
		srcType, _ := srcProp["type"].(string)
		dstType, _ := dstProp["type"].(string)
		if srcType != "" && dstType != "" && srcType != dstType {
			diags = append(diags, Diagnostic{
				Code:     "type_mismatch",
				Message:  fmt.Sprintf("field %q has type %q in source but %q in target", name, srcType, dstType),
				Path:     name,
				Severity: "error",
			})
		}
	}

	return len(diags) == 0, diags
}

func getSchemaProperties(schema map[string]any) map[string]map[string]any {
	if schema == nil {
		return nil
	}
	rawProps, ok := schema["properties"]
	if !ok {
		return nil
	}
	propsAny, ok := rawProps.(map[string]any)
	if !ok {
		return nil
	}
	props := make(map[string]map[string]any, len(propsAny))
	for k, v := range propsAny {
		if m, ok := v.(map[string]any); ok {
			props[k] = m
		}
	}
	if len(props) == 0 {
		return nil
	}
	return props
}

func getSchemaRequired(schema map[string]any) []string {
	if schema == nil {
		return nil
	}
	rawReq, ok := schema["required"]
	if !ok {
		return nil
	}
	switch v := rawReq.(type) {
	case []any:
		var out []string
		for _, item := range v {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, s)
			}
		}
		return out
	case []string:
		var out []string
		for _, s := range v {
			if strings.TrimSpace(s) != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}
