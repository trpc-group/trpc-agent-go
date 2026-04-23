//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package awaitreply

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
)

func TestTool_CallMarksInvocation(t *testing.T) {
	tl := New()
	inv := &agent.Invocation{AgentName: "clarifier"}
	ctx := agent.NewInvocationContext(context.Background(), inv)

	got, err := tl.Call(ctx, []byte(`{}`))
	require.NoError(t, err)

	resp, ok := got.(Response)
	require.True(t, ok)
	require.True(t, resp.Success)
	require.Equal(t, "clarifier", resp.AgentName)

	route, ok := agent.CurrentAwaitUserReplyRoute(inv)
	require.True(t, ok)
	require.Equal(t, "clarifier", route.AgentName)
}

func TestTool_CallWithoutInvocationContext(t *testing.T) {
	tl := New()

	got, err := tl.Call(context.Background(), []byte(`{}`))
	require.NoError(t, err)

	resp, ok := got.(Response)
	require.True(t, ok)
	require.False(t, resp.Success)
	require.Contains(t, resp.Message, "invocation context")
}

func TestTool_CallInvalidJSON(t *testing.T) {
	tl := New()
	inv := &agent.Invocation{AgentName: "clarifier"}
	ctx := agent.NewInvocationContext(context.Background(), inv)

	got, err := tl.Call(ctx, []byte(`{`))
	require.NoError(t, err)

	resp, ok := got.(Response)
	require.True(t, ok)
	require.False(t, resp.Success)
	require.Contains(t, resp.Message, "invalid request format")
}
