package dsl

import (
	"testing"
)

func TestInspectEdge(t *testing.T) {
	tests := []struct {
		name         string
		graph        *Graph
		sourceNodeID string
		targetNodeID string
		wantValid    bool
		wantErrCode  string // expected error code if not valid
		wantErr      bool   // expect function to return error
	}{
		{
			name: "valid edge: agent to agent",
			graph: &Graph{
				Nodes: []Node{
					{ID: "agent1", EngineNode: EngineNode{NodeType: "builtin.llmagent"}},
					{ID: "agent2", EngineNode: EngineNode{NodeType: "builtin.llmagent"}},
				},
			},
			sourceNodeID: "agent1",
			targetNodeID: "agent2",
			wantValid:    true,
		},
		{
			name: "valid edge: agent to end",
			graph: &Graph{
				Nodes: []Node{
					{ID: "agent1", EngineNode: EngineNode{NodeType: "builtin.llmagent"}},
					{ID: "end1", EngineNode: EngineNode{NodeType: "builtin.end"}},
				},
			},
			sourceNodeID: "agent1",
			targetNodeID: "end1",
			wantValid:    true,
		},
		{
			name: "valid edge: transform to agent",
			graph: &Graph{
				Nodes: []Node{
					{ID: "transform1", EngineNode: EngineNode{NodeType: "builtin.transform"}},
					{ID: "agent1", EngineNode: EngineNode{NodeType: "builtin.llmagent"}},
				},
			},
			sourceNodeID: "transform1",
			targetNodeID: "agent1",
			wantValid:    true,
		},
		{
			name: "invalid edge: end node as source",
			graph: &Graph{
				Nodes: []Node{
					{ID: "end1", EngineNode: EngineNode{NodeType: "builtin.end"}},
					{ID: "agent1", EngineNode: EngineNode{NodeType: "builtin.llmagent"}},
				},
			},
			sourceNodeID: "end1",
			targetNodeID: "agent1",
			wantValid:    false,
			wantErrCode:  "invalid_source",
		},
		{
			name: "invalid edge: end node with outgoing edge to another end",
			graph: &Graph{
				Nodes: []Node{
					{ID: "end1", EngineNode: EngineNode{NodeType: "builtin.end"}},
					{ID: "end2", EngineNode: EngineNode{NodeType: "builtin.end"}},
				},
			},
			sourceNodeID: "end1",
			targetNodeID: "end2",
			wantValid:    false,
			wantErrCode:  "invalid_source",
		},
		{
			name:         "error: nil graph",
			graph:        nil,
			sourceNodeID: "a",
			targetNodeID: "b",
			wantErr:      true,
		},
		{
			name: "error: empty source node id",
			graph: &Graph{
				Nodes: []Node{
					{ID: "agent1", EngineNode: EngineNode{NodeType: "builtin.llmagent"}},
				},
			},
			sourceNodeID: "",
			targetNodeID: "agent1",
			wantErr:      true,
		},
		{
			name: "error: empty target node id",
			graph: &Graph{
				Nodes: []Node{
					{ID: "agent1", EngineNode: EngineNode{NodeType: "builtin.llmagent"}},
				},
			},
			sourceNodeID: "agent1",
			targetNodeID: "",
			wantErr:      true,
		},
		{
			name: "error: source node not found",
			graph: &Graph{
				Nodes: []Node{
					{ID: "agent1", EngineNode: EngineNode{NodeType: "builtin.llmagent"}},
				},
			},
			sourceNodeID: "nonexistent",
			targetNodeID: "agent1",
			wantErr:      true,
		},
		{
			name: "error: target node not found",
			graph: &Graph{
				Nodes: []Node{
					{ID: "agent1", EngineNode: EngineNode{NodeType: "builtin.llmagent"}},
				},
			},
			sourceNodeID: "agent1",
			targetNodeID: "nonexistent",
			wantErr:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := InspectEdge(tt.graph, tt.sourceNodeID, tt.targetNodeID)

			if tt.wantErr {
				if err == nil {
					t.Errorf("InspectEdge() expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("InspectEdge() unexpected error: %v", err)
				return
			}

			if result.Valid != tt.wantValid {
				t.Errorf("InspectEdge() valid = %v, want %v", result.Valid, tt.wantValid)
			}

			if tt.wantErrCode != "" {
				if len(result.Errors) == 0 {
					t.Errorf("InspectEdge() expected error code %q, got no errors", tt.wantErrCode)
					return
				}
				found := false
				for _, diag := range result.Errors {
					if diag.Code == tt.wantErrCode {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("InspectEdge() expected error code %q, got %v", tt.wantErrCode, result.Errors)
				}
			}
		})
	}
}

func TestInspectEdge_SchemaCompatibility(t *testing.T) {
	tests := []struct {
		name        string
		graph       *Graph
		wantValid   bool
		wantErrCode string
	}{
		{
			name: "compatible: mcp output matches mcp input",
			graph: &Graph{
				Nodes: []Node{
					{
						ID: "mcp1",
						EngineNode: EngineNode{
							NodeType: "builtin.mcp",
							Config: map[string]any{
								"output_schema": map[string]any{
									"type": "object",
									"properties": map[string]any{
										"result": map[string]any{"type": "string"},
									},
								},
							},
						},
					},
					{
						ID: "mcp2",
						EngineNode: EngineNode{
							NodeType: "builtin.mcp",
							Config: map[string]any{
								"input_schema": map[string]any{
									"type": "object",
									"properties": map[string]any{
										"result": map[string]any{"type": "string"},
									},
								},
							},
						},
					},
				},
			},
			wantValid: true,
		},
		{
			name: "incompatible: type mismatch",
			graph: &Graph{
				Nodes: []Node{
					{
						ID: "mcp1",
						EngineNode: EngineNode{
							NodeType: "builtin.mcp",
							Config: map[string]any{
								"output_schema": map[string]any{
									"type": "object",
									"properties": map[string]any{
										"count": map[string]any{"type": "string"},
									},
								},
							},
						},
					},
					{
						ID: "mcp2",
						EngineNode: EngineNode{
							NodeType: "builtin.mcp",
							Config: map[string]any{
								"input_schema": map[string]any{
									"type": "object",
									"properties": map[string]any{
										"count": map[string]any{"type": "integer"},
									},
								},
							},
						},
					},
				},
			},
			wantValid:   false,
			wantErrCode: "type_mismatch",
		},
		{
			name: "incompatible: missing required field",
			graph: &Graph{
				Nodes: []Node{
					{
						ID: "mcp1",
						EngineNode: EngineNode{
							NodeType: "builtin.mcp",
							Config: map[string]any{
								"output_schema": map[string]any{
									"type": "object",
									"properties": map[string]any{
										"name": map[string]any{"type": "string"},
									},
								},
							},
						},
					},
					{
						ID: "mcp2",
						EngineNode: EngineNode{
							NodeType: "builtin.mcp",
							Config: map[string]any{
								"input_schema": map[string]any{
									"type": "object",
									"properties": map[string]any{
										"name":  map[string]any{"type": "string"},
										"email": map[string]any{"type": "string"},
									},
									"required": []any{"name", "email"},
								},
							},
						},
					},
				},
			},
			wantValid:   false,
			wantErrCode: "missing_field",
		},
		{
			name: "incompatible: agent to mcp missing required fields",
			graph: &Graph{
				Nodes: []Node{
					{
						ID: "agent",
						EngineNode: EngineNode{
							NodeType: "builtin.llmagent",
							Config:   map[string]any{},
						},
					},
					{
						ID: "mcp",
						EngineNode: EngineNode{
							NodeType: "builtin.mcp",
							Config: map[string]any{
								"input_schema": map[string]any{
									"type": "object",
									"properties": map[string]any{
										"to":      map[string]any{"type": "string"},
										"subject": map[string]any{"type": "string"},
										"body":    map[string]any{"type": "string"},
									},
									"required": []any{"to", "subject", "body"},
								},
							},
						},
					},
				},
			},
			wantValid:   false,
			wantErrCode: "missing_field",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := InspectEdge(tt.graph, tt.graph.Nodes[0].ID, tt.graph.Nodes[1].ID)
			if err != nil {
				t.Fatalf("InspectEdge() unexpected error: %v", err)
			}

			if result.Valid != tt.wantValid {
				t.Errorf("InspectEdge() valid = %v, want %v", result.Valid, tt.wantValid)
			}

			if tt.wantErrCode != "" {
				found := false
				for _, diag := range result.Errors {
					if diag.Code == tt.wantErrCode {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("InspectEdge() expected error code %q, got %v", tt.wantErrCode, result.Errors)
				}
			}
		})
	}
}
