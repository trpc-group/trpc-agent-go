package main

import "trpc.group/trpc-go/trpc-agent-go/dsl"

// ViewWorkflow is the ReactFlow-friendly view DSL used by HTTP APIs and
// frontends. It contains UI concepts (type/position/labels) plus an
// embedded engine DSL under Data.Engine.
type ViewWorkflow struct {
	Version          string                     `json:"version,omitempty"`
	Name             string                     `json:"name"`
	Description      string                     `json:"description,omitempty"`
	Nodes            []ViewNode                 `json:"nodes"`
	Edges            []dsl.Edge                 `json:"edges"`
	ConditionalEdges []dsl.ConditionalEdge      `json:"conditional_edges,omitempty"`
	EntryPoint       string                     `json:"entry_point"`
	FinishPoint      string                     `json:"finish_point,omitempty"`
	Metadata         map[string]interface{}     `json:"metadata,omitempty"`
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

// ToEngineWorkflow converts a ViewWorkflow (with UI information) into a pure
// engine-level dsl.Workflow. UI-only fields such as position, labels, and
// node types are dropped.
func (vw *ViewWorkflow) ToEngineWorkflow() *dsl.Workflow {
	if vw == nil {
		return nil
	}

	nodes := make([]dsl.Node, 0, len(vw.Nodes))
	for _, vn := range vw.Nodes {
		nodes = append(nodes, dsl.Node{
			ID:         vn.ID,
			EngineNode: vn.Data.Engine,
		})
	}

	return &dsl.Workflow{
		Version:          vw.Version,
		Name:             vw.Name,
		Description:      vw.Description,
		Nodes:            nodes,
		Edges:            vw.Edges,
		ConditionalEdges: vw.ConditionalEdges,
		EntryPoint:       vw.EntryPoint,
		FinishPoint:      vw.FinishPoint,
		Metadata:         vw.Metadata,
	}
}
