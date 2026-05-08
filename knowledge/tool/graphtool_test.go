//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package tool

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/knowledge"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/graph"
	ctool "trpc.group/trpc-go/trpc-agent-go/tool"
)

type stubGraphKnowledge struct {
	stubKnowledge

	traverseQuery  *graph.TraverseQuery
	traverseResult *graph.TraverseResult
	traverseErr    error
	pathQuery      *graph.PathQuery
	pathResult     *graph.PathResult
	pathErr        error
}

var _ knowledge.GraphKnowledge = (*stubGraphKnowledge)(nil)

func (s *stubGraphKnowledge) Traverse(
	ctx context.Context,
	query *graph.TraverseQuery,
) (*graph.TraverseResult, error) {
	s.traverseQuery = query
	return s.traverseResult, s.traverseErr
}

func (s *stubGraphKnowledge) FindPaths(
	ctx context.Context,
	query *graph.PathQuery,
) (*graph.PathResult, error) {
	s.pathQuery = query
	return s.pathResult, s.pathErr
}

func TestGraphTraverseTool(t *testing.T) {
	kb := &stubGraphKnowledge{
		traverseResult: &graph.TraverseResult{
			Nodes: []*graph.Node{{ID: "a", Name: "A", Content: "func A() {}"}},
		},
	}
	graphTool := NewGraphTraverseTool(kb)
	require.Equal(t, defaultGraphTraverseToolName, graphTool.Declaration().Name)

	result := callGraphTool[*graph.TraverseResult](t, graphTool, &GraphTraverseRequest{
		StartIDs:  []string{"a"},
		Direction: "both",
		EdgeTypes: []string{"CALLS"},
		MaxDepth:  2,
		MaxNodes:  20,
	})
	require.Len(t, result.Nodes, 1)
	require.Empty(t, result.Nodes[0].Content)
	require.Equal(t, "func A() {}", kb.traverseResult.Nodes[0].Content)
	require.Equal(t, []string{"a"}, kb.traverseQuery.StartIDs)
	require.Equal(t, graph.DirectionBoth, kb.traverseQuery.Direction)
	require.Equal(t, []string{"CALLS"}, kb.traverseQuery.EdgeTypes)
	require.Equal(t, 2, kb.traverseQuery.MaxDepth)
	require.Equal(t, 20, kb.traverseQuery.MaxNodes)

	result = callGraphTool[*graph.TraverseResult](t, graphTool, &GraphTraverseRequest{
		StartIDs:       []string{"a"},
		IncludeContent: true,
	})
	require.Equal(t, "func A() {}", result.Nodes[0].Content)
}

func TestGraphFindPathsTool(t *testing.T) {
	kb := &stubGraphKnowledge{
		pathResult: &graph.PathResult{
			Paths: []*graph.Path{{
				Nodes: []*graph.Node{{ID: "a", Content: "func A() {}"}, {ID: "b", Content: "func B() {}"}},
				Edges: []*graph.Edge{{FromID: "a", ToID: "b", Type: "CALLS"}},
			}},
		},
	}
	graphTool := NewGraphFindPathsTool(kb)
	require.Equal(t, defaultGraphFindPathsToolName, graphTool.Declaration().Name)

	result := callGraphTool[*graph.PathResult](t, graphTool, &GraphFindPathsRequest{
		FromID:    "a",
		ToID:      "b",
		Direction: "out",
		MaxDepth:  3,
		MaxPaths:  5,
	})
	require.Len(t, result.Paths, 1)
	require.Empty(t, result.Paths[0].Nodes[0].Content)
	require.Equal(t, "func A() {}", kb.pathResult.Paths[0].Nodes[0].Content)
	require.Equal(t, "a", kb.pathQuery.FromID)
	require.Equal(t, "b", kb.pathQuery.ToID)
	require.Equal(t, graph.DirectionOut, kb.pathQuery.Direction)
	require.Equal(t, 3, kb.pathQuery.MaxDepth)
	require.Equal(t, 5, kb.pathQuery.MaxPaths)

	_ = callGraphTool[*graph.PathResult](t, graphTool, &GraphFindPathsRequest{
		FromID: "a",
		ToID:   "b",
	})
	require.Equal(t, defaultGraphToolMaxPaths, kb.pathQuery.MaxPaths)

	result = callGraphTool[*graph.PathResult](t, graphTool, &GraphFindPathsRequest{
		FromID:         "a",
		ToID:           "b",
		IncludeContent: true,
	})
	require.Equal(t, "func A() {}", result.Paths[0].Nodes[0].Content)
}

func TestGraphFindPathsToolRequiresEndpointIDs(t *testing.T) {
	kb := &stubGraphKnowledge{}
	graphTool := NewGraphFindPathsTool(kb)

	_, err := graphTool.(ctool.CallableTool).Call(
		context.Background(),
		mustMarshalGraphToolArgs(t, &GraphFindPathsRequest{FromID: "a"}),
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "from_id and to_id are required")
	require.Nil(t, kb.pathQuery)
}

func TestGraphToolSet(t *testing.T) {
	kb := &stubGraphKnowledge{}
	toolSet := NewGraphToolSet(kb, map[string][]any{
		"content":           {},
		"metadata.category": {"doc", "tutorial"},
	})

	require.Equal(t, defaultGraphToolSetName, toolSet.Name())
	tools := toolSet.Tools(context.Background())
	require.Len(t, tools, 3)
	require.Equal(t, graphSearchToolName, tools[0].Declaration().Name)
	require.Equal(t, graphTraverseToolName, tools[1].Declaration().Name)
	require.Equal(t, graphFindPathsToolName, tools[2].Declaration().Name)
	require.NoError(t, toolSet.Close())
}

func TestGraphToolRejectsUnsupportedDirection(t *testing.T) {
	kb := &stubGraphKnowledge{}
	graphTool := NewGraphTraverseTool(kb)

	_, err := graphTool.(ctool.CallableTool).Call(
		context.Background(),
		mustMarshalGraphToolArgs(t, &GraphTraverseRequest{
			StartIDs:  []string{"a"},
			Direction: "sideways",
		}),
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), `unsupported direction "sideways"`)
}

func callGraphTool[T any](t *testing.T, graphTool ctool.Tool, args any) T {
	t.Helper()
	callable, ok := graphTool.(ctool.CallableTool)
	require.True(t, ok)
	result, err := callable.Call(context.Background(), mustMarshalGraphToolArgs(t, args))
	require.NoError(t, err)
	typed, ok := result.(T)
	require.Truef(t, ok, "Call() result = %T, want requested type", result)
	return typed
}

func mustMarshalGraphToolArgs(t *testing.T, args any) []byte {
	t.Helper()
	payload, err := json.Marshal(args)
	require.NoError(t, err)
	return payload
}

func TestGraphTraverseToolResult(t *testing.T) {
	tests := []struct {
		name           string
		result         *graph.TraverseResult
		includeContent bool
		wantNil        bool
		wantContent    string
	}{
		{
			name:           "nil result returns nil",
			result:         nil,
			includeContent: false,
			wantNil:        true,
		},
		{
			name: "includeContent true preserves content",
			result: &graph.TraverseResult{
				Nodes: []*graph.Node{{ID: "n1", Content: "body"}},
			},
			includeContent: true,
			wantContent:    "body",
		},
		{
			name: "includeContent false strips content",
			result: &graph.TraverseResult{
				Nodes: []*graph.Node{{ID: "n1", Content: "body"}},
				Edges: []*graph.Edge{{FromID: "n1", ToID: "n2", Type: "CALLS"}},
			},
			includeContent: false,
			wantContent:    "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := graphTraverseToolResult(tt.result, tt.includeContent)
			if tt.wantNil {
				require.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			require.Equal(t, tt.wantContent, got.Nodes[0].Content)
		})
	}
}

func TestGraphPathToolResult(t *testing.T) {
	tests := []struct {
		name           string
		result         *graph.PathResult
		includeContent bool
		wantNil        bool
		checkFunc      func(t *testing.T, got *graph.PathResult)
	}{
		{
			name:           "nil result returns nil",
			result:         nil,
			includeContent: false,
			wantNil:        true,
		},
		{
			name: "nil path in paths is preserved",
			result: &graph.PathResult{
				Paths: []*graph.Path{nil},
			},
			includeContent: false,
			checkFunc: func(t *testing.T, got *graph.PathResult) {
				require.Len(t, got.Paths, 1)
				require.Nil(t, got.Paths[0])
			},
		},
		{
			name: "includeContent true preserves content",
			result: &graph.PathResult{
				Paths: []*graph.Path{{
					Nodes: []*graph.Node{{ID: "a", Content: "code"}},
				}},
			},
			includeContent: true,
			checkFunc: func(t *testing.T, got *graph.PathResult) {
				require.Equal(t, "code", got.Paths[0].Nodes[0].Content)
			},
		},
		{
			name: "includeContent false strips content",
			result: &graph.PathResult{
				Paths: []*graph.Path{{
					Nodes: []*graph.Node{{ID: "a", Content: "code"}},
					Edges: []*graph.Edge{{FromID: "a", ToID: "b", Type: "CALLS"}},
				}},
			},
			includeContent: false,
			checkFunc: func(t *testing.T, got *graph.PathResult) {
				require.Empty(t, got.Paths[0].Nodes[0].Content)
				require.Len(t, got.Paths[0].Edges, 1)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := graphPathToolResult(tt.result, tt.includeContent)
			if tt.wantNil {
				require.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			tt.checkFunc(t, got)
		})
	}
}

func TestGraphToolNodes(t *testing.T) {
	tests := []struct {
		name           string
		nodes          []*graph.Node
		includeContent bool
		wantLen        int
		checkFunc      func(t *testing.T, got []*graph.Node)
	}{
		{
			name:           "empty slice returns empty",
			nodes:          []*graph.Node{},
			includeContent: false,
			wantLen:        0,
			checkFunc:      func(t *testing.T, got []*graph.Node) {},
		},
		{
			name:           "nil node in slice is preserved",
			nodes:          []*graph.Node{nil, {ID: "x", Content: "c"}},
			includeContent: false,
			wantLen:        2,
			checkFunc: func(t *testing.T, got []*graph.Node) {
				require.Nil(t, got[0])
				require.Empty(t, got[1].Content)
			},
		},
		{
			name:           "includeContent true returns original nodes",
			nodes:          []*graph.Node{{ID: "x", Content: "c"}},
			includeContent: true,
			wantLen:        1,
			checkFunc: func(t *testing.T, got []*graph.Node) {
				require.Equal(t, "c", got[0].Content)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := graphToolNodes(tt.nodes, tt.includeContent)
			require.Len(t, got, tt.wantLen)
			tt.checkFunc(t, got)
		})
	}
}

func TestResolveGraphToolLimit(t *testing.T) {
	tests := []struct {
		name     string
		value    int
		fallback int
		want     int
	}{
		{name: "positive value used", value: 5, fallback: 10, want: 5},
		{name: "zero uses fallback", value: 0, fallback: 10, want: 10},
		{name: "negative uses fallback", value: -1, fallback: 10, want: 10},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, resolveGraphToolLimit(tt.value, tt.fallback))
		})
	}
}

func TestGraphToolSetNameFallback(t *testing.T) {
	s := &graphToolSet{name: ""}
	require.Equal(t, defaultGraphToolSetName, s.Name())
}

func TestGraphTraverseToolRequiresStartIDs(t *testing.T) {
	kb := &stubGraphKnowledge{}
	graphTool := NewGraphTraverseTool(kb)

	_, err := graphTool.(ctool.CallableTool).Call(
		context.Background(),
		mustMarshalGraphToolArgs(t, &GraphTraverseRequest{Direction: "out"}),
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "start_ids is required")
	require.Nil(t, kb.traverseQuery)
}

func TestParseGraphDirectionIn(t *testing.T) {
	dir, err := parseGraphDirection("in")
	require.NoError(t, err)
	require.Equal(t, graph.DirectionIn, dir)
}

func TestWithGraphToolDescription(t *testing.T) {
	kb := &stubGraphKnowledge{
		traverseResult: &graph.TraverseResult{},
	}
	graphTool := NewGraphTraverseTool(kb,
		WithGraphToolDescription("custom traverse description"),
	)
	require.Equal(t, "custom traverse description", graphTool.Declaration().Description)
}

func TestGraphTraverseToolResultPreservesFields(t *testing.T) {
	result := &graph.TraverseResult{
		Nodes:     []*graph.Node{{ID: "n1", Content: "body", Name: "Func"}},
		Edges:     []*graph.Edge{{FromID: "n1", ToID: "n2", Type: "CALLS"}},
		Truncated: true,
		Message:   "truncated at 100 nodes",
	}
	got := graphTraverseToolResult(result, false)
	require.True(t, got.Truncated)
	require.Equal(t, "truncated at 100 nodes", got.Message)
	require.Empty(t, got.Nodes[0].Content)
	require.Equal(t, "Func", got.Nodes[0].Name)
	require.Len(t, got.Edges, 1)
}

func TestGraphPathToolResultPreservesFields(t *testing.T) {
	result := &graph.PathResult{
		Paths: []*graph.Path{{
			Nodes: []*graph.Node{{ID: "a", Content: "code", Name: "A"}},
			Edges: []*graph.Edge{{FromID: "a", ToID: "b", Type: "CALLS"}},
		}},
		Truncated: true,
		Message:   "path limit reached",
	}
	got := graphPathToolResult(result, false)
	require.True(t, got.Truncated)
	require.Equal(t, "path limit reached", got.Message)
	require.Empty(t, got.Paths[0].Nodes[0].Content)
	require.Equal(t, "A", got.Paths[0].Nodes[0].Name)
}

func TestGraphToolNodesPreservesNodeMetadata(t *testing.T) {
	nodes := []*graph.Node{{
		ID:       "x",
		Name:     "MyFunc",
		Content:  "func MyFunc() {}",
		Metadata: map[string]any{"type": "Function"},
	}}
	got := graphToolNodes(nodes, false)
	require.Len(t, got, 1)
	require.Equal(t, "x", got[0].ID)
	require.Equal(t, "MyFunc", got[0].Name)
	require.Empty(t, got[0].Content)
	require.Equal(t, map[string]any{"type": "Function"}, got[0].Metadata)
}

func TestGraphTraverseToolReturnsKBError(t *testing.T) {
	kb := &stubGraphKnowledge{
		traverseErr: errors.New("traverse failed"),
	}
	graphTool := NewGraphTraverseTool(kb)
	_, err := graphTool.(ctool.CallableTool).Call(
		context.Background(),
		mustMarshalGraphToolArgs(t, &GraphTraverseRequest{
			StartIDs:  []string{"a"},
			Direction: "out",
		}),
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "traverse failed")
}

func TestGraphFindPathsToolRejectsUnsupportedDirection(t *testing.T) {
	kb := &stubGraphKnowledge{}
	graphTool := NewGraphFindPathsTool(kb)
	_, err := graphTool.(ctool.CallableTool).Call(
		context.Background(),
		mustMarshalGraphToolArgs(t, &GraphFindPathsRequest{
			FromID:    "a",
			ToID:      "b",
			Direction: "diagonal",
		}),
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), `unsupported direction "diagonal"`)
}

func TestGraphFindPathsToolReturnsKBError(t *testing.T) {
	kb := &stubGraphKnowledge{
		pathErr: errors.New("path query failed"),
	}
	graphTool := NewGraphFindPathsTool(kb)
	_, err := graphTool.(ctool.CallableTool).Call(
		context.Background(),
		mustMarshalGraphToolArgs(t, &GraphFindPathsRequest{
			FromID: "a",
			ToID:   "b",
		}),
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "path query failed")
}
