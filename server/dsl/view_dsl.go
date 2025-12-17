package main

import "trpc.group/trpc-go/trpc-agent-go/dsl"

// ViewGraph is the ReactFlow-friendly view DSL used by HTTP APIs and
// frontends. It contains UI concepts (type/position/labels) plus an
// embedded engine DSL under Data.Engine.
type ViewGraph struct {
	Version          string                 `json:"version,omitempty"`
	Name             string                 `json:"name"`
	Description      string                 `json:"description,omitempty"`
	Nodes            []ViewNode             `json:"nodes"`
	Edges            []dsl.Edge             `json:"edges"`
	ConditionalEdges []dsl.ConditionalEdge  `json:"conditional_edges,omitempty"`
	StartNodeID      string                 `json:"start_node_id"`
	FinishPoint      string                 `json:"finish_point,omitempty"`
	Metadata         map[string]any         `json:"metadata,omitempty"`
}

// ViewNode is a ReactFlow-style node that embeds the engine DSL under data.engine.
type ViewNode struct {
	ID       string        `json:"id"`
	Type     string        `json:"type,omitempty"`     // UI only
	Position *ViewPosition `json:"position,omitempty"` // UI only
	Data     ViewNodeData  `json:"data"`
}

// ViewNodeData contains UI metadata and the embedded engine DSL.
type ViewNodeData struct {
	Label  string         `json:"label,omitempty"` // UI only
	Icon   string         `json:"icon,omitempty"`  // UI only
	Engine dsl.EngineNode `json:"engine"`          // engine DSL
}

// ViewPosition is the canvas position used by visual editors.
type ViewPosition struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// ToEngineGraph converts a ViewGraph (with UI information) into a pure
// engine-level dsl.Graph. UI-only fields such as position, labels, and
// node types are dropped.
func (vg *ViewGraph) ToEngineGraph() *dsl.Graph {
	if vg == nil {
		return nil
	}

	nodes := make([]dsl.Node, 0, len(vg.Nodes))
	for _, vn := range vg.Nodes {
		nodes = append(nodes, dsl.Node{
			ID:         vn.ID,
			EngineNode: vn.Data.Engine,
		})
	}

	return &dsl.Graph{
		Version:          vg.Version,
		Name:             vg.Name,
		Description:      vg.Description,
		Nodes:            nodes,
		Edges:            vg.Edges,
		ConditionalEdges: vg.ConditionalEdges,
		StartNodeID:      vg.StartNodeID,
		Metadata:         vg.Metadata,
	}
}
