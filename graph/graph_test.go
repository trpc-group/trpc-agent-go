//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package graph

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNew(t *testing.T) {
	schema := NewStateSchema()
	g := New(schema)
	assert.NotNil(t, g, "Expected non-nil graph")
	assert.NotNil(t, g.nodes, "Expected nodes map to be initialized")
	assert.NotNil(t, g.edges, "Expected edges map to be initialized")
	assert.NotNil(t, g.schema, "Expected schema to be set")
}

func TestAddNode(t *testing.T) {
	schema := NewStateSchema()
	g := New(schema)

	// Test adding valid node.
	testFunc := func(ctx context.Context, state State) (any, error) {
		return State{"processed": true}, nil
	}

	node := &Node{
		ID:       "test-node",
		Name:     "Test Node",
		Function: testFunc,
	}

	err := g.addNode(node)
	assert.NoError(t, err, "Expected no error")

	// Verify node was added.
	retrievedNode, exists := g.Node("test-node")
	assert.True(t, exists, "Expected node to exist")
	assert.Equal(t, "Test Node", retrievedNode.Name, "Expected name 'Test Node'")
	assert.NotNil(t, retrievedNode.Function, "Expected node to have function")
}

func TestAddEdge(t *testing.T) {
	schema := NewStateSchema()
	g := New(schema)

	// Add nodes first.
	testFunc := func(ctx context.Context, state State) (any, error) {
		return State{"processed": true}, nil
	}

	node1 := &Node{ID: "node1", Name: "Node 1", Function: testFunc}
	node2 := &Node{ID: "node2", Name: "Node 2", Function: testFunc}

	g.addNode(node1)
	g.addNode(node2)

	// Test adding valid edge.
	edge := &Edge{From: "node1", To: "node2"}
	err := g.addEdge(edge)
	assert.NoError(t, err, "Expected no error")

	// Verify edge was added.
	edges := g.Edges("node1")
	assert.Equal(t, 1, len(edges), "Expected 1 edge")
	assert.Equal(t, "node2", edges[0].To, "Expected edge to 'node2'")
}

func TestValidate_NoStaticReachabilityRequired(t *testing.T) {
	schema := NewStateSchema()
	sg := NewStateGraph(schema)

	// Two nodes with no edge between them; entry set to nodeA only.
	sg.AddNode("A", func(ctx context.Context, s State) (any, error) { return s, nil })
	sg.AddNode("B", func(ctx context.Context, s State) (any, error) { return s, nil })
	sg.SetEntryPoint("A")
	// No edges A->B; should still compile since we don't enforce reachability.

	g, err := sg.Compile()
	if err != nil || g == nil {
		t.Fatalf("Expected compile success without reachability enforcement, got err=%v", err)
	}
}

func TestValidate_DestinationsExistence(t *testing.T) {
	schema := NewStateSchema()
	sg := NewStateGraph(schema)

	// Add nodes
	sg.AddNode("start", func(ctx context.Context, s State) (any, error) { return s, nil }, WithDestinations(map[string]string{"finish": ""}))
	sg.AddNode("finish", func(ctx context.Context, s State) (any, error) { return s, nil })
	sg.SetEntryPoint("start").SetFinishPoint("finish")

	// Should compile: destination 'finish' exists.
	if _, err := sg.Compile(); err != nil {
		t.Fatalf("Expected compile success with valid destinations, got err=%v", err)
	}

	// Now create an invalid destination declaration.
	sg2 := NewStateGraph(schema)
	sg2.AddNode("start", func(ctx context.Context, s State) (any, error) { return s, nil }, WithDestinations(map[string]string{"missing": ""}))
	sg2.SetEntryPoint("start")
	if _, err := sg2.Compile(); err == nil {
		t.Fatal("Expected compile error due to missing declared destination, got nil")
	}
}

func TestStateSchema(t *testing.T) {
	schema := NewStateSchema().
		AddField("counter", StateField{
			Type:    reflect.TypeOf(0),
			Reducer: DefaultReducer,
		}).
		AddField("items", StateField{
			Type:    reflect.TypeOf([]any{}),
			Reducer: AppendReducer,
			Default: func() any { return []any{} },
		})

	// Test applying updates.
	state := State{"counter": 1}
	update := State{"counter": 2, "items": []any{"item1"}}

	result := schema.ApplyUpdate(state, update)

	assert.Equal(t, 2, result["counter"], "Expected counter 2")

	items, ok := result["items"].([]any)
	assert.True(t, ok, "Expected items to be a slice")
	assert.Equal(t, 1, len(items), "Expected 1 item")
	assert.Equal(t, "item1", items[0], "Expected items [item1]")
}

func TestStateReducers(t *testing.T) {
	// Test DefaultReducer.
	result := DefaultReducer("old", "new")
	assert.Equal(t, "new", result, "Expected 'new'")

	// Test AppendReducer.
	existing := []any{"a", "b"}
	update := []any{"c", "d"}
	result = AppendReducer(existing, update)
	resultSlice := result.([]any)
	assert.Equal(t, 4, len(resultSlice), "Expected length 4")

	// Test MergeReducer.
	existingMap := map[string]any{"a": 1, "b": 2}
	updateMap := map[string]any{"b": 3, "c": 4}
	result = MergeReducer(existingMap, updateMap)
	resultMap := result.(map[string]any)
	assert.Equal(t, 1, resultMap["a"], "Expected '1'")
	assert.Equal(t, 3, resultMap["b"], "Expected '3'")
	assert.Equal(t, 4, resultMap["c"], "Expected '4'")
}

// Cache namespace helpers and versioning.
func TestBuildCacheNamespaceSimple(t *testing.T) {
	ns := buildCacheNamespace("nodeA")
	if ns != CacheNamespacePrefix+":"+"nodeA" {
		t.Fatalf("unexpected namespace: %s", ns)
	}
}

func TestGraphCacheNamespace_WithAndWithoutVersion(t *testing.T) {
	sg := NewStateGraph(NewStateSchema())
	// No version
	ns := sg.graph.cacheNamespace("N1")
	expect := CacheNamespacePrefix + ":" + "N1"
	if ns != expect {
		t.Fatalf("expected %s, got %s", expect, ns)
	}
	// With version
	sg.WithGraphVersion("v100")
	ns2 := sg.graph.cacheNamespace("N1")
	expect2 := CacheNamespacePrefix + ":" + "v100" + ":" + "N1"
	if ns2 != expect2 {
		t.Fatalf("expected %s, got %s", expect2, ns2)
	}
}

func TestStateGraph_ClearCacheForNodes(t *testing.T) {
	sg := NewStateGraph(NewStateSchema())
	cache := NewInMemoryCache()
	sg.WithCache(cache)
	// Register nodes so ClearCache() without args can collect them.
	sg.AddNode("n1", func(_ context.Context, s State) (any, error) { return s, nil })
	sg.AddNode("n2", func(_ context.Context, s State) (any, error) { return s, nil })
	// Populate two nodes' namespaces.
	ns1 := sg.graph.cacheNamespace("n1")
	ns2 := sg.graph.cacheNamespace("n2")
	cache.Set(ns1, "k", 123, 0)
	cache.Set(ns2, "k", 456, time.Second)
	if _, ok := cache.Get(ns1, "k"); !ok {
		t.Fatalf("expected value in ns1 before clear")
	}
	if _, ok := cache.Get(ns2, "k"); !ok {
		t.Fatalf("expected value in ns2 before clear")
	}
	// Clear only n1 and verify n2 remains.
	sg.ClearCache("n1")
	if _, ok := cache.Get(ns1, "k"); ok {
		t.Fatalf("expected ns1 to be cleared")
	}
	if v, ok := cache.Get(ns2, "k"); !ok {
		t.Fatalf("expected ns2 to remain, missing value")
	} else {
		switch vv := v.(type) {
		case int:
			if vv != 456 {
				t.Fatalf("unexpected ns2 value: %v", vv)
			}
		case float64:
			if vv != 456 {
				t.Fatalf("unexpected ns2 value: %v", vv)
			}
		default:
			t.Fatalf("unexpected ns2 value type: %T (%v)", v, v)
		}
	}
	// Clear all by omitting args.
	sg.ClearCache()
	if _, ok := cache.Get(ns2, "k"); ok {
		t.Fatalf("expected ns2 to be cleared when clearing all")
	}
}

// Canonicalization tests (stable ordering for hashing cache keys).
func TestToCanonicalValue_MapOrdersKeysAndValues(t *testing.T) {
	input := map[string]any{
		"b": 2,
		"a": []any{map[string]any{"y": 2, "x": 1}},
	}
	canon, err := toCanonicalValue(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	b, _ := json.Marshal(canon)
	s := string(b)
	if !(containsInOrder(s, "\"k\":\"a\"", "\"k\":\"b\"")) {
		t.Fatalf("outer keys not ordered a,b: %s", s)
	}
	if !(containsInOrder(s, "\"k\":\"x\"", "\"k\":\"y\"")) {
		t.Fatalf("inner keys not ordered x,y: %s", s)
	}
}

func TestToCanonicalValue_StructOrdersFields(t *testing.T) {
	type S struct {
		Z int
		A string
	}
	canon, err := toCanonicalValue(S{Z: 9, A: "x"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	b, _ := json.Marshal(canon)
	s := string(b)
	if !(containsInOrder(s, "\"k\":\"A\"", "\"k\":\"Z\"")) {
		t.Fatalf("struct fields not ordered A,Z: %s", s)
	}
}

func TestToCanonicalValue_NonStringKeyMapStringifiesKeys(t *testing.T) {
	m := map[int]string{2: "b", 1: "a"}
	canon, err := toCanonicalValue(m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	b, _ := json.Marshal(canon)
	s := string(b)
	if !(containsInOrder(s, "\"k\":\"1\"", "\"k\":\"2\"")) {
		t.Fatalf("int keys not stringified/sorted: %s", s)
	}
}

func TestCanonicalizeMap_StateCoversHelper(t *testing.T) {
	st := State{"b": 2, "a": 1}
	canon, err := toCanonicalValue(st)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	b, _ := json.Marshal(canon)
	if !(containsInOrder(string(b), "\"k\":\"a\"", "\"k\":\"b\"")) {
		t.Fatalf("state keys not ordered a,b: %s", string(b))
	}
}

func containsInOrder(s, first, second string) bool {
	i := indexOf(s, first)
	if i < 0 {
		return false
	}
	j := indexOf(s, second)
	return j > i
}
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// Default cache policy success and error cases.
func TestDefaultCachePolicy_SuccessAndError(t *testing.T) {
	p := DefaultCachePolicy()
	if p == nil || p.KeyFunc == nil {
		t.Fatalf("default cache policy missing key func")
	}
	if _, err := p.KeyFunc(map[string]any{"x": 1, "y": []int{2, 3}}); err != nil {
		t.Fatalf("unexpected error in key func: %v", err)
	}
	if _, err := p.KeyFunc(map[string]any{"f": func() {}}); err == nil {
		t.Fatalf("expected error when marshaling unsupported func value")
	}
}

// Node/channel helpers
func TestGraph_AddNodeChannel_AppendsToNode(t *testing.T) {
	sg := NewStateGraph(NewStateSchema())
	sg.AddNode("n", func(ctx context.Context, s State) (any, error) { return s, nil })
	sg.graph.addNodeChannel("n", "chan:alpha")
	sg.graph.addNodeChannel("n", "chan:beta")
	n := sg.graph.nodes["n"]
	if n == nil {
		t.Fatalf("node not found")
	}
	if len(n.channels) != 2 || n.channels[0] != "chan:alpha" || n.channels[1] != "chan:beta" {
		t.Fatalf("unexpected channels: %+v", n.channels)
	}
}

func TestGraph_AddNode_AddEdge_Errors(t *testing.T) {
	g := New(NewStateSchema())
	if err := g.addNode(&Node{}); err == nil {
		t.Fatalf("expected error for empty node id")
	}
	if err := g.addNode(&Node{ID: "a"}); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if err := g.addNode(&Node{ID: "a"}); err == nil {
		t.Fatalf("expected duplicate id error")
	}
	if err := g.addEdge(&Edge{From: "nope", To: "a"}); err == nil {
		t.Fatalf("expected missing source error")
	}
	if err := g.addEdge(&Edge{From: "a", To: "nope"}); err == nil {
		t.Fatalf("expected missing target error")
	}
	if err := g.setEntryPoint("nope"); err == nil {
		t.Fatalf("expected missing entrypoint error")
	}
}

// Deep copy and reducer coverage
func TestDeepCopyAny_ArrayEnsuresDeepCopy(t *testing.T) {
	original := [2][]int{{1, 2}, {3}}
	copiedAny := deepCopyAny(original)
	copied, ok := copiedAny.([2][]int)
	if !ok {
		t.Fatalf("expected [2][]int after deep copy, got %T", copiedAny)
	}
	original[0][0] = 99
	if copied[0][0] != 1 {
		t.Fatalf("copied array was affected by original mutation: %+v", copied)
	}
}

func TestDefaultReducer_CompositeDeepCopy(t *testing.T) {
	update := map[string]any{"k": []int{1, 2}}
	out := DefaultReducer(nil, update)
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any, got %T", out)
	}
	update["k"].([]int)[0] = 99
	if m["k"].([]int)[0] != 1 {
		t.Fatalf("deep copy not performed; got %v", m["k"])
	}
}
