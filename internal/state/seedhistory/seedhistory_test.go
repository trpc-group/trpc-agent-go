//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package seedhistory

import (
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
)

func TestMarkAndContains(t *testing.T) {
	inv := agent.NewInvocation()

	require.False(t, Contains(inv, "event-1"))
	Mark(inv, "event-1")
	Mark(inv, "event-2")
	require.True(t, Contains(inv, "event-1"))
	require.True(t, Contains(inv, "event-2"))

	view := inv.View()
	require.True(t, Contains(view, "event-1"))
	require.True(t, Contains(view, "event-2"))
	clone := inv.Clone()
	require.True(t, Contains(clone, "event-1"))
	require.True(t, Contains(clone, "event-2"))

	Mark(inv, "event-3")
	require.True(t, Contains(inv, "event-3"))
	require.False(t, Contains(view, "event-3"))
	require.False(t, Contains(clone, "event-3"))
}

func TestMarkAndContainsIgnoreInvalidInput(t *testing.T) {
	Mark(nil, "event-1")
	Mark(agent.NewInvocation(), "")
	require.False(t, Contains(nil, "event-1"))
	require.False(t, Contains(agent.NewInvocation(), ""))
}
