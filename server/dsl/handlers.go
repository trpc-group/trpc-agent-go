package main

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/dsl"
)

// ComponentUIHints provides optional presentation hints for builtin components.
// This mapping lives in the HTTP layer to keep the DSL engine UI-agnostic.
var builtinComponentUIHints = map[string]struct {
	Icon  string
	Color string
}{
	"builtin.llm":          {Icon: "brain", Color: "#8B5CF6"},
	"builtin.llmagent":     {Icon: "🧠", Color: "#8B5CF6"},
	"builtin.http_request": {Icon: "🌐", Color: "#0EA5E9"},
	"builtin.tools":        {Icon: "🔧", Color: "#10B981"},
	"builtin.function":     {Icon: "⚙️", Color: "#6366F1"},
	"builtin.passthrough":  {Icon: "arrow-right", Color: "#6B7280"},
	"builtin.agent":        {Icon: "🤖", Color: "#10B981"},
	"builtin.code":         {Icon: "💻", Color: "#F59E0B"},
}

// ============================================================================
// Component Registry Handlers
// ============================================================================

// handleListComponents lists all registered components.
func (s *Server) handleListComponents(w http.ResponseWriter, r *http.Request) {
	// Get query parameters
	category := r.URL.Query().Get("category")
	search := r.URL.Query().Get("search")

	// Get all component metadata from the registry.
	allMetadata := s.componentRegistry.ListMetadata()

	// Filter results
	result := make([]interface{}, 0, len(allMetadata))
	for _, metadata := range allMetadata {
		// Filter by category if specified
		if category != "" && metadata.Category != category {
			continue
		}

		// Filter by search if specified
		if search != "" {
			// Simple case-insensitive search in name and description
			searchLower := strings.ToLower(search)
			if !strings.Contains(strings.ToLower(metadata.Name), searchLower) &&
				!strings.Contains(strings.ToLower(metadata.Description), searchLower) {
				continue
			}
		}

		// Extract optional UI hints: first from Meta (for app-specific components),
		// then from builtinComponentUIHints (for builtin components).
		var icon, color string
		if metadata.Meta != nil {
			if v, ok := metadata.Meta["icon"].(string); ok {
				icon = v
			}
			if v, ok := metadata.Meta["color"].(string); ok {
				color = v
			}
		}
		if icon == "" && color == "" {
			if hint, ok := builtinComponentUIHints[metadata.Name]; ok {
				icon = hint.Icon
				color = hint.Color
			}
		}

		result = append(result, map[string]any{
			"name":          metadata.Name,
			"display_name":  metadata.DisplayName,
			"description":   metadata.Description,
			"category":      metadata.Category,
			"icon":          icon,
			"color":         color,
			"version":       metadata.Version,
			"inputs":        metadata.Inputs,
			"outputs":       metadata.Outputs,
			"config_schema": metadata.ConfigSchema,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(result)
}

// handleGetComponent gets a specific component by name.
func (s *Server) handleGetComponent(w http.ResponseWriter, r *http.Request) {
	// Prefer Go 1.22+ path variables when available.
	name := r.PathValue("name")
	if name == "" {
		// Fallback for older mux patterns: extract the component name from the path.
		// Expected paths:
		//   - /api/v1/components/{name}
		path := strings.TrimPrefix(r.URL.Path, "/api/v1/components/")
		// If nothing left or still contains a slash, treat as invalid.
		if path == "" || strings.Contains(path, "/") {
			respondError(w, http.StatusBadRequest, "component name is required")
			return
		}
		name = path
	}

	component, exists := s.componentRegistry.Get(name)
	if !exists {
		respondError(w, http.StatusNotFound, "Component not found")
		return
	}

	metadata := component.Metadata()

	var icon, color string
	if metadata.Meta != nil {
		if v, ok := metadata.Meta["icon"].(string); ok {
			icon = v
		}
		if v, ok := metadata.Meta["color"].(string); ok {
			color = v
		}
	}
	if icon == "" && color == "" {
		if hint, ok := builtinComponentUIHints[metadata.Name]; ok {
			icon = hint.Icon
			color = hint.Color
		}
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"name":          name,
		"display_name":  metadata.DisplayName,
		"description":   metadata.Description,
		"category":      metadata.Category,
		"icon":          icon,
		"color":         color,
		"version":       metadata.Version,
		"inputs":        metadata.Inputs,
		"outputs":       metadata.Outputs,
		"config_schema": metadata.ConfigSchema,
	})
}

// ============================================================================
// Model Registry Handlers
// ============================================================================

// handleListModels lists all registered models.
func (s *Server) handleListModels(w http.ResponseWriter, r *http.Request) {
	models := s.modelRegistry.List()
	respondJSON(w, http.StatusOK, models)
}

// ============================================================================
// Tool Registry Handlers
// ============================================================================

// handleListTools lists all registered tools.
func (s *Server) handleListTools(w http.ResponseWriter, r *http.Request) {
	tools := s.toolRegistry.GetAll()

	// Convert to metadata format
	result := make([]interface{}, 0, len(tools))
	for name, tool := range tools {
		decl := tool.Declaration()
		result = append(result, map[string]interface{}{
			"name":         name,
			"description":  decl.Description,
			"input_schema": decl.InputSchema,
		})
	}

	respondJSON(w, http.StatusOK, result)
}

// ============================================================================
// ToolSet Registry Handlers
// ============================================================================

// handleListToolSets lists all registered tool sets.
func (s *Server) handleListToolSets(w http.ResponseWriter, r *http.Request) {
	toolSetNames := s.toolSetRegistry.List()

	// Convert to metadata format
	result := make([]map[string]interface{}, 0, len(toolSetNames))
	for _, name := range toolSetNames {
		result = append(result, map[string]interface{}{
			"name": name,
		})
	}

	respondJSON(w, http.StatusOK, result)
}

// ============================================================================
// Workflow CRUD Handlers
// ============================================================================

// handleListWorkflows lists all workflows.
func (s *Server) handleListWorkflows(w http.ResponseWriter, r *http.Request) {
	// TODO: Implement workflow storage and pagination
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"workflows": []interface{}{},
		"total":     0,
		"page":      1,
		"limit":     20,
	})
}

// handleCreateWorkflow creates a new workflow.
func (s *Server) handleCreateWorkflow(w http.ResponseWriter, r *http.Request) {
	var workflow ViewWorkflow
	if err := json.NewDecoder(r.Body).Decode(&workflow); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid workflow JSON: "+err.Error())
		return
	}

	// TODO: Validate workflow
	// TODO: Store workflow in database
	// TODO: Generate workflow ID

	respondJSON(w, http.StatusCreated, map[string]interface{}{
		"id":      "TODO-generate-id",
		"message": "Workflow created (TODO: implement storage)",
	})
}

// handleGetWorkflow gets a workflow by ID.
func (s *Server) handleGetWorkflow(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	// TODO: Retrieve workflow from storage
	_ = id

	respondError(w, http.StatusNotImplemented, "TODO: implement workflow storage")
}

// handleUpdateWorkflow updates a workflow.
func (s *Server) handleUpdateWorkflow(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	var workflow ViewWorkflow
	if err := json.NewDecoder(r.Body).Decode(&workflow); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid workflow JSON: "+err.Error())
		return
	}

	// TODO: Update workflow in storage
	_ = id

	respondError(w, http.StatusNotImplemented, "TODO: implement workflow storage")
}

// handleDeleteWorkflow deletes a workflow.
func (s *Server) handleDeleteWorkflow(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	// TODO: Delete workflow from storage
	_ = id

	respondError(w, http.StatusNotImplemented, "TODO: implement workflow storage")
}

// ============================================================================
// Validation and Compilation Handlers
// ============================================================================

// handleValidateWorkflow validates a workflow DSL.
func (s *Server) handleValidateWorkflow(w http.ResponseWriter, r *http.Request) {
	var viewWorkflow ViewWorkflow
	if err := json.NewDecoder(r.Body).Decode(&viewWorkflow); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid workflow JSON: "+err.Error())
		return
	}

	// TODO: Implement comprehensive validation
	// - Check all component references exist
	// - Check all edges connect valid nodes
	// - Check configuration parameters are valid
	// - Check for cycles (if not allowed)

	// For now, just try to compile the engine-level workflow converted from view DSL.
	engineWorkflow := viewWorkflow.ToEngineWorkflow()
	_, err := s.compiler.Compile(engineWorkflow)
	if err != nil {
		respondJSON(w, http.StatusOK, map[string]interface{}{
			"valid": false,
			"errors": []map[string]string{
				{
					"field":   "workflow",
					"message": err.Error(),
				},
			},
		})
		return
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"valid":  true,
		"errors": []interface{}{},
	})
}

// handleWorkflowSchema returns inferred state schema and field usage for a workflow.
// This is intended for frontend/editor usage to provide variable suggestions.
func (s *Server) handleWorkflowSchema(w http.ResponseWriter, r *http.Request) {
	var viewWorkflow ViewWorkflow
	if err := json.NewDecoder(r.Body).Decode(&viewWorkflow); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid workflow JSON: "+err.Error())
		return
	}

	engineWorkflow := viewWorkflow.ToEngineWorkflow()

	// Create a fresh SchemaInference using the same component registry as the compiler.
	si := dsl.NewSchemaInference(s.componentRegistry)

	_, usage, err := si.InferSchemaAndUsage(engineWorkflow)
	if err != nil {
		respondError(w, http.StatusBadRequest, "Failed to infer schema: "+err.Error())
		return
	}

	// Convert usage map to a slice for stable JSON output.
	fields := make([]dsl.FieldUsage, 0, len(usage))
	for _, u := range usage {
		fields = append(fields, u)
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"fields": fields,
	})
}

// ============================================================================
// Workflow Variable View (per-node variables for editors)
// ============================================================================

// nodeVariable describes a single variable exposed by a node for use in
// templates (e.g., {{state.xxx}} or {{nodes.node_id.output}}).
type nodeVariable struct {
	Variable   string                 `json:"variable"`
	Type       string                 `json:"type,omitempty"`
	Kind       string                 `json:"kind,omitempty"`
	JSONSchema map[string]interface{} `json:"json_schema,omitempty"`
}

// nodeVarsResponse describes variables grouped by node.
type nodeVarsResponse struct {
	ID    string         `json:"id"`
	Title string         `json:"title,omitempty"`
	Vars  []nodeVariable `json:"vars"`
}

// handleWorkflowVars returns, for a given workflow (view DSL), the list of
// variables produced by each node. This is intended for front-end editors to
// drive "variable pickers" when configuring templates or HTTP requests.
func (s *Server) handleWorkflowVars(w http.ResponseWriter, r *http.Request) {
	var viewWorkflow ViewWorkflow
	if err := json.NewDecoder(r.Body).Decode(&viewWorkflow); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid workflow JSON: "+err.Error())
		return
	}

	engineWorkflow := viewWorkflow.ToEngineWorkflow()

	// Pre-compute a map from node ID to any structured_output schema so that we
	// can attach JSON Schema to the corresponding variables (e.g., output_parsed).
	structuredSchemas := make(map[string]map[string]interface{})
	for _, n := range engineWorkflow.Nodes {
		if n.EngineNode.Component.Ref != "builtin.llmagent" {
			continue
		}
		if raw, ok := n.EngineNode.Config["structured_output"]; ok {
			if m, ok := raw.(map[string]interface{}); ok {
				structuredSchemas[n.ID] = m
			}
		}
	}

	var result []nodeVarsResponse

	for _, n := range engineWorkflow.Nodes {
		engine := n.EngineNode

		component, exists := s.componentRegistry.Get(engine.Component.Ref)
		if !exists {
			// If the component is unknown, skip variables for this node but keep a
			// placeholder entry so editors can still show the node.
			result = append(result, nodeVarsResponse{ID: n.ID, Title: engine.Name})
			continue
		}

		metadata := component.Metadata()
		vars := make([]nodeVariable, 0, len(metadata.Outputs))

		for _, out := range metadata.Outputs {
			v := nodeVariable{
				Variable: out.Name,
				Type:     out.Type,
			}

			// Derive a simple kind from GoType similar to SchemaInference.
			if out.GoType != nil {
				if kind := nodeVarKindFromGoType(out.GoType, out.Name); kind != "" {
					v.Kind = kind
				}
			}

			// Attach structured_output JSON schema for output_parsed on llmagent
			// nodes to allow editors to expand nested fields.
			if engine.Component.Ref == "builtin.llmagent" && out.Name == "output_parsed" {
				if schema, ok := structuredSchemas[n.ID]; ok {
					v.JSONSchema = schema
				}
			}

			vars = append(vars, v)
		}

		result = append(result, nodeVarsResponse{
			ID:    n.ID,
			Title: engine.Name,
			Vars:  vars,
		})
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"nodes": result,
	})
}

// nodeVarKindFromGoType mirrors the coarse-grained kind classification used in
// dsl.classifyGoType, but is defined locally to avoid exporting internal
// helpers. It is intentionally minimal and only used for editor hints.
func nodeVarKindFromGoType(t reflect.Type, fieldName string) string {
	if t == nil {
		return "opaque"
	}

	switch t.Kind() {
	case reflect.String:
		return "string"
	case reflect.Bool:
		return "boolean"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return "number"
	case reflect.Slice, reflect.Array:
		return "array"
	case reflect.Map, reflect.Struct:
		if fieldName == "output_parsed" {
			return "object"
		}
		return "object"
	default:
		return "opaque"
	}
}

// handleCompileWorkflow compiles a workflow to executable graph.
func (s *Server) handleCompileWorkflow(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	// TODO: Retrieve workflow from storage
	_ = id

	// TODO: Compile workflow
	// TODO: Cache compiled graph

	respondError(w, http.StatusNotImplemented, "TODO: implement workflow compilation")
}

// ============================================================================
// Execution Handlers
// ============================================================================

// handleExecuteWorkflow executes a workflow (non-streaming).
func (s *Server) handleExecuteWorkflow(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	// Parse execution request
	var req struct {
		Input  map[string]interface{} `json:"input"`
		Config struct {
			MaxIterations  int `json:"max_iterations"`
			TimeoutSeconds int `json:"timeout_seconds"`
		} `json:"config"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request: "+err.Error())
		return
	}

	// TODO: Retrieve workflow from storage
	// TODO: Compile workflow if not cached
	// TODO: Execute workflow
	// TODO: Collect all events
	// TODO: Return final state and events

	_ = id

	respondError(w, http.StatusNotImplemented, "TODO: implement workflow execution")
}

// handleExecuteWorkflowStream executes a workflow with SSE streaming.
func (s *Server) handleExecuteWorkflowStream(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// Parse execution request
	var req struct {
		Input  map[string]interface{} `json:"input"`
		Config struct {
			MaxIterations  int `json:"max_iterations"`
			TimeoutSeconds int `json:"timeout_seconds"`
		} `json:"config"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		// Send error event
		w.Write([]byte("event: error\n"))
		w.Write([]byte("data: {\"error\": \"Invalid request\"}\n\n"))
		return
	}

	// TODO: Retrieve workflow from storage
	// TODO: Compile workflow if not cached
	// TODO: Execute workflow
	// TODO: Stream events via SSE
	// TODO: Handle client disconnect

	_ = id

	// Send placeholder event
	w.Write([]byte("event: error\n"))
	w.Write([]byte("data: {\"error\": \"TODO: implement streaming execution\"}\n\n"))
}
