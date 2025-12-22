package dsl

import (
	"encoding/json"
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

func TestInspectEdge_FromJSON_EndNodeWithMCPConfig(t *testing.T) {
	// Test case: end node with mcp_node config (misconfigured node)
	// The node_type is builtin.end but has mcp_node config - this is a config error
	// but InspectEdge only checks edge validity based on node_type
	jsonData := `{
		"name": "test_end_node_with_mcp_config",
		"description": "End node incorrectly configured with mcp_node config",
		"nodes": [
			{
				"id": "start",
				"label": "Start",
				"node_type": "builtin.start",
				"node_version": "1.0"
			},
			{
				"id": "agent",
				"label": "Agent",
				"node_type": "builtin.llmagent",
				"node_version": "1.0",
				"config": {
					"agent_node": {
						"model_name": "",
						"model_source_id": ""
					}
				}
			},
			{
				"id": "mcp_tool",
				"label": "Send Email",
				"node_type": "builtin.end",
				"node_version": "1.0",
				"config": {
					"mcp_node": {
						"mcp_server_id": "trpc-demo-mcp",
						"tool": "send_email"
					}
				}
			}
		],
		"edges": [
			{"source": "start", "target": "agent"},
			{"source": "agent", "target": "mcp_tool"}
		],
		"start_node_id": "start"
	}`

	var graph Graph
	err := json.Unmarshal([]byte(jsonData), &graph)
	if err != nil {
		t.Fatalf("Failed to unmarshal JSON: %v", err)
	}

	for _, node := range graph.Nodes {
		t.Logf("Node ID=%s, NodeType=%s, Config=%v", node.ID, node.NodeType, node.Config)
	}

	result, err := InspectEdge(&graph, "agent", "mcp_tool")
	if err != nil {
		t.Fatalf("InspectEdge() error: %v", err)
	}

	t.Logf("InspectEdge result: Valid=%v, Errors=%v", result.Valid, result.Errors)

	// Edge is valid because node_type is builtin.end (agent -> end is allowed)
	// The mcp_node config is ignored - that's a node config validation issue
	if !result.Valid {
		t.Errorf("Expected valid=true (agent -> end is valid), got valid=false")
	}
}

// TestInspectEdge_AgentToMCP_MissingRequiredFields tests agent -> mcp edge
// When agent has no output_schema but mcp has input_schema with required fields, should report error
func TestInspectEdge_AgentToMCP_MissingRequiredFields(t *testing.T) {
	// Using correct DSL format: input_schema directly under config, not under mcp_node
	jsonData := `{
		"version": "1.0",
		"name": "test_agent_to_mcp_mismatch",
		"description": "Agent output does not match MCP tool input schema",
		"nodes": [
			{
				"id": "start",
				"label": "Start",
				"node_type": "builtin.start",
				"node_version": "1.0"
			},
			{
				"id": "agent",
				"label": "Agent",
				"node_type": "builtin.llmagent",
				"node_version": "1.0",
				"config": {
					"model_spec": {
						"model_name": "gpt-4o-mini"
					},
					"instruction": "Extract user info from the conversation."
				}
			},
			{
				"id": "mcp_tool",
				"label": "Send Email",
				"node_type": "builtin.mcp",
				"node_version": "1.0",
				"config": {
					"server_url": "https://mcp.example.com/email-service",
					"transport": "sse",
					"tool": "send_email",
					"input_schema": {
						"type": "object",
						"properties": {
							"to": {"type": "string"},
							"subject": {"type": "string"},
							"body": {"type": "string"}
						},
						"required": ["to", "subject", "body"]
					}
				}
			}
		],
		"edges": [
			{"source": "start", "target": "agent"},
			{"source": "agent", "target": "mcp_tool"}
		],
		"start_node_id": "start"
	}`

	var graph Graph
	err := json.Unmarshal([]byte(jsonData), &graph)
	if err != nil {
		t.Fatalf("Failed to unmarshal JSON: %v", err)
	}

	for _, node := range graph.Nodes {
		t.Logf("Node ID=%s, NodeType=%s, Config=%v", node.ID, node.NodeType, node.Config)
	}

	result, err := InspectEdge(&graph, "agent", "mcp_tool")
	if err != nil {
		t.Fatalf("InspectEdge() error: %v", err)
	}

	t.Logf("InspectEdge result: Valid=%v, Errors=%v", result.Valid, result.Errors)

	// Expected: invalid, because agent has no output_schema to satisfy mcp's input_schema
	if result.Valid {
		t.Errorf("Expected valid=false (agent without output_schema -> mcp with required input_schema), got valid=true")
	}

	// Check for missing_field error
	found := false
	for _, diag := range result.Errors {
		if diag.Code == "missing_field" {
			found = true
			t.Logf("Found expected error: %s - %s", diag.Code, diag.Message)
			break
		}
	}
	if !found {
		t.Errorf("Expected error code 'missing_field', got %v", result.Errors)
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
		{
			name: "valid: agent to end node with wrong id naming and mcp_node config",
			graph: &Graph{
				Nodes: []Node{
					{
						ID: "agent",
						EngineNode: EngineNode{
							NodeType: "builtin.llmagent",
							Config: map[string]any{
								"agent_node": map[string]any{
									"model_name": "",
								},
							},
						},
					},
					{
						ID: "mcp_tool",
						EngineNode: EngineNode{
							NodeType: "builtin.end",
							Config: map[string]any{
								"mcp_node": map[string]any{
									"mcp_server_id": "trpc-demo-mcp",
									"tool":          "send_email",
								},
							},
						},
					},
				},
			},
			wantValid: true,
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
