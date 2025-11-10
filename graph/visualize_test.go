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
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
)

const (
	testNodePrepare  = "prepare"
	testNodeAsk      = "ask"
	testNodeTools    = "tools"
	testNodeFallback = "fallback"
)

// buildSampleGraph constructs a small graph with runtime edges,
// a conditional edge, and declared destinations for visualization tests.
func buildSampleGraph(t *testing.T) *Graph {
	t.Helper()

	schema := NewStateSchema()
	sg := NewStateGraph(schema)

	// Prepare node (function)
	sg.AddNode(testNodePrepare, func(ctx context.Context, s State) (any, error) { return s, nil })
	// LLM node for styling
	sg.AddNode(testNodeAsk, func(ctx context.Context, s State) (any, error) { return s, nil }, WithNodeType(NodeTypeLLM))
	// Tool node for styling
	sg.AddNode(testNodeTools, func(ctx context.Context, s State) (any, error) { return s, nil }, WithNodeType(NodeTypeTool))
	// Fallback node (function)
	sg.AddNode(testNodeFallback, func(ctx context.Context, s State) (any, error) { return s, nil })

	// Runtime edges and conditional edges
	sg.AddEdge(testNodePrepare, testNodeAsk)
	sg.AddToolsConditionalEdges(testNodeAsk, testNodeTools, testNodeFallback)
	sg.SetEntryPoint(testNodePrepare).SetFinishPoint(testNodeFallback)

	// Declared destination for visualization only
	sg.AddNode("noop", func(ctx context.Context, s State) (any, error) { return s, nil },
		WithDestinations(map[string]string{End: "finish early"}))

	g, err := sg.Compile()
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}
	return g
}

func TestDOT_IncludesNodesEdgesAndStyles(t *testing.T) {
	g := buildSampleGraph(t)
	dot := g.DOT(WithRankDir(RankDirLR), WithIncludeDestinations(true), WithGraphLabel("Test"))

	// Basic header
	if !strings.Contains(dot, "digraph G {") {
		t.Fatalf("expected DOT header, got: %s", dot)
	}
	if !strings.Contains(dot, "rankdir=LR;") {
		t.Fatalf("expected rankdir LR, got: %s", dot)
	}
	if !strings.Contains(dot, "label=\"Test\";") {
		t.Fatalf("expected graph label, got: %s", dot)
	}

	// Node declarations by ID
	for _, id := range []string{testNodePrepare, testNodeAsk, testNodeTools, testNodeFallback} {
		if !strings.Contains(dot, "\""+id+"\"") {
			t.Fatalf("expected node %s in DOT, got: %s", id, dot)
		}
	}

	// Solid runtime edges
	if !strings.Contains(dot, "\""+testNodePrepare+"\" -> \""+testNodeAsk+"\"") {
		t.Fatalf("expected runtime edge prepare->ask, got: %s", dot)
	}

	// Conditional edges are dashed and labeled
	// Branch keys are node IDs in AddToolsConditionalEdges path map
	if !strings.Contains(dot, "style=dashed") {
		t.Fatalf("expected dashed conditional edge in DOT, got: %s", dot)
	}

	// Destinations appear as dotted edges
	if !strings.Contains(dot, "style=dotted") {
		t.Fatalf("expected dotted destination edge in DOT, got: %s", dot)
	}

	// Start/End default to included
	if !strings.Contains(dot, "\"__start__\"") || !strings.Contains(dot, "\"__end__\"") {
		t.Fatalf("expected virtual Start/End in DOT, got: %s", dot)
	}
}

// Multi-conditional visualization fallback: when PathMap is empty but the
// conditional edge is multi-conditional, we render dashed edges using the
// node's Ends map as potential branches.
func TestDOT_MultiConditional_UsesEndsWhenNoPathMap(t *testing.T) {
	schema := NewStateSchema()
	sg := NewStateGraph(schema)
	sg.AddNode("A", func(ctx context.Context, s State) (any, error) { return s, nil }, WithEndsMap(map[string]string{"x": "B", "y": "C"}))
	sg.AddNode("B", func(ctx context.Context, s State) (any, error) { return s, nil })
	sg.AddNode("C", func(ctx context.Context, s State) (any, error) { return s, nil })
	sg.SetEntryPoint("A")
	sg.AddMultiConditionalEdges("A", func(ctx context.Context, s State) ([]string, error) { return []string{"x", "y"}, nil }, nil)
	g, err := sg.Compile()
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}
	dot := g.DOT()
	if !strings.Contains(dot, "\"A\" -> \"B\" [style=dashed") {
		t.Fatalf("expected dashed A->B edge from ends, got: %s", dot)
	}
	if !strings.Contains(dot, "\"A\" -> \"C\" [style=dashed") {
		t.Fatalf("expected dashed A->C edge from ends, got: %s", dot)
	}
}

func TestDOT_HideStartEnd(t *testing.T) {
	g := buildSampleGraph(t)
	dot := g.DOT(WithIncludeStartEnd(false))
	if strings.Contains(dot, "\"__start__\"") || strings.Contains(dot, "\"__end__\"") {
		t.Fatalf("expected Start/End hidden, got: %s", dot)
	}
}

func TestRenderImage_SkipWhenNoDot(t *testing.T) {
	g := buildSampleGraph(t)

	// Skip if Graphviz's dot is not installed
	if _, err := exec.LookPath("dot"); err != nil {
		t.Skip("dot not found in PATH; skipping render test")
	}

	tmp, err := os.CreateTemp(t.TempDir(), "graphviz-*.png")
	if err != nil {
		t.Fatalf("temp file: %v", err)
	}
	_ = tmp.Close()

	if err := g.RenderImage(context.Background(), ImageFormatPNG, tmp.Name()); err != nil {
		t.Fatalf("render failed: %v", err)
	}
}

// Additional coverage: entry highlight when Start is hidden.
func TestDOT_HighlightEntryWhenStartHidden(t *testing.T) {
	g := buildSampleGraph(t)
	dot := g.DOT(WithIncludeStartEnd(false))
	if !strings.Contains(dot, "\"prepare\" [peripheries=2];") { // entry is 'prepare'
		t.Fatalf("expected entry highlight when hiding Start, got: %s", dot)
	}
}

// Additional coverage: styles for all node types.
func TestDOT_StylesForAllNodeTypes(t *testing.T) {
	schema := NewStateSchema()
	sg := NewStateGraph(schema)
	sg.AddNode("llm", func(ctx context.Context, s State) (any, error) { return s, nil }, WithNodeType(NodeTypeLLM))
	sg.AddNode("tool", func(ctx context.Context, s State) (any, error) { return s, nil }, WithNodeType(NodeTypeTool))
	sg.AddNode("agent", func(ctx context.Context, s State) (any, error) { return s, nil }, WithNodeType(NodeTypeAgent))
	sg.AddNode("join", func(ctx context.Context, s State) (any, error) { return s, nil }, WithNodeType(NodeTypeJoin))
	sg.AddNode("router", func(ctx context.Context, s State) (any, error) { return s, nil }, WithNodeType(NodeTypeRouter))
	sg.SetEntryPoint("llm")
	g, err := sg.Compile()
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}
	dot := g.DOT()
	checks := []struct{ id, shape, fill, color string }{
		{"llm", "shape=box", colorLLMFill, colorLLMBorder},
		{"tool", "shape=box", colorToolFill, colorToolBorder},
		{"agent", "shape=box", colorAgentFill, colorAgentBorder},
		{"join", "shape=diamond", colorJoinFill, colorJoinBorder},
		{"router", "shape=diamond", colorRouterFill, colorRouterBorder},
	}
	for _, c := range checks {
		line := "\"" + c.id + "\""
		if !strings.Contains(dot, line) || !strings.Contains(dot, c.shape) || !strings.Contains(dot, c.fill) || !strings.Contains(dot, c.color) {
			t.Fatalf("missing style for %s: dot=%s", c.id, dot)
		}
	}
}

// Additional coverage: WriteDOT wrapper writes content with a label.
func TestWriteDOT_WritesToWriter(t *testing.T) {
	g := buildSampleGraph(t)
	var buf bytes.Buffer
	if err := g.WriteDOT(&buf, WithGraphLabel("Z")); err != nil {
		t.Fatalf("WriteDOT error: %v", err)
	}
	if !strings.Contains(buf.String(), "label=\"Z\";") {
		t.Fatalf("missing graph label in DOT: %s", buf.String())
	}
}
