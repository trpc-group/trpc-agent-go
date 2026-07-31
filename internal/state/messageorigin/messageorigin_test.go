//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package messageorigin

import (
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
)

func TestMarkAndContainsOrigins(t *testing.T) {
	inv := agent.NewInvocation()

	require.False(t, IsSeedHistory(inv, "event-1"))
	require.False(t, IsCurrentTurn(inv, "event-1"))
	MarkSeedHistory(inv, "event-1")
	MarkCurrentTurn(inv, "event-2")
	require.True(t, IsSeedHistory(inv, "event-1"))
	require.False(t, IsCurrentTurn(inv, "event-1"))
	require.False(t, IsSeedHistory(inv, "event-2"))
	require.True(t, IsCurrentTurn(inv, "event-2"))

	view := inv.View()
	require.True(t, IsSeedHistory(view, "event-1"))
	require.True(t, IsCurrentTurn(view, "event-2"))
	clone := inv.Clone()
	require.True(t, IsSeedHistory(clone, "event-1"))
	require.True(t, IsCurrentTurn(clone, "event-2"))

	MarkSeedHistory(inv, "event-3")
	MarkCurrentTurn(inv, "event-3")
	require.True(t, IsSeedHistory(inv, "event-3"))
	require.True(t, IsCurrentTurn(inv, "event-3"))
	require.False(t, IsSeedHistory(view, "event-3"))
	require.False(t, IsCurrentTurn(clone, "event-3"))
}

func TestMarkAndContainsOriginsIgnoreInvalidInput(t *testing.T) {
	MarkSeedHistory(nil, "event-1")
	MarkCurrentTurn(nil, "event-1")
	MarkSeedHistory(agent.NewInvocation(), "")
	MarkCurrentTurn(agent.NewInvocation(), "")
	require.False(t, IsSeedHistory(nil, "event-1"))
	require.False(t, IsCurrentTurn(nil, "event-1"))
	require.False(t, IsSeedHistory(agent.NewInvocation(), ""))
	require.False(t, IsCurrentTurn(agent.NewInvocation(), ""))
}
