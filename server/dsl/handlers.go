package main

import (
	"encoding/json"
	"fmt"
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

// graphVar describes a single variable available in a graph context.
// It matches the GraphVar schema in engine_api.openapi.json.
type graphVar struct {
	Variable   string                 `json:"variable"`
	Origin     string                 `json:"origin,omitempty"`
	Kind       string                 `json:"kind,omitempty"`
	JSONSchema map[string]interface{} `json:"json_schema,omitempty"`
}

// variableGroup groups variables by source, such as a previous node, State,
// or Graph input. It matches the VariableGroup schema in engine_api.openapi.json.
type variableGroup struct {
	Type  string     `json:"type"`           // "node_output", "state", "graph_input"
	ID    string     `json:"id"`             // node ID or fixed identifier ("state", "graph_input")
	Title string     `json:"title,omitempty"`// Human-readable title for editors
	Vars  []graphVar `json:"vars"`           // Variables in this group
}

// graphNodeVarsRequest is used by /api/v1/graphs/vars and /api/v1/graphs/vars/node
// to request variables for a single node while still sending the full graph draft.
type graphNodeVarsRequest struct {
	Graph  ViewGraph `json:"graph"`
	NodeID string    `json:"node_id"`
}

// computeVariableGroups builds the grouped variable view for a single node
// in a compiled engine graph. It returns three kinds of groups:
//   - node_output: outputs from direct upstream nodes (e.g., input.output_text)
//   - state: graph state variables (typically declared via Start/SetState)
//   - graph_input: workflow input variables (e.g., state.user_input)
func (s *Server) computeVariableGroups(engineGraph *dsl.Graph, nodeID string) ([]variableGroup, error) {
	if engineGraph == nil || nodeID == "" {
		return nil, nil
	}

	// Index nodes by ID for quick lookup.
	nodeByID := make(map[string]dsl.Node, len(engineGraph.Nodes))
	for _, n := range engineGraph.Nodes {
		nodeByID[n.ID] = n
	}

	if _, ok := nodeByID[nodeID]; !ok {
		return nil, fmt.Errorf("node %q not found in graph", nodeID)
	}

	// Collect direct upstream node IDs (static edges only for now).
	upstreamIDs := make(map[string]struct{})
	for _, e := range engineGraph.Edges {
		if e.Target == nodeID {
			upstreamIDs[e.Source] = struct{}{}
		}
	}

	// Infer schema + usage once so that variable kinds and JSON schemas are
	// consistent with /graphs/schema.
	si := dsl.NewSchemaInference(s.componentRegistry)
	_, usage, err := si.InferSchemaAndUsage(engineGraph)
	if err != nil {
		return nil, err
	}

	// Split usage into state fields and workflow input (user_input). Hide
	// framework-internal fields that are not meant for direct authoring.
	var (
		stateFields []dsl.FieldUsage
		userInput   *dsl.FieldUsage
	)
	for _, u := range usage {
		switch u.Name {
		case "node_structured", "output_parsed",
			"messages", "last_response", "node_responses",
			"end_structured_output":
			// Internal / graph-output fields; not exposed directly in state group for editors.
			continue
		case "user_input":
			// Treat user_input as workflow input group.
			tmp := u
			userInput = &tmp
			continue
		default:
			// All other fields are considered state variables.
			stateFields = append(stateFields, u)
		}
	}

	sort.Slice(stateFields, func(i, j int) bool {
		return stateFields[i].Name < stateFields[j].Name
	})

	var groups []variableGroup

	// 1) Node output groups (direct upstream nodes).
	for upstreamID := range upstreamIDs {
		upNode, ok := nodeByID[upstreamID]
		if !ok {
			continue
		}
		engine := upNode.EngineNode

		var vars []graphVar

		// For now we special-case builtin.llmagent so editors can use
		// input.output_text / input.output_parsed with a schema that
		// mirrors the structured_output config.
		if engine.NodeType == "builtin.llmagent" {
			// Text view.
			vars = append(vars, graphVar{
				Variable: "input.output_text",
				Origin:   "node_output",
				Kind:     "string",
			})

			// Structured output view (if available).
			gv := graphVar{
				Variable: "input.output_parsed",
				Origin:   "node_output",
				Kind:     "object",
			}
			if rawSchema, ok := engine.Config["structured_output"].(map[string]any); ok {
				gv.JSONSchema = rawSchema
			}
			vars = append(vars, gv)
		}

		if len(vars) == 0 {
			continue
		}

		title := engine.Label
		if title == "" {
			title = upNode.ID
		}

		groups = append(groups, variableGroup{
			Type:  "node_output",
			ID:    upNode.ID,
			Title: title,
			Vars:  vars,
		})
	}

	// 2) State variables group.
	if len(stateFields) > 0 {
		stateVars := make([]graphVar, 0, len(stateFields))
		for _, f := range stateFields {
			// Extra safety: ensure end_structured_output never leaks into the
			// state group even if usage is enriched elsewhere.
			if f.Name == "end_structured_output" {
				continue
			}
			stateVars = append(stateVars, graphVar{
				Variable:   "state." + f.Name,
				Origin:     "state",
				Kind:       f.Kind,
				JSONSchema: f.JSONSchema,
			})
		}
		groups = append(groups, variableGroup{
			Type:  "state",
			ID:    "state",
			Title: "State",
			Vars:  stateVars,
		})
	}

	// 3) Workflow input group (user_input). Always expose it, even if it did
	// not appear in usage, so editors can reference the original text input.
	graphInputVar := graphVar{
		Variable: "state.user_input",
		Origin:   "graph_input",
		Kind:     "string",
	}
	if userInput != nil && userInput.Kind != "" {
		graphInputVar.Kind = userInput.Kind
	}
	if userInput != nil && userInput.JSONSchema != nil {
		graphInputVar.JSONSchema = userInput.JSONSchema
	}
	groups = append(groups, variableGroup{
		Type:  "graph_input",
		ID:    "graph_input",
		Title: "Workflow input",
		Vars:  []graphVar{graphInputVar},
	})

	return groups, nil
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
	groups, err := s.computeVariableGroups(engineGraph, req.NodeID)
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
