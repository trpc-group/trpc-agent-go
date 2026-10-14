//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package browser

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// The crash guard is created on first use in the invocation's state. A parallel
// worker runs against a view whose state is discarded, so a crash recorded
// there never reaches the authoritative invocation and the degradation
// threshold never trips. The tool therefore objects to sharing a turn.
func TestBrowserToolStaysOffTheParallelPath(t *testing.T) {
	t.Parallel()

	base := agent.NewInvocation()
	view := base.View()
	recordBrowserCrash(
		agent.NewInvocationContext(context.Background(), view),
		defaultProfileName,
		"signal=SIGTRAP",
	)
	viewState, ok := browserCrashStateForContext(
		agent.NewInvocationContext(context.Background(), view),
		defaultProfileName,
	)
	require.True(t, ok, "precondition: the crash is recorded on whatever invocation the worker has")
	require.Equal(t, 1, viewState.Consecutive)
	_, ok = browserCrashStateForContext(
		agent.NewInvocationContext(context.Background(), base),
		defaultProfileName,
	)
	require.False(t, ok,
		"precondition: a guard created on a parallel worker's view never reaches the base invocation")

	var browserTool tool.Tool = &Tool{}
	require.False(t, tool.IsConcurrencySafe(browserTool),
		"the browser tool must not be admitted to the parallel path")
}
