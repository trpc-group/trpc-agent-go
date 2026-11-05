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
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestTool_Declaration(t *testing.T) {
	tl := New()

	decl := tl.Declaration()
	require.NotNil(t, decl)
	require.Equal(t, ToolName, decl.Name)
	require.NotNil(t, decl.InputSchema)
	require.Equal(t, "object", decl.InputSchema.Type)
	require.Empty(t, decl.InputSchema.Properties)
}

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

	_, ok = agent.CurrentAwaitUserReplyRoute(inv)
	require.False(t, ok)
}

func TestTool_CallInvalidInvocation(t *testing.T) {
	tl := New()
	inv := &agent.Invocation{}
	ctx := agent.NewInvocationContext(context.Background(), inv)

	got, err := tl.Call(ctx, []byte(`{}`))
	require.NoError(t, err)

	resp, ok := got.(Response)
	require.True(t, ok)
	require.False(t, resp.Success)
	require.Contains(t, resp.Message, "non-empty agent target")
}

// await_user_reply must never be batched. Call stages the resume route with
// agent.MarkAwaitingUserReply, which writes to the invocation's own state;
// parallel execution gives each call a view whose state is cloned and never
// synced back, so a batched call reports success and resumes nothing. Asserted
// through tool.ConcurrencyAware, the way a scheduler resolves it.
func TestTool_IsConcurrencySafe(t *testing.T) {
	tl := New()

	require.False(
		t,
		tl.IsConcurrencySafe(),
		"await_user_reply must not run on the parallel path",
	)
	aware, ok := tool.Tool(tl).(tool.ConcurrencyAware)
	require.True(t, ok, "await_user_reply must publish tool.ConcurrencyAware")
	require.False(t, aware.IsConcurrencySafe(), "the objection must resolve through the interface")
	require.False(
		t,
		tool.IsConcurrencySafe(tl),
		"tool.IsConcurrencySafe must report await_user_reply as objecting",
	)
}
