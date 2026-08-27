//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package messageprojection

import (
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestResolveCurrentUser(t *testing.T) {
	inv := agent.NewInvocation()
	inv.Message = model.NewUserMessage("second")
	merged := model.NewUserMessage("first\n\nsecond")
	_, ok := ResolveCurrentUser(inv, merged, "second")
	require.False(t, ok)

	SetCurrentUser(inv, model.NewUserMessage("second"), merged)
	got, ok := ResolveCurrentUser(inv, merged, "second")
	require.True(t, ok)
	require.True(t, model.MessagesEqual(merged, got))

	got, ok = ResolveCurrentUser(inv, merged, "SECOND")
	require.True(t, ok)
	require.Equal(t, "first\n\nSECOND", got.Content)

	_, ok = ResolveCurrentUser(
		inv,
		model.NewUserMessage("changed elsewhere"),
		"second",
	)
	require.False(t, ok)

	ClearCurrentUser(inv)
	_, ok = ResolveCurrentUser(inv, merged, "second")
	require.False(t, ok)
}

func TestResolveCurrentUserPreservesFinalProjection(t *testing.T) {
	inv := agent.NewInvocation()
	inv.Message = model.NewUserMessage("second")
	projected := model.NewUserMessage("projected second")
	merged := model.NewUserMessage(
		"summary\n\nfirst\n\nprojected second\n\nannotation",
	)
	SetCurrentUser(inv, projected, merged)

	got, ok := ResolveCurrentUser(inv, merged, "second")
	require.True(t, ok)
	require.True(t, model.MessagesEqual(merged, got))

	got, ok = ResolveCurrentUser(inv, merged, "SECOND")
	require.True(t, ok)
	require.Equal(
		t,
		"summary\n\nfirst\n\nSECOND\n\nannotation",
		got.Content,
	)
}

func TestSetCurrentUserRejectsMissingProjectedContent(t *testing.T) {
	inv := agent.NewInvocation()
	inv.Message = model.NewUserMessage("second")
	merged := model.NewUserMessage("first")
	SetCurrentUser(inv, model.NewUserMessage("second"), merged)

	_, ok := ResolveCurrentUser(inv, merged, "second")
	require.False(t, ok)
}
