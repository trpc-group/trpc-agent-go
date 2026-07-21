//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestConcurrencyLimiter_NoGlobalLimitDoesNotDrift verifies that the
// global counter is not decremented by release when MaxActiveCalls is
// zero and acquire never incremented it.
func TestConcurrencyLimiter_NoGlobalLimitDoesNotDrift(t *testing.T) {
	c := newConcurrencyLimiter(ConcurrencyPolicy{
		PerToolLimits: map[string]int{"tool": 2},
	})
	for i := 0; i < 10; i++ {
		release, err := c.acquire(context.Background(), "tool")
		require.NoError(t, err)
		release()
	}
	require.Equal(t, int64(0), c.activeCount())
}

// TestConcurrencyLimiter_PerToolRollbackWithoutGlobalLimit verifies that
// a per-tool rejection does not decrement the global counter when no
// global increment was made.
func TestConcurrencyLimiter_PerToolRollbackWithoutGlobalLimit(t *testing.T) {
	c := newConcurrencyLimiter(ConcurrencyPolicy{
		PerToolLimits: map[string]int{"tool": 1},
	})
	release, err := c.acquire(context.Background(), "tool")
	require.NoError(t, err)
	defer release()
	_, err = c.acquire(context.Background(), "tool")
	require.Error(t, err)
	require.Equal(t, int64(0), c.activeCount())
}

// TestConcurrencyLimiter_GlobalLimitBalanced verifies that acquire and
// release keep the global counter balanced when MaxActiveCalls is set.
func TestConcurrencyLimiter_GlobalLimitBalanced(t *testing.T) {
	c := newConcurrencyLimiter(ConcurrencyPolicy{MaxActiveCalls: 1})
	release, err := c.acquire(context.Background(), "tool")
	require.NoError(t, err)
	require.Equal(t, int64(1), c.activeCount())
	_, err = c.acquire(context.Background(), "tool")
	require.Error(t, err)
	release()
	require.Equal(t, int64(0), c.activeCount())
}
