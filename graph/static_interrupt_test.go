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

func TestStaticInterruptHelpers_CoverEdgeCases(t *testing.T) {
	var nilExec *Executor
	require.Nil(t, nilExec.staticInterruptHits(nil, true))

	sg := NewStateGraph(NewStateSchema())
	sg.AddNode("a", func(ctx context.Context, state State) (any, error) {
		return nil, nil
	}, WithInterruptBefore())
	sg.SetEntryPoint("a")
	sg.SetFinishPoint("a")

	g, err := sg.Compile()
	require.NoError(t, err)

	exec, err := NewExecutor(g)
	require.NoError(t, err)

	tasks := []*Task{
		nil,
		{NodeID: ""},
		{NodeID: "missing"},
		{NodeID: "a"},
	}

	require.Equal(t, []string{"a"}, exec.staticInterruptHits(tasks, true))
	require.Nil(t, exec.staticInterruptHits(tasks, false))

	require.Nil(t, uniqueSortedTaskNodes(nil))
	require.Equal(t, []string{"a", "missing"}, uniqueSortedTaskNodes(tasks))

	require.False(t, hasSkipsForAll(map[string]any{}, nil))
	clearSkips(nil, nil, []string{"a"})

	intr := exec.maybeStaticInterruptBefore(nil, tasks, 7)
	require.NotNil(t, intr)
	require.Equal(t, StaticInterruptPhaseBefore, intr.Value.(StaticInterruptPayload).Phase)
}
