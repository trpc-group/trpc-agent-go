package codegen

import (
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/dsl"
)

func TestExpandWhileNodes_ConditionalEdgeTargetRewrite(t *testing.T) {
	// Test: conditional edge with target pointing to while node should be rewritten
	graph := &dsl.Graph{
		Name:        "test",
		StartNodeID: "start",
		Nodes: []dsl.Node{
			{ID: "start", EngineNode: dsl.EngineNode{NodeType: "builtin.start"}},
			{ID: "router", EngineNode: dsl.EngineNode{NodeType: "builtin.transform"}},
			{
				ID: "while_loop",
				EngineNode: dsl.EngineNode{
					NodeType: "builtin.while",
					Config: map[string]any{
						"condition": map[string]any{
							"expression": "state.counter < 3",
							"format":     "cel",
						},
						"body": map[string]any{
							"nodes": []any{
								map[string]any{
									"id":          "body_node",
									"engine_node": map[string]any{"node_type": "builtin.transform"},
								},
							},
							"edges":         []any{},
							"start_node_id": "body_node",
							"exit_node_id":  "body_node",
						},
					},
				},
			},
			{ID: "end", EngineNode: dsl.EngineNode{NodeType: "builtin.end"}},
		},
		Edges: []dsl.Edge{
			{Source: "start", Target: "router"},
			{Source: "while_loop", Target: "end"},
		},
		ConditionalEdges: []dsl.ConditionalEdge{
			{
				ID:   "route_to_while",
				From: "router",
				Condition: dsl.Condition{
					Cases: []dsl.Case{
						{
							Name:      "go_to_loop",
							Predicate: dsl.Expression{Expression: "true", Format: "cel"},
							Target:    "while_loop", // Points to while node
						},
					},
					Default: "end",
				},
			},
		},
	}

	expanded, _, err := expandWhileNodes(graph)
	if err != nil {
		t.Fatalf("expandWhileNodes failed: %v", err)
	}

	// Find the rewritten conditional edge
	var found bool
	for _, ce := range expanded.ConditionalEdges {
		if ce.ID == "route_to_while" {
			for _, c := range ce.Condition.Cases {
				if c.Name == "go_to_loop" {
					// Target should be rewritten from "while_loop" to "body_node" (body entry)
					if c.Target == "while_loop" {
						t.Errorf("conditional edge target was not rewritten, still points to 'while_loop'")
					}
					if c.Target == "body_node" {
						found = true
					}
				}
			}
		}
	}
	if !found {
		t.Error("expected conditional edge target to be rewritten to 'body_node'")
	}

	// Verify while_loop node is removed
	for _, n := range expanded.Nodes {
		if n.ID == "while_loop" {
			t.Error("while_loop node should be removed after expansion")
		}
	}

	// Verify body_node is added
	var bodyNodeFound bool
	for _, n := range expanded.Nodes {
		if n.ID == "body_node" {
			bodyNodeFound = true
			break
		}
	}
	if !bodyNodeFound {
		t.Error("body_node should be present after expansion")
	}
}

func TestExpandWhileNodes_ConditionalEdgeDefaultRewrite(t *testing.T) {
	// Test: conditional edge with default pointing to while node should be rewritten
	graph := &dsl.Graph{
		Name:        "test",
		StartNodeID: "start",
		Nodes: []dsl.Node{
			{ID: "start", EngineNode: dsl.EngineNode{NodeType: "builtin.start"}},
			{ID: "router", EngineNode: dsl.EngineNode{NodeType: "builtin.transform"}},
			{
				ID: "while_loop",
				EngineNode: dsl.EngineNode{
					NodeType: "builtin.while",
					Config: map[string]any{
						"condition": map[string]any{
							"expression": "state.counter < 3",
							"format":     "cel",
						},
						"body": map[string]any{
							"nodes": []any{
								map[string]any{
									"id":          "body_node",
									"engine_node": map[string]any{"node_type": "builtin.transform"},
								},
							},
							"edges":         []any{},
							"start_node_id": "body_node",
							"exit_node_id":  "body_node",
						},
					},
				},
			},
			{ID: "end", EngineNode: dsl.EngineNode{NodeType: "builtin.end"}},
		},
		Edges: []dsl.Edge{
			{Source: "start", Target: "router"},
			{Source: "while_loop", Target: "end"},
		},
		ConditionalEdges: []dsl.ConditionalEdge{
			{
				ID:   "route_default_to_while",
				From: "router",
				Condition: dsl.Condition{
					Cases: []dsl.Case{
						{
							Name:      "go_to_end",
							Predicate: dsl.Expression{Expression: "false", Format: "cel"},
							Target:    "end",
						},
					},
					Default: "while_loop", // Default points to while node
				},
			},
		},
	}

	expanded, _, err := expandWhileNodes(graph)
	if err != nil {
		t.Fatalf("expandWhileNodes failed: %v", err)
	}

	// Find the rewritten conditional edge
	for _, ce := range expanded.ConditionalEdges {
		if ce.ID == "route_default_to_while" {
			if ce.Condition.Default == "while_loop" {
				t.Errorf("conditional edge default was not rewritten, still points to 'while_loop'")
			}
			if ce.Condition.Default != "body_node" {
				t.Errorf("expected default to be 'body_node', got %q", ce.Condition.Default)
			}
		}
	}
}
