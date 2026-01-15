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
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStateGraph_BuildErr_JoinsAndIgnoresNil(t *testing.T) {
	sg := NewStateGraph(NewStateSchema())

	sg.addBuildError(nil)
	sg.addBuildError(errors.New("first build error"))
	sg.addBuildError(errors.New("second build error"))

	err := sg.buildErr()
	require.Error(t, err)
	require.Contains(t, err.Error(), "first build error")
	require.Contains(t, err.Error(), "second build error")
}

func TestStateGraph_Compile_FailsOnBufferedAddErrors(t *testing.T) {
	sg := NewStateGraph(NewStateSchema())
	pass := func(ctx context.Context, st State) (any, error) { return st, nil }

	// Add conditional edges before defining nodes, which should be surfaced at Compile time.
	sg.AddToolsConditionalEdges("llm", "tools", "fallback")

	sg.AddNode("llm", pass)
	sg.AddNode("tools", pass)
	sg.AddNode("fallback", pass)
	sg.SetEntryPoint("llm").SetFinishPoint("fallback")

	_, err := sg.Compile()
	require.Error(t, err)
	require.Contains(t, err.Error(), "graph build failed")
	require.Contains(t, err.Error(), "AddToolsConditionalEdges")
	require.Contains(t, err.Error(), "source node llm does not exist")
}

func TestStateGraph_Compile_FailsOnBufferedAddNodeErrors(t *testing.T) {
	sg := NewStateGraph(NewStateSchema())
	pass := func(ctx context.Context, st State) (any, error) { return st, nil }

	sg.AddNode("", pass)
	sg.AddNode("A", pass)
	sg.SetEntryPoint("A").SetFinishPoint("A")

	_, err := sg.Compile()
	require.Error(t, err)
	require.Contains(t, err.Error(), "graph build failed")
	require.Contains(t, err.Error(), "AddNode")

	_, ok := sg.graph.getChannel("trigger:")
	require.False(t, ok)
}

func TestStateGraph_Compile_FailsOnBufferedSetEntryPointErrors(t *testing.T) {
	sg := NewStateGraph(NewStateSchema())
	pass := func(ctx context.Context, st State) (any, error) { return st, nil }

	sg.SetEntryPoint("A")
	sg.AddNode("A", pass)
	sg.SetFinishPoint("A")

	_, err := sg.Compile()
	require.Error(t, err)
	require.Contains(t, err.Error(), "graph build failed")
	require.Contains(t, err.Error(), "SetEntryPoint")
	require.Len(t, sg.graph.Edges(Start), 0)
}

func TestStateGraph_Compile_FailsOnBufferedConditionalEdgeErrors(t *testing.T) {
	sg := NewStateGraph(NewStateSchema())
	pass := func(ctx context.Context, st State) (any, error) { return st, nil }

	sg.AddConditionalEdges("A", func(ctx context.Context, st State) (string, error) { return "B", nil }, map[string]string{"B": "B"})
	sg.AddNode("A", pass)
	sg.AddNode("B", pass)
	sg.SetEntryPoint("A").SetFinishPoint("B")

	_, err := sg.Compile()
	require.Error(t, err)
	require.Contains(t, err.Error(), "graph build failed")
	require.Contains(t, err.Error(), "AddConditionalEdges")
}

func TestStateGraph_Compile_FailsOnBufferedMultiConditionalEdgeErrors(t *testing.T) {
	sg := NewStateGraph(NewStateSchema())
	pass := func(ctx context.Context, st State) (any, error) { return st, nil }

	sg.AddNode("A", pass)
	sg.AddMultiConditionalEdges("A", func(ctx context.Context, st State) ([]string, error) { return []string{"B"}, nil }, map[string]string{"B": "B"})
	sg.AddNode("B", pass)
	sg.SetEntryPoint("A").SetFinishPoint("B")

	_, err := sg.Compile()
	require.Error(t, err)
	require.Contains(t, err.Error(), "graph build failed")
	require.Contains(t, err.Error(), "AddMultiConditionalEdges")
}

func TestStateGraph_Compile_FailsOnBufferedToolsConditionalEdgeTargetErrors(t *testing.T) {
	sg := NewStateGraph(NewStateSchema())
	pass := func(ctx context.Context, st State) (any, error) { return st, nil }

	sg.AddNode("llm", pass)
	sg.AddNode("fallback", pass)
	sg.AddToolsConditionalEdges("llm", "tools", "fallback")
	sg.AddNode("tools", pass)
	sg.SetEntryPoint("llm").SetFinishPoint("fallback")

	_, err := sg.Compile()
	require.Error(t, err)
	require.Contains(t, err.Error(), "graph build failed")
	require.Contains(t, err.Error(), "AddToolsConditionalEdges")
	require.Contains(t, err.Error(), "target node tools does not exist")
}

func TestStateGraph_Compile_FailsOnBufferedJoinEdgeErrors(t *testing.T) {
	sg := NewStateGraph(NewStateSchema())
	pass := func(ctx context.Context, st State) (any, error) { return st, nil }

	sg.AddNode("A", pass)
	sg.AddNode("B", pass)
	sg.AddJoinEdge([]string{"A", "B"}, "C")
	sg.AddNode("C", pass)
	sg.SetEntryPoint("A").SetFinishPoint("C")

	_, err := sg.Compile()
	require.Error(t, err)
	require.Contains(t, err.Error(), "graph build failed")
	require.Contains(t, err.Error(), "AddJoinEdge")

	joinChan := joinChannelName("C", []string{"A", "B"})
	_, ok := sg.graph.getChannel(joinChan)
	require.False(t, ok)
}

func TestStateGraph_Compile_FailsOnBufferedJoinEdgeSourceErrors(t *testing.T) {
	sg := NewStateGraph(NewStateSchema())
	pass := func(ctx context.Context, st State) (any, error) { return st, nil }

	sg.AddNode("A", pass)
	sg.AddNode("C", pass)
	sg.AddJoinEdge([]string{"A", "missing"}, "C")
	sg.SetEntryPoint("A").SetFinishPoint("C")

	_, err := sg.Compile()
	require.Error(t, err)
	require.Contains(t, err.Error(), "graph build failed")
	require.Contains(t, err.Error(), "AddJoinEdge")
	require.Contains(t, err.Error(), "source node missing does not exist")

	starts := normalizeJoinStarts([]string{"A", "missing"})
	joinChan := joinChannelName("C", starts)
	_, ok := sg.graph.getChannel(joinChan)
	require.False(t, ok)
}

func TestStateGraph_AddJoinEdge_ToEnd_DoesNotRecordBuildError(t *testing.T) {
	sg := NewStateGraph(NewStateSchema())
	pass := func(ctx context.Context, st State) (any, error) { return st, nil }

	sg.AddNode("A", pass)
	sg.AddNode("B", pass)
	sg.AddJoinEdge([]string{"A", "B"}, End)
	sg.SetEntryPoint("A")

	_, err := sg.Compile()
	require.NoError(t, err)
}

func TestStateGraph_AddEdge_ErrorDoesNotMutatePregelArtifacts(t *testing.T) {
	sg := NewStateGraph(NewStateSchema())
	pass := func(ctx context.Context, st State) (any, error) { return st, nil }

	sg.AddNode("A", pass)
	// "B" does not exist yet, so this should be recorded as a build error and skip Pregel setup.
	sg.AddEdge("A", "B")

	// Ensure no edge exists.
	require.Len(t, sg.graph.Edges("A"), 0)

	// Ensure no trigger mapping exists for the missing target.
	triggers := sg.graph.getTriggerToNodes()
	require.NotContains(t, triggers, "branch:to:B")

	// Ensure no writer exists on A.
	nodeA := sg.graph.nodes["A"]
	require.NotNil(t, nodeA)
	for _, w := range nodeA.writers {
		require.NotEqual(t, "branch:to:B", w.Channel)
	}

	// Ensure the channel definition has not been created.
	_, ok := sg.graph.getChannel("branch:to:B")
	require.False(t, ok)
}
