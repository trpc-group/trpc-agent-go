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
	pathQuery      *graph.PathQuery
	pathResult     *graph.PathResult
}

var _ knowledge.GraphKnowledge = (*stubGraphKnowledge)(nil)

func (s *stubGraphKnowledge) Traverse(
	ctx context.Context,
	query *graph.TraverseQuery,
) (*graph.TraverseResult, error) {
	s.traverseQuery = query
	return s.traverseResult, nil
}

func (s *stubGraphKnowledge) FindPaths(
	ctx context.Context,
	query *graph.PathQuery,
) (*graph.PathResult, error) {
	s.pathQuery = query
	return s.pathResult, nil
}

func TestGraphTraverseTool(t *testing.T) {
	kb := &stubGraphKnowledge{
		traverseResult: &graph.TraverseResult{
			Nodes: []*graph.Node{{ID: "a", Name: "A"}},
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
	require.Equal(t, []string{"a"}, kb.traverseQuery.StartIDs)
	require.Equal(t, graph.DirectionBoth, kb.traverseQuery.Direction)
	require.Equal(t, []string{"CALLS"}, kb.traverseQuery.EdgeTypes)
	require.Equal(t, 2, kb.traverseQuery.MaxDepth)
	require.Equal(t, 20, kb.traverseQuery.MaxNodes)
}

func TestGraphFindPathsTool(t *testing.T) {
	kb := &stubGraphKnowledge{
		pathResult: &graph.PathResult{
			Paths: []*graph.Path{{
				Nodes: []*graph.Node{{ID: "a"}, {ID: "b"}},
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
}

func TestGraphToolSet(t *testing.T) {
	kb := &stubGraphKnowledge{}
	toolSet := NewGraphToolSet(kb)

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
