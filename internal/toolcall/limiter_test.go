//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package toolcall

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestLimiterSharedGroup(t *testing.T) {
	limiter := NewLimiter(tool.ConcurrencyConfig{
		Groups: []tool.ConcurrencyGroup{{
			ToolNames: []string{"search", "fetch"},
			Limit:     1,
		}},
	})
	require.NotNil(t, limiter)

	releaseSearch, err := limiter.Acquire(context.Background(), "search")
	require.NoError(t, err)

	type acquireResult struct {
		release func()
		err     error
	}
	acquiredFetch := make(chan acquireResult, 1)
	go func() {
		release, acquireErr := limiter.Acquire(
			context.Background(),
			"fetch",
		)
		acquiredFetch <- acquireResult{
			release: release,
			err:     acquireErr,
		}
	}()

	select {
	case result := <-acquiredFetch:
		if result.release != nil {
			result.release()
		}
		t.Fatal("fetch acquired shared capacity before search released it")
	case <-time.After(50 * time.Millisecond):
	}

	releaseSearch()
	select {
	case result := <-acquiredFetch:
		require.NoError(t, result.err)
		require.NotNil(t, result.release)
		result.release()
	case <-time.After(time.Second):
		t.Fatal("fetch did not acquire shared capacity after search released it")
	}
}

func TestLimiterOverallLimitAndIdempotentRelease(t *testing.T) {
	limiter := NewLimiter(tool.ConcurrencyConfig{MaxConcurrency: 1})
	require.NotNil(t, limiter)

	releaseFirst, err := limiter.Acquire(context.Background(), "first")
	require.NoError(t, err)
	acquiredSecond := make(chan func(), 1)
	go func() {
		release, acquireErr := limiter.Acquire(
			context.Background(),
			"second",
		)
		if acquireErr != nil {
			acquiredSecond <- nil
			return
		}
		acquiredSecond <- release
	}()

	select {
	case release := <-acquiredSecond:
		if release != nil {
			release()
		}
		t.Fatal("second tool acquired capacity before the first released it")
	case <-time.After(50 * time.Millisecond):
	}

	releaseFirst()
	releaseFirst()

	select {
	case release := <-acquiredSecond:
		require.NotNil(t, release)
		release()
	case <-time.After(time.Second):
		t.Fatal("second tool did not acquire capacity after the first released it")
	}
}

func TestLimiterAcquireCancellationReleasesGroup(t *testing.T) {
	limiter := NewLimiter(tool.ConcurrencyConfig{
		MaxConcurrency: 1,
		Groups: []tool.ConcurrencyGroup{{
			ToolNames: []string{"search"},
			Limit:     1,
		}},
	})
	releaseGlobal, err := limiter.Acquire(context.Background(), "other")
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = limiter.Acquire(ctx, "search")
	require.Error(t, err)
	require.True(t, errors.Is(err, context.Canceled))
	releaseGlobal()

	releaseSearch, err := limiter.Acquire(context.Background(), "search")
	require.NoError(t, err)
	releaseSearch()
}

func TestLimiterFirstGroupWinsForDuplicateToolName(t *testing.T) {
	limiter := NewLimiter(tool.ConcurrencyConfig{
		Groups: []tool.ConcurrencyGroup{
			{ToolNames: []string{"search"}, Limit: 1},
			{ToolNames: []string{"search", "fetch"}, Limit: 2},
		},
	})

	releaseSearch, err := limiter.Acquire(context.Background(), "search")
	require.NoError(t, err)
	releaseFetch, err := limiter.Acquire(context.Background(), "fetch")
	require.NoError(t, err)
	releaseFetch()
	releaseSearch()
}

func TestNewLimiterIgnoresNonPositiveLimits(t *testing.T) {
	require.Nil(t, NewLimiter(tool.ConcurrencyConfig{
		MaxConcurrency: -1,
		Groups: []tool.ConcurrencyGroup{{
			ToolNames: []string{"search"},
			Limit:     0,
		}},
	}))
}
