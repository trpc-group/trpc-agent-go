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
	"strings"
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

func TestPathQueryCypherUsesPropertiesInListComprehension(t *testing.T) {
	cypher := pathQueryCypher("from-node", "to-node", "-[:CALLS*1..3]->", 5)
	for _, want := range []string{
		"[node IN nodes(p) | properties(node).id]",
		"[edge IN relationships(p) | properties(edge).id]",
		"[edge IN relationships(p) | properties(startNode(edge)).id]",
		"[edge IN relationships(p) | properties(endNode(edge)).id]",
	} {
		if !strings.Contains(cypher, want) {
			t.Fatalf("pathQueryCypher() missing %q in %s", want, cypher)
		}
	}
	for _, bad := range []string{
		"| node.id]",
		"| edge.id]",
		"| startNode(edge).id]",
		"| endNode(edge).id]",
	} {
		if strings.Contains(cypher, bad) {
			t.Fatalf("pathQueryCypher() contains AGE-incompatible property access %q in %s", bad, cypher)
		}
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

func TestEdgeTypesDeduplicatesAndSorts(t *testing.T) {
	labels, err := edgeTypes([]*graph.Edge{
		{FromID: "a", ToID: "b", Type: "CALLS"},
		{FromID: "b", ToID: "c", Type: "METHOD"},
		{FromID: "c", ToID: "d", Type: "CALLS"},
	})
	if err != nil {
		t.Fatalf("edgeTypes() error = %v", err)
	}
	if len(labels) != 2 || labels[0] != "CALLS" || labels[1] != "METHOD" {
		t.Fatalf("edgeTypes() = %+v, want [CALLS METHOD]", labels)
	}
}

func TestRelationshipPatternsExpandsMultipleEdgeTypes(t *testing.T) {
	patterns, err := relationshipPatterns(graph.DirectionOut, []string{"METHOD", "CALLS"}, 2)
	if err != nil {
		t.Fatalf("relationshipPatterns() error = %v", err)
	}
	want := []string{
		`-[:CALLS]->`,
		`-[:METHOD]->`,
		`-[:CALLS]->(:Node)-[:CALLS]->`,
		`-[:CALLS]->(:Node)-[:METHOD]->`,
		`-[:METHOD]->(:Node)-[:CALLS]->`,
		`-[:METHOD]->(:Node)-[:METHOD]->`,
	}
	if len(patterns) != len(want) {
		t.Fatalf("len(patterns) = %d, want %d: %+v", len(patterns), len(want), patterns)
	}
	for i := range want {
		if patterns[i] != want[i] {
			t.Fatalf("patterns[%d] = %q, want %q", i, patterns[i], want[i])
		}
	}
}

func TestRelationshipPatternsUsesVariableLengthForSingleEdgeType(t *testing.T) {
	patterns, err := relationshipPatterns(graph.DirectionIn, []string{"CALLS"}, 3)
	if err != nil {
		t.Fatalf("relationshipPatterns() error = %v", err)
	}
	if len(patterns) != 1 || patterns[0] != `<-[:CALLS*1..3]-` {
		t.Fatalf("patterns = %+v, want single variable-length pattern", patterns)
	}
}

func TestRelationshipPatternsRejectsCombinationExplosion(t *testing.T) {
	_, err := relationshipPatterns(graph.DirectionOut, []string{"A", "B", "C", "D", "E"}, 5)
	if err == nil {
		t.Fatal("relationshipPatterns() error is nil, want combination limit error")
	}
}
