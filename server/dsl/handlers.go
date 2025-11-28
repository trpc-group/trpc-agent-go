package main

import (
	"encoding/json"
	"net/http"
	"sort"
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

// computeNodeVars builds the per-node variable view for a compiled engine graph.
// It is shared by /graphs/vars (all nodes) and /graphs/vars/node (single node).
// Semantics: for each node, return the list of **state variables that can be
// referenced in expressions**, not just the fields written by that node.
// Variable names follow the "state.<field>" convention so that editors can
// insert them directly into templates/expressions.
func (s *Server) computeNodeVars(engineGraph *dsl.Graph) ([]nodeVarsResponse, error) {
	if engineGraph == nil {
		return nil, nil
	}

	// Reuse the same schema inference as /graphs/schema so variable
	// suggestions are derived from the canonical StateSchema + FieldUsage.
	si := dsl.NewSchemaInference(s.componentRegistry)
	_, usage, err := si.InferSchemaAndUsage(engineGraph)
	if err != nil {
		return nil, err
	}

	// Convert usage map to a deterministic slice sorted by field name.
	fields := make([]dsl.FieldUsage, 0, len(usage)+1)
	hasUserInput := false
	for _, u := range usage {
		// Hide internal-only fields from editor variable suggestions.
		if u.Name == "node_structured" || u.Name == "output_parsed" {
			continue
		}
		if u.Name == "user_input" {
			hasUserInput = true
		}
		fields = append(fields, u)
	}
	// Ensure the well-known built-in user_input field is always available as
	// a variable, even if no component metadata currently references it.
	if !hasUserInput {
		fields = append(fields, dsl.FieldUsage{
			Name: "user_input",
			Type: "string",
			Kind: "string",
		})
	}
	sort.Slice(fields, func(i, j int) bool {
		return fields[i].Name < fields[j].Name
	})

	// Pre-build the variable list that applies to all nodes. For now we expose
	// all state fields as referenceable from any node; future versions can add
	// graph-aware filtering if needed.
	baseVars := make([]nodeVariable, 0, len(fields))
	for _, f := range fields {
		baseVars = append(baseVars, nodeVariable{
			Variable:   "state." + f.Name,
			Type:       f.Type,
			Kind:       f.Kind,
			JSONSchema: f.JSONSchema,
		})
	}

	result := make([]nodeVarsResponse, 0, len(engineGraph.Nodes))
	for _, n := range engineGraph.Nodes {
		engine := n.EngineNode
		result = append(result, nodeVarsResponse{
			ID:    n.ID,
			Title: engine.Label,
			// Each node currently sees the same set of state variables.
			// This keeps the semantics simple and pushes ordering/flow
			// concerns to future, graph-aware improvements.
			Vars: baseVars,
		})
	}

	return result, nil
}

// handleGraphVars returns, for a given graph (view DSL), the list of
// variables produced by each node. This is intended for front-end editors to
// drive "variable pickers" when configuring templates or HTTP requests.
func (s *Server) handleGraphVars(w http.ResponseWriter, r *http.Request) {
	var viewGraph ViewGraph
	if err := json.NewDecoder(r.Body).Decode(&viewGraph); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid graph JSON: "+err.Error())
		return
	}

	engineGraph := viewGraph.ToEngineGraph()
	result, err := s.computeNodeVars(engineGraph)
	if err != nil {
		respondError(w, http.StatusBadRequest, "Failed to infer variables: "+err.Error())
		return
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"nodes": result,
	})
}

// graphNodeVarsRequest is used by /api/v1/graphs/vars/node to request
// variables for a single node while still sending the full graph draft.
type graphNodeVarsRequest struct {
	Graph  ViewGraph `json:"graph"`
	NodeID string    `json:"node_id"`
}

// handleGraphNodeVars returns variables for a single node inside the given
// graph. This is a convenience endpoint for editors that want to fetch
// variables scoped to the node currently being edited, without having to
// traverse the full /graphs/vars response.
func (s *Server) handleGraphNodeVars(w http.ResponseWriter, r *http.Request) {
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
	nodes, err := s.computeNodeVars(engineGraph)
	if err != nil {
		respondError(w, http.StatusBadRequest, "Failed to infer variables: "+err.Error())
		return
	}

	for _, n := range nodes {
		if n.ID == req.NodeID {
			respondJSON(w, http.StatusOK, n)
			return
		}
	}

	respondError(w, http.StatusNotFound, "node not found in graph")
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
