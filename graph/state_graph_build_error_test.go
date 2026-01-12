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
	"testing"

	"github.com/stretchr/testify/require"
)

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
