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
	"fmt"
	"sync"

	"golang.org/x/sync/semaphore"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// Limiter enforces overall and disjoint shared-group concurrency limits.
type Limiter struct {
	global  *semaphore.Weighted
	groups  map[string]*concurrencyGroup
	mu      sync.Mutex
	changed chan struct{}
}

type concurrencyGroup struct {
	*semaphore.Weighted
	key int
}

// QueueKey identifies calls that compete for the same group capacity. Zero
// denotes calls outside any group. Keys are meaningful only for this limiter.
func (l *Limiter) QueueKey(toolName string) int {
	if l != nil {
		if group := l.groups[toolName]; group != nil {
			return group.key
		}
	}
	return 0
}

type limiterContextKey struct{}

type limiterScope struct {
	limiter *Limiter
}

// WithLimiter returns a context carrying the limiter for one tool-execution
// scope. A nil limiter still shadows any limiter carried by the parent context.
func WithLimiter(ctx context.Context, limiter *Limiter) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(
		ctx,
		limiterContextKey{},
		limiterScope{limiter: limiter},
	)
}

// LimiterFromContext returns the limiter for the current tool-execution scope.
func LimiterFromContext(ctx context.Context) *Limiter {
	if ctx == nil {
		return nil
	}
	scope, ok := ctx.Value(limiterContextKey{}).(limiterScope)
	if !ok {
		return nil
	}
	return scope.limiter
}

// ValidateConcurrencyConfig validates concurrency group membership. It ignores
// empty tool names and groups with non-positive limits, and returns an error
// when a tool belongs to more than one positive-limit group.
func ValidateConcurrencyConfig(config tool.ConcurrencyConfig) error {
	seen := make(map[string]struct{})
	for _, group := range config.Groups {
		if group.Limit <= 0 {
			continue
		}
		groupNames := make(map[string]struct{})
		for _, name := range group.ToolNames {
			if name == "" {
				continue
			}
			if _, exists := groupNames[name]; exists {
				continue
			}
			groupNames[name] = struct{}{}
			if _, exists := seen[name]; exists {
				return fmt.Errorf(
					"tool %q appears in multiple concurrency groups",
					name,
				)
			}
			seen[name] = struct{}{}
		}
	}
	return nil
}

// NewLimiter builds a limiter from config. It returns nil when config contains
// no positive limits. It panics if a tool belongs to more than one
// positive-limit group.
func NewLimiter(config tool.ConcurrencyConfig) *Limiter {
	if err := ValidateConcurrencyConfig(config); err != nil {
		panic(err)
	}
	var global *semaphore.Weighted
	if config.MaxConcurrency > 0 {
		global = semaphore.NewWeighted(int64(config.MaxConcurrency))
	}
	groups := make(map[string]*concurrencyGroup)
	for i, group := range config.Groups {
		if group.Limit <= 0 {
			continue
		}
		groupLimiter := &concurrencyGroup{Weighted: semaphore.NewWeighted(int64(group.Limit)), key: i + 1}
		for _, name := range group.ToolNames {
			if name == "" {
				continue
			}
			groups[name] = groupLimiter
		}
	}
	if global == nil && len(groups) == 0 {
		return nil
	}
	return &Limiter{
		global: global,
		groups: groups,
	}
}

// Acquire waits until the named tool has both group and overall capacity.
// The returned release function is safe to call more than once.
func (l *Limiter) Acquire(
	ctx context.Context,
	toolName string,
) (func(), error) {
	if l == nil {
		return func() {}, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	group := l.groups[toolName]
	if group != nil {
		if err := group.Acquire(ctx, 1); err != nil {
			return nil, err
		}
	}
	if l.global != nil {
		if err := l.global.Acquire(ctx, 1); err != nil {
			if group != nil {
				group.Release(1)
				l.notifyRelease()
			}
			return nil, err
		}
	}
	return l.releaseFunc(group), nil
}

// TryAcquire reserves group and overall capacity without waiting. On success,
// the caller owns the returned idempotent release function. A nil limiter is
// unrestricted. Failed attempts leave both capacities unchanged.
func (l *Limiter) TryAcquire(toolName string) (release func(), ok bool) {
	if l == nil {
		return func() {}, true
	}
	// Hide a failed probe's temporary group reservation from other probes
	// and release notifications. Rolling it back must not wake this same
	// dispatcher repeatedly while the overall capacity remains exhausted.
	l.mu.Lock()
	defer l.mu.Unlock()
	group := l.groups[toolName]
	if group != nil && !group.TryAcquire(1) {
		return nil, false
	}
	if l.global != nil && !l.global.TryAcquire(1) {
		if group != nil {
			group.Release(1)
		}
		return nil, false
	}
	return l.releaseFunc(group), true
}

// Changed returns a channel closed by the next capacity release. Obtain it
// before trying to acquire capacity to avoid missing a concurrent release.
// A notification is a reason to retry, not a reservation. Nil receivers return
// a nil channel because their capacity is always available.
func (l *Limiter) Changed() <-chan struct{} {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.changed == nil {
		l.changed = make(chan struct{})
	}
	return l.changed
}

func (l *Limiter) notifyRelease() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.changed != nil {
		close(l.changed)
		l.changed = nil
	}
}

func (l *Limiter) releaseFunc(group *concurrencyGroup) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			if l.global != nil {
				l.global.Release(1)
			}
			if group != nil {
				group.Release(1)
			}
			l.notifyRelease()
		})
	}
}
