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
	"fmt"
	"sync"
	"sync/atomic"
)

// ConcurrencyPolicy configures per-guard and per-tool concurrency limits.
// When MaxActiveCalls is zero, no global limit is enforced. When
// PerToolLimits is empty, no per-tool limit is enforced beyond the global
// cap.
type ConcurrencyPolicy struct {
	// MaxActiveCalls caps the total number of concurrently in-flight
	// tool calls the guard has permitted but not yet released. Zero
	// disables the global limit.
	MaxActiveCalls int
	// PerToolLimits caps the number of concurrent in-flight calls for
	// one tool name. Zero disables the per-tool limit. Keys are tool
	// names.
	PerToolLimits map[string]int
}

// concurrencyLimiter enforces the global and per-tool active-call caps.
// It is safe for concurrent use. Acquire uses an atomic compare-and-swap
// for the global counter so two concurrent acquire calls cannot both
// succeed when the limit is 1. Per-tool counters use a mutex because
// they are keyed by tool name.
//
// The limiter is acquired AFTER the scan completes and the decision is
// allow. Deny/ask/error paths never acquire a slot, so they never leak.
// The release function is stashed on the guard and invoked from the
// after-tool callback; when no callback is attached or the callback is
// short-circuited, the guard's Close or a TTL sweep reclaims orphaned
// slots.
type concurrencyLimiter struct {
	policy ConcurrencyPolicy
	active int64 // atomic; global count

	mu      sync.Mutex
	perTool map[string]int
}

func newConcurrencyLimiter(p ConcurrencyPolicy) *concurrencyLimiter {
	return &concurrencyLimiter{
		policy:  p,
		perTool: make(map[string]int),
	}
}

// acquire reserves one slot for toolName. It returns a release function
// the caller must invoke when the tool call completes. When the global
// or per-tool cap is exceeded, acquire returns a non-nil error.
//
// The global counter uses an atomic CAS loop so the check-and-increment
// is atomic: two concurrent callers cannot both observe count < max and
// both increment.
func (c *concurrencyLimiter) acquire(ctx context.Context, toolName string) (func(), error) {
	if c == nil {
		return func() {}, nil
	}
	// Global CAS loop.
	if c.policy.MaxActiveCalls > 0 {
		for {
			cur := atomic.LoadInt64(&c.active)
			if cur >= int64(c.policy.MaxActiveCalls) {
				return nil, fmt.Errorf("concurrency limit exceeded: %d active calls (max %d)",
					cur, c.policy.MaxActiveCalls)
			}
			if atomic.CompareAndSwapInt64(&c.active, cur, cur+1) {
				break
			}
			// CAS failed; another goroutine won. Retry.
		}
	}
	// Per-tool check under mutex.
	if max, ok := c.policy.PerToolLimits[toolName]; ok && max > 0 {
		c.mu.Lock()
		if c.perTool[toolName] >= max {
			c.mu.Unlock()
			// Roll back the global increment.
			atomic.AddInt64(&c.active, -1)
			return nil, fmt.Errorf("concurrency limit exceeded for tool %q: %d active (max %d)",
				toolName, c.perTool[toolName], max)
		}
		c.perTool[toolName]++
		c.mu.Unlock()
	}
	once := sync.Once{}
	release := func() {
		once.Do(func() {
			atomic.AddInt64(&c.active, -1)
			if max, ok := c.policy.PerToolLimits[toolName]; ok && max > 0 {
				c.mu.Lock()
				if c.perTool[toolName] > 0 {
					c.perTool[toolName]--
				}
				c.mu.Unlock()
			}
		})
	}
	return release, nil
}

// activeCount returns the current global active-call count. Used by
// tests and diagnostics.
func (c *concurrencyLimiter) activeCount() int64 {
	if c == nil {
		return 0
	}
	return atomic.LoadInt64(&c.active)
}
