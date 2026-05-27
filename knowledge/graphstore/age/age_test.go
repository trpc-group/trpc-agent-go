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
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/graph"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/graphstore"
	"trpc.group/trpc-go/trpc-agent-go/storage/postgres"
)

// mockPostgresClient implements postgres.Client for testing.
type mockPostgresClient struct {
	execFn  func(ctx context.Context, query string, args ...any) (sql.Result, error)
	txFn    func(ctx context.Context, fn postgres.TxFunc) error
	closeFn func() error
}

func (m *mockPostgresClient) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if m.execFn != nil {
		return m.execFn(ctx, query, args...)
	}
	return nil, nil
}

func (m *mockPostgresClient) Query(ctx context.Context, fn postgres.HandlerFunc, query string, args ...any) error {
	return nil
}

func (m *mockPostgresClient) Transaction(ctx context.Context, fn postgres.TxFunc) error {
	if m.txFn != nil {
		return m.txFn(ctx, fn)
	}
	return nil
}

func (m *mockPostgresClient) Close() error {
	if m.closeFn != nil {
		return m.closeFn()
	}
	return nil
}

func newTestStore(client postgres.Client) *Store {
	return &Store{
		client:     client,
		option:     options{graphName: "test_graph"},
		edgeLabels: make(map[string]struct{}),
	}
}

// sqlmockClient wraps a sqlmock-backed *sql.DB as a postgres.Client.
// It provides real *sql.Tx objects to the transaction functions.
type sqlmockClient struct {
	db *sql.DB
}

func (c *sqlmockClient) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return c.db.ExecContext(ctx, query, args...)
}

func (c *sqlmockClient) Query(ctx context.Context, fn postgres.HandlerFunc, query string, args ...any) error {
	rows, err := c.db.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	return fn(rows)
}

func (c *sqlmockClient) Transaction(ctx context.Context, fn postgres.TxFunc) error {
	tx, err := c.db.Begin()
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (c *sqlmockClient) Close() error {
	return c.db.Close()
}

func newSqlmockStore(t *testing.T) (*Store, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create sqlmock: %v", err)
	}
	store := &Store{
		client:     &sqlmockClient{db: db},
		option:     options{graphName: "test_graph"},
		edgeLabels: make(map[string]struct{}),
	}
	return store, mock
}

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

func TestParseAgStringListPreservesQuotedCommas(t *testing.T) {
	values := parseAgStringList(`["a,b", "c\\d"]::agtype`)
	want := []string{"a,b", `c\d`}
	if !reflect.DeepEqual(values, want) {
		t.Fatalf("parseAgStringList() = %#v, want %#v", values, want)
	}
}

func TestPathQueryCypherUsesPropertiesInListComprehension(t *testing.T) {
	cypher := pathQueryCypher("from-node", "to-node", "-[:CALLS*1..3]->", 5)
	for _, want := range []string{
		"WITH p ORDER BY length(p) ASC LIMIT 5 RETURN",
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

func TestTraverseNodeQueryCypherOrdersByNearestDistance(t *testing.T) {
	cypher := traverseNodeQueryCypher("node-a", "-[:CALLS*1..2]->", 10)
	for _, want := range []string{
		`MATCH p=(start:Node {id: "node-a"})-[:CALLS*1..2]->(n:Node)`,
		"UNWIND nodes(p) AS node",
		"WITH node, min(length(p)) AS distance",
		"ORDER BY distance ASC, node.id ASC LIMIT 10 RETURN",
		"node.id, node.name, node.content, node.metadata",
	} {
		if !strings.Contains(cypher, want) {
			t.Fatalf("traverseNodeQueryCypher() missing %q in %s", want, cypher)
		}
	}
}

func TestTraverseEdgeQueryCypherUsesProperties(t *testing.T) {
	cypher := traverseEdgeQueryCypher("node-a", "-[:CALLS*1..2]->", 10)
	for _, want := range []string{
		"properties(edge).id",
		"properties(startNode(edge)).id",
		"properties(endNode(edge)).id",
		"properties(edge).metadata",
	} {
		if !strings.Contains(cypher, want) {
			t.Fatalf("traverseEdgeQueryCypher() missing %q in %s", want, cypher)
		}
	}
	for _, bad := range []string{
		" edge.id,",
		"startNode(edge).id,",
		"endNode(edge).id,",
		" edge.metadata",
	} {
		if strings.Contains(cypher, bad) {
			t.Fatalf("traverseEdgeQueryCypher() contains AGE-incompatible property access %q in %s", bad, cypher)
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

func TestValidateIdentifier(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{"simple alpha", "Node", false},
		{"lowercase", "node", false},
		{"with underscore", "edge_type", false},
		{"leading underscore", "_private", false},
		{"with digits", "Type2", false},
		{"all caps", "CALLS", false},
		{"single char", "x", false},
		{"empty string", "", true},
		{"starts with digit", "1bad", true},
		{"contains dot", "file.path", true},
		{"contains dash", "edge-type", true},
		{"contains space", "edge type", true},
		{"contains special", "type@name", true},
		{"unicode", "类型", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateIdentifier("test field", tt.value)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateIdentifier(%q) error = %v, wantErr %v", tt.value, err, tt.wantErr)
			}
		})
	}
}

func TestValidateIdentifierErrorMessage(t *testing.T) {
	err := validateIdentifier("graph name", "")
	if err == nil || !strings.Contains(err.Error(), "graph name") {
		t.Fatalf("expected error mentioning 'graph name', got %v", err)
	}
	err = validateIdentifier("edge type", "bad-id")
	if err == nil || !strings.Contains(err.Error(), "edge type") || !strings.Contains(err.Error(), "bad-id") {
		t.Fatalf("expected error mentioning field name and value, got %v", err)
	}
}

func TestCypherString(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello", `"hello"`},
		{"", `""`},
		{`has "quotes"`, `"has \"quotes\""`},
		{"has\nnewline", `"has\nnewline"`},
		{"has\ttab", `"has\ttab"`},
		{"backslash\\here", `"backslash\\here"`},
		{"unicode日本語", `"unicode日本語"`},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := cypherString(tt.input)
			if got != tt.want {
				t.Errorf("cypherString(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestCypherKey(t *testing.T) {
	tests := []struct {
		name string
		key  string
		want string
	}{
		{"valid identifier", "kind", "kind"},
		{"underscore prefix", "_type", "_type"},
		{"with digits", "field2", "field2"},
		{"dotted key", "file.path", "`file.path`"},
		{"dashed key", "edge-type", "`edge-type`"},
		{"space key", "my key", "`my key`"},
		{"starts with digit", "1key", "`1key`"},
		{"backtick in key", "has`tick", "`has``tick`"},
		{"empty key", "", "``"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cypherKey(tt.key)
			if got != tt.want {
				t.Errorf("cypherKey(%q) = %q, want %q", tt.key, got, tt.want)
			}
		})
	}
}

func TestCypherValueNil(t *testing.T) {
	if got := cypherValue(nil); got != "null" {
		t.Errorf("cypherValue(nil) = %q, want %q", got, "null")
	}
}

func TestCypherValueScalars(t *testing.T) {
	tests := []struct {
		name  string
		input any
		want  string
	}{
		{"integer", 42, "42"},
		{"float", 3.14, "3.14"},
		{"true", true, "true"},
		{"false", false, "false"},
		{"string", "hello", `"hello"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cypherValue(tt.input)
			if got != tt.want {
				t.Errorf("cypherValue(%v) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestCypherValueSlice(t *testing.T) {
	got := cypherValue([]any{"a", 1, true})
	want := `["a", 1, true]`
	if got != want {
		t.Errorf("cypherValue(slice) = %q, want %q", got, want)
	}
}

func TestCypherValueEmptySlice(t *testing.T) {
	got := cypherValue([]any{})
	if got != "[]" {
		t.Errorf("cypherValue(empty slice) = %q, want %q", got, "[]")
	}
}

func TestCypherValueEmptyMap(t *testing.T) {
	got := cypherValue(map[string]any{})
	if got != "{}" {
		t.Errorf("cypherValue(empty map) = %q, want %q", got, "{}")
	}
}

func TestCypherValueNonStringKeyMap(t *testing.T) {
	got := cypherValue(map[int]string{1: "a"})
	if got != "null" {
		t.Errorf("cypherValue(non-string-key map) = %q, want %q", got, "null")
	}
}

func TestCypherValueNestedMap(t *testing.T) {
	got := cypherValue(map[string]any{
		"a": map[string]any{"b": 1},
	})
	want := `{a: {b: 1}}`
	if got != want {
		t.Errorf("cypherValue(nested) = %q, want %q", got, want)
	}
}

func TestCypherReflectValueInvalid(t *testing.T) {
	got := cypherReflectValue(reflect.Value{})
	if got != "null" {
		t.Errorf("cypherReflectValue(invalid) = %q, want %q", got, "null")
	}
}

func TestCypherReflectValueNilPointer(t *testing.T) {
	var p *string
	got := cypherReflectValue(reflect.ValueOf(&p).Elem())
	if got != "null" {
		t.Errorf("cypherReflectValue(nil pointer) = %q, want %q", got, "null")
	}
}

func TestCypherReflectValueNilInterface(t *testing.T) {
	var iface any
	got := cypherReflectValue(reflect.ValueOf(&iface).Elem())
	if got != "null" {
		t.Errorf("cypherReflectValue(nil interface) = %q, want %q", got, "null")
	}
}

func TestCypherReflectValueDereferencesPointer(t *testing.T) {
	s := "hello"
	got := cypherReflectValue(reflect.ValueOf(&s))
	if got != `"hello"` {
		t.Errorf("cypherReflectValue(ptr to string) = %q, want %q", got, `"hello"`)
	}
}

func TestParseAgString(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"simple value", `"hello"::agtype`, "hello"},
		{"null value", `null::agtype`, ""},
		{"bare null", "null", ""},
		{"with spaces", ` "world" ::agtype`, "world"},
		{"no agtype suffix", `"plain"`, "plain"},
		{"empty quoted", `""`, ""},
		{"empty raw", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseAgString(tt.input)
			if got != tt.want {
				t.Errorf("parseAgString(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseAgMetadata(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantNil bool
		wantKey string
		wantVal any
	}{
		{"null value", "null::agtype", true, "", nil},
		{"bare null", "null", true, "", nil},
		{"empty string", "", true, "", nil},
		{"valid json", `{"key": "val"}::agtype`, false, "key", "val"},
		{"valid json no suffix", `{"num": 42}`, false, "num", float64(42)},
		{"invalid json", `not-json`, true, "", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseAgMetadata(tt.input)
			if tt.wantNil {
				if got != nil {
					t.Errorf("parseAgMetadata(%q) = %v, want nil", tt.input, got)
				}
				return
			}
			if got == nil {
				t.Fatalf("parseAgMetadata(%q) = nil, want non-nil", tt.input)
			}
			if v, ok := got[tt.wantKey]; !ok || !reflect.DeepEqual(v, tt.wantVal) {
				t.Errorf("parseAgMetadata(%q)[%q] = %v, want %v", tt.input, tt.wantKey, v, tt.wantVal)
			}
		})
	}
}

func TestParseAgStringListEmpty(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"empty brackets", "[]::agtype"},
		{"empty string", ""},
		{"whitespace only", "   ::agtype"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseAgStringList(tt.input)
			if len(got) != 0 {
				t.Errorf("parseAgStringList(%q) = %v, want empty", tt.input, got)
			}
		})
	}
}

func TestUniqueNodes(t *testing.T) {
	nodes := []*graph.Node{
		{ID: "a", Name: "A"},
		{ID: "b", Name: "B"},
		{ID: "a", Name: "A-dup"},
		nil,
		{ID: "", Name: "empty"},
		{ID: "c", Name: "C"},
	}
	got := uniqueNodes(nodes)
	if len(got) != 3 {
		t.Fatalf("uniqueNodes() returned %d nodes, want 3", len(got))
	}
	ids := make([]string, len(got))
	for i, n := range got {
		ids[i] = n.ID
	}
	if ids[0] != "a" || ids[1] != "b" || ids[2] != "c" {
		t.Errorf("uniqueNodes() IDs = %v, want [a b c]", ids)
	}
	if got[0].Name != "A" {
		t.Errorf("uniqueNodes() should keep first occurrence, got Name=%q", got[0].Name)
	}
}

func TestUniqueNodesEmpty(t *testing.T) {
	got := uniqueNodes(nil)
	if len(got) != 0 {
		t.Errorf("uniqueNodes(nil) = %v, want empty", got)
	}
}

func TestLimitNodes(t *testing.T) {
	nodes := []*graph.Node{{ID: "a"}, {ID: "b"}, {ID: "c"}}

	got := limitNodes(nodes, 2)
	if len(got) != 2 {
		t.Errorf("limitNodes(3, 2) = %d, want 2", len(got))
	}

	got = limitNodes(nodes, 5)
	if len(got) != 3 {
		t.Errorf("limitNodes(3, 5) = %d, want 3", len(got))
	}

	got = limitNodes(nodes, 3)
	if len(got) != 3 {
		t.Errorf("limitNodes(3, 3) = %d, want 3", len(got))
	}

	got = limitNodes(nodes, 0)
	if len(got) != 3 {
		t.Errorf("limitNodes(3, 0) = %d, want 3 (no limit)", len(got))
	}

	got = limitNodes(nodes, -1)
	if len(got) != 3 {
		t.Errorf("limitNodes(3, -1) = %d, want 3 (negative = no limit)", len(got))
	}
}

func TestUniqueEdges(t *testing.T) {
	edges := []*graph.Edge{
		{ID: "e1", FromID: "a", ToID: "b", Type: "CALLS"},
		{ID: "e1", FromID: "a", ToID: "b", Type: "CALLS"},
		nil,
		{ID: "", FromID: "a", ToID: "b", Type: "CALLS"},
		{ID: "", FromID: "a", ToID: "b", Type: "CALLS"},
		{ID: "e2", FromID: "b", ToID: "c", Type: "METHOD"},
	}
	got := uniqueEdges(edges)
	if len(got) != 3 {
		t.Fatalf("uniqueEdges() returned %d edges, want 3", len(got))
	}
	if got[0].ID != "e1" {
		t.Errorf("uniqueEdges()[0].ID = %q, want %q", got[0].ID, "e1")
	}
	if got[1].ID != "" {
		t.Errorf("uniqueEdges()[1].ID = %q, want empty (keyed by from:type:to)", got[1].ID)
	}
	if got[2].ID != "e2" {
		t.Errorf("uniqueEdges()[2].ID = %q, want %q", got[2].ID, "e2")
	}
}

func TestUniqueEdgesEmpty(t *testing.T) {
	got := uniqueEdges(nil)
	if len(got) != 0 {
		t.Errorf("uniqueEdges(nil) = %v, want empty", got)
	}
}

func TestRelationshipPattern(t *testing.T) {
	tests := []struct {
		name      string
		direction graph.Direction
		edgeTypes []string
		depth     int
		want      string
		wantErr   bool
	}{
		{"out no types", graph.DirectionOut, nil, 3, `-[*1..3]->`, false},
		{"in no types", graph.DirectionIn, nil, 2, `<-[*1..2]-`, false},
		{"both no types", graph.DirectionBoth, nil, 1, `-[*1..1]-`, false},
		{"empty direction defaults to out", "", nil, 2, `-[*1..2]->`, false},
		{"out with type", graph.DirectionOut, []string{"CALLS"}, 3, `-[:CALLS*1..3]->`, false},
		{"in with type", graph.DirectionIn, []string{"METHOD"}, 1, `<-[:METHOD*1..1]-`, false},
		{"both with types", graph.DirectionBoth, []string{"A", "B"}, 2, `-[:A|B*1..2]-`, false},
		{"unsupported direction", graph.Direction("upward"), nil, 1, "", true},
		{"invalid edge type", graph.DirectionOut, []string{"bad-type"}, 1, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := relationshipPattern(tt.direction, tt.edgeTypes, tt.depth)
			if (err != nil) != tt.wantErr {
				t.Errorf("relationshipPattern() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("relationshipPattern() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRelationshipSegment(t *testing.T) {
	tests := []struct {
		name      string
		direction graph.Direction
		edgeType  string
		want      string
	}{
		{"out", graph.DirectionOut, "CALLS", `-[:CALLS]->`},
		{"in", graph.DirectionIn, "CALLS", `<-[:CALLS]-`},
		{"both", graph.DirectionBoth, "CALLS", `-[:CALLS]-`},
		{"empty direction", "", "METHOD", `-[:METHOD]->`},
		{"unknown direction", graph.Direction("unknown"), "X", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := relationshipSegment(tt.direction, tt.edgeType)
			if got != tt.want {
				t.Errorf("relationshipSegment(%q, %q) = %q, want %q", tt.direction, tt.edgeType, got, tt.want)
			}
		})
	}
}

func TestEdgeTypeFilter(t *testing.T) {
	tests := []struct {
		name    string
		types   []string
		want    string
		wantErr bool
	}{
		{"empty types", nil, "", false},
		{"single type", []string{"CALLS"}, ":CALLS", false},
		{"multiple types sorted", []string{"METHOD", "CALLS"}, ":CALLS|METHOD", false},
		{"duplicate types", []string{"CALLS", "CALLS"}, ":CALLS", false},
		{"invalid type", []string{"bad-type"}, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := edgeTypeFilter(tt.types)
			if (err != nil) != tt.wantErr {
				t.Errorf("edgeTypeFilter() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("edgeTypeFilter() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNormalizeEdgeTypes(t *testing.T) {
	tests := []struct {
		name    string
		types   []string
		want    []string
		wantErr bool
	}{
		{"nil input", nil, []string{}, false},
		{"empty input", []string{}, []string{}, false},
		{"single", []string{"CALLS"}, []string{"CALLS"}, false},
		{"dedup and sort", []string{"METHOD", "CALLS", "METHOD"}, []string{"CALLS", "METHOD"}, false},
		{"invalid", []string{"bad-id"}, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeEdgeTypes(tt.types)
			if (err != nil) != tt.wantErr {
				t.Errorf("normalizeEdgeTypes() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && !reflect.DeepEqual(got, tt.want) {
				t.Errorf("normalizeEdgeTypes() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPowInt(t *testing.T) {
	tests := []struct {
		base, exp, want int
	}{
		{2, 0, 1},
		{2, 1, 2},
		{2, 3, 8},
		{3, 2, 9},
		{5, 3, 125},
		{1, 100, 1},
		{0, 5, 0},
	}
	for _, tt := range tests {
		got := powInt(tt.base, tt.exp)
		if got != tt.want {
			t.Errorf("powInt(%d, %d) = %d, want %d", tt.base, tt.exp, got, tt.want)
		}
	}
}

func TestBuildEdgeTypeSequences(t *testing.T) {
	var result [][]string
	buildEdgeTypeSequences(&result, nil, []string{"A", "B"}, 2)
	want := [][]string{{"A", "A"}, {"A", "B"}, {"B", "A"}, {"B", "B"}}
	if !reflect.DeepEqual(result, want) {
		t.Errorf("buildEdgeTypeSequences() = %v, want %v", result, want)
	}
}

func TestBuildEdgeTypeSequencesZeroRemaining(t *testing.T) {
	var result [][]string
	buildEdgeTypeSequences(&result, nil, []string{"A", "B"}, 0)
	if len(result) != 1 || len(result[0]) != 0 {
		t.Errorf("buildEdgeTypeSequences(remaining=0) = %v, want [[]]", result)
	}
}

func TestRelationshipPatternForSequence(t *testing.T) {
	tests := []struct {
		name      string
		direction graph.Direction
		types     []string
		want      string
	}{
		{"single out", graph.DirectionOut, []string{"CALLS"}, `-[:CALLS]->`},
		{"multi out", graph.DirectionOut, []string{"CALLS", "METHOD"}, `-[:CALLS]->(:Node)-[:METHOD]->`},
		{"single in", graph.DirectionIn, []string{"CALLS"}, `<-[:CALLS]-`},
		{"multi in", graph.DirectionIn, []string{"A", "B"}, `<-[:A]-(:Node)<-[:B]-`},
		{"single both", graph.DirectionBoth, []string{"X"}, `-[:X]-`},
		{"multi both", graph.DirectionBoth, []string{"X", "Y"}, `-[:X]-(:Node)-[:Y]-`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := relationshipPatternForSequence(tt.direction, tt.types)
			if got != tt.want {
				t.Errorf("relationshipPatternForSequence() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDollarQuoteNormal(t *testing.T) {
	got := dollarQuote("MATCH (n) RETURN n")
	want := "$age$MATCH (n) RETURN n$age$"
	if got != want {
		t.Errorf("dollarQuote() = %q, want %q", got, want)
	}
}

func TestDollarQuoteMultipleConflicts(t *testing.T) {
	input := "$age$$age1$some data"
	got := dollarQuote(input)
	if !strings.HasPrefix(got, "$age2$") || !strings.HasSuffix(got, "$age2$") {
		t.Errorf("dollarQuote() = %q, want $age2$ delimiters", got)
	}
	if !strings.Contains(got, input) {
		t.Errorf("dollarQuote() should contain original input")
	}
}

func TestPathEdgesNilPath(t *testing.T) {
	got := pathEdges(nil)
	if got != nil {
		t.Errorf("pathEdges(nil) = %v, want nil", got)
	}
}

func TestPathEdgesMismatchedLengths(t *testing.T) {
	edges := pathEdges(&agPath{
		edgeIDs:   []string{"e1"},
		fromIDs:   []string{"a", "b", "c"},
		toIDs:     []string{"b"},
		edgeTypes: []string{"CALLS", "METHOD"},
	})
	if len(edges) != 1 {
		t.Fatalf("pathEdges() with mismatched lengths = %d edges, want 1 (min of fromIDs/toIDs/edgeTypes constrained)", len(edges))
	}
}

func TestFilterEdgesByNodesEmptyInputs(t *testing.T) {
	if got := filterEdgesByNodes(nil, []*graph.Node{{ID: "a"}}); got != nil {
		t.Errorf("filterEdgesByNodes(nil edges, ...) = %v, want nil", got)
	}
	if got := filterEdgesByNodes([]*graph.Edge{{ID: "e1"}}, nil); got != nil {
		t.Errorf("filterEdgesByNodes(..., nil nodes) = %v, want nil", got)
	}
}

func TestFilterEdgesByNodesSkipsNils(t *testing.T) {
	edges := []*graph.Edge{
		nil,
		{ID: "e1", FromID: "a", ToID: "b"},
	}
	nodes := []*graph.Node{nil, {ID: "a"}, {ID: "b"}, {ID: ""}}
	got := filterEdgesByNodes(edges, nodes)
	if len(got) != 1 || got[0].ID != "e1" {
		t.Errorf("filterEdgesByNodes() = %v, want [e1]", got)
	}
}

func TestEdgeTypesErrorCases(t *testing.T) {
	t.Run("nil edge", func(t *testing.T) {
		_, err := edgeTypes([]*graph.Edge{nil})
		if err == nil || !strings.Contains(err.Error(), "nil") {
			t.Errorf("edgeTypes(nil edge) error = %v, want nil edge error", err)
		}
	})
	t.Run("empty fromID", func(t *testing.T) {
		_, err := edgeTypes([]*graph.Edge{{FromID: "", ToID: "b", Type: "CALLS"}})
		if err == nil || !strings.Contains(err.Error(), "empty endpoint") {
			t.Errorf("edgeTypes(empty from) error = %v, want empty endpoint error", err)
		}
	})
	t.Run("empty toID", func(t *testing.T) {
		_, err := edgeTypes([]*graph.Edge{{FromID: "a", ToID: "", Type: "CALLS"}})
		if err == nil || !strings.Contains(err.Error(), "empty endpoint") {
			t.Errorf("edgeTypes(empty to) error = %v, want empty endpoint error", err)
		}
	})
	t.Run("invalid type", func(t *testing.T) {
		_, err := edgeTypes([]*graph.Edge{{FromID: "a", ToID: "b", Type: "bad-type"}})
		if err == nil {
			t.Error("edgeTypes(invalid type) error = nil, want error")
		}
	})
}

func TestRelationshipPatternsDefaultDepth(t *testing.T) {
	patterns, err := relationshipPatterns(graph.DirectionOut, nil, 0)
	if err != nil {
		t.Fatalf("relationshipPatterns() error = %v", err)
	}
	if len(patterns) != 1 || patterns[0] != "-[*1..1]->" {
		t.Errorf("relationshipPatterns(depth=0) = %v, want [-[*1..1]->]", patterns)
	}
}

func TestRelationshipPatternsInvalidDirection(t *testing.T) {
	_, err := relationshipPatterns(graph.Direction("invalid"), nil, 1)
	if err == nil || !strings.Contains(err.Error(), "unsupported direction") {
		t.Errorf("relationshipPatterns(invalid dir) error = %v, want unsupported direction error", err)
	}
}

func TestRelationshipPatternsBothDirection(t *testing.T) {
	patterns, err := relationshipPatterns(graph.DirectionBoth, []string{"CALLS"}, 2)
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if len(patterns) != 1 || patterns[0] != "-[:CALLS*1..2]-" {
		t.Errorf("patterns = %v, want [-[:CALLS*1..2]-]", patterns)
	}
}

func TestPathQueryCypherFormat(t *testing.T) {
	cypher := pathQueryCypher("node-a", "node-b", "-[:CALLS*1..2]->", 10)
	if !strings.Contains(cypher, `"node-a"`) {
		t.Errorf("pathQueryCypher() missing escaped fromID")
	}
	if !strings.Contains(cypher, `"node-b"`) {
		t.Errorf("pathQueryCypher() missing escaped toID")
	}
	if !strings.Contains(cypher, "LIMIT 10") {
		t.Errorf("pathQueryCypher() missing LIMIT clause")
	}
	if !strings.Contains(cypher, "WITH p ORDER BY length(p) ASC LIMIT 10 RETURN") {
		t.Errorf("pathQueryCypher() missing shortest-path ordering")
	}
	if !strings.Contains(cypher, "-[:CALLS*1..2]->") {
		t.Errorf("pathQueryCypher() missing pattern")
	}
}

func TestNewInvalidGraphName(t *testing.T) {
	tests := []struct {
		name      string
		graphName string
		wantErr   string
	}{
		{"empty graph name", "", "graph name is required"},
		{"invalid characters", "my-graph", "invalid graph name"},
		{"starts with digit", "1graph", "invalid graph name"},
		{"special chars", "graph@name", "invalid graph name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New(WithGraphName(tt.graphName))
			if err == nil {
				t.Fatal("New() error = nil, want error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("New() error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestNewWithoutDSNOrInstance(t *testing.T) {
	_, err := New(WithGraphName("valid_graph"))
	if err == nil {
		t.Fatal("New() error = nil, want error for missing connection config")
	}
	if !strings.Contains(err.Error(), "requires WithClientDSN or WithPostgresInstance") {
		t.Errorf("New() error = %v, want containing connection config hint", err)
	}
}

func TestNewWithInvalidInstanceName(t *testing.T) {
	_, err := New(WithPostgresInstance("nonexistent_instance"))
	if err == nil {
		t.Fatal("New() error = nil, want error for nonexistent instance")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("New() error = %v, want containing 'not found'", err)
	}
}

func TestNewWithClientBuilderError(t *testing.T) {
	oldBuilder := postgres.GetClientBuilder()
	defer postgres.SetClientBuilder(oldBuilder)

	postgres.SetClientBuilder(func(ctx context.Context, opts ...postgres.ClientBuilderOpt) (postgres.Client, error) {
		return nil, errors.New("connection refused")
	})

	_, err := New(WithGraphName("valid_graph"), WithClientDSN("postgres://bad"))
	if err == nil {
		t.Fatal("New() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "connection refused") {
		t.Errorf("New() error = %v, want containing 'connection refused'", err)
	}
}

func TestNewWithInitDBError(t *testing.T) {
	oldBuilder := postgres.GetClientBuilder()
	defer postgres.SetClientBuilder(oldBuilder)

	closeCalled := false
	postgres.SetClientBuilder(func(ctx context.Context, opts ...postgres.ClientBuilderOpt) (postgres.Client, error) {
		return &mockPostgresClient{
			execFn: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
				return nil, errors.New("extension not available")
			},
			txFn: nil,
			closeFn: func() error {
				closeCalled = true
				return nil
			},
		}, nil
	})

	_, err := New(WithGraphName("valid_graph"), WithClientDSN("postgres://localhost/test"))
	if err == nil {
		t.Fatal("New() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "extension") {
		t.Errorf("New() error = %v, want containing 'extension'", err)
	}
	if !closeCalled {
		t.Error("expected Close() to be called on client after initDB failure")
	}
}

func TestCloseNilClient(t *testing.T) {
	store := &Store{
		client:     nil,
		option:     options{graphName: "test_graph"},
		edgeLabels: make(map[string]struct{}),
	}
	err := store.Close()
	if err != nil {
		t.Errorf("Close() with nil client = %v, want nil", err)
	}
}

func TestCloseReturnsError(t *testing.T) {
	store := newTestStore(&mockPostgresClient{
		closeFn: func() error {
			return errors.New("close failed")
		},
	})
	err := store.Close()
	if err == nil || !strings.Contains(err.Error(), "close failed") {
		t.Errorf("Close() = %v, want 'close failed' error", err)
	}
}

func TestCloseSuccess(t *testing.T) {
	closed := false
	store := newTestStore(&mockPostgresClient{
		closeFn: func() error {
			closed = true
			return nil
		},
	})
	err := store.Close()
	if err != nil {
		t.Errorf("Close() = %v, want nil", err)
	}
	if !closed {
		t.Error("Close() did not call client.Close()")
	}
}

func TestAddNodesEmptySlice(t *testing.T) {
	store := newTestStore(&mockPostgresClient{
		txFn: func(ctx context.Context, fn postgres.TxFunc) error {
			t.Fatal("Transaction should not be called for empty nodes")
			return nil
		},
	})
	err := store.AddNodes(context.Background(), nil)
	if err != nil {
		t.Errorf("AddNodes(nil) = %v, want nil", err)
	}
	err = store.AddNodes(context.Background(), []*graph.Node{})
	if err != nil {
		t.Errorf("AddNodes([]) = %v, want nil", err)
	}
}

func TestAddNodesTransactionCalled(t *testing.T) {
	txCalled := false
	store := newTestStore(&mockPostgresClient{
		txFn: func(ctx context.Context, fn postgres.TxFunc) error {
			txCalled = true
			return errors.New("tx mock")
		},
	})
	err := store.AddNodes(context.Background(), []*graph.Node{{ID: "a", Name: "A"}})
	if !txCalled {
		t.Error("expected Transaction to be called")
	}
	if err == nil || !strings.Contains(err.Error(), "tx mock") {
		t.Errorf("AddNodes() error = %v, want tx mock error", err)
	}
}

func TestAddEdgesEmptySlice(t *testing.T) {
	store := newTestStore(&mockPostgresClient{
		txFn: func(ctx context.Context, fn postgres.TxFunc) error {
			t.Fatal("Transaction should not be called for empty edges")
			return nil
		},
	})
	err := store.AddEdges(context.Background(), nil)
	if err != nil {
		t.Errorf("AddEdges(nil) = %v, want nil", err)
	}
	err = store.AddEdges(context.Background(), []*graph.Edge{})
	if err != nil {
		t.Errorf("AddEdges([]) = %v, want nil", err)
	}
}

func TestAddEdgesNilEdge(t *testing.T) {
	store := newTestStore(&mockPostgresClient{})
	err := store.AddEdges(context.Background(), []*graph.Edge{nil})
	if err == nil || !strings.Contains(err.Error(), "nil") {
		t.Errorf("AddEdges([nil]) error = %v, want nil edge error", err)
	}
}

func TestAddEdgesEmptyEndpoints(t *testing.T) {
	store := newTestStore(&mockPostgresClient{})
	err := store.AddEdges(context.Background(), []*graph.Edge{
		{FromID: "", ToID: "b", Type: "CALLS"},
	})
	if err == nil || !strings.Contains(err.Error(), "empty endpoint") {
		t.Errorf("AddEdges(empty from) error = %v, want empty endpoint error", err)
	}

	err = store.AddEdges(context.Background(), []*graph.Edge{
		{FromID: "a", ToID: "", Type: "CALLS"},
	})
	if err == nil || !strings.Contains(err.Error(), "empty endpoint") {
		t.Errorf("AddEdges(empty to) error = %v, want empty endpoint error", err)
	}
}

func TestAddEdgesInvalidType(t *testing.T) {
	store := newTestStore(&mockPostgresClient{})
	err := store.AddEdges(context.Background(), []*graph.Edge{
		{FromID: "a", ToID: "b", Type: "bad-type"},
	})
	if err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Errorf("AddEdges(invalid type) error = %v, want invalid type error", err)
	}
}

func TestAddEdgesTransactionError(t *testing.T) {
	store := newTestStore(&mockPostgresClient{
		txFn: func(ctx context.Context, fn postgres.TxFunc) error {
			return errors.New("tx failed")
		},
	})
	err := store.AddEdges(context.Background(), []*graph.Edge{
		{FromID: "a", ToID: "b", Type: "CALLS"},
	})
	if err == nil || !strings.Contains(err.Error(), "tx failed") {
		t.Errorf("AddEdges() error = %v, want tx failed error", err)
	}
}

func TestTraverseNilQuery(t *testing.T) {
	store := newTestStore(&mockPostgresClient{})
	_, err := store.Traverse(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "query is required") {
		t.Errorf("Traverse(nil) error = %v, want required error", err)
	}
}

func TestTraverseEmptyStartIDs(t *testing.T) {
	store := newTestStore(&mockPostgresClient{})
	_, err := store.Traverse(context.Background(), &graph.TraverseQuery{})
	if err == nil || !strings.Contains(err.Error(), "start_ids cannot be empty") {
		t.Errorf("Traverse(empty startIDs) error = %v, want start_ids error", err)
	}
}

func TestTraverseReturnsEmptyResultOnNoData(t *testing.T) {
	store := newTestStore(&mockPostgresClient{})
	result, err := store.Traverse(context.Background(), &graph.TraverseQuery{
		StartIDs: []string{"a"},
		MaxDepth: 2,
		MaxNodes: 50,
	})
	if err != nil {
		t.Fatalf("Traverse() error = %v", err)
	}
	if result == nil {
		t.Fatal("Traverse() returned nil result")
	}
	if len(result.Nodes) != 0 {
		t.Errorf("Traverse() nodes = %d, want 0", len(result.Nodes))
	}
	if result.Truncated {
		t.Error("Traverse() truncated = true, want false")
	}
}

func TestTraverseTransactionError(t *testing.T) {
	store := newTestStore(&mockPostgresClient{
		txFn: func(ctx context.Context, fn postgres.TxFunc) error {
			return errors.New("db unavailable")
		},
	})
	_, err := store.Traverse(context.Background(), &graph.TraverseQuery{
		StartIDs: []string{"a"},
	})
	if err == nil || !strings.Contains(err.Error(), "db unavailable") {
		t.Errorf("Traverse() error = %v, want db unavailable error", err)
	}
}

func TestFindPathsNilQuery(t *testing.T) {
	store := newTestStore(&mockPostgresClient{})
	_, err := store.FindPaths(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "path query is required") {
		t.Errorf("FindPaths(nil) error = %v, want required error", err)
	}
}

func TestFindPathsEmptyFromID(t *testing.T) {
	store := newTestStore(&mockPostgresClient{})
	_, err := store.FindPaths(context.Background(), &graph.PathQuery{
		FromID: "",
		ToID:   "b",
	})
	if err == nil || !strings.Contains(err.Error(), "from_id and to_id are required") {
		t.Errorf("FindPaths(empty from) error = %v, want required error", err)
	}
}

func TestFindPathsEmptyToID(t *testing.T) {
	store := newTestStore(&mockPostgresClient{})
	_, err := store.FindPaths(context.Background(), &graph.PathQuery{
		FromID: "a",
		ToID:   "",
	})
	if err == nil || !strings.Contains(err.Error(), "from_id and to_id are required") {
		t.Errorf("FindPaths(empty to) error = %v, want required error", err)
	}
}

func TestFindPathsReturnsEmptyResult(t *testing.T) {
	store := newTestStore(&mockPostgresClient{})
	result, err := store.FindPaths(context.Background(), &graph.PathQuery{
		FromID:   "a",
		ToID:     "b",
		MaxDepth: 3,
		MaxPaths: 5,
	})
	if err != nil {
		t.Fatalf("FindPaths() error = %v", err)
	}
	if result == nil {
		t.Fatal("FindPaths() returned nil result")
	}
	if len(result.Paths) != 0 {
		t.Errorf("FindPaths() paths = %d, want 0", len(result.Paths))
	}
}

func TestFindPathsTransactionError(t *testing.T) {
	store := newTestStore(&mockPostgresClient{
		txFn: func(ctx context.Context, fn postgres.TxFunc) error {
			return errors.New("connection lost")
		},
	})
	_, err := store.FindPaths(context.Background(), &graph.PathQuery{
		FromID: "a",
		ToID:   "b",
	})
	if err == nil || !strings.Contains(err.Error(), "connection lost") {
		t.Errorf("FindPaths() error = %v, want connection lost error", err)
	}
}

func TestEnsureEdgeLabelsEmpty(t *testing.T) {
	store := newTestStore(&mockPostgresClient{
		txFn: func(ctx context.Context, fn postgres.TxFunc) error {
			t.Fatal("Transaction should not be called for empty labels")
			return nil
		},
	})
	err := store.ensureEdgeLabels(context.Background(), nil)
	if err != nil {
		t.Errorf("ensureEdgeLabels(nil) = %v, want nil", err)
	}
	err = store.ensureEdgeLabels(context.Background(), []string{})
	if err != nil {
		t.Errorf("ensureEdgeLabels([]) = %v, want nil", err)
	}
}

func TestEnsureEdgeLabelsAlreadyCached(t *testing.T) {
	store := newTestStore(&mockPostgresClient{
		txFn: func(ctx context.Context, fn postgres.TxFunc) error {
			t.Fatal("Transaction should not be called for cached labels")
			return nil
		},
	})
	store.edgeLabels["CALLS"] = struct{}{}
	store.edgeLabels["METHOD"] = struct{}{}

	err := store.ensureEdgeLabels(context.Background(), []string{"CALLS", "METHOD"})
	if err != nil {
		t.Errorf("ensureEdgeLabels(cached) = %v, want nil", err)
	}
}

func TestEnsureEdgeLabelsTransactionError(t *testing.T) {
	store := newTestStore(&mockPostgresClient{
		txFn: func(ctx context.Context, fn postgres.TxFunc) error {
			return errors.New("lock timeout")
		},
	})
	err := store.ensureEdgeLabels(context.Background(), []string{"NEW_LABEL"})
	if err == nil || !strings.Contains(err.Error(), "lock timeout") {
		t.Errorf("ensureEdgeLabels() error = %v, want lock timeout error", err)
	}
	if _, ok := store.edgeLabels["NEW_LABEL"]; ok {
		t.Error("label should not be cached after transaction error")
	}
}

func TestEnsureEdgeLabelsSuccess(t *testing.T) {
	store := newTestStore(&mockPostgresClient{})
	err := store.ensureEdgeLabels(context.Background(), []string{"NEW_LABEL", "ANOTHER"})
	if err != nil {
		t.Fatalf("ensureEdgeLabels() error = %v", err)
	}
	if _, ok := store.edgeLabels["NEW_LABEL"]; !ok {
		t.Error("expected NEW_LABEL to be cached")
	}
	if _, ok := store.edgeLabels["ANOTHER"]; !ok {
		t.Error("expected ANOTHER to be cached")
	}
}

func TestEnsureEdgeLabelsPartialCache(t *testing.T) {
	store := newTestStore(&mockPostgresClient{})
	store.edgeLabels["EXISTING"] = struct{}{}

	err := store.ensureEdgeLabels(context.Background(), []string{"EXISTING", "NEW_ONE"})
	if err != nil {
		t.Fatalf("ensureEdgeLabels() error = %v", err)
	}
	if _, ok := store.edgeLabels["NEW_ONE"]; !ok {
		t.Error("expected NEW_ONE to be cached")
	}
}

func TestWithAgeTxTransactionError(t *testing.T) {
	store := newTestStore(&mockPostgresClient{
		txFn: func(ctx context.Context, fn postgres.TxFunc) error {
			return errors.New("begin failed")
		},
	})
	err := store.withAgeTx(context.Background(), func(tx *sql.Tx) error {
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "begin failed") {
		t.Errorf("withAgeTx() error = %v, want begin failed", err)
	}
}

func TestWithLockedAgeTxTransactionError(t *testing.T) {
	store := newTestStore(&mockPostgresClient{
		txFn: func(ctx context.Context, fn postgres.TxFunc) error {
			return errors.New("lock acquire failed")
		},
	})
	err := store.withLockedAgeTx(context.Background(), func(tx *sql.Tx) error {
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "lock acquire failed") {
		t.Errorf("withLockedAgeTx() error = %v, want lock acquire failed", err)
	}
}

func TestCypherSQL(t *testing.T) {
	store := newTestStore(nil)
	got := store.cypherSQL("MATCH (n) RETURN n", "result agtype")
	if !strings.Contains(got, "test_graph") {
		t.Errorf("cypherSQL() missing graph name, got %q", got)
	}
	if !strings.Contains(got, "$age$") {
		t.Errorf("cypherSQL() missing dollar quoting, got %q", got)
	}
	if !strings.Contains(got, "result agtype") {
		t.Errorf("cypherSQL() missing columns, got %q", got)
	}
	if !strings.Contains(got, "SELECT * FROM cypher(") {
		t.Errorf("cypherSQL() missing cypher function call, got %q", got)
	}
}

func TestOptionsBuilderOptions(t *testing.T) {
	t.Run("empty options returns error", func(t *testing.T) {
		o := options{graphName: "test"}
		_, err := o.builderOptions()
		if err == nil {
			t.Fatal("builderOptions() error = nil, want error for missing connection config")
		}
		if !strings.Contains(err.Error(), "requires WithClientDSN or WithPostgresInstance") {
			t.Errorf("builderOptions() error = %v, want containing connection config hint", err)
		}
	})

	t.Run("dsn returns WithClientConnString", func(t *testing.T) {
		o := options{graphName: "test", dsn: "postgres://localhost/test"}
		opts, err := o.builderOptions()
		if err != nil {
			t.Fatalf("builderOptions() error = %v", err)
		}
		if len(opts) != 1 {
			t.Errorf("builderOptions() returned %d opts, want 1", len(opts))
		}
	})

	t.Run("nonexistent instance returns error", func(t *testing.T) {
		o := options{graphName: "test", instanceName: "nonexistent"}
		_, err := o.builderOptions()
		if err == nil || !strings.Contains(err.Error(), "not found") {
			t.Errorf("builderOptions() error = %v, want not found error", err)
		}
	})
}

func TestWithGraphNameOption(t *testing.T) {
	o := defaultOptions
	WithGraphName("custom_graph")(&o)
	if o.graphName != "custom_graph" {
		t.Errorf("WithGraphName() graphName = %q, want %q", o.graphName, "custom_graph")
	}
}

func TestWithClientDSNOption(t *testing.T) {
	o := defaultOptions
	WithClientDSN("postgres://host/db")(&o)
	if o.dsn != "postgres://host/db" {
		t.Errorf("WithClientDSN() dsn = %q, want %q", o.dsn, "postgres://host/db")
	}
}

func TestWithPostgresInstanceOption(t *testing.T) {
	o := defaultOptions
	WithPostgresInstance("my_instance")(&o)
	if o.instanceName != "my_instance" {
		t.Errorf("WithPostgresInstance() instanceName = %q, want %q", o.instanceName, "my_instance")
	}
}

func TestDefaultOptions(t *testing.T) {
	if defaultOptions.graphName != defaultGraphName {
		t.Errorf("defaultOptions.graphName = %q, want %q", defaultOptions.graphName, defaultGraphName)
	}
}

func TestTraverseDefaultDepthAndMaxNodes(t *testing.T) {
	store := newTestStore(&mockPostgresClient{
		txFn: func(ctx context.Context, fn postgres.TxFunc) error {
			return errors.New("stop here")
		},
	})
	_, err := store.Traverse(context.Background(), &graph.TraverseQuery{
		StartIDs: []string{"a"},
		MaxDepth: 0,
		MaxNodes: 0,
	})
	if err == nil {
		t.Fatal("expected error from mock")
	}
}

func TestFindPathsDefaultDepthAndMaxPaths(t *testing.T) {
	store := newTestStore(&mockPostgresClient{
		txFn: func(ctx context.Context, fn postgres.TxFunc) error {
			return errors.New("stop here")
		},
	})
	_, err := store.FindPaths(context.Background(), &graph.PathQuery{
		FromID:   "a",
		ToID:     "b",
		MaxDepth: 0,
		MaxPaths: 0,
	})
	if err == nil {
		t.Fatal("expected error from mock")
	}
}

func TestParseAgMetadataInvalidJSON(t *testing.T) {
	got := parseAgMetadata("{not valid json}")
	if got != nil {
		t.Errorf("parseAgMetadata(invalid) = %v, want nil", got)
	}
}

func TestCypherValueMapWithNilValue(t *testing.T) {
	got := cypherValue(map[string]any{"key": nil})
	want := "{key: null}"
	if got != want {
		t.Errorf("cypherValue(map with nil) = %q, want %q", got, want)
	}
}

func TestCypherValueNestedSliceInMap(t *testing.T) {
	got := cypherValue(map[string]any{
		"items": []any{1, 2, 3},
	})
	want := "{items: [1, 2, 3]}"
	if got != want {
		t.Errorf("cypherValue(nested slice) = %q, want %q", got, want)
	}
}

func TestCypherValueArray(t *testing.T) {
	got := cypherValue([3]int{1, 2, 3})
	want := "[1, 2, 3]"
	if got != want {
		t.Errorf("cypherValue(array) = %q, want %q", got, want)
	}
}

func TestAddNodesWithTransactionError(t *testing.T) {
	store := newTestStore(&mockPostgresClient{
		txFn: func(ctx context.Context, fn postgres.TxFunc) error {
			return errors.New("connection reset")
		},
	})
	err := store.AddNodes(context.Background(), []*graph.Node{
		{ID: "a", Name: "Node A"},
	})
	if err == nil || !strings.Contains(err.Error(), "connection reset") {
		t.Errorf("AddNodes() error = %v, want connection reset error", err)
	}
}

func TestAddEdgesEnsureLabelsError(t *testing.T) {
	store := newTestStore(&mockPostgresClient{
		txFn: func(ctx context.Context, fn postgres.TxFunc) error {
			return errors.New("label creation failed")
		},
	})
	err := store.AddEdges(context.Background(), []*graph.Edge{
		{FromID: "a", ToID: "b", Type: "CALLS"},
	})
	if err == nil || !strings.Contains(err.Error(), "label creation failed") {
		t.Errorf("AddEdges() error = %v, want label creation failed", err)
	}
}

func TestAddEdgesMultipleEdgeTypes(t *testing.T) {
	txCount := 0
	store := newTestStore(&mockPostgresClient{
		txFn: func(ctx context.Context, fn postgres.TxFunc) error {
			txCount++
			return errors.New("stop")
		},
	})
	_ = store.AddEdges(context.Background(), []*graph.Edge{
		{FromID: "a", ToID: "b", Type: "CALLS"},
		{FromID: "b", ToID: "c", Type: "METHOD"},
	})
	if txCount < 1 {
		t.Error("expected at least one transaction call for edge label creation")
	}
}

func TestAddNodesMultipleNodes(t *testing.T) {
	store := newTestStore(&mockPostgresClient{
		txFn: func(ctx context.Context, fn postgres.TxFunc) error {
			return errors.New("stop at tx")
		},
	})
	err := store.AddNodes(context.Background(), []*graph.Node{
		{ID: "a", Name: "A"},
		{ID: "b", Name: "B"},
	})
	if err == nil || !strings.Contains(err.Error(), "stop at tx") {
		t.Errorf("AddNodes() error = %v, want stop at tx", err)
	}
}

func TestStoreImplementsInterface(t *testing.T) {
	var _ graphstore.Store = (*Store)(nil)
}

func TestRelationshipPatternsEmptyDirection(t *testing.T) {
	patterns, err := relationshipPatterns("", nil, 2)
	if err != nil {
		t.Fatalf("relationshipPatterns() error = %v", err)
	}
	if len(patterns) != 1 || patterns[0] != "-[*1..2]->" {
		t.Errorf("patterns = %v, want [-[*1..2]->]", patterns)
	}
}

func TestRelationshipPatternsMultipleTypesInDirection(t *testing.T) {
	patterns, err := relationshipPatterns(graph.DirectionIn, []string{"A", "B"}, 1)
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	want := []string{"<-[:A]-", "<-[:B]-"}
	if len(patterns) != len(want) {
		t.Fatalf("len(patterns) = %d, want %d", len(patterns), len(want))
	}
	for i := range want {
		if patterns[i] != want[i] {
			t.Errorf("patterns[%d] = %q, want %q", i, patterns[i], want[i])
		}
	}
}

func TestRelationshipPatternsMultipleTypesBothDirection(t *testing.T) {
	patterns, err := relationshipPatterns(graph.DirectionBoth, []string{"X", "Y"}, 1)
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	want := []string{"-[:X]-", "-[:Y]-"}
	if len(patterns) != len(want) {
		t.Fatalf("len(patterns) = %d, want %d", len(patterns), len(want))
	}
	for i := range want {
		if patterns[i] != want[i] {
			t.Errorf("patterns[%d] = %q, want %q", i, patterns[i], want[i])
		}
	}
}

func TestUniqueEdgesEmptyID(t *testing.T) {
	edges := []*graph.Edge{
		{ID: "", FromID: "x", ToID: "y", Type: "A"},
		{ID: "", FromID: "x", ToID: "y", Type: "A"},
		{ID: "", FromID: "x", ToID: "y", Type: "B"},
	}
	got := uniqueEdges(edges)
	if len(got) != 2 {
		t.Errorf("uniqueEdges() = %d, want 2", len(got))
	}
}

func TestPathEdgesEmptySlices(t *testing.T) {
	edges := pathEdges(&agPath{
		edgeIDs:   nil,
		fromIDs:   nil,
		toIDs:     nil,
		edgeTypes: nil,
	})
	if len(edges) != 0 {
		t.Errorf("pathEdges(empty) = %d edges, want 0", len(edges))
	}
}

func TestFilterEdgesByNodesNilEdgeInSlice(t *testing.T) {
	edges := []*graph.Edge{
		nil,
		nil,
	}
	nodes := []*graph.Node{{ID: "a"}}
	got := filterEdgesByNodes(edges, nodes)
	if len(got) != 0 {
		t.Errorf("filterEdgesByNodes(all nil edges) = %d, want 0", len(got))
	}
}

func TestParseAgStringNullVariants(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"null::agtype", ""},
		{" null ::agtype", ""},
		{"null", ""},
	}
	for _, tt := range tests {
		got := parseAgString(tt.input)
		if got != tt.want {
			t.Errorf("parseAgString(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestParseAgStringListSingle(t *testing.T) {
	got := parseAgStringList(`["hello"]::agtype`)
	if len(got) != 1 || got[0] != "hello" {
		t.Errorf("parseAgStringList(single) = %v, want [hello]", got)
	}
}

func TestCypherValueMapSortedKeys(t *testing.T) {
	got := cypherValue(map[string]any{
		"z": 1,
		"a": 2,
		"m": 3,
	})
	if !strings.HasPrefix(got, "{a:") {
		t.Errorf("cypherValue() keys not sorted, got %q", got)
	}
}

func TestCypherValueJSONMarshalError(t *testing.T) {
	got := cypherValue(make(chan int))
	if got != "null" {
		t.Errorf("cypherValue(unmarshalable) = %q, want %q", got, "null")
	}
}

func TestWithAgeTxSuccess(t *testing.T) {
	store, mock := newSqlmockStore(t)
	defer store.client.Close()

	mock.ExpectBegin()
	mock.ExpectExec("LOAD 'age'").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`SET search_path`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()

	fnCalled := false
	err := store.withAgeTx(context.Background(), func(tx *sql.Tx) error {
		fnCalled = true
		return nil
	})
	if err != nil {
		t.Fatalf("withAgeTx() error = %v", err)
	}
	if !fnCalled {
		t.Error("withAgeTx() did not call fn")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestWithAgeTxLoadExtensionError(t *testing.T) {
	store, mock := newSqlmockStore(t)
	defer store.client.Close()

	mock.ExpectBegin()
	mock.ExpectExec("LOAD 'age'").WillReturnError(errors.New("extension missing"))
	mock.ExpectRollback()

	err := store.withAgeTx(context.Background(), func(tx *sql.Tx) error {
		t.Fatal("fn should not be called")
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "load extension") {
		t.Errorf("withAgeTx() error = %v, want load extension error", err)
	}
}

func TestWithAgeTxSetSearchPathError(t *testing.T) {
	store, mock := newSqlmockStore(t)
	defer store.client.Close()

	mock.ExpectBegin()
	mock.ExpectExec("LOAD 'age'").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`SET search_path`).WillReturnError(errors.New("path error"))
	mock.ExpectRollback()

	err := store.withAgeTx(context.Background(), func(tx *sql.Tx) error {
		t.Fatal("fn should not be called")
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "set search path") {
		t.Errorf("withAgeTx() error = %v, want set search path error", err)
	}
}

func TestWithAgeTxFnError(t *testing.T) {
	store, mock := newSqlmockStore(t)
	defer store.client.Close()

	mock.ExpectBegin()
	mock.ExpectExec("LOAD 'age'").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`SET search_path`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectRollback()

	err := store.withAgeTx(context.Background(), func(tx *sql.Tx) error {
		return errors.New("user error")
	})
	if err == nil || !strings.Contains(err.Error(), "user error") {
		t.Errorf("withAgeTx() error = %v, want user error", err)
	}
}

func TestWithLockedAgeTxSuccess(t *testing.T) {
	store, mock := newSqlmockStore(t)
	defer store.client.Close()

	mock.ExpectBegin()
	mock.ExpectExec("SELECT pg_advisory_xact_lock").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("LOAD 'age'").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`SET search_path`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()

	fnCalled := false
	err := store.withLockedAgeTx(context.Background(), func(tx *sql.Tx) error {
		fnCalled = true
		return nil
	})
	if err != nil {
		t.Fatalf("withLockedAgeTx() error = %v", err)
	}
	if !fnCalled {
		t.Error("withLockedAgeTx() did not call fn")
	}
}

func TestWithLockedAgeTxLockError(t *testing.T) {
	store, mock := newSqlmockStore(t)
	defer store.client.Close()

	mock.ExpectBegin()
	mock.ExpectExec("SELECT pg_advisory_xact_lock").WillReturnError(errors.New("lock contention"))
	mock.ExpectRollback()

	err := store.withLockedAgeTx(context.Background(), func(tx *sql.Tx) error {
		t.Fatal("fn should not be called")
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "acquire advisory lock") {
		t.Errorf("withLockedAgeTx() error = %v, want advisory lock error", err)
	}
}

func TestWithLockedAgeTxLoadError(t *testing.T) {
	store, mock := newSqlmockStore(t)
	defer store.client.Close()

	mock.ExpectBegin()
	mock.ExpectExec("SELECT pg_advisory_xact_lock").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("LOAD 'age'").WillReturnError(errors.New("not found"))
	mock.ExpectRollback()

	err := store.withLockedAgeTx(context.Background(), func(tx *sql.Tx) error {
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "load extension") {
		t.Errorf("withLockedAgeTx() error = %v, want load extension error", err)
	}
}

func TestWithLockedAgeTxSetPathError(t *testing.T) {
	store, mock := newSqlmockStore(t)
	defer store.client.Close()

	mock.ExpectBegin()
	mock.ExpectExec("SELECT pg_advisory_xact_lock").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("LOAD 'age'").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`SET search_path`).WillReturnError(errors.New("config error"))
	mock.ExpectRollback()

	err := store.withLockedAgeTx(context.Background(), func(tx *sql.Tx) error {
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "set search path") {
		t.Errorf("withLockedAgeTx() error = %v, want set search path error", err)
	}
}

func TestExecCypherSuccess(t *testing.T) {
	store, mock := newSqlmockStore(t)
	defer store.client.Close()

	mock.ExpectBegin()
	mock.ExpectExec("SELECT \\* FROM cypher").WillReturnResult(sqlmock.NewResult(0, 0))

	tx, _ := store.client.(*sqlmockClient).db.Begin()
	err := store.execCypher(context.Background(), tx, "MATCH (n) RETURN n")
	if err != nil {
		t.Fatalf("execCypher() error = %v", err)
	}
}

func TestExecCypherError(t *testing.T) {
	store, mock := newSqlmockStore(t)
	defer store.client.Close()

	mock.ExpectBegin()
	mock.ExpectExec("SELECT \\* FROM cypher").WillReturnError(errors.New("syntax error"))

	tx, _ := store.client.(*sqlmockClient).db.Begin()
	err := store.execCypher(context.Background(), tx, "INVALID CYPHER")
	if err == nil || !strings.Contains(err.Error(), "execute cypher") {
		t.Errorf("execCypher() error = %v, want execute cypher error", err)
	}
}

func TestAddNodesWithSqlmock(t *testing.T) {
	store, mock := newSqlmockStore(t)
	defer store.client.Close()

	mock.ExpectBegin()
	mock.ExpectExec("LOAD 'age'").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`SET search_path`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("SELECT \\* FROM cypher").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()

	err := store.AddNodes(context.Background(), []*graph.Node{
		{ID: "node_1", Name: "Test", Content: "content", Metadata: map[string]any{"k": "v"}},
	})
	if err != nil {
		t.Fatalf("AddNodes() error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestAddNodesNilNodeWithSqlmock(t *testing.T) {
	store, mock := newSqlmockStore(t)
	defer store.client.Close()

	mock.ExpectBegin()
	mock.ExpectExec("LOAD 'age'").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`SET search_path`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectRollback()

	err := store.AddNodes(context.Background(), []*graph.Node{nil})
	if err == nil || !strings.Contains(err.Error(), "nil") {
		t.Errorf("AddNodes([nil]) error = %v, want nil node error", err)
	}
}

func TestAddNodesEmptyIDWithSqlmock(t *testing.T) {
	store, mock := newSqlmockStore(t)
	defer store.client.Close()

	mock.ExpectBegin()
	mock.ExpectExec("LOAD 'age'").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`SET search_path`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectRollback()

	err := store.AddNodes(context.Background(), []*graph.Node{{ID: "", Name: "no-id"}})
	if err == nil || !strings.Contains(err.Error(), "empty id") {
		t.Errorf("AddNodes(empty id) error = %v, want empty id error", err)
	}
}

func TestAddNodesMultipleWithSqlmock(t *testing.T) {
	store, mock := newSqlmockStore(t)
	defer store.client.Close()

	mock.ExpectBegin()
	mock.ExpectExec("LOAD 'age'").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`SET search_path`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("SELECT \\* FROM cypher").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("SELECT \\* FROM cypher").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()

	err := store.AddNodes(context.Background(), []*graph.Node{
		{ID: "a", Name: "A"},
		{ID: "b", Name: "B"},
	})
	if err != nil {
		t.Fatalf("AddNodes() error = %v", err)
	}
}

func TestAddNodesExecError(t *testing.T) {
	store, mock := newSqlmockStore(t)
	defer store.client.Close()

	mock.ExpectBegin()
	mock.ExpectExec("LOAD 'age'").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`SET search_path`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("SELECT \\* FROM cypher").WillReturnError(errors.New("db full"))
	mock.ExpectRollback()

	err := store.AddNodes(context.Background(), []*graph.Node{{ID: "a", Name: "A"}})
	if err == nil || !strings.Contains(err.Error(), "execute cypher") {
		t.Errorf("AddNodes() error = %v, want execute cypher error", err)
	}
}

func TestAddEdgesWithSqlmock(t *testing.T) {
	store, mock := newSqlmockStore(t)
	defer store.client.Close()
	store.edgeLabels["CALLS"] = struct{}{}

	mock.ExpectBegin()
	mock.ExpectExec("LOAD 'age'").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`SET search_path`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("SELECT \\* FROM cypher").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()

	err := store.AddEdges(context.Background(), []*graph.Edge{
		{ID: "e1", FromID: "a", ToID: "b", Type: "CALLS", Metadata: map[string]any{"w": 1}},
	})
	if err != nil {
		t.Fatalf("AddEdges() error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestAddEdgesWithoutIDWithSqlmock(t *testing.T) {
	store, mock := newSqlmockStore(t)
	defer store.client.Close()
	store.edgeLabels["CALLS"] = struct{}{}

	mock.ExpectBegin()
	mock.ExpectExec("LOAD 'age'").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`SET search_path`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("SELECT \\* FROM cypher").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()

	err := store.AddEdges(context.Background(), []*graph.Edge{
		{FromID: "a", ToID: "b", Type: "CALLS"},
	})
	if err != nil {
		t.Fatalf("AddEdges() error = %v", err)
	}
}

func TestAddEdgesNilInsideTxWithSqlmock(t *testing.T) {
	store, mock := newSqlmockStore(t)
	defer store.client.Close()
	store.edgeLabels["CALLS"] = struct{}{}

	mock.ExpectBegin()
	mock.ExpectExec("LOAD 'age'").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`SET search_path`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("SELECT \\* FROM cypher").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectRollback()

	err := store.AddEdges(context.Background(), []*graph.Edge{
		{FromID: "a", ToID: "b", Type: "CALLS"},
		nil,
	})
	if err == nil || !strings.Contains(err.Error(), "nil") {
		t.Errorf("AddEdges(nil at index 1) error = %v, want nil error", err)
	}
}

func TestAddEdgesEmptyEndpointInsideTxWithSqlmock(t *testing.T) {
	store, mock := newSqlmockStore(t)
	defer store.client.Close()
	store.edgeLabels["CALLS"] = struct{}{}

	mock.ExpectBegin()
	mock.ExpectExec("LOAD 'age'").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`SET search_path`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("SELECT \\* FROM cypher").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectRollback()

	err := store.AddEdges(context.Background(), []*graph.Edge{
		{FromID: "a", ToID: "b", Type: "CALLS"},
		{FromID: "", ToID: "c", Type: "CALLS"},
	})
	if err == nil || !strings.Contains(err.Error(), "empty endpoint") {
		t.Errorf("AddEdges(empty endpoint in tx) error = %v, want empty endpoint error", err)
	}
}

func TestAddEdgesInvalidTypeInsideTxWithSqlmock(t *testing.T) {
	store, mock := newSqlmockStore(t)
	defer store.client.Close()
	store.edgeLabels["CALLS"] = struct{}{}
	store.edgeLabels["bad_type"] = struct{}{}

	mock.ExpectBegin()
	mock.ExpectExec("LOAD 'age'").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`SET search_path`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("SELECT \\* FROM cypher").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectRollback()

	err := store.AddEdges(context.Background(), []*graph.Edge{
		{FromID: "a", ToID: "b", Type: "CALLS"},
		{FromID: "b", ToID: "c", Type: "bad-type"},
	})
	if err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Errorf("AddEdges(invalid type in tx) error = %v, want invalid error", err)
	}
}

func TestTraverseWithSqlmock(t *testing.T) {
	store, mock := newSqlmockStore(t)
	defer store.client.Close()

	mock.ExpectBegin()
	mock.ExpectExec("LOAD 'age'").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`SET search_path`).WillReturnResult(sqlmock.NewResult(0, 0))

	nodeRows := sqlmock.NewRows([]string{"id", "name", "content", "metadata"}).
		AddRow(`"node_a"::agtype`, `"Node A"::agtype`, `"content a"::agtype`, `{"kind": "func"}::agtype`)
	mock.ExpectQuery("SELECT \\* FROM cypher").WillReturnRows(nodeRows)

	traverseNodeRows := sqlmock.NewRows([]string{"id", "name", "content", "metadata"}).
		AddRow(`"node_b"::agtype`, `"Node B"::agtype`, `"content b"::agtype`, `null::agtype`)
	mock.ExpectQuery("SELECT \\* FROM cypher").WillReturnRows(traverseNodeRows)

	edgeRows := sqlmock.NewRows([]string{"id", "from_id", "to_id", "edge_type", "metadata"}).
		AddRow(`"e1"::agtype`, `"node_a"::agtype`, `"node_b"::agtype`, `"CALLS"::agtype`, `null::agtype`)
	mock.ExpectQuery("SELECT \\* FROM cypher").WillReturnRows(edgeRows)

	mock.ExpectCommit()

	result, err := store.Traverse(context.Background(), &graph.TraverseQuery{
		StartIDs:  []string{"node_a"},
		Direction: graph.DirectionOut,
		MaxDepth:  1,
		MaxNodes:  100,
	})
	if err != nil {
		t.Fatalf("Traverse() error = %v", err)
	}
	if result == nil {
		t.Fatal("Traverse() returned nil")
	}
	if len(result.Nodes) < 1 {
		t.Errorf("Traverse() returned %d nodes, want >= 1", len(result.Nodes))
	}
}

func TestTraverseSetsTruncatedOnQueryOverflow(t *testing.T) {
	store, mock := newSqlmockStore(t)
	defer store.client.Close()

	mock.ExpectBegin()
	mock.ExpectExec("LOAD 'age'").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`SET search_path`).WillReturnResult(sqlmock.NewResult(0, 0))

	nodeRows := sqlmock.NewRows([]string{"id", "name", "content", "metadata"}).
		AddRow(`"node_a"::agtype`, `"Node A"::agtype`, `""::agtype`, `null::agtype`)
	mock.ExpectQuery("SELECT \\* FROM cypher").WillReturnRows(nodeRows)

	traverseNodeRows := sqlmock.NewRows([]string{"id", "name", "content", "metadata"}).
		AddRow(`"node_b"::agtype`, `"Node B"::agtype`, `""::agtype`, `null::agtype`).
		AddRow(`"node_c"::agtype`, `"Node C"::agtype`, `""::agtype`, `null::agtype`)
	mock.ExpectQuery("SELECT \\* FROM cypher").WillReturnRows(traverseNodeRows)

	edgeRows := sqlmock.NewRows([]string{"id", "from_id", "to_id", "edge_type", "metadata"})
	mock.ExpectQuery("SELECT \\* FROM cypher").WillReturnRows(edgeRows)

	mock.ExpectCommit()

	result, err := store.Traverse(context.Background(), &graph.TraverseQuery{
		StartIDs:  []string{"node_a"},
		Direction: graph.DirectionOut,
		MaxDepth:  1,
		MaxNodes:  1,
	})
	if err != nil {
		t.Fatalf("Traverse() error = %v", err)
	}
	if !result.Truncated {
		t.Fatal("Traverse() truncated = false, want true")
	}
	if len(result.Nodes) != 1 {
		t.Fatalf("Traverse() nodes = %d, want 1", len(result.Nodes))
	}
}

func TestTraverseNotTruncatedWhenOnlyEdgesOverflow(t *testing.T) {
	store, mock := newSqlmockStore(t)
	defer store.client.Close()

	mock.ExpectBegin()
	mock.ExpectExec("LOAD 'age'").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`SET search_path`).WillReturnResult(sqlmock.NewResult(0, 0))

	nodeRows := sqlmock.NewRows([]string{"id", "name", "content", "metadata"}).
		AddRow(`"a"::agtype`, `"A"::agtype`, `""::agtype`, `null::agtype`)
	mock.ExpectQuery("SELECT \\* FROM cypher").WillReturnRows(nodeRows)

	traverseNodeRows := sqlmock.NewRows([]string{"id", "name", "content", "metadata"}).
		AddRow(`"b"::agtype`, `"B"::agtype`, `""::agtype`, `null::agtype`).
		AddRow(`"c"::agtype`, `"C"::agtype`, `""::agtype`, `null::agtype`)
	mock.ExpectQuery("SELECT \\* FROM cypher").WillReturnRows(traverseNodeRows)

	// sqlmock does not enforce the SQL LIMIT in traverseEdgeQueryCypher.
	// These extra rows verify edge-only overflow does not set node truncation.
	edgeRows := sqlmock.NewRows([]string{"id", "from_id", "to_id", "edge_type", "metadata"}).
		AddRow(`"e1"::agtype`, `"a"::agtype`, `"b"::agtype`, `"CALLS"`, `null::agtype`).
		AddRow(`"e2"::agtype`, `"a"::agtype`, `"b"::agtype`, `"IMPORTS"`, `null::agtype`).
		AddRow(`"e3"::agtype`, `"a"::agtype`, `"c"::agtype`, `"CALLS"`, `null::agtype`).
		AddRow(`"e4"::agtype`, `"a"::agtype`, `"c"::agtype`, `"IMPORTS"`, `null::agtype`)
	mock.ExpectQuery("SELECT \\* FROM cypher").WillReturnRows(edgeRows)

	mock.ExpectCommit()

	result, err := store.Traverse(context.Background(), &graph.TraverseQuery{
		StartIDs:  []string{"a"},
		Direction: graph.DirectionOut,
		MaxDepth:  1,
		MaxNodes:  3,
	})
	if err != nil {
		t.Fatalf("Traverse() error = %v", err)
	}
	if result.Truncated {
		t.Fatal("Traverse() truncated = true, want false (only edges exceeded maxNodes)")
	}
	if len(result.Nodes) != 3 {
		t.Fatalf("Traverse() nodes = %d, want 3", len(result.Nodes))
	}
}

func TestTraverseQueryNodesByIDsError(t *testing.T) {
	store, mock := newSqlmockStore(t)
	defer store.client.Close()

	mock.ExpectBegin()
	mock.ExpectExec("LOAD 'age'").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`SET search_path`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT \\* FROM cypher").WillReturnError(errors.New("query failed"))
	mock.ExpectRollback()

	_, err := store.Traverse(context.Background(), &graph.TraverseQuery{
		StartIDs: []string{"a"},
		MaxDepth: 1,
	})
	if err == nil || !strings.Contains(err.Error(), "query") {
		t.Errorf("Traverse() error = %v, want query error", err)
	}
}

func TestFindPathsWithSqlmock(t *testing.T) {
	store, mock := newSqlmockStore(t)
	defer store.client.Close()

	mock.ExpectBegin()
	mock.ExpectExec("LOAD 'age'").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`SET search_path`).WillReturnResult(sqlmock.NewResult(0, 0))

	pathRows := sqlmock.NewRows([]string{"node_ids", "edge_ids", "from_ids", "to_ids", "edge_types"}).
		AddRow(
			`["a", "b"]::agtype`,
			`["e1"]::agtype`,
			`["a"]::agtype`,
			`["b"]::agtype`,
			`["CALLS"]::agtype`,
		)
	mock.ExpectQuery("SELECT \\* FROM cypher").WillReturnRows(pathRows)

	nodeARows := sqlmock.NewRows([]string{"id", "name", "content", "metadata"}).
		AddRow(`"a"::agtype`, `"A"::agtype`, `""::agtype`, `null::agtype`)
	mock.ExpectQuery("SELECT \\* FROM cypher").WillReturnRows(nodeARows)

	nodeBRows := sqlmock.NewRows([]string{"id", "name", "content", "metadata"}).
		AddRow(`"b"::agtype`, `"B"::agtype`, `""::agtype`, `null::agtype`)
	mock.ExpectQuery("SELECT \\* FROM cypher").WillReturnRows(nodeBRows)

	mock.ExpectCommit()

	result, err := store.FindPaths(context.Background(), &graph.PathQuery{
		FromID:   "a",
		ToID:     "b",
		MaxDepth: 3,
		MaxPaths: 5,
	})
	if err != nil {
		t.Fatalf("FindPaths() error = %v", err)
	}
	if len(result.Paths) != 1 {
		t.Fatalf("FindPaths() paths = %d, want 1", len(result.Paths))
	}
	if len(result.Paths[0].Nodes) != 2 {
		t.Errorf("path nodes = %d, want 2", len(result.Paths[0].Nodes))
	}
	if len(result.Paths[0].Edges) != 1 {
		t.Errorf("path edges = %d, want 1", len(result.Paths[0].Edges))
	}
}

func TestFindPathsSetsTruncatedOnOverflow(t *testing.T) {
	store, mock := newSqlmockStore(t)
	defer store.client.Close()

	mock.ExpectBegin()
	mock.ExpectExec("LOAD 'age'").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`SET search_path`).WillReturnResult(sqlmock.NewResult(0, 0))

	pathRows := sqlmock.NewRows([]string{"node_ids", "edge_ids", "from_ids", "to_ids", "edge_types"}).
		AddRow(
			`["a", "b"]::agtype`,
			`["e1"]::agtype`,
			`["a"]::agtype`,
			`["b"]::agtype`,
			`["CALLS"]::agtype`,
		).
		AddRow(
			`["a", "c"]::agtype`,
			`["e2"]::agtype`,
			`["a"]::agtype`,
			`["c"]::agtype`,
			`["CALLS"]::agtype`,
		)
	mock.ExpectQuery("SELECT \\* FROM cypher").WillReturnRows(pathRows)

	nodeARows := sqlmock.NewRows([]string{"id", "name", "content", "metadata"}).
		AddRow(`"a"::agtype`, `"A"::agtype`, `""::agtype`, `null::agtype`)
	mock.ExpectQuery("SELECT \\* FROM cypher").WillReturnRows(nodeARows)

	nodeBRows := sqlmock.NewRows([]string{"id", "name", "content", "metadata"}).
		AddRow(`"b"::agtype`, `"B"::agtype`, `""::agtype`, `null::agtype`)
	mock.ExpectQuery("SELECT \\* FROM cypher").WillReturnRows(nodeBRows)

	mock.ExpectCommit()

	result, err := store.FindPaths(context.Background(), &graph.PathQuery{
		FromID:   "a",
		ToID:     "b",
		MaxDepth: 3,
		MaxPaths: 1,
	})
	if err != nil {
		t.Fatalf("FindPaths() error = %v", err)
	}
	if len(result.Paths) != 1 {
		t.Fatalf("FindPaths() paths = %d, want 1", len(result.Paths))
	}
	if !result.Truncated {
		t.Fatal("FindPaths() truncated = false, want true")
	}
}

func TestFindPathsDoesNotSetTruncatedWhenLimitExactlyFilled(t *testing.T) {
	store, mock := newSqlmockStore(t)
	defer store.client.Close()

	mock.ExpectBegin()
	mock.ExpectExec("LOAD 'age'").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`SET search_path`).WillReturnResult(sqlmock.NewResult(0, 0))

	pathRows := sqlmock.NewRows([]string{"node_ids", "edge_ids", "from_ids", "to_ids", "edge_types"}).
		AddRow(
			`["a", "b"]::agtype`,
			`["e1"]::agtype`,
			`["a"]::agtype`,
			`["b"]::agtype`,
			`["CALLS"]::agtype`,
		)
	mock.ExpectQuery("SELECT \\* FROM cypher").WillReturnRows(pathRows)

	nodeARows := sqlmock.NewRows([]string{"id", "name", "content", "metadata"}).
		AddRow(`"a"::agtype`, `"A"::agtype`, `""::agtype`, `null::agtype`)
	mock.ExpectQuery("SELECT \\* FROM cypher").WillReturnRows(nodeARows)

	nodeBRows := sqlmock.NewRows([]string{"id", "name", "content", "metadata"}).
		AddRow(`"b"::agtype`, `"B"::agtype`, `""::agtype`, `null::agtype`)
	mock.ExpectQuery("SELECT \\* FROM cypher").WillReturnRows(nodeBRows)

	mock.ExpectCommit()

	result, err := store.FindPaths(context.Background(), &graph.PathQuery{
		FromID:   "a",
		ToID:     "b",
		MaxDepth: 3,
		MaxPaths: 1,
	})
	if err != nil {
		t.Fatalf("FindPaths() error = %v", err)
	}
	if len(result.Paths) != 1 {
		t.Fatalf("FindPaths() paths = %d, want 1", len(result.Paths))
	}
	if result.Truncated {
		t.Fatal("FindPaths() truncated = true, want false")
	}
}

func TestFindPathsSetsTruncatedWhenLaterPatternHasPaths(t *testing.T) {
	store, mock := newSqlmockStore(t)
	defer store.client.Close()

	mock.ExpectBegin()
	mock.ExpectExec("LOAD 'age'").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`SET search_path`).WillReturnResult(sqlmock.NewResult(0, 0))

	pathRows := sqlmock.NewRows([]string{"node_ids", "edge_ids", "from_ids", "to_ids", "edge_types"}).
		AddRow(
			`["a", "b"]::agtype`,
			`["e1"]::agtype`,
			`["a"]::agtype`,
			`["b"]::agtype`,
			`["CALLS"]::agtype`,
		)
	mock.ExpectQuery("SELECT \\* FROM cypher").WillReturnRows(pathRows)

	nodeARows := sqlmock.NewRows([]string{"id", "name", "content", "metadata"}).
		AddRow(`"a"::agtype`, `"A"::agtype`, `""::agtype`, `null::agtype`)
	mock.ExpectQuery("SELECT \\* FROM cypher").WillReturnRows(nodeARows)

	nodeBRows := sqlmock.NewRows([]string{"id", "name", "content", "metadata"}).
		AddRow(`"b"::agtype`, `"B"::agtype`, `""::agtype`, `null::agtype`)
	mock.ExpectQuery("SELECT \\* FROM cypher").WillReturnRows(nodeBRows)

	probeRows := sqlmock.NewRows([]string{"node_ids", "edge_ids", "from_ids", "to_ids", "edge_types"}).
		AddRow(
			`["a", "b"]::agtype`,
			`["e2"]::agtype`,
			`["a"]::agtype`,
			`["b"]::agtype`,
			`["METHOD"]::agtype`,
		)
	mock.ExpectQuery("SELECT \\* FROM cypher").WillReturnRows(probeRows)

	mock.ExpectCommit()

	result, err := store.FindPaths(context.Background(), &graph.PathQuery{
		FromID:    "a",
		ToID:      "b",
		Direction: graph.DirectionOut,
		EdgeTypes: []string{"CALLS", "METHOD"},
		MaxDepth:  1,
		MaxPaths:  1,
	})
	if err != nil {
		t.Fatalf("FindPaths() error = %v", err)
	}
	if len(result.Paths) != 1 {
		t.Fatalf("FindPaths() paths = %d, want 1", len(result.Paths))
	}
	if !result.Truncated {
		t.Fatal("FindPaths() truncated = false, want true")
	}
}

func TestFindPathsDoesNotSetTruncatedWhenLaterPatternsAreEmpty(t *testing.T) {
	store, mock := newSqlmockStore(t)
	defer store.client.Close()

	mock.ExpectBegin()
	mock.ExpectExec("LOAD 'age'").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`SET search_path`).WillReturnResult(sqlmock.NewResult(0, 0))

	pathRows := sqlmock.NewRows([]string{"node_ids", "edge_ids", "from_ids", "to_ids", "edge_types"}).
		AddRow(
			`["a", "b"]::agtype`,
			`["e1"]::agtype`,
			`["a"]::agtype`,
			`["b"]::agtype`,
			`["CALLS"]::agtype`,
		)
	mock.ExpectQuery("SELECT \\* FROM cypher").WillReturnRows(pathRows)

	nodeARows := sqlmock.NewRows([]string{"id", "name", "content", "metadata"}).
		AddRow(`"a"::agtype`, `"A"::agtype`, `""::agtype`, `null::agtype`)
	mock.ExpectQuery("SELECT \\* FROM cypher").WillReturnRows(nodeARows)

	nodeBRows := sqlmock.NewRows([]string{"id", "name", "content", "metadata"}).
		AddRow(`"b"::agtype`, `"B"::agtype`, `""::agtype`, `null::agtype`)
	mock.ExpectQuery("SELECT \\* FROM cypher").WillReturnRows(nodeBRows)

	probeRows := sqlmock.NewRows([]string{"node_ids", "edge_ids", "from_ids", "to_ids", "edge_types"})
	mock.ExpectQuery("SELECT \\* FROM cypher").WillReturnRows(probeRows)

	mock.ExpectCommit()

	result, err := store.FindPaths(context.Background(), &graph.PathQuery{
		FromID:    "a",
		ToID:      "b",
		Direction: graph.DirectionOut,
		EdgeTypes: []string{"CALLS", "METHOD"},
		MaxDepth:  1,
		MaxPaths:  1,
	})
	if err != nil {
		t.Fatalf("FindPaths() error = %v", err)
	}
	if len(result.Paths) != 1 {
		t.Fatalf("FindPaths() paths = %d, want 1", len(result.Paths))
	}
	if result.Truncated {
		t.Fatal("FindPaths() truncated = true, want false")
	}
}

func TestFindPathsQueryError(t *testing.T) {
	store, mock := newSqlmockStore(t)
	defer store.client.Close()

	mock.ExpectBegin()
	mock.ExpectExec("LOAD 'age'").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`SET search_path`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT \\* FROM cypher").WillReturnError(errors.New("path query failed"))
	mock.ExpectRollback()

	_, err := store.FindPaths(context.Background(), &graph.PathQuery{
		FromID: "a",
		ToID:   "b",
	})
	if err == nil || !strings.Contains(err.Error(), "path") {
		t.Errorf("FindPaths() error = %v, want path error", err)
	}
}

func TestInitDBWithSqlmock(t *testing.T) {
	store, mock := newSqlmockStore(t)
	defer store.client.Close()

	mock.ExpectExec("CREATE EXTENSION IF NOT EXISTS age").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectBegin()
	mock.ExpectExec("SELECT pg_advisory_xact_lock").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("LOAD 'age'").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`SET search_path`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT COUNT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery("SELECT COUNT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectCommit()

	err := store.initDB(context.Background())
	if err != nil {
		t.Fatalf("initDB() error = %v", err)
	}
}

func TestInitDBGraphNotExists(t *testing.T) {
	store, mock := newSqlmockStore(t)
	defer store.client.Close()

	mock.ExpectExec("CREATE EXTENSION IF NOT EXISTS age").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectBegin()
	mock.ExpectExec("SELECT pg_advisory_xact_lock").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("LOAD 'age'").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`SET search_path`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT COUNT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectExec("SELECT ag_catalog.create_graph").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT COUNT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectExec("SELECT ag_catalog.create_vlabel").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()

	err := store.initDB(context.Background())
	if err != nil {
		t.Fatalf("initDB() error = %v", err)
	}
}

func TestEnsureEdgeLabelsWithSqlmock(t *testing.T) {
	store, mock := newSqlmockStore(t)
	defer store.client.Close()

	mock.ExpectBegin()
	mock.ExpectExec("SELECT pg_advisory_xact_lock").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("LOAD 'age'").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`SET search_path`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT COUNT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectExec("SELECT ag_catalog.create_elabel").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()

	err := store.ensureEdgeLabels(context.Background(), []string{"NEW_EDGE"})
	if err != nil {
		t.Fatalf("ensureEdgeLabels() error = %v", err)
	}
	if _, ok := store.edgeLabels["NEW_EDGE"]; !ok {
		t.Error("expected NEW_EDGE to be cached")
	}
}

func TestEnsureLabelTxAlreadyExists(t *testing.T) {
	store, mock := newSqlmockStore(t)
	defer store.client.Close()

	mock.ExpectBegin()
	mock.ExpectExec("SELECT pg_advisory_xact_lock").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("LOAD 'age'").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`SET search_path`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT COUNT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectCommit()

	err := store.ensureEdgeLabels(context.Background(), []string{"EXISTING"})
	if err != nil {
		t.Fatalf("ensureEdgeLabels() error = %v", err)
	}
}

func TestEnsureLabelTxScanError(t *testing.T) {
	store, mock := newSqlmockStore(t)
	defer store.client.Close()

	mock.ExpectBegin()
	mock.ExpectExec("SELECT pg_advisory_xact_lock").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("LOAD 'age'").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`SET search_path`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT COUNT").WillReturnError(errors.New("scan fail"))
	mock.ExpectRollback()

	err := store.ensureEdgeLabels(context.Background(), []string{"LABEL"})
	if err == nil || !strings.Contains(err.Error(), "check label") {
		t.Errorf("ensureEdgeLabels() error = %v, want check label error", err)
	}
}

func TestEnsureLabelTxCreateError(t *testing.T) {
	store, mock := newSqlmockStore(t)
	defer store.client.Close()

	mock.ExpectBegin()
	mock.ExpectExec("SELECT pg_advisory_xact_lock").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("LOAD 'age'").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`SET search_path`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT COUNT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectExec("SELECT ag_catalog.create_elabel").WillReturnError(errors.New("create fail"))
	mock.ExpectRollback()

	err := store.ensureEdgeLabels(context.Background(), []string{"LABEL"})
	if err == nil || !strings.Contains(err.Error(), "create label") {
		t.Errorf("ensureEdgeLabels() error = %v, want create label error", err)
	}
}

func TestInitDBCheckGraphScanError(t *testing.T) {
	store, mock := newSqlmockStore(t)
	defer store.client.Close()

	mock.ExpectExec("CREATE EXTENSION IF NOT EXISTS age").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectBegin()
	mock.ExpectExec("SELECT pg_advisory_xact_lock").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("LOAD 'age'").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`SET search_path`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT COUNT").WillReturnError(errors.New("scan err"))
	mock.ExpectRollback()

	err := store.initDB(context.Background())
	if err == nil || !strings.Contains(err.Error(), "check graph") {
		t.Errorf("initDB() error = %v, want check graph error", err)
	}
}

func TestInitDBCreateGraphError(t *testing.T) {
	store, mock := newSqlmockStore(t)
	defer store.client.Close()

	mock.ExpectExec("CREATE EXTENSION IF NOT EXISTS age").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectBegin()
	mock.ExpectExec("SELECT pg_advisory_xact_lock").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("LOAD 'age'").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`SET search_path`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT COUNT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectExec("SELECT ag_catalog.create_graph").WillReturnError(errors.New("permission denied"))
	mock.ExpectRollback()

	err := store.initDB(context.Background())
	if err == nil || !strings.Contains(err.Error(), "create graph") {
		t.Errorf("initDB() error = %v, want create graph error", err)
	}
}

func TestQueryNodesRowsScanError(t *testing.T) {
	store, mock := newSqlmockStore(t)
	defer store.client.Close()

	mock.ExpectBegin()
	mock.ExpectExec("LOAD 'age'").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`SET search_path`).WillReturnResult(sqlmock.NewResult(0, 0))

	rows := sqlmock.NewRows([]string{"id", "name", "content", "metadata"}).
		AddRow(`"a"::agtype`, `"A"::agtype`, `"c"::agtype`, `null::agtype`).
		RowError(0, errors.New("row error"))
	mock.ExpectQuery("SELECT \\* FROM cypher").WillReturnRows(rows)
	mock.ExpectRollback()

	_, err := store.Traverse(context.Background(), &graph.TraverseQuery{
		StartIDs: []string{"a"},
		MaxDepth: 1,
	})
	if err == nil {
		t.Error("expected error from rows")
	}
}

func TestQueryEdgesWithSqlmock(t *testing.T) {
	store, mock := newSqlmockStore(t)
	defer store.client.Close()

	mock.ExpectBegin()
	mock.ExpectExec("LOAD 'age'").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`SET search_path`).WillReturnResult(sqlmock.NewResult(0, 0))

	nodeRows := sqlmock.NewRows([]string{"id", "name", "content", "metadata"}).
		AddRow(`"a"::agtype`, `"A"::agtype`, `""::agtype`, `null::agtype`)
	mock.ExpectQuery("SELECT \\* FROM cypher").WillReturnRows(nodeRows)

	traverseRows := sqlmock.NewRows([]string{"id", "name", "content", "metadata"}).
		AddRow(`"b"::agtype`, `"B"::agtype`, `""::agtype`, `null::agtype`)
	mock.ExpectQuery("SELECT \\* FROM cypher").WillReturnRows(traverseRows)

	edgeRows := sqlmock.NewRows([]string{"id", "from_id", "to_id", "edge_type", "metadata"}).
		AddRow(`""::agtype`, `"a"::agtype`, `"b"::agtype`, `"CALLS"::agtype`, `{"w": 1}::agtype`)
	mock.ExpectQuery("SELECT \\* FROM cypher").WillReturnRows(edgeRows)
	mock.ExpectCommit()

	result, err := store.Traverse(context.Background(), &graph.TraverseQuery{
		StartIDs: []string{"a"},
		MaxDepth: 1,
	})
	if err != nil {
		t.Fatalf("Traverse() error = %v", err)
	}
	if len(result.Edges) == 0 {
		t.Error("expected at least one edge")
	}
	if len(result.Edges) > 0 && result.Edges[0].ID != "a:CALLS:b" {
		t.Errorf("edge.ID = %q, want generated ID", result.Edges[0].ID)
	}
}

func TestQueryEdgesError(t *testing.T) {
	store, mock := newSqlmockStore(t)
	defer store.client.Close()

	mock.ExpectBegin()
	mock.ExpectExec("LOAD 'age'").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`SET search_path`).WillReturnResult(sqlmock.NewResult(0, 0))

	nodeRows := sqlmock.NewRows([]string{"id", "name", "content", "metadata"})
	mock.ExpectQuery("SELECT \\* FROM cypher").WillReturnRows(nodeRows)

	traverseRows := sqlmock.NewRows([]string{"id", "name", "content", "metadata"})
	mock.ExpectQuery("SELECT \\* FROM cypher").WillReturnRows(traverseRows)

	mock.ExpectQuery("SELECT \\* FROM cypher").WillReturnError(errors.New("edge query fail"))
	mock.ExpectRollback()

	_, err := store.Traverse(context.Background(), &graph.TraverseQuery{
		StartIDs: []string{"a"},
		MaxDepth: 1,
	})
	if err == nil || !strings.Contains(err.Error(), "query edge") {
		t.Errorf("Traverse() error = %v, want edge query error", err)
	}
}

func TestFindPathsNodeQueryError(t *testing.T) {
	store, mock := newSqlmockStore(t)
	defer store.client.Close()

	mock.ExpectBegin()
	mock.ExpectExec("LOAD 'age'").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`SET search_path`).WillReturnResult(sqlmock.NewResult(0, 0))

	pathRows := sqlmock.NewRows([]string{"node_ids", "edge_ids", "from_ids", "to_ids", "edge_types"}).
		AddRow(`["a", "b"]::agtype`, `["e1"]::agtype`, `["a"]::agtype`, `["b"]::agtype`, `["CALLS"]::agtype`)
	mock.ExpectQuery("SELECT \\* FROM cypher").WillReturnRows(pathRows)

	mock.ExpectQuery("SELECT \\* FROM cypher").WillReturnError(errors.New("node lookup fail"))
	mock.ExpectRollback()

	_, err := store.FindPaths(context.Background(), &graph.PathQuery{
		FromID: "a",
		ToID:   "b",
	})
	if err == nil || !strings.Contains(err.Error(), "node") {
		t.Errorf("FindPaths() error = %v, want node error", err)
	}
}

func TestAddEdgesExecError(t *testing.T) {
	store, mock := newSqlmockStore(t)
	defer store.client.Close()
	store.edgeLabels["CALLS"] = struct{}{}

	mock.ExpectBegin()
	mock.ExpectExec("LOAD 'age'").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`SET search_path`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("SELECT \\* FROM cypher").WillReturnError(errors.New("disk full"))
	mock.ExpectRollback()

	err := store.AddEdges(context.Background(), []*graph.Edge{
		{FromID: "a", ToID: "b", Type: "CALLS"},
	})
	if err == nil || !strings.Contains(err.Error(), "execute cypher") {
		t.Errorf("AddEdges() error = %v, want execute cypher error", err)
	}
}

func TestQueryNodesByIDsSkipsEmpty(t *testing.T) {
	store, mock := newSqlmockStore(t)
	defer store.client.Close()

	mock.ExpectBegin()
	mock.ExpectExec("LOAD 'age'").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`SET search_path`).WillReturnResult(sqlmock.NewResult(0, 0))

	nodeRows := sqlmock.NewRows([]string{"id", "name", "content", "metadata"}).
		AddRow(`"a"::agtype`, `"A"::agtype`, `""::agtype`, `null::agtype`)
	mock.ExpectQuery("SELECT \\* FROM cypher").WillReturnRows(nodeRows)

	emptyRows := sqlmock.NewRows([]string{"id", "name", "content", "metadata"})
	mock.ExpectQuery("SELECT \\* FROM cypher").WillReturnRows(emptyRows)
	emptyEdges := sqlmock.NewRows([]string{"id", "from_id", "to_id", "edge_type", "metadata"})
	mock.ExpectQuery("SELECT \\* FROM cypher").WillReturnRows(emptyEdges)

	emptyRows2 := sqlmock.NewRows([]string{"id", "name", "content", "metadata"})
	mock.ExpectQuery("SELECT \\* FROM cypher").WillReturnRows(emptyRows2)
	emptyEdges2 := sqlmock.NewRows([]string{"id", "from_id", "to_id", "edge_type", "metadata"})
	mock.ExpectQuery("SELECT \\* FROM cypher").WillReturnRows(emptyEdges2)

	mock.ExpectCommit()

	result, err := store.Traverse(context.Background(), &graph.TraverseQuery{
		StartIDs: []string{"", "a"},
		MaxDepth: 1,
	})
	if err != nil {
		t.Fatalf("Traverse() error = %v", err)
	}
	if len(result.Nodes) != 1 || result.Nodes[0].ID != "a" {
		t.Errorf("expected only node 'a', got %v", result.Nodes)
	}
}

func TestAddEdgesSuccessWithLabelCreation(t *testing.T) {
	store := newTestStore(&mockPostgresClient{})
	err := store.AddEdges(context.Background(), []*graph.Edge{
		{FromID: "a", ToID: "b", Type: "CALLS"},
		{FromID: "b", ToID: "c", Type: "METHOD"},
	})
	if err != nil {
		t.Fatalf("AddEdges() error = %v", err)
	}
	if _, ok := store.edgeLabels["CALLS"]; !ok {
		t.Error("expected CALLS to be cached")
	}
	if _, ok := store.edgeLabels["METHOD"]; !ok {
		t.Error("expected METHOD to be cached")
	}
}

func TestAddEdgesSecondCallUsesCache(t *testing.T) {
	txCount := 0
	store := newTestStore(&mockPostgresClient{
		txFn: func(ctx context.Context, fn postgres.TxFunc) error {
			txCount++
			return nil
		},
	})

	err := store.AddEdges(context.Background(), []*graph.Edge{
		{FromID: "a", ToID: "b", Type: "CALLS"},
	})
	if err != nil {
		t.Fatalf("first AddEdges() error = %v", err)
	}
	firstTxCount := txCount

	err = store.AddEdges(context.Background(), []*graph.Edge{
		{FromID: "c", ToID: "d", Type: "CALLS"},
	})
	if err != nil {
		t.Fatalf("second AddEdges() error = %v", err)
	}
	if txCount != firstTxCount+1 {
		t.Errorf("expected one less transaction (cached labels), got txCount delta = %d", txCount-firstTxCount)
	}
}

func TestAddNodesSuccess(t *testing.T) {
	store := newTestStore(&mockPostgresClient{})
	err := store.AddNodes(context.Background(), []*graph.Node{
		{ID: "a", Name: "Node A", Content: "content"},
	})
	if err != nil {
		t.Errorf("AddNodes() error = %v, want nil", err)
	}
}

func TestTraverseWithDirectionAndEdgeTypes(t *testing.T) {
	store := newTestStore(&mockPostgresClient{})
	result, err := store.Traverse(context.Background(), &graph.TraverseQuery{
		StartIDs:  []string{"a", "b"},
		Direction: graph.DirectionIn,
		EdgeTypes: []string{"CALLS"},
		MaxDepth:  3,
		MaxNodes:  10,
	})
	if err != nil {
		t.Fatalf("Traverse() error = %v", err)
	}
	if result == nil {
		t.Fatal("Traverse() returned nil result")
	}
}

func TestTraverseWithBothDirection(t *testing.T) {
	store := newTestStore(&mockPostgresClient{})
	result, err := store.Traverse(context.Background(), &graph.TraverseQuery{
		StartIDs:  []string{"x"},
		Direction: graph.DirectionBoth,
		EdgeTypes: []string{"A", "B"},
		MaxDepth:  2,
	})
	if err != nil {
		t.Fatalf("Traverse() error = %v", err)
	}
	if result == nil {
		t.Fatal("Traverse() returned nil result")
	}
}

func TestFindPathsWithDirection(t *testing.T) {
	store := newTestStore(&mockPostgresClient{})
	result, err := store.FindPaths(context.Background(), &graph.PathQuery{
		FromID:    "start",
		ToID:      "end",
		Direction: graph.DirectionIn,
		EdgeTypes: []string{"CALLS"},
		MaxDepth:  4,
		MaxPaths:  3,
	})
	if err != nil {
		t.Fatalf("FindPaths() error = %v", err)
	}
	if result == nil {
		t.Fatal("FindPaths() returned nil result")
	}
}

func TestFindPathsWithMultipleEdgeTypes(t *testing.T) {
	store := newTestStore(&mockPostgresClient{})
	result, err := store.FindPaths(context.Background(), &graph.PathQuery{
		FromID:    "a",
		ToID:      "b",
		Direction: graph.DirectionBoth,
		EdgeTypes: []string{"X", "Y"},
		MaxDepth:  2,
	})
	if err != nil {
		t.Fatalf("FindPaths() error = %v", err)
	}
	if result == nil {
		t.Fatal("FindPaths() returned nil result")
	}
}

func TestNewWithValidGraphName(t *testing.T) {
	oldBuilder := postgres.GetClientBuilder()
	defer postgres.SetClientBuilder(oldBuilder)

	postgres.SetClientBuilder(func(ctx context.Context, opts ...postgres.ClientBuilderOpt) (postgres.Client, error) {
		return &mockPostgresClient{}, nil
	})

	store, err := New(WithGraphName("my_graph"), WithClientDSN("postgres://localhost/test"))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if store == nil {
		t.Fatal("New() returned nil store")
	}
	if store.option.graphName != "my_graph" {
		t.Errorf("store.option.graphName = %q, want %q", store.option.graphName, "my_graph")
	}
}

func TestNewWithDSN(t *testing.T) {
	oldBuilder := postgres.GetClientBuilder()
	defer postgres.SetClientBuilder(oldBuilder)

	var receivedOpts []postgres.ClientBuilderOpt
	postgres.SetClientBuilder(func(ctx context.Context, opts ...postgres.ClientBuilderOpt) (postgres.Client, error) {
		receivedOpts = opts
		return &mockPostgresClient{}, nil
	})

	_, err := New(WithGraphName("test_graph"), WithClientDSN("postgres://localhost/test"))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if len(receivedOpts) != 1 {
		t.Errorf("expected 1 builder option (DSN), got %d", len(receivedOpts))
	}
}

func TestNewDefaultGraphName(t *testing.T) {
	oldBuilder := postgres.GetClientBuilder()
	defer postgres.SetClientBuilder(oldBuilder)

	postgres.SetClientBuilder(func(ctx context.Context, opts ...postgres.ClientBuilderOpt) (postgres.Client, error) {
		return &mockPostgresClient{}, nil
	})

	store, err := New(WithClientDSN("postgres://localhost/test"))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if store.option.graphName != defaultGraphName {
		t.Errorf("store.option.graphName = %q, want %q", store.option.graphName, defaultGraphName)
	}
}

func TestInitDBCreateExtensionError(t *testing.T) {
	store := &Store{
		client: &mockPostgresClient{
			execFn: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
				if strings.Contains(query, "CREATE EXTENSION") {
					return nil, errors.New("permission denied")
				}
				return nil, nil
			},
		},
		option:     options{graphName: "test_graph"},
		edgeLabels: make(map[string]struct{}),
	}
	err := store.initDB(context.Background())
	if err == nil || !strings.Contains(err.Error(), "create extension") {
		t.Errorf("initDB() error = %v, want create extension error", err)
	}
}

func TestInitDBTransactionError(t *testing.T) {
	store := &Store{
		client: &mockPostgresClient{
			execFn: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
				return nil, nil
			},
			txFn: func(ctx context.Context, fn postgres.TxFunc) error {
				return errors.New("transaction failed")
			},
		},
		option:     options{graphName: "test_graph"},
		edgeLabels: make(map[string]struct{}),
	}
	err := store.initDB(context.Background())
	if err == nil || !strings.Contains(err.Error(), "transaction failed") {
		t.Errorf("initDB() error = %v, want transaction failed error", err)
	}
}
