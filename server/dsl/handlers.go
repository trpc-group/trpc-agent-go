package main

import (
	"encoding/json"
	"net/http"
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
// Graph CRUD Handlers
// ============================================================================

// handleListGraphs lists all graphs.
func (s *Server) handleListGraphs(w http.ResponseWriter, r *http.Request) {
	// TODO: Implement graph storage and pagination
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"graphs": []interface{}{},
		"total":  0,
		"page":   1,
		"limit":  20,
	})
}

// handleCreateGraph creates a new graph.
func (s *Server) handleCreateGraph(w http.ResponseWriter, r *http.Request) {
	var viewGraph ViewGraph
	if err := json.NewDecoder(r.Body).Decode(&viewGraph); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid graph JSON: "+err.Error())
		return
	}

	// TODO: Validate graph
	// TODO: Store graph in database
	// TODO: Generate graph ID

	respondJSON(w, http.StatusCreated, map[string]interface{}{
		"id":      "TODO-generate-id",
		"message": "Graph created (TODO: implement storage)",
	})
}

// handleGetGraph gets a graph by ID.
func (s *Server) handleGetGraph(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	// TODO: Retrieve graph from storage
	_ = id

	respondError(w, http.StatusNotImplemented, "TODO: implement graph storage")
}

// handleUpdateGraph updates a graph.
func (s *Server) handleUpdateGraph(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	var viewGraph ViewGraph
	if err := json.NewDecoder(r.Body).Decode(&viewGraph); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid graph JSON: "+err.Error())
		return
	}

	// TODO: Update graph in storage
	_ = id

	respondError(w, http.StatusNotImplemented, "TODO: implement graph storage")
}

// handleDeleteGraph deletes a graph.
func (s *Server) handleDeleteGraph(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	// TODO: Delete graph from storage
	_ = id

	respondError(w, http.StatusNotImplemented, "TODO: implement graph storage")
}

// ============================================================================
// Validation and Compilation Handlers
// ============================================================================

// handleValidateGraph validates a graph DSL.
func (s *Server) handleValidateGraph(w http.ResponseWriter, r *http.Request) {
	var viewGraph ViewGraph
	if err := json.NewDecoder(r.Body).Decode(&viewGraph); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid graph JSON: "+err.Error())
		return
	}

	// TODO: Implement comprehensive validation
	// - Check all component references exist
	// - Check all edges connect valid nodes
	// - Check configuration parameters are valid
	// - Check for cycles (if not allowed)

	// For now, just try to compile the engine-level graph converted from view DSL.
	engineGraph := viewGraph.ToEngineGraph()
	_, err := s.compiler.Compile(engineGraph)
	if err != nil {
		respondJSON(w, http.StatusOK, map[string]interface{}{
			"valid": false,
			"errors": []map[string]string{
				{
					"field":   "graph",
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

// handleGraphSchema returns inferred state schema and field usage for a graph.
// This is intended for frontend/editor usage to provide variable suggestions.
func (s *Server) handleGraphSchema(w http.ResponseWriter, r *http.Request) {
	var viewGraph ViewGraph
	if err := json.NewDecoder(r.Body).Decode(&viewGraph); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid graph JSON: "+err.Error())
		return
	}

	engineGraph := viewGraph.ToEngineGraph()

	// Create a fresh SchemaInference using the same component registry as the compiler.
	si := dsl.NewSchemaInference(s.componentRegistry)

	_, usage, err := si.InferSchemaAndUsage(engineGraph)
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
// Graph Variable View (per-node variables for editors)
// ============================================================================

// graphNodeVarsRequest is used by /api/v1/graphs/vars and /api/v1/graphs/vars/node
// to request variables for a single node while still sending the full graph draft.
type graphNodeVarsRequest struct {
	Graph  ViewGraph `json:"graph"`
	NodeID string    `json:"node_id"`
}

// handleGraphVars returns grouped variables for the given node in the graph.
// It matches the GraphVarsRequest/GraphVarsResponse shape described in
// dsl/schema/engine_api.openapi.json, returning groups such as:
//   - previous node outputs (node_output)
//   - State variables (state)
//   - workflow input (graph_input)
func (s *Server) handleGraphVars(w http.ResponseWriter, r *http.Request) {
	var req graphNodeVarsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request JSON: "+err.Error())
		return
	}
	if req.NodeID == "" {
		respondError(w, http.StatusBadRequest, "node_id is required")
		return
	}

	engineGraph := req.Graph.ToEngineGraph()
	si := dsl.NewSchemaInference(s.componentRegistry)
	groups, err := si.ComputeVariableGroups(engineGraph, req.NodeID)
	if err != nil {
		respondError(w, http.StatusBadRequest, "Failed to infer variables: "+err.Error())
		return
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"groups": groups,
	})
}

// handleGraphNodeVars returns variables for a single node inside the given
// graph. This is a convenience endpoint for editors that want to fetch
// variables scoped to the node currently being edited, without having to
// traverse the full /graphs/vars response.
func (s *Server) handleGraphNodeVars(w http.ResponseWriter, r *http.Request) {
	// Alias for handleGraphVars so both endpoints share the same semantics.
	s.handleGraphVars(w, r)
}

// handleInspectEdge inspects a connection between two nodes and returns
// source/target schemas plus diagnostics. It matches the EdgeInspectionRequest
// / EdgeInspectionResult shapes described in dsl/schema/engine_api.openapi.json.
func (s *Server) handleInspectEdge(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Graph dsl.Graph `json:"graph"`
		Edge  struct {
			SourceNodeID string `json:"source_node_id"`
			TargetNodeID string `json:"target_node_id"`
		} `json:"edge"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request JSON: "+err.Error())
		return
	}
	if req.Edge.SourceNodeID == "" || req.Edge.TargetNodeID == "" {
		respondError(w, http.StatusBadRequest, "source_node_id and target_node_id are required")
		return
	}

	result, err := dsl.InspectEdge(&req.Graph, req.Edge.SourceNodeID, req.Edge.TargetNodeID)
	if err != nil {
		respondError(w, http.StatusBadRequest, "Failed to inspect edge: "+err.Error())
		return
	}

	respondJSON(w, http.StatusOK, result)
}

// handleCompileGraph compiles a graph definition to an executable runtime graph.
func (s *Server) handleCompileGraph(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	// TODO: Retrieve graph from storage
	_ = id

	// TODO: Compile graph
	// TODO: Cache compiled graph

	respondError(w, http.StatusNotImplemented, "TODO: implement graph compilation")
}

// ============================================================================
// Execution Handlers
// ============================================================================

// handleExecuteGraph executes a graph (non-streaming).
func (s *Server) handleExecuteGraph(w http.ResponseWriter, r *http.Request) {
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

	// TODO: Retrieve graph from storage
	// TODO: Compile graph if not cached
	// TODO: Execute graph
	// TODO: Collect all events
	// TODO: Return final state and events

	_ = id

	respondError(w, http.StatusNotImplemented, "TODO: implement graph execution")
}

// handleExecuteGraphStream executes a graph with SSE streaming.
func (s *Server) handleExecuteGraphStream(w http.ResponseWriter, r *http.Request) {
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

	// TODO: Retrieve graph from storage
	// TODO: Compile graph if not cached
	// TODO: Execute graph
	// TODO: Stream events via SSE
	// TODO: Handle client disconnect

	_ = id

	// Send placeholder event
	w.Write([]byte("event: error\n"))
	w.Write([]byte("data: {\"error\": \"TODO: implement streaming execution\"}\n\n"))
}
