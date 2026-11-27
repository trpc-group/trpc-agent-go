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

	ctx, cancel := context.WithTimeout(
		context.Background(),
		10*time.Millisecond,
	)
	defer cancel()
	_, err = limiter.Acquire(ctx, "search")
	require.Error(t, err)
	require.True(t, errors.Is(err, context.DeadlineExceeded))
	releaseGlobal()

	releaseSearch, err := limiter.Acquire(context.Background(), "search")
	require.NoError(t, err)
	canceledCtx, cancelImmediately := context.WithCancel(
		context.Background(),
	)
	cancelImmediately()
	_, err = limiter.Acquire(canceledCtx, "search")
	require.ErrorIs(t, err, context.Canceled)
	releaseSearch()
}

func TestValidateConcurrencyConfigRejectsDuplicateToolName(t *testing.T) {
	config := tool.ConcurrencyConfig{
		Groups: []tool.ConcurrencyGroup{
			{ToolNames: []string{"search"}, Limit: 1},
			{ToolNames: []string{"search", "fetch"}, Limit: 2},
		},
	}
	err := ValidateConcurrencyConfig(config)
	require.EqualError(
		t,
		err,
		`tool "search" appears in multiple concurrency groups`,
	)
	require.Panics(t, func() {
		NewLimiter(config)
	})
}

func TestValidateConcurrencyConfigIgnoresDisabledGroups(t *testing.T) {
	config := tool.ConcurrencyConfig{
		Groups: []tool.ConcurrencyGroup{
			{ToolNames: []string{"search"}, Limit: 0},
			{ToolNames: []string{"", "search", "search"}, Limit: 1},
		},
	}
	require.NoError(t, ValidateConcurrencyConfig(config))
	require.NotNil(t, NewLimiter(config))
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

func TestLimiterNilReceiverAndContext(t *testing.T) {
	var limiter *Limiter
	release, err := limiter.Acquire(nil, "search")
	require.NoError(t, err)
	release()

	limiter = NewLimiter(tool.ConcurrencyConfig{MaxConcurrency: 1})
	release, err = limiter.Acquire(nil, "search")
	require.NoError(t, err)
	release()
}

func TestLimiterContextScope(t *testing.T) {
	limiter := NewLimiter(tool.ConcurrencyConfig{MaxConcurrency: 1})
	parent := WithLimiter(nil, limiter)
	require.Same(t, limiter, LimiterFromContext(parent))

	child := WithLimiter(parent, nil)
	require.Nil(t, LimiterFromContext(child))
}

func TestLimiterTryAcquireAndReleaseNotifications(t *testing.T) {
	l := NewLimiter(tool.ConcurrencyConfig{MaxConcurrency: 2,
		Groups: []tool.ConcurrencyGroup{{ToolNames: []string{"a", "b"}, Limit: 1}}})
	require.Equal(t, l.QueueKey("a"), l.QueueKey("b"))
	require.NotEqual(t, l.QueueKey("a"), l.QueueKey("other"))
	notice := l.Changed()
	releaseA, ok := l.TryAcquire("a")
	require.True(t, ok)
	_, ok = l.TryAcquire("b")
	require.False(t, ok)
	releaseOther, ok := l.TryAcquire("other")
	require.True(t, ok)
	_, ok = l.TryAcquire("another")
	require.False(t, ok)
	releaseA()
	releaseA()
	select {
	case <-notice:
	default:
		t.Fatal("release did not notify a scheduler")
	}
	secondNotice := l.Changed()
	select {
	case <-secondNotice:
		t.Fatal("new subscription inherited an earlier notification")
	default:
	}
	releaseB, ok := l.TryAcquire("b")
	require.True(t, ok)
	releaseB()
	releaseOther()
}

func TestLimiterTryAcquireRollsBackGroup(t *testing.T) {
	l := NewLimiter(tool.ConcurrencyConfig{MaxConcurrency: 1,
		Groups: []tool.ConcurrencyGroup{{ToolNames: []string{"grouped"}, Limit: 1}}})
	release, err := l.Acquire(context.Background(), "other")
	require.NoError(t, err)
	notice := l.Changed()
	_, ok := l.TryAcquire("grouped")
	require.False(t, ok)
	select {
	case <-notice:
		t.Fatal("failed probe must not repeatedly wake its own dispatcher")
	default:
	}
	release()
	<-notice
	release, ok = l.TryAcquire("grouped")
	require.True(t, ok, "failed overall acquisition leaked the group permit")
	release()
}

type acquireObservedContext struct {
	context.Context
	doneCalls chan struct{}
}

func (c acquireObservedContext) Done() <-chan struct{} {
	c.doneCalls <- struct{}{}
	return c.Context.Done()
}

func TestLimiterCanceledAcquireNotifiesDispatcher(t *testing.T) {
	for _, tc := range []struct {
		name    string
		config  tool.ConcurrencyConfig
		holder  string
		waiting string
		stages  int
	}{
		{name: "group", config: tool.ConcurrencyConfig{
			Groups: []tool.ConcurrencyGroup{{ToolNames: []string{"grouped"}, Limit: 1}},
		}, holder: "grouped", waiting: "grouped", stages: 1},
		{name: "overall", config: tool.ConcurrencyConfig{MaxConcurrency: 1}, holder: "other", waiting: "other", stages: 1},
		{name: "group_then_overall", config: tool.ConcurrencyConfig{
			MaxConcurrency: 1,
			Groups:         []tool.ConcurrencyGroup{{ToolNames: []string{"grouped"}, Limit: 1}},
		}, holder: "other", waiting: "grouped", stages: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := NewLimiter(tc.config)
			release, err := l.Acquire(context.Background(), tc.holder)
			require.NoError(t, err)
			defer release()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			observed := acquireObservedContext{Context: ctx, doneCalls: make(chan struct{}, tc.stages)}
			done := make(chan error, 1)
			go func() {
				releaseWaiting, err := l.Acquire(observed, tc.waiting)
				if releaseWaiting != nil {
					releaseWaiting()
				}
				done <- err
			}()
			for i := 0; i < tc.stages; i++ {
				select {
				case <-observed.doneCalls:
				case <-time.After(time.Second):
					t.Fatal("acquisition did not reach the expected semaphore")
				}
			}
			notice := l.Changed()
			cancel()
			require.ErrorIs(t, <-done, context.Canceled)
			// A canceled semaphore acquisition may roll back a granted permit
			// internally. Always wake the dispatcher to recheck capacity, even
			// when no explicit release function was returned to the caller.
			select {
			case <-notice:
			default:
				t.Fatal("canceled acquisition did not wake the dispatcher")
			}
			release()
			releaseWaiting, ok := l.TryAcquire(tc.waiting)
			require.True(t, ok, "canceled acquisition leaked capacity")
			releaseWaiting()
		})
	}
}

func TestNilLimiterDispatchHelpers(t *testing.T) {
	var l *Limiter
	require.Zero(t, l.QueueKey("any"))
	require.Nil(t, l.Changed())
	release, ok := l.TryAcquire("any")
	require.True(t, ok)
	release()
}
