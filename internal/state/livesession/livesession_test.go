//
// Tencent is pleased to support the open source community by making trpc-agent-go
// available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package livesession

import (
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestLiveSession_Attach_Get_Clear(t *testing.T) {
	inv := agent.NewInvocation()
	sess := session.NewSession("app", "user", "session-1")

	got, ok := Get(inv)
	require.False(t, ok)
	require.Nil(t, got)

	Attach(inv, sess)
	got, ok = Get(inv)
	require.True(t, ok)
	require.Same(t, sess, got)

	// Clones inherit the same holder so updates and clears propagate.
	clone := inv.Clone()
	got, ok = Get(clone)
	require.True(t, ok)
	require.Same(t, sess, got)

	Clear(inv)
	got, ok = Get(inv)
	require.False(t, ok)
	require.Nil(t, got)

	got, ok = Get(clone)
	require.False(t, ok)
	require.Nil(t, got)
}

func TestLiveSession_Attach_Replaces(t *testing.T) {
	inv := agent.NewInvocation()
	first := session.NewSession("app", "user", "first")
	second := session.NewSession("app", "user", "second")

	Attach(inv, first)
	Attach(inv, second)

	got, ok := Get(inv)
	require.True(t, ok)
	require.Same(t, second, got)
}

func TestLiveSession_NilAndEmptyCases(t *testing.T) {
	require.NotPanics(t, func() { Attach(nil, nil) })
	require.NotPanics(t, func() { Clear(nil) })

	got, ok := Get(nil)
	require.False(t, ok)
	require.Nil(t, got)

	inv := agent.NewInvocation()
	Attach(inv, nil)
	got, ok = Get(inv)
	require.False(t, ok)
	require.Nil(t, got)
}
