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
// The release function is stashed on the guard and invoked from wrapper
// completion; the guard's Close releases any orphaned slots.
type concurrencyLimiter struct {
	policy ConcurrencyPolicy
	active int

	mu      sync.Mutex
	perTool map[string]int
}

// newConcurrencyLimiter returns a concurrencyLimiter enforcing p.
func newConcurrencyLimiter(p ConcurrencyPolicy) *concurrencyLimiter {
	perToolLimits := make(map[string]int, len(p.PerToolLimits))
	for name, limit := range p.PerToolLimits {
		perToolLimits[name] = limit
	}
	p.PerToolLimits = perToolLimits
	return &concurrencyLimiter{
		policy:  p,
		perTool: make(map[string]int),
	}
}

// acquire reserves one slot for toolName. It returns a release function
// the caller must invoke when the tool call completes. When the global
// or per-tool cap is exceeded, acquire returns a non-nil error.
func (c *concurrencyLimiter) acquire(ctx context.Context, toolName string) (func(), error) {
	if c == nil {
		return func() {}, nil
	}
	// Do not grab a slot for an already-cancelled call.
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	globalTracked := c.policy.MaxActiveCalls > 0
	if c.policy.MaxActiveCalls > 0 &&
		c.active >= c.policy.MaxActiveCalls {
		active := c.active
		c.mu.Unlock()
		return nil, fmt.Errorf(
			"concurrency limit exceeded: %d active calls (max %d)",
			active, c.policy.MaxActiveCalls,
		)
	}
	max, perToolTracked := c.policy.PerToolLimits[toolName]
	perToolTracked = perToolTracked && max > 0
	if perToolTracked {
		active := c.perTool[toolName]
		if active >= max {
			c.mu.Unlock()
			return nil, fmt.Errorf("concurrency limit exceeded for tool %q: %d active (max %d)",
				toolName, active, max)
		}
	}
	if globalTracked {
		c.active++
	}
	if perToolTracked {
		c.perTool[toolName]++
	}
	c.mu.Unlock()
	once := sync.Once{}
	release := func() {
		once.Do(func() {
			c.mu.Lock()
			if globalTracked && c.active > 0 {
				c.active--
			}
			if perToolTracked && c.perTool[toolName] > 0 {
				c.perTool[toolName]--
			}
			c.mu.Unlock()
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
	c.mu.Lock()
	defer c.mu.Unlock()
	return int64(c.active)
}

func (c *concurrencyLimiter) enabled() bool {
	if c == nil {
		return false
	}
	if c.policy.MaxActiveCalls > 0 {
		return true
	}
	for _, limit := range c.policy.PerToolLimits {
		if limit > 0 {
			return true
		}
	}
	return false
}
