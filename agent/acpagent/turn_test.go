//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package acpagent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestActiveTurnRegistryClearsOnlyMatchingTurn(t *testing.T) {
	t.Parallel()

	registry := newActiveTurnRegistry()
	first := &activeTurn{ctx: context.Background()}
	second := &activeTurn{ctx: context.Background()}
	registry.set("session-1", first)

	got, ok := registry.get("session-1")
	require.True(t, ok)
	require.Same(t, first, got)

	registry.set("session-1", second)
	registry.clear("session-1", first)
	got, ok = registry.get("session-1")
	require.True(t, ok)
	require.Same(t, second, got)

	registry.clear("session-1", second)
	_, ok = registry.get("session-1")
	require.False(t, ok)
}
