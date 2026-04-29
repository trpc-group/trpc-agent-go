//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package age

import (
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/graph"
)

func TestPathEdgesBuildsOrderedEdges(t *testing.T) {
	edges := pathEdges(&agPath{
		edgeIDs:   []string{"edge-1", ""},
		fromIDs:   []string{"a", "b"},
		toIDs:     []string{"b", "c"},
		edgeTypes: []string{"CALLS", "CONTAINS"},
	})

	if len(edges) != 2 {
		t.Fatalf("len(edges) = %d, want 2", len(edges))
	}
	if edges[0].ID != "edge-1" || edges[0].FromID != "a" || edges[0].ToID != "b" || edges[0].Type != "CALLS" {
		t.Fatalf("unexpected first edge: %+v", edges[0])
	}
	if edges[1].ID != "b:CONTAINS:c" || edges[1].FromID != "b" || edges[1].ToID != "c" || edges[1].Type != "CONTAINS" {
		t.Fatalf("unexpected second edge: %+v", edges[1])
	}
}

func TestFilterEdgesByNodesDropsDanglingEdges(t *testing.T) {
	edges := []*graph.Edge{
		{ID: "a-b", FromID: "a", ToID: "b", Type: "CALLS"},
		{ID: "b-c", FromID: "b", ToID: "c", Type: "CALLS"},
	}
	nodes := []*graph.Node{{ID: "a"}, {ID: "b"}}

	filtered := filterEdgesByNodes(edges, nodes)
	if len(filtered) != 1 {
		t.Fatalf("len(filtered) = %d, want 1", len(filtered))
	}
	if filtered[0].ID != "a-b" {
		t.Fatalf("filtered[0].ID = %q, want a-b", filtered[0].ID)
	}
}

func TestParseAgStringListPreservesNullPositions(t *testing.T) {
	values := parseAgStringList(`["edge-1", null, "edge-3"]::agtype`)
	if len(values) != 3 {
		t.Fatalf("len(values) = %d, want 3", len(values))
	}
	if values[0] != "edge-1" || values[1] != "" || values[2] != "edge-3" {
		t.Fatalf("values = %+v, want preserved null slot", values)
	}
}

func TestDollarQuoteAvoidsExistingDelimiter(t *testing.T) {
	quoted := dollarQuote("MATCH (n) RETURN '$age$' AS value")
	if quoted[:6] != "$age1$" {
		t.Fatalf("quoted = %q, want alternate delimiter", quoted)
	}
}

func TestCypherValueFormatsMetadataMap(t *testing.T) {
	value := cypherValue(map[string]any{
		"kind":      "function",
		"file.path": "main.go",
		"tags":      []any{"rpc", "client"},
	})
	want := `{` + "`file.path`" + `: "main.go", kind: "function", tags: ["rpc", "client"]}`
	if value != want {
		t.Fatalf("cypherValue() = %s, want %s", value, want)
	}
}
